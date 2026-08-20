---
title: wand dispatch
weight: 196
summary: Pick the one ticket to run next, and run it — the selector over the loop.
---

`dispatch` is a thin selector over [`wand run`](../run/) and
[`wand scope`](../scope/): a read-mostly pass that picks the one ticket a
repository works next and runs it through whichever of those two
orchestrators the ticket belongs to.

## Synopsis

```
wand dispatch [--team-key <key>] [--harness <name>] [--model <m>] [--effort <e>]
wand dispatch [--team-key <key>] --watch [--interval <duration>] [...]
```

## What it does

The Todo gate lives here, deliberately, and not in `run` or `scope`
themselves: a human typing `wand run WND-9` has already made the decision
that WND-9 is the ticket to work. An unattended selector has not, so
`dispatch` makes it the same way [`wand queue`](../queue/) prints it —
ranked, then vetted, nothing dropped silently.

One pass is:

1. **Lock.** Take this repository's own dispatch lock — a directory and a
   pid, not a file lock: only one dispatch process may select for a given
   repo at a time, and a directory holding a small pid file is inspectable
   and reclaimable by hand the way the `flock(1)` idiom a cron job might
   otherwise reach for is, on a platform that ships it. A holder still
   running refuses; a holder provably dead on this host is reclaimed and
   retried once; a holder on another host is never assumed dead.
2. **Gc dead leases.** Every run this repository has in flight occupies a
   lane, up to `caps.lanes`. Read-only: a run whose lease says its holder
   is provably dead does not hold a lane, whatever phase its journal last
   opened — the same [journal.Report.Zombie] proof `wand sweep` acts on.
3. **Read and rank.** Todo and Scoping, each ranked and vetted the way
   `wand queue` ranks Todo.
4. **Pick the winner.** The highest-ranked, vetted Todo issue when a lane
   is free, through `wand run`; otherwise the highest-ranked, vetted
   Scoping issue, through `wand scope`. A scope needs no lane, so an
   eligible Scoping ticket dispatches even at full lane occupancy —
   research is never starved by full lane occupancy — and it is also what
   a pass falls back to when Todo simply has nothing startable.
5. **Run it.** One ticket per pass, to one of `run`'s or `scope`'s own
   terminal states.

## `--watch`

`--watch` polls instead of running one pass and exiting. Each tick counts
lanes (including winners this session has already spawned but that may not
have registered a journal run yet), and when a winner exists and a lane is
free — or it needs none — spawns it as a **detached child process**: its
own session, so neither this process's own context being canceled nor a
signal to its controlling terminal reaches the child. That child survives
the watcher, which is what lets several lanes run at once from one `--watch`
session. Logging is state-change-only: a tick that changes nothing prints
nothing, so a long watch does not drown its own signal in a poll interval's
worth of repeated silence.

The dispatch lock is held once, for the watch loop's whole lifetime.
Concurrency across lanes comes from the children it spawns, never from a
second `wand dispatch` process racing the first's selection — that second
process refuses, locked, exactly as a single-shot pass would.

Each spawned child's own narration (the same lines `wand run` or
`wand scope` prints when you run them yourself) goes to a log file under
the run journal's state directory, one file per child, since the journal
itself only carries the structured half of that account.

## Exit codes

A scheduler's whole view of a pass is a status and a log, so every ending a
single-shot pass can reach gets its own code:

| Code | Meaning |
|---|---|
| `0` | Converged. |
| `1` | Refused: nothing started (bad flags, missing configuration), or the winner was chosen but its claim raced and lost. |
| `2` | Handed back or parked — the run/scope journal has the detail. |
| `3` | Locked: another dispatch process already holds this repo's selector lock. |
| `4` | Nothing to do: Todo and Scoping are both empty or fully vetted out. |
| `5` | Linear could not be reached (a transport-level failure — DNS, connection refused, a timeout — as opposed to a reachable API answering with an error). |

`--watch` runs until interrupted and does not use this contract itself —
each pass it dispatches is a detached child, not this process's own exit.

## Flags

| Flag | Meaning |
|---|---|
| `--team-key` | Linear team key, e.g. `WND`. Falls back to `[team] key` in the nearest `wand.toml` — since dispatch is run from inside the repository, that is normally where it comes from, and the flag is only for running against another team. |
| `--harness` | Worker harness: `claude-code` (default) or `codex`. |
| `--model` | Model for every worker. Default: the harness's default. |
| `--effort` | Reasoning effort for every worker. Default: the harness's default. |
| `--watch` | Poll and dispatch continuously, spawning detached children. |
| `--interval` | How often `--watch` polls. Default `1m`. |

## Requirements

Same as `wand run` and `wand scope`: `LINEAR_API_KEY`, an authenticated
`gh`, `commands.verify` in [`wand.toml`](../../covenant/), run from inside
the repository — which is also where the team key comes from, unless
`--team-key` overrides it.

`caps.lanes` in the covenant sets how many `wand run` loops this repository
runs at once (default `1`); a Scoping ticket never counts against it.

## See also

[`wand run`](../run/) and [`wand scope`](../scope/) are the two
orchestrators a winner runs through; [`wand sweep`](../sweep/) is
everything that happens after one of them exits; [`wand queue`](../queue/)
is the same read layer this selector's ranking reuses.
