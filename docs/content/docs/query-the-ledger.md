---
title: Query the ledger yourself
weight: 16
summary: The journal files are JSONL by design — jq and DuckDB examples for reading them directly, without wand.
---

[`wand stats`](../commands/stats/) covers the aggregations most people want:
token velocity, a per-harness x per-phase breakdown, convergence, and
per-ticket totals. It is deliberately not the only way to read the
ledger — the files it reads are plain, newline-delimited JSON, one object
per line, and pointing your own tools at them is supported and encouraged.

What is not supported: embedding a query engine inside `wand` itself.
[The run journal](../journal/) is a set of per-run files on purpose, not a
database — see that page's "Where the ledger lives" section for why. `jq`
and DuckDB both read newline-delimited JSON natively, so nothing needs
adding to get a real query language over this data; it just does not run
inside this binary.

## Where the files are

One directory per run, under the journal store's root:

```
$WAND_STATE_DIR/runs/<run-id>/journal.jsonl
```

`$WAND_STATE_DIR` defaults to `$XDG_STATE_HOME/wand` (`~/.local/state/wand`
on a machine with no `XDG_STATE_HOME` set) on Unix; see
`journal.DefaultRoot` in `internal/journal/store.go` for the exact
resolution, including Windows. `wand stats` and `wand sweep` both print the
store root's runs they act on if you need to confirm it locally:

```bash
echo "${WAND_STATE_DIR:-$HOME/.local/state/wand}/runs"
```

Every record schema — `run.started`, `phase.started`, `phase.ended`,
`run.ended`, and the `phase.ended` `detail` object's fields — is documented
on [the run journal](../journal/) page. The examples below assume that page's
field names.

## jq

Total tokens across every run:

```bash
jq -s '
  [.[] | select(.kind == "phase.ended") | .detail |
    ((.tokens_in // 0) + (.tokens_out // 0))] | add
' "$WAND_STATE_DIR"/runs/*/journal.jsonl
```

Every run's outcome, one line per run:

```bash
jq -c 'select(.kind == "run.ended") | {outcome, reason}' \
  "$WAND_STATE_DIR"/runs/*/journal.jsonl
```

Round-deduped review-round counts per run — this is the one place jq needs
the same care [the run journal](../journal/#a-retried-phase-and-review-rounds)
warns about: a retried phase writes more than one `phase.ended` record at
the same round, so group by `(phase, round)` before counting, don't count
records:

```bash
jq -s '
  [.[] | select(.kind == "phase.ended" and .phase == "review")] |
  group_by([.phase, .round]) | length
' "$WAND_STATE_DIR"/runs/WND-41-*/journal.jsonl
```

Tokens by harness, folding retried attempts into their round first so a
retry is not double-counted:

```bash
jq -s '
  [.[] | select(.kind == "phase.ended")] |
  group_by([.phase, .round]) |
  map({
    harness: (.[0].detail.harness),
    tokens_in:  ([.[].detail.tokens_in  // 0] | add),
    tokens_out: ([.[].detail.tokens_out // 0] | add)
  }) |
  group_by(.harness) |
  map({harness: .[0].harness,
       tokens_in: (map(.tokens_in) | add),
       tokens_out: (map(.tokens_out) | add)})
' "$WAND_STATE_DIR"/runs/*/journal.jsonl
```

## DuckDB

DuckDB reads newline-delimited JSON directly with `read_ndjson_auto`, glob
and all:

```sql
select
  detail.harness       as harness,
  phase,
  round,
  detail.tokens_in      as tokens_in,
  detail.tokens_out     as tokens_out,
  detail.wall_clock     as wall_clock,
  detail.attempt        as attempt
from read_ndjson_auto('runs/*/journal.jsonl')
where kind = 'phase.ended'
order by harness, phase, round;
```

The same round-dedup rule applies here: `attempt` distinguishes retries at
one round, so group by `(phase, round)` before aggregating, not by raw row:

```sql
with rounds as (
  select
    detail.harness as harness,
    phase,
    round,
    sum(coalesce(detail.tokens_in, 0))  as tokens_in,
    sum(coalesce(detail.tokens_out, 0)) as tokens_out
  from read_ndjson_auto('runs/*/journal.jsonl')
  where kind = 'phase.ended'
  group by detail.harness, phase, round
)
select harness, phase, count(*) as rounds,
       sum(tokens_in) as tokens_in, sum(tokens_out) as tokens_out
from rounds
group by harness, phase
order by harness, phase;
```

Convergence rate by harness, joining each run's `run.started` (for its
harness) against its own `run.ended` (for its outcome) by the file both
records live in — a run's whole history is one file, so the join key is
just `filename`:

```sql
with records as (
  select filename, kind, run.harness as harness, outcome
  from read_ndjson_auto('runs/*/journal.jsonl', filename = true)
),
started as (select filename, harness from records where kind = 'run.started'),
ended   as (select filename, outcome from records where kind = 'run.ended')
select s.harness,
       count(*) filter (where e.outcome = 'converged') as converged,
       count(*) as total
from started s
join ended e using (filename)
group by s.harness
order by s.harness;
```

## See also

[`wand stats`](../commands/stats/) runs the same kind of aggregation natively,
with the round-dedup rule already applied — reach for it first, and reach
for `jq`/DuckDB when you want a cut it does not report.
