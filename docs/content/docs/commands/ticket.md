---
title: wand ticket
weight: 150
summary: Print one ticket whole, for a reader with no context.
---

`ticket` renders one issue for someone who knows nothing about it — a
worker being prompted, or a human catching up on a ticket they last saw
three weeks ago.

## Synopsis

```
wand ticket <identifier>
```

## What it does

Prints a header block (status, priority, assignee, labels, blockers,
created date, URL), then the description **whole**, then every comment
oldest-first.

The description is never truncated. It is the ticket; a summarized ticket
is a different ticket, and the reader has no way to know what was cut.

Comments are held to a per-comment budget of 2000 characters rather than a
whole-ticket one. The reason is the shape of a real conversation: a parked
ticket usually ends with a short answer from a human, and a whole-ticket
budget spent on the long analysis at the top would crowd out precisely the
part that unblocks the reader. Every page of comments is fetched, not just
the first.

A cut is never silent. A truncated comment ends with
`[comment truncated: N of M chars shown]`, so a cold reader knows to open
Linear rather than assume they saw everything.

Ordering is by creation time with the comment id as a tiebreak, sorted by
wand rather than taken from the API's connection order — the reading order
of a conversation is not an API detail.

## Flags

None. The identifier is a positional argument, and exactly one is
required.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | The ticket was printed. |
| `1` | It could not be: no `LINEAR_API_KEY`, no such identifier, or an API failure. |

## Examples

```bash
wand ticket WND-42
```

```
WND-42  Adapter: codex

status:    In Progress
priority:  Medium
assignee:  Ada Lovelace
labels:    adapter
blocked:   by WND-8 (Done)
created:   2026-08-04
url:       https://linear.app/acme/issue/WND-42/adapter-codex

Add a worker adapter for codex, conforming to the isolation suite ...

comments (1):

--- Ada Lovelace, 2026-08-11 09:22 ---
The isolation flags differ from the Claude Code adapter's; see the
conformance run in the linked PR.
```

Feed one to a worker as its whole briefing:

```bash
wand ticket WND-42 | my-worker --prompt-stdin
```
