---
title: wand claim
weight: 160
summary: Take one blessed issue — In Progress and assigned, before any work.
---

`claim` is the first thing a session does and the only thing it does before
touching the filesystem: it vets one issue, then moves it to In Progress
and assigns it to the key holder in a single write.

## Synopsis

```
wand claim <identifier>
```

## What it does

The vetting is [`wand queue`](../queue/)'s own — not labeled `human-only`,
no unresolved blockers — plus a status gate:

**The issue must be in Todo.** Claim starts blessed work, and blessing is a
human act. An issue in Backlog, Triage or To Plan is not yours to start,
however ready it looks, and `claim` refuses rather than promoting it. (It
could not promote it anyway: [`wand guard`](../guard/) blocks the write.)

On success it moves the issue to In Progress and assigns it to whoever owns
the API key, in **one** write. A claim that set the status and then failed
to assign would leave a ticket that reads as taken and is owned by nobody.

### Claim before anything else

The status move is the cheapest place in the whole run to lose a race. Two
sessions that each branch, set up a worktree, read the code and *then*
claim have both done work one of them must throw away. Two that claim first
collide before either has anything to lose.

## Flags

None. The identifier is a positional argument, and exactly one is
required.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Claimed. The identifier, the new status, the assignee and the ticket's branch name are on stdout. |
| `1` | Not claimed — and nothing was written. The issue is outside Todo, is labeled `human-only`, has an unresolved blocker, or the call failed. The reason is on stderr. |

A non-zero exit here means the work is not yours. Do not proceed.

## Examples

```bash
wand claim WND-42
```

```
claimed WND-42: In Progress, assigned to Ada Lovelace
branch:  ada/wnd-42-adapter-codex
```

Then branch from the name it printed — Linear's own, so the ticket and the
branch stay linked:

```bash
git switch -c ada/wnd-42-adapter-codex
```

A refusal — nothing was written, and the reason names what stands in the
way:

```bash
wand claim WND-30
```

```
  ERROR

  WND-30 may not be started: blocked by WND-28 (In Progress).
```

## See also

[`wand handback`](../handback/) parks a claimed issue on a human;
[`wand abandon`](../abandon/) returns one the evidence undoes.
