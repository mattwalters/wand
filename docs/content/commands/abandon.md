---
title: wand abandon
weight: 180
summary: Hand one issue back to Backlog, with the evidence that undoes it.
---

`abandon` is for a ticket that turned out to be wrong rather than blocked.
It posts your evidence as a comment, then — in a single write — corrects
the description, moves the issue to Backlog and unassigns it.

## Synopsis

```
wand abandon <identifier> [-m MESSAGE] [--replace OLD --with NEW]
wand abandon <identifier> < evidence.md
```

## What it does

**Backlog, never Canceled, Done or Duplicate.** Closing is a human's call
however wrong the ticket turned out to be, and [`wand
guard`](../guard/) enforces it. Backlog is the one downward move an agent
may make, and it is safe precisely because it removes authorization rather
than granting it.

The order mirrors [`wand handback`](../handback/)'s, for the same reason:
evidence first, then the demotion. Everything checkable without writing is
checked before the comment goes up — the target status is resolved, and the
correction is composed against the current description — because the
comment promises the correction lands "in the same action", and a comment
followed by a refusal would record a correction that never happened.

An empty evidence message is refused: a hand-back with no reasons on it
reads as a bot giving up. A ticket a human already closed is refused too.

### Correcting the description

`--replace` takes the exact wording the evidence disproved and `--with`
takes what should stand instead. The two travel together; passing one
without the other is an error. `--with ""` deletes the wording outright.

The anchor **must match exactly once**. Zero matches or several, and the
verb refuses before writing anything, rather than guessing at an edit in
someone else's prose.

The old wording is quoted into the evidence comment. That quote is the
record: Linear surfaces description history poorly, and the comment is
where a disproven claim survives for whoever later audits why the ticket
came back.

## Flags

| Flag | Description |
|---|---|
| `-m`, `--message MESSAGE` | The evidence: what you found and why it undoes the premise. When absent, read from stdin. |
| `--replace OLD` | Exact description wording the evidence disproved. Requires `--with`. |
| `--with NEW` | What the description should say instead. Empty deletes the wording. Requires `--replace`. |

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Evidence posted, issue in Backlog, unassigned — and the description corrected, if a correction was given. |
| `1` | Refused or failed: no evidence, `--replace` without `--with`, an anchor that did not match exactly once, an already-closed ticket, or an API failure. |

## Examples

Hand a ticket back with the reasoning:

```bash
wand abandon WND-42 -m "The codex CLI removed --sandbox in
  1.2, so there is no flag to pass. Meeting the isolation
  suite now needs a wrapper, which is a different ticket."
```

```
abandoned WND-42: evidence posted, now Backlog, unassigned
```

Correct the premise in the same act:

```bash
wand abandon WND-42 \
  -m "Measured it: the call is 12 ms, not 400 ms. The
      regression is in the retry loop above it." \
  --replace "the adapter's startup costs ~400 ms" \
  --with "the retry loop above the adapter costs ~400 ms"
```

```
abandoned WND-42: evidence posted, now Backlog, unassigned
description corrected in the same write
```

## See also

[`wand handback`](../handback/) when the ticket is right but you need a
decision. [`wand file`](../file/) when what you found is a *different*
piece of work.
