---
title: wand doctor
weight: 120
summary: Diff the live Linear team against the covenant. Writes nothing.
---

`doctor` reads the team's statuses, labels, PR automations and settings,
diffs them against the covenant, and reports the drift. It writes nothing —
[`wand init`](../init/) is the verb that repairs.

## Synopsis

```
wand doctor --team-key KEY
```

## What it does

The diff is [`wand init`](../init/)'s own plan, read rather than applied,
plus the checks a plan cannot express: team settings like triage and the
estimate scale, which are properties of the team rather than actions
against it.

`doctor` also makes one filesystem check, alongside its Linear diff: each
harness shim `init` writes (`.claude/settings.json`, `.codex/hooks.json`)
that is present and already matches what `init` would write, but is not
tracked by git, is reported as drift — a shim generated but never
committed protects only the checkout it ran in, which is exactly the
[`wand init`](../init/) doc's warning made concrete. This check is purely
local; it runs before the Linear API is ever called, and if it cannot be
answered (no git, or the working directory is not a repository), that is
reported as exit `2`, the same as any other could-not-check, taking
precedence over whatever the Linear diff would have said.

Findings print one per line, prefixed `drift:`, followed by a count. A
clean team prints one line and nothing else.

### What is not drift

Extra statuses outside the machine's path, extra labels, and automations on
events the covenant does not mention are tolerated strangers. The covenant
says what must be true of a board, not what may not also be true of it — a
team that added a `Waiting on vendor` column has not drifted, and a doctor
that said so would be a doctor teams learn to ignore.

## Flags

| Flag | Description |
|---|---|
| `--team-key KEY` | The Linear team key, e.g. `WAND`. Falls back to `[team] key` in the nearest `wand.toml`; required if neither is set. |

## Exit codes

The three codes are the point of this command — it is built to be a CI
step. The precondition half of `2` (no `LINEAR_API_KEY`, no resolvable
team key) is pinned by a test in `e2e/` against the compiled binary; `0`,
`1`, and the rest of `2` — the Linear diff, the repo-local shim check, and
how the two combine — need a live or faked Linear team to exercise, so
they are pinned in-process in `internal/doctor` instead, the tier that
tier can't reach.

| Code | Meaning |
|---|---|
| `0` | The team satisfies the covenant, and every installed shim is tracked by git. |
| `1` | Drift found: in the Linear diff, or an installed-but-untracked shim, or both. The findings are on stdout. |
| `2` | A check could not run: no `LINEAR_API_KEY`, no resolvable team key (neither `--team-key` nor `[team] key` in `wand.toml`), no team with that key, a broken `wand.toml`, an API failure, or the repo-local shim check itself failing (no git, or the working directory is not a git repository). The reason is on stderr. |

`1` and `2` are kept apart deliberately. A CI job that cannot tell "your
board drifted" from "I could not reach Linear" turns an outage into a
false accusation, and then into a job people disable.

## Examples

Check a board:

```bash
wand doctor --team-key WAND
```

As a CI step that fails on drift but is loud about an outage:

```bash
if wand doctor --team-key WAND; then
  echo "board is at the covenant"
else
  case $? in
    1) echo "::error::the board has drifted from wand.toml"; exit 1 ;;
    2) echo "::error::could not check the board"; exit 1 ;;
  esac
fi
```

Repair what it found:

```bash
wand init --team-key WAND --dry-run   # read the plan first
wand init --team-key WAND
```
