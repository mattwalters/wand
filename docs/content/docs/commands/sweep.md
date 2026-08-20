---
title: wand sweep
weight: 197
summary: Act on one thing left over after a run ended — everything dispatch's loop does not see.
---

`sweep` is everything that happens after [`wand run`](../run/) or
[`wand scope`](../scope/) exits. Each orchestrator ends in exactly one
terminal state, and each terminal state is where its own responsibility
stops; three things can still become true of a ticket afterward, and
nothing else in wand watches for them.

## Synopsis

```
wand sweep --team-key <key>
```

## What it acts on

One pass ranks every candidate it finds and writes against at most one —
the highest-ranked whose preflight does not refuse it:

1. **A lease whose owner is provably dead.** The journal says a run is
   still going and its lease's holder is gone — the zombie, a ticket
   In Progress with nothing behind it. Sweep reopens the run, parks its
   journal with the lease's own account, and hands the ticket back to a
   human, in that order, so the journal is never left saying "still going"
   after a person has already been told otherwise.
2. **A re-review label.** A human labeled a converged ticket
   `re-review` — another cycle, asked for explicitly. Sweep hands it back
   to a human with a comment naming the label.
3. **An unresolved thread on a ready-for-human PR.** Necessarily left
   *after* convergence: `wand run`'s own check for this runs only at the
   moment of converging, so a human review posted afterward is exactly
   what nothing else catches. Sweep hands the ticket back, quoting the
   thread count and the PR.

Ranked by severity, then oldest first: a dead lease before a re-review
before an unresolved thread, because a zombie is the one state actively
lying about itself on the board. A candidate whose preflight refuses it —
resolved in the interim, or the ticket closed — is skipped in the same
pass, never retried within it, so one unstartable candidate cannot starve
every other one behind it forever. The next pass re-evaluates everything
fresh.

## What it reports, and never acts on

Every pass also reports tickets sitting In Progress with **no journal run
behind them at all** — not even a dead one. There is nothing there to
reap: no lease to prove dead, no run to reopen. "Looks stuck" and "is
stuck" are different claims, and only a person can tell which this is, so
sweep prints it and moves on.

## Exit codes

`sweep` exits `0` on a completed pass — whether or not it found anything
to act on — and `1` on a failure that stopped the pass before it could
finish reading or acting, with the reason on stderr.

## Requirements

`LINEAR_API_KEY` and an authenticated `gh` (sweep reads a converged
ticket's PR the same way `wand run`'s own convergence check does — the
checkout it is run from, since a converged run already removed its own
worktree). Run it from inside the repository.

## See also

[`wand dispatch`](../dispatch/) is the selector that runs a ticket in the
first place; [`wand run`](../run/) is where the re-review and
unresolved-thread conditions originate; the `re-review` label is covenant
topology, created by [`wand init`](../init/) like `ready-for-human` and
`human-only`.
