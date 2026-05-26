package cmds

import (
	"errors"
	"fmt"
	"strings"

	"github.com/boblangley/gh-claim-issue/internal/config"
	"github.com/boblangley/gh-claim-issue/internal/ghapi"
)

// HandoffOptions controls Handoff.
type HandoffOptions struct {
	Issue       IssueRef
	Note        string // required: the handoff message body
	Block       string // optional: when set, status moves to Blocked and a label is applied
	ProjectID   string // optional override; otherwise config / env
	ReviewStatus string // status to move to when not blocking (default "Review")
	BlockedStatus string // status to move to when blocking (default "Blocked")
	BlockedLabel  string // label to apply when blocking (default "blocked")
}

// HandoffResult describes the outcome.
type HandoffResult struct {
	Number     int
	URL        string
	Title      string
	NewStatus  string
	Commented  bool
	LabelAdded string
}

// Handoff composes set-status + comment + (optionally) a "blocked" label
// into one atomic operation. Mirrors kanban-md's primitive of the same
// name. The note is required so the next picker has context.
func Handoff(gh *ghapi.Client, cfg *config.Config, opts HandoffOptions) (*HandoffResult, error) {
	if strings.TrimSpace(opts.Note) == "" {
		return nil, errors.New("--note is required (the next picker needs context)")
	}
	reviewStatus := opts.ReviewStatus
	if reviewStatus == "" {
		reviewStatus = "Review"
	}
	blockedStatus := opts.BlockedStatus
	if blockedStatus == "" {
		blockedStatus = "Blocked"
	}
	blockedLabel := opts.BlockedLabel
	if blockedLabel == "" {
		blockedLabel = "blocked"
	}

	projectID, err := ResolveProjectID(opts.ProjectID, cfg)
	if err != nil {
		return nil, err
	}
	item, err := gh.FindIssueProjectItem(opts.Issue.Owner, opts.Issue.Repo, opts.Issue.Number, projectID)
	if err != nil {
		return nil, err
	}

	targetStatus := reviewStatus
	if opts.Block != "" {
		targetStatus = blockedStatus
	}
	fieldID, optID, err := FindStatusOption(gh, projectID, targetStatus)
	if err != nil {
		return nil, err
	}

	body := opts.Note
	if opts.Block != "" {
		body = fmt.Sprintf("**Blocked:** %s\n\n%s", opts.Block, opts.Note)
	}
	if err := gh.AddComment(opts.Issue.Owner, opts.Issue.Repo, opts.Issue.Number, body); err != nil {
		return nil, fmt.Errorf("comment on %s: %w", item.Issue.URL, err)
	}

	if err := gh.SetSingleSelectField(projectID, item.ItemID, fieldID, optID); err != nil {
		return nil, fmt.Errorf("set status on %s: %w", item.Issue.URL, err)
	}

	res := &HandoffResult{
		Number:    item.Issue.Number,
		URL:       item.Issue.URL,
		Title:     item.Issue.Title,
		NewStatus: targetStatus,
		Commented: true,
	}
	if opts.Block != "" {
		if err := gh.EnsureLabel(opts.Issue.Owner, opts.Issue.Repo, blockedLabel); err != nil {
			return res, fmt.Errorf("ensure %q label: %w", blockedLabel, err)
		}
		if err := gh.AddLabels(opts.Issue.Owner, opts.Issue.Repo, opts.Issue.Number, []string{blockedLabel}); err != nil {
			return res, fmt.Errorf("apply %q label: %w", blockedLabel, err)
		}
		res.LabelAdded = blockedLabel
	}
	return res, nil
}
