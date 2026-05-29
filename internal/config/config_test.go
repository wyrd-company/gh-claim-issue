package config

import (
	"strings"
	"testing"
)

// A config that uses project-dependent keys but omits project_id must load
// successfully — the effective project id can still come from --project or the
// GH_CLAIM_ISSUE_PROJECT_ID env var, which Load() cannot see.
func TestValidateAllowsProjectKeysWithoutProjectID(t *testing.T) {
	cfgs := []*Config{
		{ProjectStatuses: []string{"Todo"}},
		{ClaimStatus: "In Progress"},
		{ProjectIteration: "current"},
	}
	for _, c := range cfgs {
		if err := c.validate(); err != nil {
			t.Errorf("validate() rejected project keys without project_id: %v", err)
		}
	}
}

// field_rules validation is independent of project id and must still fire.
func TestValidateFieldRules(t *testing.T) {
	c := &Config{FieldRules: []FieldRule{{Field: ""}}}
	if err := c.validate(); err == nil {
		t.Error("validate() accepted a field_rule with no field name")
	}
	c = &Config{FieldRules: []FieldRule{{Field: "Priority"}}}
	if err := c.validate(); err == nil {
		t.Error("validate() accepted a field_rule with neither allow nor deny")
	}
}

func TestValidateProjectID(t *testing.T) {
	// With an effective project id, project-dependent keys are fine.
	c := &Config{ProjectStatuses: []string{"Todo"}, ClaimStatus: "Doing", ProjectIteration: "current"}
	if err := c.ValidateProjectID("PVT_xxx"); err != nil {
		t.Errorf("ValidateProjectID with id rejected valid config: %v", err)
	}

	// Without one, each project-dependent key must be reported.
	for name, c := range map[string]*Config{
		"project_statuses":  {ProjectStatuses: []string{"Todo"}},
		"claim_status":      {ClaimStatus: "Doing"},
		"project_iteration": {ProjectIteration: "current"},
	} {
		err := c.ValidateProjectID("")
		if err == nil {
			t.Errorf("ValidateProjectID(%q) accepted missing project id", name)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("ValidateProjectID(%q) error %q does not mention the key", name, err)
		}
	}

	// A config without project-dependent keys needs no project id.
	if err := (&Config{}).ValidateProjectID(""); err != nil {
		t.Errorf("ValidateProjectID rejected an empty config: %v", err)
	}
}
