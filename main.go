package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/boblangley/gh-claim-issue/internal/claim"
	"github.com/boblangley/gh-claim-issue/internal/config"
	"github.com/boblangley/gh-claim-issue/internal/ghapi"
	"github.com/boblangley/gh-claim-issue/internal/names"
)

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
		case "help", "-h", "--help":
			printUsage(os.Stdout)
			return nil
		}
	}
	return cmdClaim(args)
}

// projectFlag is a string flag whose value is optional: `--project` alone
// means "use project_id from config", while `--project=ID` overrides it.
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
	)
	fs.Var(&project, "project", "project mode; bare form uses project_id from config, or pass --project=PVT_xxx")
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
	})
	if err != nil {
		return err
	}

	if *jsonOut {
		fmt.Printf(`{"number":%d,"url":%q,"title":%q,"assignee":%q,"agent_name":%q,"status":%q,"config":%q}`+"\n",
			res.Number, res.URL, res.Title, res.Assignee, res.AgentName, res.StatusMoved, cfgPath)
		return nil
	}
	fmt.Printf("claimed #%d %s\n", res.Number, res.Title)
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

// resolveProjectID picks the effective project id: explicit flag value wins,
// bare --project falls back to config, otherwise config (if any) is used.
func resolveProjectID(flagVal *projectFlag, cfg *config.Config) (string, error) {
	if flagVal.present {
		if flagVal.value != "" {
			return flagVal.value, nil
		}
		if cfg.ProjectID == "" {
			return "", errors.New("--project given without a value, but project_id is not set in config")
		}
		return cfg.ProjectID, nil
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
  gh claim-issue [flags]
  gh claim-issue generate-name
  gh claim-issue init-config

Flags (claim):
  --repo owner/name        target repo (default: current repo; optional in project mode)
  --project[=ID]           claim from a Projects v2 board; bare form uses
                           project_id from config, --project=PVT_xxx overrides
  --agent-name NAME        sub-agent identifier (required when sub_agent_field is set)
  --generate-agent-name    pick a random adjective-noun name (%d×%d=%d combos)
  --lock-wait DURATION     wait this long for the local mutex (default 30s)
  --json                   print the claim result as a single JSON line

Defaults:
  An issue is available when it is OPEN, unassigned, and not blocked by a
  GitHub issue dependency. A YAML file at $XDG_CONFIG_HOME/gh-claim-issue/
  config.yml may further restrict availability — run "init-config" to
  scaffold one.

  In project mode (--project or project_id in config) the candidate pool is
  the project's items across every repo. Pass --repo to narrow it to one
  repo; omit --repo to consider the entire project.

A local file mutex serialises concurrent agents on the same machine racing
for the same backlog (keyed by project in project mode, by owner/repo
otherwise), so only one agent can mutate at a time.
`, adj, noun, adj*noun)
}
