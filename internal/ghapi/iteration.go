package ghapi

import (
	"fmt"
	"time"
)

// ProjectIterationField is the metadata for a project's iteration field.
// The Iterations slice contains the current iteration first (if any), then
// upcoming iterations in chronological order. CompletedIterations holds
// past iterations.
type ProjectIterationField struct {
	FieldID              string
	Name                 string
	Iterations           []ProjectIteration
	CompletedIterations  []ProjectIteration
}

// ProjectIteration is a single iteration value.
type ProjectIteration struct {
	ID        string
	Title     string
	StartDate string // YYYY-MM-DD
	Duration  int    // days
}

// Current returns the iteration containing today, or nil. The first entry
// in Iterations is "current" iff today falls inside its window.
func (f *ProjectIterationField) Current() *ProjectIteration {
	if len(f.Iterations) == 0 {
		return nil
	}
	it := f.Iterations[0]
	start, err := time.Parse("2006-01-02", it.StartDate)
	if err != nil {
		return nil
	}
	end := start.AddDate(0, 0, it.Duration)
	now := time.Now()
	if !now.Before(start) && now.Before(end) {
		return &it
	}
	return nil
}

// Next returns the iteration immediately after the current one.
// If no iteration is current, the closest upcoming iteration is returned.
func (f *ProjectIterationField) Next() *ProjectIteration {
	if len(f.Iterations) == 0 {
		return nil
	}
	if f.Current() != nil {
		if len(f.Iterations) < 2 {
			return nil
		}
		it := f.Iterations[1]
		return &it
	}
	it := f.Iterations[0]
	return &it
}

// FindByTitle returns the iteration whose title matches name (case-insensitive),
// searching active then completed iterations.
func (f *ProjectIterationField) FindByTitle(name string) *ProjectIteration {
	for i := range f.Iterations {
		if equalFold(f.Iterations[i].Title, name) {
			return &f.Iterations[i]
		}
	}
	for i := range f.CompletedIterations {
		if equalFold(f.CompletedIterations[i].Title, name) {
			return &f.CompletedIterations[i]
		}
	}
	return nil
}

// LookupIterationField finds an iteration field on the project. fieldName
// is the iteration field's display name (typically "Iteration"). Returns
// an error if no iteration field with that name exists.
func (c *Client) LookupIterationField(projectID, fieldName string) (*ProjectIterationField, error) {
	const query = `
	query($id:ID!) {
	  node(id:$id) {
	    ... on ProjectV2 {
	      fields(first:100) {
	        nodes {
	          ... on ProjectV2IterationField {
	            id name
	            configuration {
	              iterations { id title startDate duration }
	              completedIterations { id title startDate duration }
	            }
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
					ID            string
					Name          string
					Configuration struct {
						Iterations          []iterPayload
						CompletedIterations []iterPayload
					}
				}
			}
		}
	}
	if err := c.GraphQL.Do(query, map[string]interface{}{"id": projectID}, &resp); err != nil {
		return nil, fmt.Errorf("lookup iteration field: %w", err)
	}
	for _, f := range resp.Node.Fields.Nodes {
		if f.ID == "" || !equalFold(f.Name, fieldName) {
			continue
		}
		out := &ProjectIterationField{FieldID: f.ID, Name: f.Name}
		for _, it := range f.Configuration.Iterations {
			out.Iterations = append(out.Iterations, it.to())
		}
		for _, it := range f.Configuration.CompletedIterations {
			out.CompletedIterations = append(out.CompletedIterations, it.to())
		}
		return out, nil
	}
	return nil, fmt.Errorf("iteration field %q not found on project", fieldName)
}

type iterPayload struct {
	ID        string
	Title     string
	StartDate string
	Duration  int
}

func (p iterPayload) to() ProjectIteration {
	return ProjectIteration{ID: p.ID, Title: p.Title, StartDate: p.StartDate, Duration: p.Duration}
}

// SetIterationField writes an iteration option onto a project item.
func (c *Client) SetIterationField(projectID, itemID, fieldID, iterationID string) error {
	const mut = `
	mutation($pid:ID!,$iid:ID!,$fid:ID!,$it:String!) {
	  updateProjectV2ItemFieldValue(input:{
	    projectId:$pid, itemId:$iid, fieldId:$fid,
	    value:{iterationId:$it}
	  }) { projectV2Item { id } }
	}`
	vars := map[string]interface{}{
		"pid": projectID, "iid": itemID, "fid": fieldID, "it": iterationID,
	}
	var resp struct{}
	if err := c.GraphQL.Do(mut, vars, &resp); err != nil {
		return fmt.Errorf("set iteration field: %w", err)
	}
	return nil
}
