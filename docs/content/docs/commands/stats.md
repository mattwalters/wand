---
title: wand stats
weight: 198
summary: Aggregate reports over the run journal — token velocity, per-harness x per-phase, convergence, and per-ticket totals.
---

`stats` reads every run in the local journal store and prints four plain-text
reports. It is read-only, native Go, and reads nothing but the journal: no
`LINEAR_API_KEY`, no network, no new dependency. See
[the run journal](../../journal/) for the record schema it reads, and
[query the ledger](../../query-the-ledger/) for reading the same files
yourself with `jq` or DuckDB.

## Synopsis

```
wand stats [--since <duration>]
```

## What it prints

### Token velocity

Tokens summed by UTC calendar day, oldest first — the "am I laying down
tokens consistently?" curve. `--since` limits this report to phase-rounds
that closed within that duration of now; every other report always reads
the whole history.

### Harness x phase

One row per (harness, phase) pair: how many rounds it took, and the tokens
and wall-clock they cost. Rows are ordered by the phase's place in its
pipeline, then harness name alphabetically — never by a metric. This is the
operator's own telemetry, not a leaderboard, and the ordering is deliberate:
nothing here is meant to be read as "harness A beat harness B".

A round counts once however many attempts it took to close — a phase
retried after a transient failure still closes at the same round (see
[the run journal](../../journal/#a-retried-phase-and-review-rounds)) — so
this table is also where review-round counts live: the row where
`phase == review` is `wand run`'s review-round count for that harness.

### Convergence (harness x verb)

One row per harness, one column per verb (`run`, `plan`): how many of that
harness's ended runs converged, out of how many ended. A run still in
progress has no verdict yet and is not counted. Alphabetical harness order,
no ranking, no highlighting — the same not-a-leaderboard rule as the phase
table.

### Per-ticket totals

One row per ticket: tokens and wall-clock summed across every run recorded
against it (a re-planned or re-run ticket keeps every attempt), and a list
of each run's id and outcome.

### Skipped runs

A run whose journal fails to read or fails to replay is dropped from every
report and named in a `skipped` line at the end, rather than failing the
whole command.

## Flags

| Flag | Description |
|---|---|
| `--since <duration>` | A Go duration (`168h`, `72h`, `30m`) bounding the velocity report to phase-rounds that closed within that window of now. Default: the whole history. |

## Exit codes

| Code | Meaning |
|---|---|
| `0` | The reports were printed — including when the store is empty. |
| `1` | The journal store could not be opened, or `--since` did not parse as a Go duration. |

## Examples

```bash
wand stats --since 168h
```

```
token velocity
day         tokens in  tokens out
2026-08-18      42100       9800
2026-08-19      38650       8120

harness x phase
harness      phase      rounds  tokens in  tokens out  wall clock
claude-code  implement       6      31200       7400        24m
claude-code  review          4      12900       2600        11m
codex        implement       3      14300       3100        13m

convergence (harness x verb)
harness             run       plan
claude-code  4/5 (80%)  2/2 (100%)
codex        1/3 (33%)          —

per-ticket totals
ticket  tokens in  tokens out  wall clock  runs
WND-41       9300       2100          8m  run(WND-41-...): converged
WND-44      12600       2900         11m  plan(WND-44-...): handed_back, run(WND-44-...): converged
```

## See also

[The run journal](../../journal/) documents the `phase.ended` record schema
this command reads. [Query the ledger](../../query-the-ledger/) shows the
same aggregations with `jq` and DuckDB, for anything this command does not
report.
