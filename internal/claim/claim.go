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
	Number    int
	Title     string
	URL       string
	Assignee  string
	AgentName string
	LockPath  string
}

// Run performs one claim attempt against the given repository.
func Run(ctx context.Context, gh *ghapi.Client, cfg *config.Config, opts Options) (*Result, error) {
	if cfg.SubAgentField != "" && opts.AgentName == "" {
		return nil, errors.New("config has sub_agent_field set; pass --agent-name to identify this agent")
	}

	// Serialise concurrent local agents racing for the same repo.
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

	// When projects are involved, resolve the field metadata up front.
	var (
		agentField  *ghapi.ProjectField
		statusField *ghapi.ProjectField
	)
	if cfg.SubAgentField != "" {
		agentField, err = gh.LookupField(cfg.ProjectID, cfg.SubAgentField)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(agentField.Type, "TEXT") {
			return nil, fmt.Errorf("sub_agent_field %q must be a text field (is %s)", cfg.SubAgentField, agentField.Type)
		}
	}
	if len(cfg.ProjectStatuses) > 0 {
		statusField, err = gh.LookupField(cfg.ProjectID, "Status")
		if err != nil {
			return nil, fmt.Errorf("project_statuses set but %w", err)
		}
		_ = statusField // reserved for future option-id resolution
	}

	// Enforce the "one open issue per (viewer, agent name)" rule before
	// touching candidates, so we fail fast.
	if cfg.SubAgentField != "" {
		held, err := findHeldByAgent(gh, cfg, viewer, opts.AgentName)
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

	// Build the candidate pool.
	candidates, err := buildCandidates(gh, cfg, opts)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, errors.New("no available issues match the configured rules")
	}

	chosen := candidates[0]

	// Perform the claim. Both assignment and (if configured) field stamp
	// must succeed for the claim to count.
	if err := gh.AddAssignee(chosen.Issue.Repository.Owner, chosen.Issue.Repository.Name, chosen.Issue.Number, viewer); err != nil {
		return nil, fmt.Errorf("assign %s: %w", chosen.Issue.URL, err)
	}
	if cfg.SubAgentField != "" {
		itemID := chosen.ItemID
		if itemID == "" {
			itemID, err = gh.AddIssueToProject(cfg.ProjectID, chosen.Issue.ID)
			if err != nil {
				return nil, fmt.Errorf("add %s to project: %w", chosen.Issue.URL, err)
			}
		}
		if err := gh.SetTextField(cfg.ProjectID, itemID, agentField.ID, opts.AgentName); err != nil {
			return nil, fmt.Errorf("stamp agent field on %s: %w", chosen.Issue.URL, err)
		}
	}

	return &Result{
		Number:    chosen.Issue.Number,
		Title:     chosen.Issue.Title,
		URL:       chosen.Issue.URL,
		Assignee:  viewer,
		AgentName: opts.AgentName,
		LockPath:  lockPath,
	}, nil
}

// buildCandidates returns the filtered, ordered pool of issues we could
// claim. The first element is the one Run picks.
func buildCandidates(gh *ghapi.Client, cfg *config.Config, opts Options) ([]ghapi.ProjectItem, error) {
	if cfg.ProjectID != "" {
		return projectCandidates(gh, cfg, opts)
	}
	return repoCandidates(gh, cfg, opts)
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
	items, err := gh.ListProjectIssues(cfg.ProjectID, "Status", cfg.SubAgentField, 500)
	if err != nil {
		return nil, err
	}
	allowedStatus := lowerSet(cfg.ProjectStatuses)
	out := make([]ghapi.ProjectItem, 0, len(items))
	for _, it := range items {
		// Restrict to the repo this invocation targets.
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
		if cfg.SubAgentField != "" && strings.TrimSpace(it.AgentValue) != "" {
			continue
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

// findHeldByAgent returns an open issue already claimed by agentName on
// the configured project, or nil if none.
func findHeldByAgent(gh *ghapi.Client, cfg *config.Config, viewer, agentName string) (*ghapi.Issue, error) {
	items, err := gh.ListProjectIssues(cfg.ProjectID, "", cfg.SubAgentField, 500)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if !strings.EqualFold(strings.TrimSpace(it.AgentValue), agentName) {
			continue
		}
		assignedToViewer := false
		for _, a := range it.Issue.Assignees {
			if strings.EqualFold(a, viewer) {
				assignedToViewer = true
				break
			}
		}
		if assignedToViewer {
			held := it.Issue
			return &held, nil
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
