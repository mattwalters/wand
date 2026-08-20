---
title: wand pm
weight: 185
summary: Propose a set of tickets from a decided product brief, then bless the set into Triage.
---

`pm` is the stage-zero orchestrator: a decided product brief in, a
hard-validated set of proposed tickets out. It is the sibling of
[`wand scope`](../scope/) one stage earlier in the lifecycle —
idea → pm → (bless) → scope → (bless) → run — and it borrows scope's
central discipline whole: an invalid proposal writes nothing.

The brainstorm itself does not need wand; that happens in any chat. What pm
owns is the last mile: a decided initiative becoming well-formed,
dependency-ordered tickets that `wand dispatch` can eventually drain.

## Two commands, not one

pm splits propose from bless, and the split is batch-shaped on purpose —
there is no resident conversational surface, and none of this blocks on a
terminal:

```
wand pm [brief-file] [--team-key KEY] [--harness NAME] [--model M] [--effort E]
wand pm bless <proposal-file> [--team-key KEY]
```

1. **`wand pm`** reads a brief — a file argument, or stdin when none is
   given — sends a cold scout over it and this team's board (existing
   projects, and every Triage and Backlog title), and validates what the
   scout hands back. A valid proposal is written to a file — JSON and a
   human-readable Markdown rendering beside it — under this run's journal
   directory, and its path is printed. **No Linear write happens on this
   path at all**: read the file, edit it if you want to change something
   the scout got wrong, then bless it.
2. **`wand pm bless`** re-parses and re-validates that file against the
   same schema, re-checks it against whatever the board looks like *now*
   (a title or a project may have moved since the file was written), and
   files the whole set as the single writer: any new projects first, then
   every ticket into Triage — with the `agent-filed` label, no priority, no
   assignee, exactly where [`wand file`](../file/) lands an agent's own
   finding — in `blocked_by`-dependency order, then every `blocked_by`
   relation last, once every ticket in the batch exists.

Nothing here promotes anything. Every ticket lands in Triage; blessing it
onward to Todo, the same as blessing a scope's Needs Input, stays a human's
act. That is also why `wand pm bless` is a CLI command at all, unlike the
promotion a human makes in [`wand ui`](../ui/): filing into Triage is a
status an agent may already set on its own (`wand file` does exactly this
today), so the file-then-bless split adds a validated, reviewable proposal
in front of that write without granting anything the guard exists to keep
out of agents' hands.

## Nothing is written unless the whole proposal is valid

Every rule below is checked before the first byte is written anywhere,
proposal file or Linear:

- the premise is `"sound"` or `"wrong"` — a brief already done, or already
  covered by an existing ticket, is reported rather than proposed around;
- at least one ticket, each with a title and a body stating the goal and
  what is still open;
- every `blocked_by` entry names another title in the *same* proposed set,
  exactly — never a ticket already on the board, never itself, and never
  part of a cycle;
- every proposed project has a name and an argument for why it needs to
  exist;
- titles are unique within the set — `blocked_by` resolves by title, and
  two tickets sharing one could not be told apart.

A misspelled field is a loud failure, the same as in `wand scope`'s
handoff.

### When the scout says the brief is wrong

A scout that finds the brief already done, or already tracked by an
existing ticket, reports that instead of proposing tickets around it.
Nothing is written — no file, no Linear call — the account goes to the
journal and to stdout, and the command exits `2`.

## The board-read stage

Before the scout drafts anything, pm reads the team's existing projects and
every open Triage and Backlog title, and hands that summary to the scout —
so it proposes a new project only when nothing already covers the work, and
does not propose a ticket the board already carries. Past the draft, pm
runs one near-duplicate search per proposed title (the same search
`wand file` runs before filing) and folds any hits into the rendered
proposal, so possible duplicates are in front of you before you bless
anything.

`wand pm bless` re-runs the exact-title half of that check against the live
board — not the fuzzy search, which you have already weighed by the time
you bless — and refuses if a title in the file now exactly matches
something already in Triage or Backlog. The board can move between propose
and bless; the file cannot un-know what it said when it was written.

## The proposal file's schema

A written proposal carries a `schema_version` field, the same idea as
[the covenant's own `schema`](../../covenant/): the file may be read back by
a different `wand` than the one that wrote it, once you have edited it.
`wand pm bless` refuses a file declaring a schema newer than it speaks
rather than guess at fields it does not know.

## The batched write's order

Bless writes in a fixed order because later writes depend on earlier ones
having landed:

1. **New projects**, so their ids are ready for every ticket that
   references one.
2. **Tickets**, in `blocked_by`-dependency order — a ticket's own blockers,
   if also proposed, are filed before it.
3. **`blocked_by` relations**, last, once every ticket in the batch exists.

A failure partway through the batch stops rather than retries or guesses at
what to skip: the run parks, and the journal — and the command's own
output — say exactly what had already landed, so nothing is filed twice
and nothing is silently missing.

## Flags

### `wand pm`

| Flag | Description |
|---|---|
| `--team-key KEY` | Linear team key, e.g. `WND` (falls back to `[team] key` in `wand.toml`). |
| `--harness NAME` | Worker harness: `claude-code` (default) or `codex`. |
| `--model M` | Model for the scout. Default: the harness's own. |
| `--effort E` | Reasoning effort for the scout. Default: the harness's own. |

### `wand pm bless`

| Flag | Description |
|---|---|
| `--team-key KEY` | Linear team key, e.g. `WND` (falls back to `[team] key` in `wand.toml`). |

## Exit codes

### `wand pm`

| Code | Meaning |
|---|---|
| `0` | Proposed. The proposal file's path was printed. |
| `1` | The run never started: no API key, no brief, no team key. Nothing was written. |
| `2` | Handed back. The scout judged the brief's premise wrong; nothing was proposed. |
| `3` | Parked. The run stopped without deciding — a board read failed, an unusable handoff, a worker failure. The journal says which. |

### `wand pm bless`

| Code | Meaning |
|---|---|
| `0` | Filed. Every project and ticket in the proposal landed, and every `blocked_by` relation was wired. |
| `1` | Refused before any write: the file did not parse, its schema is newer than this binary speaks, or the board has drifted under it. |
| `3` | A write failed partway through the batch. The journal, and the command's own output, say exactly what had already landed. |

## The journal

Both commands are journaled runs, the same discipline `wand scope` and
`wand run` follow: every phase recorded before it happens, exactly one
terminal record. Because pm's input is a brief rather than an existing
Linear ticket, the journal names a synthetic identifier derived from the
brief's own content rather than a real one — `wand sweep`'s dead-lease
recovery still runs against it uneventfully; the hand-back it attempts on a
synthetic identifier simply fails and is logged, the same best-effort
handling any hand-back failure gets there.

## Requirements

`LINEAR_API_KEY` in the environment, and — for `wand pm` — the harness on
`PATH` and a git repository to run in. `wand pm bless` needs no harness: it
spawns no worker, only reads a file and writes to Linear.

## Examples

```bash
wand pm brief.md
```

```
proposing from a brief (812 bytes), journaling to ~/.local/state/wand/runs/pm-4f2a9c1e0b7d-20260820T090000Z
phase scout: spawning a cold worker (claude-code)
wrote the proposal: …/proposal.json (…/proposal.md)
run pm-4f2a9c1e0b7d-20260820T090000Z ended: converged — proposed 3 ticket(s) and 1 new project(s); written to …/proposal.json
proposal: ~/.local/state/wand/runs/pm-4f2a9c1e0b7d-20260820T090000Z/proposal.json
```

Read `proposal.md`, edit `proposal.json` if you want changes, then:

```bash
wand pm bless ~/.local/state/wand/runs/pm-4f2a9c1e0b7d-20260820T090000Z/proposal.json
```

## See also

[`wand scope`](../scope/) is the next stage: once a human promotes one of
pm's filed tickets into Scoping, scope researches it into a plan.
