// Package config loads the optional user-profile YAML that customises
// availability rules for `gh claim-issue`.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config represents the on-disk configuration. Every field is optional;
// an absent file is equivalent to an empty Config.
type Config struct {
	// SubAgentField is the *name* of an organisation-level GitHub Issue
	// Field (text type) used to stamp an agent's identity onto an issue
	// when it claims it.  When set, an issue is unavailable if this
	// field already has a value, and an agent cannot claim if it already
	// owns an open issue carrying its own identifier in the same field.
	SubAgentField string `yaml:"sub_agent_field"`

	// ExcludeLabels removes issues carrying ANY of these labels from
	// the candidate pool.
	ExcludeLabels []string `yaml:"exclude_labels"`

	// RequireLabels (when non-empty) limits the candidate pool to issues
	// carrying ALL of these labels.
	RequireLabels []string `yaml:"require_labels"`

	// ProjectID is the GraphQL node id of a Projects v2 board. When set,
	// only issues that are items on this project are eligible.
	ProjectID string `yaml:"project_id"`

	// ProjectStatuses (when non-empty, requires ProjectID) limits the pool
	// to project items whose Status field is one of these values.
	ProjectStatuses []string `yaml:"project_statuses"`

	// ClaimStatus, when set with ProjectID, names the Status option the
	// project item is moved to immediately after a successful claim.
	ClaimStatus string `yaml:"claim_status"`

	// ProjectIteration, when set with ProjectID, restricts the candidate
	// pool to items whose Iteration field matches. Accepted values:
	//
	//   "current" — the iteration containing today's date
	//   "next"    — the iteration immediately after current
	//   anything else — matched against iteration titles literally
	//
	// Items with no iteration assignment are excluded when this is set.
	ProjectIteration string `yaml:"project_iteration"`

	// FieldRules layers allow/deny filters on top of org-level Issue Field
	// values. Each rule names a field and lists values that are accepted
	// (allow) and/or rejected (deny). Matching is case-insensitive. When
	// both lists are present a deny match excludes the issue first; an
	// allow list (when set) then requires the current value to be in it.
	// An unset field is matched as the empty string "".
	FieldRules []FieldRule `yaml:"field_rules"`
}

// FieldRule describes one allow/deny filter on an org-level issue field.
type FieldRule struct {
	// Field is the name of an org-level GitHub Issue Field. Resolved
	// against the target owner's org-level fields, same as SubAgentField.
	Field string `yaml:"field"`

	// Allow, when non-empty, requires the field's current value to be one
	// of these (case-insensitive). Use "" to allow issues with no value.
	Allow []string `yaml:"allow,omitempty"`

	// Deny excludes any issue whose field value matches one of these
	// (case-insensitive). Use "" to exclude issues with no value.
	Deny []string `yaml:"deny,omitempty"`
}

// Path returns the conventional location of the config file:
//
//	$XDG_CONFIG_HOME/gh-claim-issue/config.yml  (or ~/.config/... fallback)
//
// On Windows os.UserConfigDir is used directly.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, "gh-claim-issue", "config.yml"), nil
}

// Load reads the config file if it exists. A missing file is not an error —
// the returned Config is zero-valued.
func Load() (*Config, string, error) {
	path, err := Path()
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, path, nil
		}
		return nil, path, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, path, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, path, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return &c, path, nil
}

// validate checks invariants that hold regardless of where the project id
// comes from. Project-dependent keys (project_statuses, claim_status,
// project_iteration) are deliberately NOT checked here: the effective project
// id may be supplied at runtime via --project or GH_CLAIM_ISSUE_PROJECT_ID,
// neither of which Load() can see. Those are validated by ValidateProjectID
// once the effective id has been resolved.
func (c *Config) validate() error {
	for i, r := range c.FieldRules {
		if r.Field == "" {
			return fmt.Errorf("field_rules[%d]: field name is required", i)
		}
		if len(r.Allow) == 0 && len(r.Deny) == 0 {
			return fmt.Errorf("field_rules[%d] (%q): set at least one of allow or deny", i, r.Field)
		}
	}
	return nil
}

// ValidateProjectID checks the project-dependent config keys against the
// effective project id resolved at runtime (from --project, the
// GH_CLAIM_ISSUE_PROJECT_ID env var, or config.project_id). When the effective
// id is empty, any project-dependent key is a misconfiguration.
func (c *Config) ValidateProjectID(effectiveID string) error {
	if effectiveID != "" {
		return nil
	}
	const hint = " (set project_id in config, export GH_CLAIM_ISSUE_PROJECT_ID, or pass --project=ID)"
	if len(c.ProjectStatuses) > 0 {
		return errors.New("project_statuses requires a project id" + hint)
	}
	if c.ClaimStatus != "" {
		return errors.New("claim_status requires a project id" + hint)
	}
	if c.ProjectIteration != "" {
		return errors.New("project_iteration requires a project id" + hint)
	}
	return nil
}

// WriteSample writes a commented example config to path, creating parent
// directories as needed. It refuses to overwrite an existing file.
func WriteSample(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("refusing to overwrite existing config: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(sample), 0o644)
}

const sample = `# gh-claim-issue configuration
#
# All fields are optional. Delete or comment-out any you don't need.

# Name of an organisation-level GitHub Issue Field (text type) used by
# agents to stamp their identity on an issue when they claim it. When set:
#   - an issue is unavailable while this field already has a value
#   - an agent cannot claim if it already holds an open issue tagged
#     with its own --agent-name in this field (across the whole org)
# sub_agent_field: "Claimed By"

# Skip issues that carry any of these labels.
# exclude_labels:
#   - blocked
#   - needs-triage

# Only consider issues carrying ALL of these labels.
# require_labels:
#   - ready

# Restrict to a Projects v2 board (GraphQL node id, e.g. PVT_kwDOA...).
# project_id: ""

# When project_id is set, restrict to items in these Status values.
# project_statuses:
#   - Todo
#   - Ready

# When project_id is set, move the item to this Status immediately after
# a successful claim. Must be one of the project's Status options.
# claim_status: "In Progress"

# When project_id is set, restrict to items whose Iteration field matches.
# Accepted: "current", "next", or a literal iteration title (e.g. "Sprint 4").
# Items with no iteration are excluded.
# project_iteration: "current"

# Allow/deny filters on org-level Issue Field values. Each rule names a
# field and lists accepted (allow) and/or rejected (deny) values; matching
# is case-insensitive. When both lists are present a deny match excludes
# the issue first, then allow (if set) requires the value to be in the
# list. Use "" to match issues with no value for that field.
# field_rules:
#   - field: "Priority"
#     allow:
#       - P0
#       - P1
#   - field: "Area"
#     deny:
#       - Infrastructure
`
