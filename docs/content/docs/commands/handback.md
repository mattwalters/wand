---
title: wand handback
weight: 170
summary: Park one issue on a human — the question first, Needs Input second.
---

`handback` is what a session does when it hits a wall only a person can
clear. It posts your question as a comment, then moves the issue to Needs
Input.

## Synopsis

```
wand handback <identifier> [-m MESSAGE]
wand handback <identifier> < question.md
```

## What it does

Two writes, and **the order is the contract**:

1. the question, as a comment;
2. the status, to Needs Input.

Never the reverse. If the status write fails, the ticket stays In Progress
carrying its question, and a later pass can finish the move. The reverse
failure leaves a Needs Input ticket that asks nothing — and a ticket that
asks nothing parks forever, because the human who opens it has no idea what
they were supposed to decide.

Before either write, `handback` resolves the target status. That is a pure
read, so a drifted board or a guard refusal stops the verb with nothing
written; a failure discovered *after* the comment would make a repaired
re-run post the question twice.

An empty question is refused. So is a ticket a human has already closed —
reopening a close is their call, not the verb's.

### What makes a good question

The refusal message says it, and it is the whole point of the verb: what
you need to know, the options you see, and which one you would pick. A
handback that says only "blocked, please advise" moves the work to a human
without moving them any closer to answering.

## Flags

| Flag | Description |
|---|---|
| `-m`, `--message MESSAGE` | The question. When absent, the question is read from stdin. |

Long markdown reads better from a heredoc than from a quoted flag, which is
why stdin is a first-class input. An interactive terminal with no `-m`
refuses rather than blocking on a read you cannot see. A blank `-m` is an
error, not a cue to fall back to stdin.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | The question is posted and the issue is in Needs Input. |
| `1` | Something refused or failed. If it failed between the two writes, the comment stands and the status does not — re-running is safe only after you check which. |

## Examples

A short question:

```bash
wand handback WND-42 -m "The codex CLI dropped --sandbox.
  Options: wrap it, or pin to 0.9 which had it. I would pin.
  Which?"
```

```
handed back WND-42: question posted, now Needs Input
```

A long one, from a heredoc:

```bash
wand handback WND-42 <<'MSG'
## What I need

The isolation suite passes only with `--no-network`, which also disables
the adapter's own telemetry upload.

## Options

1. Ship with telemetry off. Simple; loses the metric.
2. Allowlist the telemetry host. Keeps the metric; widens the sandbox.

## What I would do

(1). The metric is not worth a hole in the isolation boundary.
MSG
```

## See also

Use [`wand abandon`](../abandon/) instead when the ticket is *wrong* rather
than blocked — when the evidence undoes its premise.
