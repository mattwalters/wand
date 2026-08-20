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
| `--team-key KEY` | The Linear team key, e.g. `WAND`. **Required.** |

## Exit codes

The three codes are the point of this command — it is built to be a CI
step, and the codes are pinned by a test in `e2e/` so they cannot drift
from what is written here.

| Code | Meaning |
|---|---|
| `0` | The team satisfies the covenant. |
| `1` | Drift found. The findings are on stdout. |
| `2` | The check could not run: no `LINEAR_API_KEY`, no `--team-key`, no team with that key, a broken `wand.toml`, or an API failure. The reason is on stderr. |

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
