// Package ghapi wraps go-gh REST and GraphQL clients with the queries
// gh-claim-issue needs: enumerating candidate issues, inspecting
// dependencies, reading and writing Projects v2 fields.
package ghapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
)

// Client bundles a REST and GraphQL client.
type Client struct {
	REST    *api.RESTClient
	GraphQL *api.GraphQLClient
}

// New constructs a Client using gh's default auth.
func New() (*Client, error) {
	rest, err := api.DefaultRESTClient()
	if err != nil {
		return nil, fmt.Errorf("rest client: %w", err)
	}
	gql, err := api.DefaultGraphQLClient()
	if err != nil {
		return nil, fmt.Errorf("graphql client: %w", err)
	}
	return &Client{REST: rest, GraphQL: gql}, nil
}

// Viewer returns the authenticated user's login.
func (c *Client) Viewer() (string, error) {
	var q struct {
		Viewer struct {
			Login string
		}
	}
	if err := c.GraphQL.Query("Viewer", &q, nil); err != nil {
		return "", fmt.Errorf("query viewer: %w", err)
	}
	return q.Viewer.Login, nil
}

// Issue is a slim projection of a repo issue used during filtering.
type Issue struct {
	ID         string // GraphQL node id
	Number     int
	Title      string
	URL        string
	Repository struct {
		Owner string
		Name  string
	}
	Labels    []string
	Assignees []string
}

// ListOpenUnassigned returns up to `limit` open, unassigned issues in the
// given repo, ordered oldest-first (FIFO).
func (c *Client) ListOpenUnassigned(owner, repo string, limit int) ([]Issue, error) {
	const query = `
	query($owner:String!,$name:String!,$first:Int!,$after:String) {
	  repository(owner:$owner, name:$name) {
	    issues(states:OPEN, first:$first, after:$after, orderBy:{field:CREATED_AT, direction:ASC}, filterBy:{assignee:null}) {
	      pageInfo { hasNextPage endCursor }
	      nodes {
	        id number title url
	        labels(first:50) { nodes { name } }
	        assignees(first:10) { nodes { login } }
	      }
	    }
	  }
	}`
	var out []Issue
	var cursor *string
	for len(out) < limit {
		page := 50
		if remaining := limit - len(out); remaining < page {
			page = remaining
		}
		vars := map[string]interface{}{
			"owner": owner, "name": repo, "first": page, "after": cursor,
		}
		var resp struct {
			Repository struct {
				Issues struct {
					PageInfo struct {
						HasNextPage bool
						EndCursor   string
					}
					Nodes []struct {
						ID     string
						Number int
						Title  string
						URL    string
						Labels struct {
							Nodes []struct{ Name string }
						}
						Assignees struct {
							Nodes []struct{ Login string }
						}
					}
				}
			}
		}
		if err := c.GraphQL.Do(query, vars, &resp); err != nil {
			return nil, fmt.Errorf("list issues: %w", err)
		}
		for _, n := range resp.Repository.Issues.Nodes {
			is := Issue{ID: n.ID, Number: n.Number, Title: n.Title, URL: n.URL}
			is.Repository.Owner = owner
			is.Repository.Name = repo
			for _, l := range n.Labels.Nodes {
				is.Labels = append(is.Labels, l.Name)
			}
			for _, a := range n.Assignees.Nodes {
				is.Assignees = append(is.Assignees, a.Login)
			}
			out = append(out, is)
		}
		if !resp.Repository.Issues.PageInfo.HasNextPage {
			break
		}
		cursor = &resp.Repository.Issues.PageInfo.EndCursor
	}
	return out, nil
}

// BlockedBy reports whether the issue has at least one open "blocked by"
// dependency, using GitHub's issue dependencies REST API. If the API is
// unavailable on this repo/plan (404) the issue is treated as unblocked.
func (c *Client) BlockedBy(owner, repo string, number int) (bool, error) {
	var resp []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
	}
	path := fmt.Sprintf("repos/%s/%s/issues/%d/dependencies/blocked_by", owner, repo, number)
	err := c.REST.Get(path, &resp)
	if err != nil {
		var httpErr *api.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, fmt.Errorf("blocked_by %s#%d: %w", repo, number, err)
	}
	for _, d := range resp {
		if strings.EqualFold(d.State, "open") {
			return true, nil
		}
	}
	return false, nil
}

// AddAssignee assigns login to the issue.
func (c *Client) AddAssignee(owner, repo string, number int, login string) error {
	body := struct {
		Assignees []string `json:"assignees"`
	}{Assignees: []string{login}}
	path := fmt.Sprintf("repos/%s/%s/issues/%d/assignees", owner, repo, number)
	return c.REST.Post(path, jsonBody(body), nil)
}

// RemoveAssignee unassigns login from the issue.
func (c *Client) RemoveAssignee(owner, repo string, number int, login string) error {
	body := struct {
		Assignees []string `json:"assignees"`
	}{Assignees: []string{login}}
	path := fmt.Sprintf("repos/%s/%s/issues/%d/assignees", owner, repo, number)
	return c.REST.Do(http.MethodDelete, path, jsonBody(body), nil)
}

// SearchOpenAssignedTo returns open issues assigned to login across all
// repos visible to the viewer. Used to enforce the "one in-flight per
// agent" rule when a sub-agent field is configured.
func (c *Client) SearchOpenAssignedTo(login string) ([]Issue, error) {
	const query = `
	query($q:String!) {
	  search(query:$q, type:ISSUE, first:100) {
	    nodes {
	      ... on Issue {
	        id number title url
	        repository { owner { login } name }
	      }
	    }
	  }
	}`
	q := fmt.Sprintf("assignee:%s is:issue is:open", login)
	var resp struct {
		Search struct {
			Nodes []struct {
				ID         string
				Number     int
				Title      string
				URL        string
				Repository struct {
					Owner struct{ Login string }
					Name  string
				}
			}
		}
	}
	if err := c.GraphQL.Do(query, map[string]interface{}{"q": q}, &resp); err != nil {
		return nil, fmt.Errorf("search assigned: %w", err)
	}
	out := make([]Issue, 0, len(resp.Search.Nodes))
	for _, n := range resp.Search.Nodes {
		is := Issue{ID: n.ID, Number: n.Number, Title: n.Title, URL: n.URL}
		is.Repository.Owner = n.Repository.Owner.Login
		is.Repository.Name = n.Repository.Name
		out = append(out, is)
	}
	return out, nil
}
