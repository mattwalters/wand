---
title: wand file
weight: 190
summary: File a finding into Triage, after searching for duplicates.
---

`file` is how a session records something it found that is not its ticket.
It searches for near-duplicates first, then files into Triage with the
`agent-filed` label.

## Synopsis

```
wand file <title> --team-key KEY [-d DESCRIPTION] [--force]
```

## What it does

**The search comes first, and it can stop the whole command.** If the
search finds candidates, nothing is filed: the candidates are printed with
their statuses, and the command fails. Read them, then either comment on
the issue that already exists or re-run with `--force`.

Search-first is the rule because a duplicate filed is a duplicate a human
must later notice, judge and close. The cheap moment to catch it is before
it exists.

Past the search, the issue lands with:

* status **Triage** — not Backlog, not Todo;
* the **`agent-filed`** label, so a human can see at a glance where their
  triage queue came from;
* **no priority** and **no assignee**.

Ranking work and owning it are part of blessing it, and an agent never
promotes what it filed. That is not just convention here —
[`wand guard`](../guard/) blocks the write.

## Flags

| Flag | Description |
|---|---|
| `--team-key KEY` | The Linear team key, e.g. `WND`. Falls back to `[team] key` in the nearest `wand.toml`; required if neither is set. |
| `-d`, `--description TEXT` | The finding: what you saw, where, and why it matters. |
| `--force` | File even though the search found candidates. |

The title is a positional argument, and exactly one is required.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Filed. The identifier and URL are on stdout. |
| `1` | Not filed. **This includes the duplicate refusal**, which is the expected outcome often enough to be worth scripting for — the candidates are on stdout, the refusal on stderr. Also covers no resolvable team key (neither `--team-key` nor `[team] key` in `wand.toml`), an empty title, no team with that key, and API failures. |

## Examples

```bash
wand file "Guard: uuid state values pass unchecked" \
  --team-key WND \
  -d "CheckState matches names and types but not ids, so a
      model that looks up the uuid first gets a forbidden
      write through. Known gap in the package doc."
```

```
filed WND-51 into Triage (agent-filed): Guard: uuid state values pass unchecked
url:   https://linear.app/acme/issue/WND-51/guard-uuid-state-values-pass-unchecked
```

The search finding something:

```bash
wand file "Guard: uuid state values pass unchecked" --team-key WND
```

```
the search found existing issues near that title:
  WND-31  Backlog      Guard: raw uuid state values are not matched
```

and, on stderr, the refusal that makes the exit non-zero:

```
  ERROR

  Not filed: rule these out first — comment on the existing issue, or
  re-run with --force if this is genuinely new work.
```

## See also

[`wand abandon`](../abandon/) when the thing you found undoes the ticket
you are on, rather than being separate work.
