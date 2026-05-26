package cmds

import (
	"errors"
	"fmt"
	"strings"

	"github.com/boblangley/gh-claim-issue/internal/config"
	"github.com/boblangley/gh-claim-issue/internal/ghapi"
)

// ReleaseOptions controls the Release flow.
type ReleaseOptions struct {
	Issue        *IssueRef
	ProjectID    string // optional override; otherwise config / env
	AgentName    string // for --force only
	Force        bool   // allow releasing an issue held by a different agent
	ReleaseStatus string // status to move the item to; "" means leave Status alone
}

// ReleaseResult describes the outcome of a release.
type ReleaseResult struct {
	Number       int
	URL          string
	Title        string
	AgentCleared string
	Unassigned   string
	StatusMoved  string
}

// Release clears the sub-agent field on an issue and (optionally) moves
// its project Status. Without --force the caller must currently hold the
// issue: either the agent-name on the field matches AgentName, or the
// caller is the lone assignee.
func Release(gh *ghapi.Client, cfg *config.Config, opts ReleaseOptions) (*ReleaseResult, error) {
	viewer, err := gh.Viewer()
	if err != nil {
		return nil, err
	}

	ref, err := resolveReleaseTarget(gh, cfg, viewer, &opts)
	if err != nil {
		return nil, err
	}

	var agentField *ghapi.OrgIssueField
	if cfg.SubAgentField != "" {
		agentField, err = gh.FindOrgIssueField(ref.Owner, cfg.SubAgentField)
		if err != nil {
			return nil, err
		}
	}

	currentAgent := ""
	if agentField != nil {
		vals, err := gh.GetIssueFieldValues(ref.Owner, ref.Repo, ref.Number)
		if err != nil {
			return nil, err
		}
		for _, v := range vals {
			if v.FieldID == agentField.ID {
				currentAgent = strings.TrimSpace(v.AsText())
				break
			}
		}
	}

	if !opts.Force && agentField != nil && opts.AgentName != "" && currentAgent != "" &&
		!strings.EqualFold(currentAgent, opts.AgentName) {
		return nil, fmt.Errorf("issue %s/%s#%d is held by %q, not %q; pass --force to release anyway",
			ref.Owner, ref.Repo, ref.Number, currentAgent, opts.AgentName)
	}

	// Resolve project context (if any) before mutating, so a misconfigured
	// release_status fails fast.
	var (
		projectID      string
		itemID         string
		statusFieldID  string
		statusOptionID string
	)
	if opts.ReleaseStatus != "" {
		projectID, err = ResolveProjectID(opts.ProjectID, cfg)
		if err != nil {
			return nil, fmt.Errorf("--release-status: %w", err)
		}
		item, err := gh.FindIssueProjectItem(ref.Owner, ref.Repo, ref.Number, projectID)
		if err != nil {
			return nil, err
		}
		itemID = item.ItemID
		statusFieldID, statusOptionID, err = FindStatusOption(gh, projectID, opts.ReleaseStatus)
		if err != nil {
			return nil, err
		}
	}

	res := &ReleaseResult{Number: ref.Number}

	if agentField != nil && currentAgent != "" {
		if err := gh.ClearIssueTextField(ref.Owner, ref.Repo, ref.Number, agentField.ID); err != nil {
			return nil, fmt.Errorf("clear %s on %s/%s#%d: %w",
				cfg.SubAgentField, ref.Owner, ref.Repo, ref.Number, err)
		}
		res.AgentCleared = currentAgent
	}

	if err := gh.RemoveAssignee(ref.Owner, ref.Repo, ref.Number, viewer); err != nil {
		return res, fmt.Errorf("unassign %s/%s#%d: %w", ref.Owner, ref.Repo, ref.Number, err)
	}
	res.Unassigned = viewer

	if itemID != "" {
		if err := gh.SetSingleSelectField(projectID, itemID, statusFieldID, statusOptionID); err != nil {
			return res, fmt.Errorf("move %s/%s#%d to status %q: %w",
				ref.Owner, ref.Repo, ref.Number, opts.ReleaseStatus, err)
		}
		res.StatusMoved = opts.ReleaseStatus
	}

	return res, nil
}

// resolveReleaseTarget figures out which issue to release. With an explicit
// IssueRef we use it directly. Otherwise we look for an open issue held by
// the viewer (and by AgentName, when the sub-agent field is configured).
func resolveReleaseTarget(gh *ghapi.Client, cfg *config.Config, viewer string, opts *ReleaseOptions) (IssueRef, error) {
	if opts.Issue != nil {
		return *opts.Issue, nil
	}
	// No issue provided: look at the viewer's open issues.
	mine, err := gh.SearchOpenAssignedTo(viewer)
	if err != nil {
		return IssueRef{}, err
	}
	if len(mine) == 0 {
		return IssueRef{}, errors.New("you have no open issues to release; pass --issue N")
	}

	if cfg.SubAgentField == "" {
		if len(mine) > 1 {
			return IssueRef{}, fmt.Errorf("you hold %d open issues; pass --issue N to disambiguate", len(mine))
		}
		is := mine[0]
		return IssueRef{Owner: is.Repository.Owner, Repo: is.Repository.Name, Number: is.Number}, nil
	}

	// With a sub-agent field, prefer the issue stamped with this agent name.
	if opts.AgentName != "" {
		for _, is := range mine {
			field, err := gh.FindOrgIssueField(is.Repository.Owner, cfg.SubAgentField)
			if err != nil {
				continue
			}
			vals, err := gh.GetIssueFieldValues(is.Repository.Owner, is.Repository.Name, is.Number)
			if err != nil {
				continue
			}
			for _, v := range vals {
				if v.FieldID == field.ID && strings.EqualFold(strings.TrimSpace(v.AsText()), opts.AgentName) {
					return IssueRef{Owner: is.Repository.Owner, Repo: is.Repository.Name, Number: is.Number}, nil
				}
			}
		}
		return IssueRef{}, fmt.Errorf("no open issue stamped with agent %q is assigned to you", opts.AgentName)
	}
	if len(mine) > 1 {
		return IssueRef{}, fmt.Errorf("you hold %d open issues; pass --issue N or --agent-name to disambiguate", len(mine))
	}
	is := mine[0]
	return IssueRef{Owner: is.Repository.Owner, Repo: is.Repository.Name, Number: is.Number}, nil
}

