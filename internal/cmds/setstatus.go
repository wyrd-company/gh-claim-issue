package cmds

import (
	"fmt"

	"github.com/boblangley/gh-claim-issue/internal/config"
	"github.com/boblangley/gh-claim-issue/internal/ghapi"
)

// SetStatusOptions controls SetStatus.
type SetStatusOptions struct {
	Issue      IssueRef
	StatusName string
	ProjectID  string // optional override; otherwise config / env
}

// SetStatusResult describes the change applied.
type SetStatusResult struct {
	Number     int
	URL        string
	Title      string
	OldStatus  string
	NewStatus  string
}

// SetStatus moves an issue's Status on the configured project to the named
// option. Resolves project_id + Status field + option name on the user's
// behalf so callers don't have to juggle three GraphQL ids.
func SetStatus(gh *ghapi.Client, cfg *config.Config, opts SetStatusOptions) (*SetStatusResult, error) {
	projectID, err := ResolveProjectID(opts.ProjectID, cfg)
	if err != nil {
		return nil, err
	}
	item, err := gh.FindIssueProjectItem(opts.Issue.Owner, opts.Issue.Repo, opts.Issue.Number, projectID)
	if err != nil {
		return nil, err
	}
	fieldID, optID, err := FindStatusOption(gh, projectID, opts.StatusName)
	if err != nil {
		return nil, err
	}
	if err := gh.SetSingleSelectField(projectID, item.ItemID, fieldID, optID); err != nil {
		return nil, fmt.Errorf("set status on %s: %w", item.Issue.URL, err)
	}
	return &SetStatusResult{
		Number:    item.Issue.Number,
		URL:       item.Issue.URL,
		Title:     item.Issue.Title,
		OldStatus: item.StatusName,
		NewStatus: opts.StatusName,
	}, nil
}
