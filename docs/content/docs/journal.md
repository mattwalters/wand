---
title: The run journal
weight: 15
summary: The append-only ledger every run writes, and the phase-metrics schema readers can build against.
---

Every `wand run` and `wand plan` writes an append-only JSON Lines journal
under the run's directory — one record per line, synced as it is written.
It exists first as the crash-only record of what a run did: an orchestrator
that dies mid-phase leaves a journal a resume can read, never a ticket
stuck In Progress with nothing to explain it. That property is documented
in the package doc of `internal/journal`, and it is not this page's
subject.

This page is the other half: the `phase.ended` record's `detail` field is
where every phase's operational metrics land — harness, model, tokens,
wall-clock time, lines changed, and (implicitly, from the outer record)
review round and terminal state. It is the substrate `wand stats`, the UI
usage panel and `wand analyze` are meant to read, and it is documented here
because those are readers outside the orchestrator that produces it: a
stable contract, not an implementation detail.

## Where the ledger lives

Per run, inside that run's own journal directory — never a parallel
per-repo file. A run's journal is already the crash-only source of truth
for that run, and every metric this page describes falls out of the same
`phase.ended` record the crash-only story already writes. Aggregating
across runs is a reader's job, not a writer's: a per-repo file would be a
second thing to keep consistent with the per-run ones, for no reader this
ticket has yet.

## The `phase.ended` record

Every `journal.Record` carries `kind`, `seq`, `at`, `phase` and `round` (see
`internal/journal`'s package doc for the record stream's own guarantees).
On a `phase.ended` record, `detail` is a JSON object shaped like this
(`run`-verb fields on the left; `plan` carries every field except
`diff_stat`, since a plan run has no worktree):

| field | type | present when |
|---|---|---|
| `exit_code` | number | always |
| `timed_out` | bool | omitted when `false` |
| `handoff` | bool | always — whether the worker left a handoff |
| `error` | string | omitted when the phase did not error |
| `output_tail` | string | omitted when the phase did not error |
| `harness` | string | always — `claude-code`, `codex`, … |
| `model` | string | omitted when the run used the harness's default |
| `tokens_in` | number | omitted when the harness reported no usage |
| `tokens_out` | number | omitted when the harness reported no usage |
| `wall_clock` | string | always — a Go duration (`"1m2.5s"`) |
| `diff_stat` | string | `run` only; omitted when there is no diff yet |

Two rules hold for every reader of this schema:

- **Absent, never estimated.** A harness that cannot report tokens, or
  whose output a parser could not read, leaves `tokens_in`/`tokens_out`
  out of the object entirely — never a faked zero. A reader that treats a
  missing field as zero will undercount; it must treat it as unknown.
- **Terminal state and review round are not duplicated here.** They live
  on the outer record: `kind: "run.ended"` carries `outcome`
  (`converged`/`handed_back`/`parked`) and `reason`, and every
  `phase.started`/`phase.ended` pair carries its own `round`. A reader
  wanting "how many review rounds did this run take" counts `phase.ended`
  records where `phase == "review"`, rather than looking for a count
  inside `detail`.

## Why token counts come from the harness, not the worker

A worker session could self-report tokens in its handoff, but that number
would be exactly as trustworthy as any other self-report a cold worker
makes — and unlike a plan deviation or a status, there is no way to check
it after the fact. `tokens_in`/`tokens_out` are instead parsed from each
harness CLI's own structured output (Claude Code's `--output-format json`
result, Codex's `--json` event stream) — the harness's own accounting,
not the worker's. Neither shape is a versioned, guaranteed-stable API, so a
parse miss is designed to degrade to "absent" rather than to error the run
or to guess: a metrics gap costs a reader a blank cell, where a wrong
number costs it a wrong conclusion.
