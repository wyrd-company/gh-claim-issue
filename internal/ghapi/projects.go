package ghapi

import (
	"fmt"
)

// ProjectField describes one field on a Projects v2 board.
type ProjectField struct {
	ID      string
	Name    string
	Type    string   // "TEXT" or "SINGLE_SELECT"
	Options []ProjectFieldOption
}

// ProjectFieldOption is a single-select option (name/id pair).
type ProjectFieldOption struct {
	ID   string
	Name string
}

// ProjectItem links an issue to its position on a Projects v2 board,
// along with the values currently set on the fields we care about.
type ProjectItem struct {
	ItemID     string
	Issue      Issue
	StatusName string
	AgentValue string
}

// LookupField finds a field on a project by case-insensitive name. The
// returned struct includes single-select options when applicable.
func (c *Client) LookupField(projectID, fieldName string) (*ProjectField, error) {
	const query = `
	query($id:ID!) {
	  node(id:$id) {
	    ... on ProjectV2 {
	      fields(first:100) {
	        nodes {
	          ... on ProjectV2Field { id name dataType }
	          ... on ProjectV2SingleSelectField {
	            id name dataType
	            options { id name }
	          }
	        }
	      }
	    }
	  }
	}`
	var resp struct {
		Node struct {
			Fields struct {
				Nodes []struct {
					ID       string
					Name     string
					DataType string
					Options  []struct {
						ID   string
						Name string
					}
				}
			}
		}
	}
	if err := c.GraphQL.Do(query, map[string]interface{}{"id": projectID}, &resp); err != nil {
		return nil, fmt.Errorf("lookup field: %w", err)
	}
	for _, f := range resp.Node.Fields.Nodes {
		if !equalFold(f.Name, fieldName) {
			continue
		}
		pf := &ProjectField{ID: f.ID, Name: f.Name, Type: f.DataType}
		for _, o := range f.Options {
			pf.Options = append(pf.Options, ProjectFieldOption{ID: o.ID, Name: o.Name})
		}
		return pf, nil
	}
	return nil, fmt.Errorf("field %q not found on project", fieldName)
}

// ListProjectIssues returns project items whose content is an open Issue,
// projecting the optional Status (single-select) and agent (text) fields.
// statusFieldName/agentFieldName may be "" to skip projection.
func (c *Client) ListProjectIssues(projectID, statusFieldName, agentFieldName string, limit int) ([]ProjectItem, error) {
	const query = `
	query($id:ID!,$first:Int!,$after:String) {
	  node(id:$id) {
	    ... on ProjectV2 {
	      items(first:$first, after:$after) {
	        pageInfo { hasNextPage endCursor }
	        nodes {
	          id
	          content {
	            __typename
	            ... on Issue {
	              id number title url state
	              repository { owner { login } name }
	              labels(first:50) { nodes { name } }
	              assignees(first:10) { nodes { login } }
	            }
	          }
	          fieldValues(first:50) {
	            nodes {
	              __typename
	              ... on ProjectV2ItemFieldTextValue {
	                text
	                field { ... on ProjectV2FieldCommon { name } }
	              }
	              ... on ProjectV2ItemFieldSingleSelectValue {
	                name
	                field { ... on ProjectV2FieldCommon { name } }
	              }
	            }
	          }
	        }
	      }
	    }
	  }
	}`
	var out []ProjectItem
	var cursor *string
	for len(out) < limit {
		page := 100
		if remaining := limit - len(out); remaining < page {
			page = remaining
		}
		vars := map[string]interface{}{"id": projectID, "first": page, "after": cursor}
		var resp struct {
			Node struct {
				Items struct {
					PageInfo struct {
						HasNextPage bool
						EndCursor   string
					}
					Nodes []struct {
						ID      string
						Content struct {
							Typename   string `json:"__typename"`
							ID         string
							Number     int
							Title      string
							URL        string
							State      string
							Repository struct {
								Owner struct{ Login string }
								Name  string
							}
							Labels struct {
								Nodes []struct{ Name string }
							}
							Assignees struct {
								Nodes []struct{ Login string }
							}
						}
						FieldValues struct {
							Nodes []struct {
								Typename string `json:"__typename"`
								Text     string
								Name     string
								Field    struct{ Name string }
							}
						}
					}
				}
			}
		}
		if err := c.GraphQL.Do(query, vars, &resp); err != nil {
			return nil, fmt.Errorf("list project items: %w", err)
		}
		for _, n := range resp.Node.Items.Nodes {
			if n.Content.Typename != "Issue" || n.Content.State != "OPEN" {
				continue
			}
			item := ProjectItem{ItemID: n.ID}
			item.Issue = Issue{
				ID: n.Content.ID, Number: n.Content.Number,
				Title: n.Content.Title, URL: n.Content.URL,
			}
			item.Issue.Repository.Owner = n.Content.Repository.Owner.Login
			item.Issue.Repository.Name = n.Content.Repository.Name
			for _, l := range n.Content.Labels.Nodes {
				item.Issue.Labels = append(item.Issue.Labels, l.Name)
			}
			for _, a := range n.Content.Assignees.Nodes {
				item.Issue.Assignees = append(item.Issue.Assignees, a.Login)
			}
			for _, fv := range n.FieldValues.Nodes {
				switch {
				case statusFieldName != "" && equalFold(fv.Field.Name, statusFieldName) &&
					fv.Typename == "ProjectV2ItemFieldSingleSelectValue":
					item.StatusName = fv.Name
				case agentFieldName != "" && equalFold(fv.Field.Name, agentFieldName) &&
					fv.Typename == "ProjectV2ItemFieldTextValue":
					item.AgentValue = fv.Text
				}
			}
			out = append(out, item)
		}
		if !resp.Node.Items.PageInfo.HasNextPage {
			break
		}
		cursor = &resp.Node.Items.PageInfo.EndCursor
	}
	return out, nil
}

// FindProjectItemForIssue returns the project item id for an issue on a
// given project, plus the current value of the named text field (or "").
// Returns "", "", nil if the issue is not on the project.
func (c *Client) FindProjectItemForIssue(projectID string, issueNodeID string, agentFieldName string) (itemID, agentValue string, err error) {
	const query = `
	query($id:ID!) {
	  node(id:$id) {
	    ... on Issue {
	      projectItems(first:20) {
	        nodes {
	          id
	          project { id }
	          fieldValues(first:50) {
	            nodes {
	              __typename
	              ... on ProjectV2ItemFieldTextValue {
	                text
	                field { ... on ProjectV2FieldCommon { name } }
	              }
	            }
	          }
	        }
	      }
	    }
	  }
	}`
	var resp struct {
		Node struct {
			ProjectItems struct {
				Nodes []struct {
					ID      string
					Project struct{ ID string }
					FieldValues struct {
						Nodes []struct {
							Typename string `json:"__typename"`
							Text     string
							Field    struct{ Name string }
						}
					}
				}
			}
		}
	}
	if err := c.GraphQL.Do(query, map[string]interface{}{"id": issueNodeID}, &resp); err != nil {
		return "", "", fmt.Errorf("find project item: %w", err)
	}
	for _, n := range resp.Node.ProjectItems.Nodes {
		if n.Project.ID != projectID {
			continue
		}
		var av string
		if agentFieldName != "" {
			for _, fv := range n.FieldValues.Nodes {
				if fv.Typename == "ProjectV2ItemFieldTextValue" && equalFold(fv.Field.Name, agentFieldName) {
					av = fv.Text
					break
				}
			}
		}
		return n.ID, av, nil
	}
	return "", "", nil
}

// AddIssueToProject inserts an issue into a project and returns the new
// item id.
func (c *Client) AddIssueToProject(projectID, issueNodeID string) (string, error) {
	const mut = `
	mutation($pid:ID!,$cid:ID!) {
	  addProjectV2ItemById(input:{projectId:$pid, contentId:$cid}) {
	    item { id }
	  }
	}`
	var resp struct {
		AddProjectV2ItemByID struct {
			Item struct{ ID string }
		} `json:"addProjectV2ItemById"`
	}
	vars := map[string]interface{}{"pid": projectID, "cid": issueNodeID}
	if err := c.GraphQL.Do(mut, vars, &resp); err != nil {
		return "", fmt.Errorf("add to project: %w", err)
	}
	return resp.AddProjectV2ItemByID.Item.ID, nil
}

// SetTextField writes a text value to a Projects v2 text field on an item.
func (c *Client) SetTextField(projectID, itemID, fieldID, value string) error {
	const mut = `
	mutation($pid:ID!,$iid:ID!,$fid:ID!,$v:String!) {
	  updateProjectV2ItemFieldValue(input:{
	    projectId:$pid, itemId:$iid, fieldId:$fid,
	    value:{text:$v}
	  }) { projectV2Item { id } }
	}`
	vars := map[string]interface{}{
		"pid": projectID, "iid": itemID, "fid": fieldID, "v": value,
	}
	var resp struct{}
	if err := c.GraphQL.Do(mut, vars, &resp); err != nil {
		return fmt.Errorf("set text field: %w", err)
	}
	return nil
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}
