# gh-claim-issue

A [`gh`](https://cli.github.com/) extension that hands the first available
GitHub issue to the calling agent, coordinating safely with other agents on
the same machine through a local file mutex.

Designed for orchestration setups where several worker agents share one
backlog: each one calls `gh claim-issue`, takes whichever issue it gets,
and the next call returns a different one.

## Install

```sh
gh extension install wyrd-company/gh-claim-issue
```

## Usage

```sh
# Claim the oldest open, unassigned, unblocked issue in the current repo:
gh claim-issue

# Claim from a specific repo, with a random adjective-noun agent name:
gh claim-issue --repo wyrd-company/widgets --generate-agent-name

# Bring your own agent name (required if sub_agent_field is configured):
gh claim-issue --agent-name eldritch-netrunner

# Claim from a Projects v2 board across every repo it spans
# (uses project_id from config):
gh claim-issue --project

# Override the configured project, and narrow to one repo:
gh claim-issue --project=PVT_kwDOABCDEF --repo wyrd-company/widgets

# Machine-readable output:
gh claim-issue --json
```

### Subcommands

| Command | Description |
|---|---|
| `gh claim-issue` | Claim the first available issue (default flow). |
| `gh claim-issue generate-name` | Print one random `adjective-noun` name (50×50 = 2,500 combos, last 20 suppressed). |
| `gh claim-issue init-config` | Scaffold a commented `config.yml` at the default location. |
| `gh claim-issue help` | Print usage. |

## Availability rules

By default an issue is **available** when it is:

1. Open,
2. Unassigned, and
3. Not blocked by a [GitHub issue dependency](https://docs.github.com/en/issues/tracking-your-work-with-issues/configuring-issues/about-issue-dependencies) (`/issues/N/dependencies/blocked_by`).

The "first" available issue is the oldest by creation date (FIFO).

Additional rules can be layered in via configuration.

### Repo vs project scope

By default the pool is the issues in a single repository — current repo
unless `--repo owner/name` says otherwise.

When `--project` is passed (or `project_id` is set in config), the pool is
the items on that Projects v2 board across every repo. `--repo` becomes
**optional** in project mode: pass it to narrow the pool to one repo on
the board, or omit it to consider the whole project.

`--project` accepts an optional value:

| Form | Effect |
|---|---|
| `--project` | Use `project_id` from config. Errors if not set. |
| `--project=PVT_kwDOA...` | Override (or supply) the project id. |

Note: `--project VALUE` (space-separated) does **not** consume the next
arg as the value — use the `=` form.

## Configuration

Optional. Lives at `$XDG_CONFIG_HOME/gh-claim-issue/config.yml`
(or the OS-equivalent user config dir). Run `gh claim-issue init-config` to
write a commented starter file.

```yaml
# Name of an organisation-level GitHub Issue Field (text type) used by
# agents to stamp their identity on an issue when they claim it. With
# this set:
#   - an issue is unavailable while this field already has a value
#   - an agent cannot claim if it already holds an open issue tagged
#     with its own --agent-name in this field (checked across every
#     repo visible to the viewer)
sub_agent_field: "Claimed By"

# Drop issues carrying any of these labels.
exclude_labels:
  - blocked
  - needs-triage

# Require every issue to carry all of these labels.
require_labels:
  - ready

# Restrict to a Projects v2 board (GraphQL node id, e.g. PVT_kwDOA...).
project_id: "PVT_kwDOABCDEF"

# When project_id is set, only items in these Status values are eligible.
project_statuses:
  - Todo
  - Ready

# When project_id is set, move the claimed item to this Status option
# immediately after assignment.
claim_status: "In Progress"
```

### About `sub_agent_field`

Uses GitHub's [organisation-level Issue Fields](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/adding-and-managing-issue-fields)
(public preview as of May 2026). Define a `text`-typed field on the
org under **Settings → Issues → Issue fields**, then reference it by
name in `sub_agent_field`. Because issue fields live at the org level
and apply to every issue in every repo, the "one open issue per agent
name" check is genuinely global.

### About `claim_status`

Requires `project_id`. The value must be one of the options on the
project's `Status` single-select field. A typo fails the run before
any mutation happens.

## Multi-agent coordination

Each invocation takes a cross-process file lock (via `flock(2)`) stored
in the user's cache dir. The key is the project id when in project mode
(so all agents racing for the same project pool serialise, regardless of
any `--repo` filter), and `owner/repo` otherwise. So if two agents on
the same machine race for the same backlog:

```
agent A: gh claim-issue   ┐
agent B: gh claim-issue   ├──► serialised → A gets #42, B gets #43
```

Only one agent can be inside the *query → filter → assign → stamp →
move-status* window at a time, so the same issue can never be handed
out twice. The window is bounded by `--lock-wait` (default 30s).

For coordination *across* machines, rely on GitHub itself: the
assignment step is the authoritative claim — a second machine reaching
the assign call after the first will see the issue already taken on
its next listing.

## Agent names

`generate-name` (and `--generate-agent-name`) draw from 50 geeky
adjectives × 50 fantasy / mythology / sci-fi / cyberpunk nouns
(2,500 combinations). The last 20 names handed out are remembered in
`<cache>/gh-claim-issue/name-history` and suppressed from future
generation, so short-window collisions effectively never happen.

## Files

| Path | Purpose |
|---|---|
| `$XDG_CONFIG_HOME/gh-claim-issue/config.yml` | Configuration (optional). |
| `<cache>/gh-claim-issue/<sha>.lock` | Per-repo claim mutex. |
| `<cache>/gh-claim-issue/name-history` | Rolling 20-name history. |
| `<cache>/gh-claim-issue/name-history.lock` | History mutex. |

## Exit codes

- `0` — issue claimed (printed to stdout).
- `1` — anything else: no available issue, lock timeout, API error, or
  configuration problem. The reason is printed to stderr.
