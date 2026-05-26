package cmds

import (
	"strings"

	"github.com/boblangley/gh-claim-issue/internal/config"
	"github.com/boblangley/gh-claim-issue/internal/ghapi"
)

// ListOptions controls List.
type ListOptions struct {
	ProjectID string // optional override; otherwise config / env
	AgentName string // when set, only items whose sub-agent value matches
	Mine      bool   // when true, only items whose sub-agent matches the caller's `gh` viewer (or assignee == viewer when no sub-agent field is configured)
}

// ListEntry is one row of the list output.
type ListEntry struct {
	Number     int
	Title      string
	URL        string
	Repo       string // "owner/name"
	Status     string
	Iteration  string
	AgentName  string // value of sub-agent field, if any
	Assignees  []string
}

// List returns project items annotated with their sub-agent values. The
// sub-agent value is read from the org-level sub_agent_field when
// configured, falling back to a project-level text field on the project
// item (any of Subagent / Sub Agent / Agent / Claimed By).
func List(gh *ghapi.Client, cfg *config.Config, opts ListOptions) ([]ListEntry, error) {
	projectID, err := ResolveProjectID(opts.ProjectID, cfg)
	if err != nil {
		return nil, err
	}
	items, err := gh.ListProjectIssues(projectID, "Status", 500)
	if err != nil {
		return nil, err
	}

	var viewer string
	if opts.Mine {
		v, err := gh.Viewer()
		if err != nil {
			return nil, err
		}
		viewer = v
	}

	// Resolve the org-level sub-agent field once if it's configured. We
	// only need it when items don't already carry a project-level text
	// match, but in practice teams use one or the other.
	var (
		agentFieldsByOrg = map[string]*ghapi.OrgIssueField{}
		agentLookupDone  = map[string]bool{}
	)
	getOrgAgentField := func(owner string) *ghapi.OrgIssueField {
		if cfg.SubAgentField == "" {
			return nil
		}
		if f, ok := agentFieldsByOrg[owner]; ok {
			return f
		}
		if agentLookupDone[owner] {
			return nil
		}
		agentLookupDone[owner] = true
		f, err := gh.FindOrgIssueField(owner, cfg.SubAgentField)
		if err != nil {
			return nil
		}
		agentFieldsByOrg[owner] = f
		return f
	}

	out := make([]ListEntry, 0, len(items))
	for _, it := range items {
		agentName := it.SubAgentText
		if agentName == "" && cfg.SubAgentField != "" {
			f := getOrgAgentField(it.Issue.Repository.Owner)
			if f != nil {
				vals, err := gh.GetIssueFieldValues(it.Issue.Repository.Owner, it.Issue.Repository.Name, it.Issue.Number)
				if err == nil {
					for _, v := range vals {
						if v.FieldID == f.ID {
							agentName = strings.TrimSpace(v.AsText())
							break
						}
					}
				}
			}
		}

		if opts.AgentName != "" && !strings.EqualFold(agentName, opts.AgentName) {
			continue
		}
		if opts.Mine {
			matchesAgent := false
			if agentName != "" && strings.EqualFold(agentName, viewer) {
				matchesAgent = true
			}
			matchesAssignee := false
			for _, a := range it.Issue.Assignees {
				if strings.EqualFold(a, viewer) {
					matchesAssignee = true
					break
				}
			}
			if !(matchesAgent || matchesAssignee) {
				continue
			}
		}

		out = append(out, ListEntry{
			Number:    it.Issue.Number,
			Title:     it.Issue.Title,
			URL:       it.Issue.URL,
			Repo:      it.Issue.Repository.Owner + "/" + it.Issue.Repository.Name,
			Status:    it.StatusName,
			Iteration: it.IterationTitle,
			AgentName: agentName,
			Assignees: it.Issue.Assignees,
		})
	}
	return out, nil
}
