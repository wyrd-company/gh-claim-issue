package ghapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/cli/go-gh/v2/pkg/api"
)

// OrgIssueField describes one organisation-level GitHub Issue Field.
// Issue fields apply to every issue in every repository owned by the
// organisation. See:
//   https://docs.github.com/en/rest/orgs/issue-fields
type OrgIssueField struct {
	ID      int64                  `json:"id"`
	NodeID  string                 `json:"node_id"`
	Name    string                 `json:"name"`
	Type    string                 `json:"data_type"` // "text" | "number" | "date" | "single_select"
	Options []OrgIssueFieldOption  `json:"options,omitempty"`
}

// OrgIssueFieldOption is a single-select option.
type OrgIssueFieldOption struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// ListOrgIssueFields returns every issue field defined on org. Returns
// (nil, nil) if the owner is not an organisation or the org has no
// fields configured (404 from the API).
func (c *Client) ListOrgIssueFields(org string) ([]OrgIssueField, error) {
	var resp []OrgIssueField
	err := c.REST.Get(fmt.Sprintf("orgs/%s/issue-fields", org), &resp)
	if err != nil {
		var httpErr *api.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("list org issue fields: %w", err)
	}
	return resp, nil
}

// FindOrgIssueField finds an org-level issue field by case-insensitive name.
func (c *Client) FindOrgIssueField(org, name string) (*OrgIssueField, error) {
	fields, err := c.ListOrgIssueFields(org)
	if err != nil {
		return nil, err
	}
	for i := range fields {
		if equalFold(fields[i].Name, name) {
			return &fields[i], nil
		}
	}
	return nil, fmt.Errorf("issue field %q not found in org %q (define it under org Settings → Issue fields)", name, org)
}

// IssueFieldValue is the projection returned by GetIssueFieldValues. The
// raw API returns a typed payload; we collapse single-select to its name
// and text/number/date to their string representations for filtering.
type IssueFieldValue struct {
	FieldID int64  `json:"field_id"`
	Type    string `json:"data_type"`
	// Value is the field's current value as a JSON-encoded scalar (or
	// "null"). For text it's a quoted string, for single_select an option
	// id, etc. Use AsText for an "is anything set" check.
	Value json.RawMessage `json:"value"`
}

// AsText returns the field's value as a string for empty-check / display
// purposes. Empty / null values produce "".
func (v IssueFieldValue) AsText() string {
	if len(v.Value) == 0 || string(v.Value) == "null" {
		return ""
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(v.Value, &s); err == nil {
		return s
	}
	// Otherwise fall back to the raw JSON (number, option id, date).
	return string(v.Value)
}

// GetIssueFieldValues fetches the current issue-field values for an issue.
func (c *Client) GetIssueFieldValues(owner, repo string, number int) ([]IssueFieldValue, error) {
	var resp []IssueFieldValue
	path := fmt.Sprintf("repos/%s/%s/issues/%d/issue-field-values", owner, repo, number)
	err := c.REST.Get(path, &resp)
	if err != nil {
		var httpErr *api.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get issue field values for %s/%s#%d: %w", owner, repo, number, err)
	}
	return resp, nil
}

// SetIssueTextField sets a single text-typed issue field. The PUT body is
// a full replacement of all field values, so we re-send every existing
// value alongside the one we're writing.
func (c *Client) SetIssueTextField(owner, repo string, number int, fieldID int64, value string) error {
	existing, err := c.GetIssueFieldValues(owner, repo, number)
	if err != nil {
		return err
	}
	type writeVal struct {
		FieldID int64       `json:"field_id"`
		Value   interface{} `json:"value"`
	}
	body := make([]writeVal, 0, len(existing)+1)
	wrote := false
	for _, v := range existing {
		if v.FieldID == fieldID {
			body = append(body, writeVal{FieldID: fieldID, Value: value})
			wrote = true
			continue
		}
		var raw interface{}
		if err := json.Unmarshal(v.Value, &raw); err != nil {
			raw = nil
		}
		body = append(body, writeVal{FieldID: v.FieldID, Value: raw})
	}
	if !wrote {
		body = append(body, writeVal{FieldID: fieldID, Value: value})
	}
	path := fmt.Sprintf("repos/%s/%s/issues/%d/issue-field-values", owner, repo, number)
	return c.REST.Put(path, jsonBody(body), nil)
}

// ClearIssueTextField removes the value of fieldID by sending a PUT that
// replays every other current value and drops this one.
func (c *Client) ClearIssueTextField(owner, repo string, number int, fieldID int64) error {
	existing, err := c.GetIssueFieldValues(owner, repo, number)
	if err != nil {
		return err
	}
	type writeVal struct {
		FieldID int64       `json:"field_id"`
		Value   interface{} `json:"value"`
	}
	body := make([]writeVal, 0, len(existing))
	for _, v := range existing {
		if v.FieldID == fieldID {
			continue
		}
		var raw interface{}
		if err := json.Unmarshal(v.Value, &raw); err != nil {
			raw = nil
		}
		body = append(body, writeVal{FieldID: v.FieldID, Value: raw})
	}
	path := fmt.Sprintf("repos/%s/%s/issues/%d/issue-field-values", owner, repo, number)
	return c.REST.Put(path, jsonBody(body), nil)
}
