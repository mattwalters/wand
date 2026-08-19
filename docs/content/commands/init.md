---
title: wand init
weight: 110
summary: Bootstrap a Linear team to the covenant and install the guard hook.
---

`init` brings a repository under wand: it creates or adopts the repo's
Linear team, brings that team to the covenant, and installs the hook that
routes the repo's agent sessions through [`wand guard`](../guard/).

## Synopsis

```
wand init --team-key KEY [--team-name NAME] [--dry-run]
```

## What it does

Four things, in this order:

1. **Loads the covenant.** A checked-in `wand.toml` in the working
   directory parameterizes it; absent one, the stock covenant applies.
   Which one was used is printed, because a run that silently fell back to
   defaults looks exactly like one that read your file.
2. **Installs the guard hook**, into `.claude/settings.json`. This comes
   first because it is the only purely local step: from here on, this
   repo's own sessions are under the guard, even if the Linear half fails.
   The entry routes tool calls matching `mcp__.*__save_issue` to
   `wand guard`. The matcher is loose in the middle on purpose — the Linear
   MCP server's connector id changes when Linear is reconnected, and a
   matcher pinned to today's id would go dead silently, which for a guard
   means allowing everything with no sign of it. Existing settings are
   merged, not clobbered; every key `init` does not need to touch survives.
3. **Creates or adopts the team.** A team with the given key already in the
   workspace is adopted, not duplicated.
4. **Plans and applies the difference** between the team and the covenant:
   the workflow statuses, the labels, the PR automations, the team
   settings. Every decision is made in the plan, which `--dry-run` prints
   without writing.

`init` is idempotent. A team already at the covenant plans no writes and
says so. Run it again after a covenant change to bring the board along.

The command assumes `wand` is on `PATH` — the hook entry it writes is the
literal command `wand guard`, not an absolute path, because
`.claude/settings.json` is checked in and shared across machines. `make
install` or `go install` arranges this.

### It verifies rather than assumes

Both write paths read back what they wrote. After installing the hook,
`init` re-reads the settings file and re-checks it; after applying the
plan, it re-reads the team and re-plans, and fails if anything is still
outstanding. "Installed" and "done" mean observed.

## Flags

| Flag | Description |
|---|---|
| `--team-key KEY` | The Linear team key, e.g. `WAND`. **Required.** |
| `--team-name NAME` | Team name to use when creating. Defaults to the key. Ignored when adopting. |
| `--dry-run` | Print the plan and write nothing — the hook install included. |

## Exit codes

| Code | Meaning |
|---|---|
| `0` | The team satisfies the covenant, and the hook is installed. |
| `1` | Anything went wrong: no `LINEAR_API_KEY`, no `--team-key`, an API failure, a broken `wand.toml`, or a post-apply verification that still found work outstanding. |

## Examples

Bootstrap a new team:

```bash
export LINEAR_API_KEY=lin_api_...
wand init --team-key WAND --team-name Wand
```

See what a run would do to an existing board, without touching it:

```bash
wand init --team-key WAND --dry-run
```

Re-run after editing `wand.toml`, to bring the board to the new
parameters:

```bash
wand init --team-key WAND
```

## See also

[`wand doctor`](../doctor/) reports the same difference without repairing
it — the read-only half of this command, and the one to put in CI.
