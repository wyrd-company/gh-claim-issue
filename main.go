package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/boblangley/gh-claim-issue/internal/claim"
	"github.com/boblangley/gh-claim-issue/internal/cmds"
	"github.com/boblangley/gh-claim-issue/internal/config"
	"github.com/boblangley/gh-claim-issue/internal/ghapi"
	"github.com/boblangley/gh-claim-issue/internal/names"
)

// envProjectID is read as a fallback when neither the flag nor the config
// supplies a project id.
const envProjectID = "GH_CLAIM_ISSUE_PROJECT_ID"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "generate-name":
			n, err := names.Generate()
			if err != nil {
				return err
			}
			fmt.Println(n)
			return nil
		case "init-config":
			return cmdInitConfig()
		case "release":
			return cmdRelease(args[1:])
		case "set-status":
			return cmdSetStatus(args[1:])
		case "handoff":
			return cmdHandoff(args[1:])
		case "list":
			return cmdList(args[1:])
		case "help", "-h", "--help":
			printUsage(os.Stdout)
			return nil
		}
	}
	return cmdClaim(args)
}

// projectFlag is a string flag whose value is optional: `--project` alone
// means "use project_id from config / env", while `--project=ID` overrides it.
// The IsBoolFlag hook lets the std flag package accept the bare form.
type projectFlag struct {
	present bool
	value   string
}

func (p *projectFlag) String() string {
	if p == nil {
		return ""
	}
	return p.value
}

func (p *projectFlag) Set(s string) error {
	p.present = true
	// std flag passes "true" when the user writes the bare `--project` form
	// (because IsBoolFlag returns true). Treat that as "no explicit value".
	if s == "true" {
		return nil
	}
	p.value = s
	return nil
}

func (p *projectFlag) IsBoolFlag() bool { return true }

func cmdClaim(args []string) error {
	fs := flag.NewFlagSet("claim", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		repoFlag  = fs.String("repo", "", "owner/name (defaults to current repo in repo mode; optional filter in project mode)")
		project   projectFlag
		agentName = fs.String("agent-name", "", "sub-agent identifier (required when sub_agent_field is configured)")
		genName   = fs.Bool("generate-agent-name", false, "generate a random agent name and use it for this claim")
		lockWait  = fs.Duration("lock-wait", 30*time.Second, "max time to wait for the local mutex")
		jsonOut   = fs.Bool("json", false, "emit the claim result as JSON")
		dryRun    = fs.Bool("dry-run", false, "resolve the candidate issue without mutating anything")
	)
	fs.Var(&project, "project", "project mode; bare form uses env/config project id, or pass --project=PVT_xxx")
	fs.Usage = func() { printUsage(fs.Output()) }
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, cfgPath, err := config.Load()
	if err != nil {
		return err
	}

	projectID, err := resolveProjectID(&project, cfg)
	if err != nil {
		return err
	}
	projectMode := projectID != ""

	owner, name, err := resolveRepo(*repoFlag, projectMode)
	if err != nil {
		return err
	}

	if *genName && *agentName == "" {
		n, err := names.Generate()
		if err != nil {
			return err
		}
		*agentName = n
	}

	gh, err := ghapi.New()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	res, err := claim.Run(ctx, gh, cfg, claim.Options{
		Owner:     owner,
		Repo:      name,
		ProjectID: projectID,
		AgentName: *agentName,
		LockWait:  *lockWait,
		DryRun:    *dryRun,
	})
	if err != nil {
		return err
	}

	if *jsonOut {
		out := map[string]interface{}{
			"number":     res.Number,
			"url":        res.URL,
			"title":      res.Title,
			"assignee":   res.Assignee,
			"agent_name": res.AgentName,
			"status":     res.StatusMoved,
			"config":     cfgPath,
			"dry_run":    res.DryRun,
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
		return nil
	}
	prefix := "claimed"
	if res.DryRun {
		prefix = "[dry-run] would claim"
	}
	fmt.Printf("%s #%d %s\n", prefix, res.Number, res.Title)
	fmt.Printf("  url:        %s\n", res.URL)
	fmt.Printf("  assignee:   %s\n", res.Assignee)
	if res.AgentName != "" {
		fmt.Printf("  agent:      %s\n", res.AgentName)
	}
	if res.StatusMoved != "" {
		fmt.Printf("  status:     %s\n", res.StatusMoved)
	}
	return nil
}

func cmdInitConfig() error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	if err := config.WriteSample(path); err != nil {
		return err
	}
	fmt.Printf("wrote sample config to %s\n", path)
	return nil
}

func cmdRelease(args []string) error {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		issueNum     = fs.Int("issue", 0, "issue number to release (default: the one you currently hold)")
		repoFlag     = fs.String("repo", "", "owner/name when --issue is used and you're not in the target repo")
		project      projectFlag
		agentName    = fs.String("agent-name", "", "agent identifier; required with --force to release someone else's claim")
		force        = fs.Bool("force", false, "release the field even if it's stamped to a different agent")
		releaseStat  = fs.String("release-status", "", "move the project item to this Status after release (e.g. \"Backlog\")")
		jsonOut      = fs.Bool("json", false, "emit JSON")
	)
	fs.Var(&project, "project", "project mode; bare form uses env/config project id, or pass --project=PVT_xxx")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: gh claim-issue release [--issue N] [--repo owner/name]")
		fmt.Fprintln(fs.Output(), "                              [--force --agent-name X] [--release-status STATUS]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := config.Load()
	if err != nil {
		return err
	}
	if *force && *agentName == "" {
		return errors.New("--force requires --agent-name to make the override explicit")
	}

	gh, err := ghapi.New()
	if err != nil {
		return err
	}

	var ref *cmds.IssueRef
	if *issueNum != 0 {
		owner, name, err := resolveRepo(*repoFlag, false)
		if err != nil {
			return err
		}
		ref = &cmds.IssueRef{Owner: owner, Repo: name, Number: *issueNum}
	}

	projID, _ := resolveProjectID(&project, cfg)

	res, err := cmds.Release(gh, cfg, cmds.ReleaseOptions{
		Issue:         ref,
		ProjectID:     projID,
		AgentName:     *agentName,
		Force:         *force,
		ReleaseStatus: *releaseStat,
	})
	if err != nil {
		return err
	}

	if *jsonOut {
		b, _ := json.Marshal(map[string]interface{}{
			"number":        res.Number,
			"agent_cleared": res.AgentCleared,
			"unassigned":    res.Unassigned,
			"status":        res.StatusMoved,
		})
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("released #%d\n", res.Number)
	if res.AgentCleared != "" {
		fmt.Printf("  agent cleared: %s\n", res.AgentCleared)
	}
	if res.Unassigned != "" {
		fmt.Printf("  unassigned:    %s\n", res.Unassigned)
	}
	if res.StatusMoved != "" {
		fmt.Printf("  status:        %s\n", res.StatusMoved)
	}
	return nil
}

func cmdSetStatus(args []string) error {
	fs := flag.NewFlagSet("set-status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		project projectFlag
		jsonOut = fs.Bool("json", false, "emit JSON")
	)
	fs.Var(&project, "project", "project mode; bare form uses env/config project id, or pass --project=PVT_xxx")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: gh claim-issue set-status <ISSUE> <STATUS_NAME> [--project[=ID]]")
		fmt.Fprintln(fs.Output(), "  ISSUE: N | #N | owner/repo#N | owner/repo/N")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	pos := fs.Args()
	if len(pos) < 2 {
		fs.Usage()
		return errors.New("set-status requires <ISSUE> and <STATUS_NAME>")
	}
	ref, err := cmds.ParseIssueRef(pos[0])
	if err != nil {
		return err
	}
	status := strings.Join(pos[1:], " ")

	cfg, _, err := config.Load()
	if err != nil {
		return err
	}
	gh, err := ghapi.New()
	if err != nil {
		return err
	}
	projID, _ := resolveProjectID(&project, cfg)

	res, err := cmds.SetStatus(gh, cfg, cmds.SetStatusOptions{
		Issue:      ref,
		StatusName: status,
		ProjectID:  projID,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		b, _ := json.Marshal(map[string]interface{}{
			"number":     res.Number,
			"url":        res.URL,
			"title":      res.Title,
			"old_status": res.OldStatus,
			"new_status": res.NewStatus,
		})
		fmt.Println(string(b))
		return nil
	}
	if res.OldStatus != "" && !strings.EqualFold(res.OldStatus, res.NewStatus) {
		fmt.Printf("#%d %s → %s\n", res.Number, res.OldStatus, res.NewStatus)
	} else {
		fmt.Printf("#%d status = %s\n", res.Number, res.NewStatus)
	}
	fmt.Printf("  url: %s\n", res.URL)
	return nil
}

func cmdHandoff(args []string) error {
	fs := flag.NewFlagSet("handoff", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		note          = fs.String("note", "", "handoff message body (required)")
		block         = fs.String("block", "", "set the issue to Blocked with this reason; applies the blocked label")
		project       projectFlag
		jsonOut       = fs.Bool("json", false, "emit JSON")
		reviewStatus  = fs.String("review-status", "Review", "Status option to move to when not blocking")
		blockedStatus = fs.String("blocked-status", "Blocked", "Status option to move to when --block is set")
		blockedLabel  = fs.String("blocked-label", "blocked", "label to add when --block is set")
	)
	fs.Var(&project, "project", "project mode; bare form uses env/config project id, or pass --project=PVT_xxx")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: gh claim-issue handoff <ISSUE> --note \"...\" [--block \"reason\"]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	pos := fs.Args()
	if len(pos) < 1 {
		fs.Usage()
		return errors.New("handoff requires an issue reference")
	}
	ref, err := cmds.ParseIssueRef(pos[0])
	if err != nil {
		return err
	}

	cfg, _, err := config.Load()
	if err != nil {
		return err
	}
	gh, err := ghapi.New()
	if err != nil {
		return err
	}
	projID, _ := resolveProjectID(&project, cfg)

	res, err := cmds.Handoff(gh, cfg, cmds.HandoffOptions{
		Issue:         ref,
		Note:          *note,
		Block:         *block,
		ProjectID:     projID,
		ReviewStatus:  *reviewStatus,
		BlockedStatus: *blockedStatus,
		BlockedLabel:  *blockedLabel,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		b, _ := json.Marshal(map[string]interface{}{
			"number":     res.Number,
			"url":        res.URL,
			"title":      res.Title,
			"new_status": res.NewStatus,
			"label":      res.LabelAdded,
		})
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("handed off #%d → %s\n", res.Number, res.NewStatus)
	fmt.Printf("  url:   %s\n", res.URL)
	if res.LabelAdded != "" {
		fmt.Printf("  label: %s\n", res.LabelAdded)
	}
	return nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		project   projectFlag
		agentName = fs.String("agent-name", "", "only items whose sub-agent value matches NAME")
		mine      = fs.Bool("mine", false, "only items held by you (sub-agent matches viewer, or assignee == viewer)")
		jsonOut   = fs.Bool("json", false, "emit JSON")
	)
	fs.Var(&project, "project", "project mode; bare form uses env/config project id, or pass --project=PVT_xxx")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: gh claim-issue list [--agent-name NAME] [--mine] [--project[=ID]]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := config.Load()
	if err != nil {
		return err
	}
	gh, err := ghapi.New()
	if err != nil {
		return err
	}
	projID, err := resolveProjectID(&project, cfg)
	if err != nil {
		return err
	}
	if projID == "" {
		return errors.New("list requires a project id (set project_id in config, export " + envProjectID + ", or pass --project=ID)")
	}

	entries, err := cmds.List(gh, cfg, cmds.ListOptions{
		ProjectID: projID,
		AgentName: *agentName,
		Mine:      *mine,
	})
	if err != nil {
		return err
	}

	if *jsonOut {
		b, _ := json.Marshal(entries)
		fmt.Println(string(b))
		return nil
	}
	if len(entries) == 0 {
		fmt.Println("no matching items")
		return nil
	}
	for _, e := range entries {
		agent := e.AgentName
		if agent == "" {
			agent = "-"
		}
		status := e.Status
		if status == "" {
			status = "-"
		}
		extra := ""
		if e.Iteration != "" {
			extra = " [" + e.Iteration + "]"
		}
		fmt.Printf("%-30s #%-5d %-12s %-20s %s%s\n", e.Repo, e.Number, truncate(status, 12), truncate(agent, 20), e.Title, extra)
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

// resolveProjectID picks the effective project id. Priority:
//   1. explicit --project=VALUE
//   2. GH_CLAIM_ISSUE_PROJECT_ID env var
//   3. config.ProjectID
//
// The bare --project form forces a project mode and falls back through
// the same priority list (skipping itself).
func resolveProjectID(flagVal *projectFlag, cfg *config.Config) (string, error) {
	if flagVal.present {
		if flagVal.value != "" {
			return flagVal.value, nil
		}
		if env := strings.TrimSpace(os.Getenv(envProjectID)); env != "" {
			return env, nil
		}
		if cfg.ProjectID != "" {
			return cfg.ProjectID, nil
		}
		return "", fmt.Errorf("--project given without a value, but %s is not set and project_id is not in config", envProjectID)
	}
	if env := strings.TrimSpace(os.Getenv(envProjectID)); env != "" {
		return env, nil
	}
	return cfg.ProjectID, nil
}

// resolveRepo parses --repo, or auto-detects the current repo in repo mode.
// In project mode, --repo is optional; omitting it means no repo filter.
func resolveRepo(flagValue string, projectMode bool) (owner, name string, err error) {
	if flagValue != "" {
		parts := strings.SplitN(flagValue, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf("invalid --repo %q (want owner/name)", flagValue)
		}
		return parts[0], parts[1], nil
	}
	if projectMode {
		return "", "", nil
	}
	r, err := repository.Current()
	if err != nil {
		return "", "", errors.New("could not detect current repo; pass --repo owner/name")
	}
	return r.Owner, r.Name, nil
}

func printUsage(w io.Writer) {
	adj, noun := names.Counts()
	fmt.Fprintf(w, `gh claim-issue — claim the first available GitHub issue (multi-agent safe)

Usage:
  gh claim-issue [flags]                       claim the first available issue
  gh claim-issue release [flags]               clear your claim on an issue
  gh claim-issue set-status <ISSUE> <STATUS>   move an issue's project Status
  gh claim-issue handoff <ISSUE> --note "…"    set-status + comment in one op
  gh claim-issue list [flags]                  show items + who holds each
  gh claim-issue generate-name                 print one random agent name
  gh claim-issue init-config                   scaffold the config file

Flags (claim):
  --repo owner/name        target repo (default: current repo; optional in project mode)
  --project[=ID]           claim from a Projects v2 board; bare form uses
                           %s if set, otherwise project_id
                           from config. --project=PVT_xxx overrides both.
  --agent-name NAME        sub-agent identifier (required when sub_agent_field is set)
  --generate-agent-name    pick a random adjective-noun name (%d×%d=%d combos)
  --lock-wait DURATION     wait this long for the local mutex (default 30s)
  --dry-run                resolve the candidate without mutating anything
  --json                   print the claim result as a single JSON line

Flags (release):
  --issue N                issue to release (default: the one you currently hold)
  --repo owner/name        repo for --issue when not the current repo
  --project[=ID]           project context (same precedence as on claim)
  --force --agent-name X   release a claim stamped to agent X (recovery)
  --release-status STATUS  move the project item to this Status after release
  --json                   print the release result as JSON

Flags (set-status):
  ISSUE accepts: N, #N, owner/repo#N, or owner/repo/N. STATUS is the option
  name on the project's Status field (e.g. "In Progress").

Flags (handoff):
  --note "…"               required: the handoff message body (posted as a comment)
  --block "reason"         move to Blocked instead of Review; applies the "blocked" label
  --review-status NAME     status used when not blocking (default "Review")
  --blocked-status NAME    status used when blocking   (default "Blocked")
  --blocked-label NAME     label applied when blocking (default "blocked")

Flags (list):
  --agent-name NAME        only items whose sub-agent value matches NAME
  --mine                   only items held by you (sub-agent or assignee)
  --json                   print rows as JSON

Defaults:
  An issue is available when it is OPEN, unassigned, and not blocked by a
  GitHub issue dependency. A YAML file at $XDG_CONFIG_HOME/gh-claim-issue/
  config.yml may further restrict availability — run "init-config" to
  scaffold one. Project id precedence is --project=ID > %s
  > config.project_id.

  In project mode (--project, config.project_id, or %s) the
  candidate pool is the project's items across every repo. Pass --repo
  to narrow it to one repo; omit --repo to consider the entire project.

A local file mutex serialises concurrent agents on the same machine racing
for the same backlog (keyed by project in project mode, by owner/repo
otherwise), so only one agent can mutate at a time.
`, envProjectID, adj, noun, adj*noun, envProjectID, envProjectID)
}
