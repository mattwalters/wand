---
title: wand queue
weight: 140
summary: Print the ranked, vetted Todo queue — what an agent may start next.
---

`queue` answers one question: what should be picked up next. It prints the
team's Todo issues in start order, with the ones an agent may not start
separated out and labelled with the reason.

## Synopsis

```
wand queue --team-key KEY
```

## What it does

### The order

Priority ascending, so Urgent leads and **No priority sorts last** — Linear
stores "no priority" as `0`, and a naive ascending sort would rank
unranked work above everything. Within a priority, oldest first: work that
has waited longest goes first. Identifier is the final tiebreak, so two
readers looking at the same board at the same moment agree on what is next.

### The vetting

An issue is printed under `skipped:` when an agent may not start it:

* **labeled `human-only`** — reserved for a person, whatever its rank;
* **blocked by an issue that is not completed or canceled.** A *started*
  blocker still blocks. "It's already In Progress" is precisely the race
  the blocked-by relation exists to prevent.

Skipped issues are printed, never dropped. A queue that quietly comes up
short reads exactly like a queue in order, and the reader has no way to
tell that three things were hidden from them.

An empty Todo prints `Todo is empty: nothing for you right now.` and exits
`0`. Nothing to do is not an error.

## Flags

| Flag | Description |
|---|---|
| `--team-key KEY` | The Linear team key, e.g. `WND`. **Required.** |

## Exit codes

| Code | Meaning |
|---|---|
| `0` | The queue was read and printed — **including when it is empty**, and when every issue in it was vetted out. |
| `1` | The queue could not be read: no `LINEAR_API_KEY`, no `--team-key`, a broken `wand.toml`, or an API failure. |

Do not script `wand queue` as if a non-zero exit meant "nothing to do".
Read the output.

## Examples

```bash
wand queue --team-key WND
```

```
WND-31  Urgent       Guard: uuid state values pass unchecked
WND-24  Medium       Docs: command reference pages for every implemented command
WND-19  No priority  Adapter: codex

skipped:
WND-28  labeled human-only
WND-30  blocked by WND-28 (In Progress)
```

Then take the top of the list:

```bash
wand claim WND-31
```

## See also

[`wand ticket`](../ticket/) prints one of these whole, for a reader with no
context. [`wand claim`](../claim/) vets an issue exactly as `queue` does,
then takes it.
