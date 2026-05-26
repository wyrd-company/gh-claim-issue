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
	// SubAgentField is the *name* of a Projects v2 text field used to
	// stamp an agent's identity onto an issue when it claims it.
	// When set, an issue is unavailable if this field already has a value,
	// and an agent cannot claim if it already owns an open issue carrying
	// its own identifier in the same field.
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

func (c *Config) validate() error {
	if len(c.ProjectStatuses) > 0 && c.ProjectID == "" {
		return errors.New("project_statuses requires project_id")
	}
	if c.SubAgentField != "" && c.ProjectID == "" {
		return errors.New("sub_agent_field requires project_id (the field lives on a project)")
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

# Projects v2 text field used by agents to stamp their identity on an
# issue when they claim it. When set:
#   - an issue is unavailable while this field already has a value
#   - an agent cannot claim if it already holds an open issue tagged
#     with its own --agent-name in this field
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
`
