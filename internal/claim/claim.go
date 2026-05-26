// Package claim wires availability rules, the local mutex, and the
// GitHub mutations that hand an issue to a single agent.
package claim

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/boblangley/gh-claim-issue/internal/config"
	"github.com/boblangley/gh-claim-issue/internal/ghapi"
	"github.com/boblangley/gh-claim-issue/internal/lock"
)

// Options bundles request-time inputs for a claim attempt.
type Options struct {
	Owner     string
	Repo      string
	AgentName string        // optional; required when config.SubAgentField is set
	LockWait  time.Duration // total time to wait for the cross-process lock
}

// Result describes the issue that was claimed.
type Result struct {
	Number       int
	Title        string
	URL          string
	Assignee     string
	AgentName    string
	StatusMoved  string
	LockPath     string
}

// Run performs one claim attempt against the given repository.
func Run(ctx context.Context, gh *ghapi.Client, cfg *config.Config, opts Options) (*Result, error) {
	if cfg.SubAgentField != "" && opts.AgentName == "" {
		return nil, errors.New("config has sub_agent_field set; pass --agent-name to identify this agent")
	}

	lockCtx, cancel := context.WithTimeout(ctx, opts.LockWait)
	defer cancel()
	release, lockPath, err := lock.Acquire(lockCtx, opts.Owner+"/"+opts.Repo, 250*time.Millisecond)
	if err != nil {
		return nil, err
	}
	defer func() { _ = release() }()

	viewer, err := gh.Viewer()
	if err != nil {
		return nil, err
	}

	// Resolve project + field metadata up front so a misconfigured setup
	// fails fast (before we mutate anything).
	var (
		agentField  *ghapi.OrgIssueField
		statusField *ghapi.ProjectStatusField
		statusOptID string
	)
	if cfg.SubAgentField != "" {
		f, err := gh.FindOrgIssueField(opts.Owner, cfg.SubAgentField)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(f.Type, "text") {
			return nil, fmt.Errorf("sub_agent_field %q must be a text issue field (is %s)", cfg.SubAgentField, f.Type)
		}
		agentField = f
	}
	if cfg.ProjectID != "" && (len(cfg.ProjectStatuses) > 0 || cfg.ClaimStatus != "") {
		statusField, err = gh.LookupSingleSelectField(cfg.ProjectID, "Status")
		if err != nil {
			return nil, err
		}
		if cfg.ClaimStatus != "" {
			opt := statusField.FindOption(cfg.ClaimStatus)
			if opt == nil {
				return nil, fmt.Errorf("claim_status %q is not an option on the project's Status field", cfg.ClaimStatus)
			}
			statusOptID = opt.ID
		}
	}

	// Enforce the "one open issue per (agent name)" rule before touching
	// candidates so we fail fast.
	if cfg.SubAgentField != "" {
		held, err := findHeldByAgent(gh, agentField, viewer, opts.AgentName)
		if err != nil {
			return nil, err
		}
		if held != nil {
			return nil, fmt.Errorf(
				"agent %q already holds issue %s/%s#%d (%s); finish it before claiming another",
				opts.AgentName, held.Repository.Owner, held.Repository.Name, held.Number, held.URL,
			)
		}
	}

	candidates, err := buildCandidates(gh, cfg, agentField, opts)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, errors.New("no available issues match the configured rules")
	}
	chosen := candidates[0]

	if err := gh.AddAssignee(chosen.Issue.Repository.Owner, chosen.Issue.Repository.Name, chosen.Issue.Number, viewer); err != nil {
		return nil, fmt.Errorf("assign %s: %w", chosen.Issue.URL, err)
	}

	if agentField != nil {
		if err := gh.SetIssueTextField(chosen.Issue.Repository.Owner, chosen.Issue.Repository.Name, chosen.Issue.Number, agentField.ID, opts.AgentName); err != nil {
			return nil, fmt.Errorf("stamp agent field on %s: %w", chosen.Issue.URL, err)
		}
	}

	res := &Result{
		Number:    chosen.Issue.Number,
		Title:     chosen.Issue.Title,
		URL:       chosen.Issue.URL,
		Assignee:  viewer,
		AgentName: opts.AgentName,
		LockPath:  lockPath,
	}

	if statusOptID != "" {
		if chosen.ItemID == "" {
			return res, fmt.Errorf("claim_status requested but %s has no project item (project_id mismatch?)", chosen.Issue.URL)
		}
		if err := gh.SetSingleSelectField(cfg.ProjectID, chosen.ItemID, statusField.FieldID, statusOptID); err != nil {
			return res, fmt.Errorf("move %s to status %q: %w", chosen.Issue.URL, cfg.ClaimStatus, err)
		}
		res.StatusMoved = cfg.ClaimStatus
	}

	return res, nil
}

func buildCandidates(gh *ghapi.Client, cfg *config.Config, agentField *ghapi.OrgIssueField, opts Options) ([]ghapi.ProjectItem, error) {
	var pool []ghapi.ProjectItem
	if cfg.ProjectID != "" {
		items, err := projectCandidates(gh, cfg, opts)
		if err != nil {
			return nil, err
		}
		pool = items
	} else {
		items, err := repoCandidates(gh, cfg, opts)
		if err != nil {
			return nil, err
		}
		pool = items
	}

	if agentField == nil {
		return pool, nil
	}
	out := make([]ghapi.ProjectItem, 0, len(pool))
	for _, it := range pool {
		vals, err := gh.GetIssueFieldValues(it.Issue.Repository.Owner, it.Issue.Repository.Name, it.Issue.Number)
		if err != nil {
			return nil, err
		}
		taken := false
		for _, v := range vals {
			if v.FieldID == agentField.ID && strings.TrimSpace(v.AsText()) != "" {
				taken = true
				break
			}
		}
		if !taken {
			out = append(out, it)
		}
	}
	return out, nil
}

func repoCandidates(gh *ghapi.Client, cfg *config.Config, opts Options) ([]ghapi.ProjectItem, error) {
	issues, err := gh.ListOpenUnassigned(opts.Owner, opts.Repo, 200)
	if err != nil {
		return nil, err
	}
	out := make([]ghapi.ProjectItem, 0, len(issues))
	for _, is := range issues {
		if !labelsAllowed(is.Labels, cfg) {
			continue
		}
		blocked, err := gh.BlockedBy(opts.Owner, opts.Repo, is.Number)
		if err != nil {
			return nil, err
		}
		if blocked {
			continue
		}
		out = append(out, ghapi.ProjectItem{Issue: is})
	}
	return out, nil
}

func projectCandidates(gh *ghapi.Client, cfg *config.Config, opts Options) ([]ghapi.ProjectItem, error) {
	statusField := ""
	if len(cfg.ProjectStatuses) > 0 {
		statusField = "Status"
	}
	items, err := gh.ListProjectIssues(cfg.ProjectID, statusField, 500)
	if err != nil {
		return nil, err
	}
	allowedStatus := lowerSet(cfg.ProjectStatuses)
	out := make([]ghapi.ProjectItem, 0, len(items))
	for _, it := range items {
		if !strings.EqualFold(it.Issue.Repository.Owner, opts.Owner) ||
			!strings.EqualFold(it.Issue.Repository.Name, opts.Repo) {
			continue
		}
		if len(it.Issue.Assignees) > 0 {
			continue
		}
		if !labelsAllowed(it.Issue.Labels, cfg) {
			continue
		}
		if len(allowedStatus) > 0 {
			if _, ok := allowedStatus[strings.ToLower(it.StatusName)]; !ok {
				continue
			}
		}
		blocked, err := gh.BlockedBy(opts.Owner, opts.Repo, it.Issue.Number)
		if err != nil {
			return nil, err
		}
		if blocked {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

// findHeldByAgent looks across every open issue assigned to the viewer
// (any repo) for one whose org-level sub-agent field already carries this
// agent's name.
func findHeldByAgent(gh *ghapi.Client, agentField *ghapi.OrgIssueField, viewer, agentName string) (*ghapi.Issue, error) {
	mine, err := gh.SearchOpenAssignedTo(viewer)
	if err != nil {
		return nil, err
	}
	for _, is := range mine {
		vals, err := gh.GetIssueFieldValues(is.Repository.Owner, is.Repository.Name, is.Number)
		if err != nil {
			return nil, err
		}
		for _, v := range vals {
			if v.FieldID == agentField.ID && strings.EqualFold(strings.TrimSpace(v.AsText()), agentName) {
				held := is
				return &held, nil
			}
		}
	}
	return nil, nil
}

func labelsAllowed(labels []string, cfg *config.Config) bool {
	if len(cfg.ExcludeLabels) > 0 {
		excl := lowerSet(cfg.ExcludeLabels)
		for _, l := range labels {
			if _, bad := excl[strings.ToLower(l)]; bad {
				return false
			}
		}
	}
	if len(cfg.RequireLabels) > 0 {
		have := lowerSet(labels)
		for _, r := range cfg.RequireLabels {
			if _, ok := have[strings.ToLower(r)]; !ok {
				return false
			}
		}
	}
	return true
}

func lowerSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		out[strings.ToLower(s)] = struct{}{}
	}
	return out
}
