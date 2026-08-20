---
title: wand run
weight: 195
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
branch Linear names for the ticket:

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
crash into a clean pass. Parking writes only the journal, deliberately, so
it is reachable even when Linear itself is what failed. The worktree is
preserved for inspection.

A later run of the same ticket resumes its branch: a preserved worktree
that is clean (everything committed on the branch) is removed and replaced;
one holding uncommitted work makes the new run refuse, naming the old
worktree's path — work at risk is a human's call, never collateral of a
resume.

## What the handoff carries back

Beyond its status, a worker's handoff can carry **description
corrections** — exact ticket wording its work disproved — and **plan
deviations**. The orchestrator applies corrections to the ticket (quoting
the old wording in a comment first, in the same act) and carries deviations
into the PR body and the terminal comment. One writer per ticket, and
deviations reach the ticket instead of dying in a worker's transcript.

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
| `0` | Converged: In Review, `ready-for-human`, PR open. |
| `1` | The run never started — refused claim, missing configuration, no journal. Nothing to sweep. |
| `2` | Handed back: Needs Input, with the reason posted as a comment. |
| `3` | Parked: the journal has the reason; the worktree is preserved. |

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
