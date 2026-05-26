package ghapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/cli/go-gh/v2/pkg/api"
)

// FindIssueProjectItem looks up the issue by number and returns its
// project item id on projectID, plus the issue meta. Returns an error if
// the issue is not on that project.
func (c *Client) FindIssueProjectItem(owner, repo string, number int, projectID string) (item ProjectItem, err error) {
	const query = `
	query($owner:String!,$name:String!,$num:Int!) {
	  repository(owner:$owner, name:$name) {
	    issue(number:$num) {
	      id number title url state
	      assignees(first:10) { nodes { login } }
	      labels(first:50) { nodes { name } }
	      projectItems(first:20, includeArchived:false) {
	        nodes {
	          id
	          project { id }
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
	var resp struct {
		Repository struct {
			Issue struct {
				ID        string
				Number    int
				Title     string
				URL       string
				State     string
				Assignees struct {
					Nodes []struct{ Login string }
				}
				Labels struct {
					Nodes []struct{ Name string }
				}
				ProjectItems struct {
					Nodes []struct {
						ID      string
						Project struct{ ID string }
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
	}
	vars := map[string]interface{}{"owner": owner, "name": repo, "num": number}
	if err := c.GraphQL.Do(query, vars, &resp); err != nil {
		return ProjectItem{}, fmt.Errorf("lookup issue %s/%s#%d: %w", owner, repo, number, err)
	}
	is := resp.Repository.Issue
	if is.ID == "" {
		return ProjectItem{}, fmt.Errorf("issue %s/%s#%d not found", owner, repo, number)
	}
	issue := Issue{ID: is.ID, Number: is.Number, Title: is.Title, URL: is.URL}
	issue.Repository.Owner = owner
	issue.Repository.Name = repo
	for _, l := range is.Labels.Nodes {
		issue.Labels = append(issue.Labels, l.Name)
	}
	for _, a := range is.Assignees.Nodes {
		issue.Assignees = append(issue.Assignees, a.Login)
	}
	for _, pi := range is.ProjectItems.Nodes {
		if pi.Project.ID != projectID {
			continue
		}
		item.ItemID = pi.ID
		item.Issue = issue
		for _, fv := range pi.FieldValues.Nodes {
			switch fv.Typename {
			case "ProjectV2ItemFieldSingleSelectValue":
				if equalFold(fv.Field.Name, "Status") {
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
		return item, nil
	}
	return ProjectItem{}, fmt.Errorf("issue %s/%s#%d is not an item on the configured project", owner, repo, number)
}

// AddComment posts a comment on an issue via the REST API.
func (c *Client) AddComment(owner, repo string, number int, body string) error {
	payload := struct {
		Body string `json:"body"`
	}{Body: body}
	path := fmt.Sprintf("repos/%s/%s/issues/%d/comments", owner, repo, number)
	return c.REST.Post(path, jsonBody(payload), nil)
}

// AddLabels adds labels to an issue. Missing labels cause a 422; let the
// caller decide whether that's fatal.
func (c *Client) AddLabels(owner, repo string, number int, labels []string) error {
	payload := struct {
		Labels []string `json:"labels"`
	}{Labels: labels}
	path := fmt.Sprintf("repos/%s/%s/issues/%d/labels", owner, repo, number)
	return c.REST.Post(path, jsonBody(payload), nil)
}

// EnsureLabel creates the label if it doesn't already exist. Idempotent.
func (c *Client) EnsureLabel(owner, repo, name string) error {
	path := fmt.Sprintf("repos/%s/%s/labels/%s", owner, repo, name)
	var resp struct{}
	if err := c.REST.Get(path, &resp); err == nil {
		return nil
	} else {
		var httpErr *api.HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
			return fmt.Errorf("check label %q: %w", name, err)
		}
	}
	create := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.REST.Post(fmt.Sprintf("repos/%s/%s/labels", owner, repo), jsonBody(create), nil)
}
