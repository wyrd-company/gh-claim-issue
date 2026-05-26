// Package cmds implements the secondary subcommands (release, set-status,
// handoff, list) that share the same project context as the primary
// claim flow.
package cmds

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/boblangley/gh-claim-issue/internal/config"
	"github.com/boblangley/gh-claim-issue/internal/ghapi"
)

// IssueRef identifies a single issue by repo coordinates and number.
type IssueRef struct {
	Owner  string
	Repo   string
	Number int
}

// ParseIssueRef accepts forms: "owner/repo#N", "owner/repo/N", "#N", "N".
// In the short forms the current repo (from go-gh) is filled in.
func ParseIssueRef(s string) (IssueRef, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return IssueRef{}, errors.New("empty issue reference")
	}
	if strings.Contains(s, "/") {
		// owner/repo#N or owner/repo/N
		var owner, repo, numStr string
		if i := strings.Index(s, "#"); i > 0 {
			or := s[:i]
			numStr = s[i+1:]
			parts := strings.SplitN(or, "/", 2)
			if len(parts) != 2 {
				return IssueRef{}, fmt.Errorf("invalid issue reference %q", s)
			}
			owner, repo = parts[0], parts[1]
		} else {
			parts := strings.SplitN(s, "/", 3)
			if len(parts) != 3 {
				return IssueRef{}, fmt.Errorf("invalid issue reference %q", s)
			}
			owner, repo, numStr = parts[0], parts[1], parts[2]
		}
		n, err := strconv.Atoi(numStr)
		if err != nil || n <= 0 {
			return IssueRef{}, fmt.Errorf("invalid issue number in %q", s)
		}
		return IssueRef{Owner: owner, Repo: repo, Number: n}, nil
	}
	numStr := strings.TrimPrefix(s, "#")
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return IssueRef{}, fmt.Errorf("invalid issue number %q", s)
	}
	r, err := repository.Current()
	if err != nil {
		return IssueRef{}, fmt.Errorf("could not detect current repo for %q; use owner/repo#N", s)
	}
	return IssueRef{Owner: r.Owner, Repo: r.Name, Number: n}, nil
}

// ResolveProjectID picks the effective project id from explicit override,
// env var, then config (in that order).
func ResolveProjectID(override string, cfg *config.Config) (string, error) {
	if override != "" {
		return override, nil
	}
	if cfg.ProjectID != "" {
		return cfg.ProjectID, nil
	}
	return "", errors.New("no project id available (set project_id in config, export GH_CLAIM_ISSUE_PROJECT_ID, or pass --project=ID)")
}

// FindStatusOption returns the option id for an option name on the project's
// Status field, with a friendly error if the name doesn't exist.
func FindStatusOption(gh *ghapi.Client, projectID, statusName string) (fieldID, optID string, err error) {
	field, err := gh.LookupSingleSelectField(projectID, "Status")
	if err != nil {
		return "", "", err
	}
	opt := field.FindOption(statusName)
	if opt == nil {
		names := make([]string, 0, len(field.Options))
		for _, o := range field.Options {
			names = append(names, o.Name)
		}
		return "", "", fmt.Errorf("status %q is not an option on the project's Status field (options: %s)",
			statusName, strings.Join(names, ", "))
	}
	return field.FieldID, opt.ID, nil
}
