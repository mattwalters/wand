---
title: wand run
weight: 196
summary: Own one ticket from claim to a terminal state — implement, CI, review, revise.
---

`run` is the core orchestrator: one process owning one blessed ticket from
claim to a terminal state, spawning a cold worker per phase. Workers do the
work and are mute; the orchestrator makes **every** Linear and GitHub
write.

## Synopsis

```
wand run <identifier> [--harness <name>] [--model <m>] [--effort <e>]
```

## What it does

`run` claims the ticket exactly as [`wand claim`](../claim/) does — in
Todo, not `human-only`, no unresolved blockers, one write — then opens a
run in the journal and works the loop in a run-private git worktree, on the
branch Linear names for the ticket, started from the remote-tracking ref of
the repository's default branch (`origin/main`, not a local `main` that may
be stale or absent):

1. **implement** — a cold worker gets the whole ticket (description and
   comments) and commits its work in the worktree.
2. **CI** — the orchestrator runs the covenant's verify command. Every
   failure spawns a cold **fix-CI** worker, up to `caps.ci_attempts`
   failures across the whole run.
3. Verify green, the orchestrator pushes and **opens the PR** — titled
   `[<identifier>] …` at open, repaired at convergence, with the ticket
   referenced in the body as a gloss (identifier, title, link), never a
   bare id. Conventions the orchestrator can make true are made true in
   code, not remembered by agents.
4. **review** — a cold reviewer reads the branch against the ticket and
   returns a verdict. Findings without a concrete failure scenario are
   dropped in code before anything downstream sees them.
5. **revise** — surviving findings go to a cold reviser, verify runs
   again, the branch is pushed again, and a fresh reviewer takes the next
   round, up to `caps.review_rounds` rounds.

Workers never write Linear or GitHub — they cannot: their environment is
stripped of every credential, which the isolation conformance suite proves
per harness. They commit; the orchestrator pushes, opens and titles the PR,
comments, labels and moves the ticket.

## The three terminal states

Every run ends in exactly one, journaled by the run journal:

**Converged** — the reviewer approved *on positive evidence* (its handoff
must state what it verified), and no human review thread on the PR stands
unresolved. A thread a revision has outdated still counts as unresolved —
outdated is not answered — and standing threads hand the run back instead
of converging over them. On convergence the orchestrator posts the summary
to the ticket, adds the `ready-for-human` label, moves the ticket to
In Review, and removes the now-clean worktree. The PR is a human's to
merge.

If a human merges it first — between the reviewer's approval and the
orchestrator's last look, a race the loop cannot prevent — that is still
convergence, not a failure: the work landed, which is what the loop was
for. The run posts its summary and stops there. It does **not** move the
ticket to In Review, because the merge automation owns the status now and
In Review over a ticket the merge already closed would reopen a close,
which is a human's call. A PR that is genuinely absent, or closed without
merging, still parks.

**Handed back** — a human owns the next move. The reason is posted as a
comment **first**, then the ticket moves to Needs Input — in that order,
always, so a failure between the two never leaves a Needs Input ticket
that asks nothing. Hand-backs happen when:

- a worker reports itself blocked — the comment quotes the worker's own
  account verbatim, never a canned guess inferred from the phase;
- the fix-CI cap runs out — the comment says so and quotes the last
  failure;
- the review-round cap runs out — the comment quotes the final round's
  findings whole. Convergence only ever happens on positive evidence,
  never on the exhaustion of a counter: a cap that mislabels a real
  final-round finding as "a disagreement" hands work back unattempted.

**Parked** — the run stopped without deciding: an interrupt (the signal is
the recorded reason), a worker that left no parseable handoff, a dirty
tree, a worker timeout. A reviewer that produces no parseable handoff
**parks** rather than converges — anything else would turn every reviewer
crash into a clean pass. The journal is written first and is the run's real
ending, so a park is reachable even when Linear itself is what failed; the
ticket then gets the same sentence as a comment and the `parked` label,
best-effort and never fatal. The worktree is preserved for inspection.

The mark is a label rather than a status move. A park reports that the
machine stopped, not that the work was judged — often for a reason that has
nothing to do with the ticket, like a host that slept mid-phase — so the
ticket keeps its place and its blessing. Remove the label once you have
looked and the ticket is dispatchable again.

A later run of the same ticket resumes its branch: a preserved worktree
that is clean (everything committed on the branch) is removed and replaced;
one holding uncommitted work makes the new run refuse, naming the old
worktree's path — work at risk is a human's call, never collateral of a
resume. Only wand's own worktrees, though. If the branch is checked out
somewhere you made — your own `git worktree`, or the repository itself —
the run refuses and names it rather than removing it: `git worktree remove`
declines uncommitted *tracked* work and nothing else, so a clean checkout
would go quietly and take its ignored files (`.env`, build caches) with
it.

## What the handoff carries back

Beyond its status, a worker's handoff can carry **description
corrections** — exact ticket wording its work disproved — and **plan
deviations**. The orchestrator applies corrections to the ticket (quoting
the old wording in a comment first, in the same act) and carries deviations
into the PR body, the terminal comment — converged or handed back alike —
and the run journal. One writer per ticket, and deviations reach the ticket
instead of dying in a worker's transcript. A deviation reported by a revise
worker arrives after the PR body was written, so the hand-back and the
journal are what carry it; nothing about a run's ending is allowed to be
the place its account gets dropped.

## Flags

| Flag | Meaning |
|---|---|
| `--harness` | Worker harness: `claude-code` (default) or `codex`. |
| `--model` | Model for every worker. Default: the harness's default. |
| `--effort` | Reasoning effort for every worker. Default: the harness's default. |

## Exit codes

The codes are a contract a scheduler can read:

| Code | Meaning |
|---|---|
| `0` | Converged: In Review, `ready-for-human`, PR open — or the PR already merged, in which case the summary is posted and the status left to the merge automation. |
| `1` | The run never started — refused claim, missing configuration, no journal. Nothing to sweep. |
| `2` | Handed back: Needs Input, with the reason posted as a comment. |
| `3` | Parked: the journal has the reason, the ticket carries it as a comment and the `parked` label, and the worktree is preserved. |

Exit 1 keeps its "nothing to sweep" promise even when the failure lands
after the claim: a run whose journal will not open hands the ticket
straight back (Needs Input, with the store failure as the comment) before
exiting.

## Requirements

- `LINEAR_API_KEY` in the environment.
- An authenticated `gh` (the orchestrator pushes and opens the PR).
- `commands.verify` in [`wand.toml`](../../covenant/) — `run` refuses to
  start without it, because it cannot tell green from red.
- Run from inside the repository the ticket is about.

The caps come from the covenant: `caps.review_rounds`, `caps.ci_attempts`
and `caps.worker_timeout_minutes`, with the stock values applying absent a
covenant file.

## Examples

```bash
wand run WND-42
```

```
claimed WND-42: In Progress, assigned to Ada Lovelace
run WND-42-20260819T120000Z journaling to ~/.local/state/wand/runs/WND-42-20260819T120000Z
worktree …/tree on branch ada/wnd-42-adapter-codex (base main)
phase implement round 1: spawning worker (claude-code)
verify: make check
verify: green
opened PR https://github.com/you/repo/pull/7
phase review round 1: spawning worker (claude-code)
run WND-42-20260819T120000Z ended: converged — reviewer approved on round 1; PR … is ready for a human
```

## See also

[`wand claim`](../claim/) is the claim this command starts with;
[`wand handback`](../handback/) is the ordering rule its hand-backs reuse;
[`wand queue`](../queue/) is where the next identifier comes from.
