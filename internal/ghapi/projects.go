package ghapi

import (
	"fmt"
)

// ProjectStatusField is the metadata needed to flip a project item's
// Status (single-select) field to a chosen option.
type ProjectStatusField struct {
	FieldID  string
	Name     string
	Options  []ProjectStatusOption
}

// ProjectStatusOption is one option on a single-select field.
type ProjectStatusOption struct {
	ID   string
	Name string
}

// FindOption returns the option matching name (case-insensitive), or nil.
func (f *ProjectStatusField) FindOption(name string) *ProjectStatusOption {
	for i := range f.Options {
		if equalFold(f.Options[i].Name, name) {
			return &f.Options[i]
		}
	}
	return nil
}

// ProjectItem links an issue to its position on a Projects v2 board and
// carries projected field values (Status, Iteration, SubAgent).
type ProjectItem struct {
	ItemID         string
	Issue          Issue
	StatusName     string
	IterationID    string
	IterationTitle string
	// SubAgentText is the text value of the project's sub-agent field, if
	// one is configured at the project level. (The org-level sub-agent
	// field, when used instead, is read separately via issue-field-values.)
	SubAgentText string
}

// LookupSingleSelectField returns the metadata for a single-select field
// on the given project. Used to find the Status field (and its options)
// for the claim_status transition.
func (c *Client) LookupSingleSelectField(projectID, fieldName string) (*ProjectStatusField, error) {
	const query = `
	query($id:ID!) {
	  node(id:$id) {
	    ... on ProjectV2 {
	      fields(first:100) {
	        nodes {
	          ... on ProjectV2SingleSelectField {
	            id name
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
					ID      string
					Name    string
					Options []struct {
						ID   string
						Name string
					}
				}
			}
		}
	}
	if err := c.GraphQL.Do(query, map[string]interface{}{"id": projectID}, &resp); err != nil {
		return nil, fmt.Errorf("lookup single-select field: %w", err)
	}
	for _, f := range resp.Node.Fields.Nodes {
		if f.ID == "" || !equalFold(f.Name, fieldName) {
			continue
		}
		out := &ProjectStatusField{FieldID: f.ID, Name: f.Name}
		for _, o := range f.Options {
			out.Options = append(out.Options, ProjectStatusOption{ID: o.ID, Name: o.Name})
		}
		return out, nil
	}
	return nil, fmt.Errorf("single-select field %q not found on project", fieldName)
}

// ListProjectIssues returns project items whose content is an open Issue.
// Projects the current Status (single-select), Iteration, and any text-typed
// "Subagent" / "Sub Agent" / "Sub-Agent" project field values when present.
func (c *Client) ListProjectIssues(projectID, statusFieldName string, limit int) ([]ProjectItem, error) {
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
	              ... on ProjectV2ItemFieldSingleSelectValue {
	                name
	                field { ... on ProjectV2FieldCommon { name } }
	              }
	              ... on ProjectV2ItemFieldIterationValue {
	                iterationId
	                title
	                field { ... on ProjectV2FieldCommon { name } }
	              }
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
								Typename    string `json:"__typename"`
								Name        string
								IterationID string
								Title       string
								Text        string
								Field       struct{ Name string }
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
				switch fv.Typename {
				case "ProjectV2ItemFieldSingleSelectValue":
					if statusFieldName != "" && equalFold(fv.Field.Name, statusFieldName) {
						item.StatusName = fv.Name
					}
				case "ProjectV2ItemFieldIterationValue":
					if equalFold(fv.Field.Name, "Iteration") {
						item.IterationID = fv.IterationID
						item.IterationTitle = fv.Title
					}
				case "ProjectV2ItemFieldTextValue":
					if isSubAgentFieldName(fv.Field.Name) {
						item.SubAgentText = fv.Text
					}
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

// SetSingleSelectField writes a single-select option onto a project item.
func (c *Client) SetSingleSelectField(projectID, itemID, fieldID, optionID string) error {
	const mut = `
	mutation($pid:ID!,$iid:ID!,$fid:ID!,$oid:String!) {
	  updateProjectV2ItemFieldValue(input:{
	    projectId:$pid, itemId:$iid, fieldId:$fid,
	    value:{singleSelectOptionId:$oid}
	  }) { projectV2Item { id } }
	}`
	vars := map[string]interface{}{
		"pid": projectID, "iid": itemID, "fid": fieldID, "oid": optionID,
	}
	var resp struct{}
	if err := c.GraphQL.Do(mut, vars, &resp); err != nil {
		return fmt.Errorf("set single-select field: %w", err)
	}
	return nil
}

// isSubAgentFieldName matches common spellings of a sub-agent text field
// on a Projects v2 board (used by `list` to display who has what claimed
// without an explicit org-level sub_agent_field configuration).
func isSubAgentFieldName(name string) bool {
	switch {
	case equalFold(name, "subagent"),
		equalFold(name, "sub agent"),
		equalFold(name, "sub-agent"),
		equalFold(name, "agent"),
		equalFold(name, "claimed by"):
		return true
	}
	return false
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
