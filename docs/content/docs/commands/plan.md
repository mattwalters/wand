---
title: wand plan
weight: 195
summary: Research one Scoping ticket into a plan a human can bless.
aliases:
  - /docs/commands/scope/
---

`plan` is the research orchestrator. It sends a cold, read-only scout over
your repository to research one ticket, validates what the scout hands back,
and writes the result onto the ticket: the plan into the description, the
approaches and their trade-offs as a comment, the estimate, and Scoped
last.

It is the smallest orchestrator wand has — no worktree, no branch, no PR, no
CI — and the one to run first if you want to see the machinery work before
you let it write code.

## Synopsis

```
wand plan <identifier> [--interactive] [--harness NAME] [--model M] [--effort E]
```

## The ticket must be in Scoping

Blessing research is a human act, the same way blessing building is: a
ticket in Scoping is one you have decided is worth spending a scout on.
`plan` refuses anything else, and refuses a `human-only` or `parked`
ticket outright — a plan run is a full cold research pass, and re-buying one
that already stopped is the most expensive way to learn nothing.

It does **not** refuse a blocked ticket. A ticket waiting on another is
often exactly the one worth planning early — the blocker stops the building,
not the reading. That is a deliberate difference from `wand claim`, which
does refuse.

## What it writes, and in what order

Four writes, and the order is the contract:

1. **The plan**, into a marker-fenced region of the description
   (`<!-- wand:plan -->`). Every byte outside that region belongs to
   whoever wrote it; the region is replaced whole on every plan run, never
   merged and never appended to. This is absolute, not a default: the
   ticket's goals, problem statement and title are the human's and are
   never rewritten by a plan run, first or re-plan alike, because the body
   has to stay the thing the plan is checked *against* — a scout that
   misunderstood the ticket and could also rewrite it would be erasing the
   evidence of its own misunderstanding. A scout that finds the ticket
   itself wrong reports that instead (see below); it does not correct the
   body in the same act as planning it.
2. **The options comment**: what the scout took the ticket to be asking,
   every approach with its trade-off, which one is recommended and why,
   what the plan rests on, what is still open, and the ask.
3. **The estimate**, on the team's scale.
4. **Scoped**.

Each deliverable lands before the transition that advertises it. A ticket in
Scoped promises a finished plan to judge, so the status move is last, and if
anything before it fails the ticket stays in Scoping — carrying whatever
did land, and nothing claiming to be finished.

The comment comes before the estimate for the same reason: a ticket carrying
the argument for an estimate it does not have is recoverable, while a ticket
carrying a number nothing explains is a number nobody can weigh.

Promoting the result to Todo is yours. An agent does not bless its own plan.

## Nothing is written unless the whole handoff is valid

The scout's handoff is validated before the first Linear call, and a handoff
that fails validation writes **nothing at all**. Half a plan on a ticket is
worse than none, because it reads like a whole one — a human blesses the
plan on the strength of the argument beside it, and nobody re-derives
afterwards whether that argument held together.

What is checked:

- one to three approaches, each with a real trade-off;
- a recommendation naming one of those approaches, with the argument for it;
- at least one usable file citation, cited `path:line` (a range or a
  comma-separated list of either also counts) — the line is what makes it a
  citation rather than a filename. A citation carrying no line is **dropped,
  not fatal**: it leaves the file map, the plan says which one went and why,
  and the run carries on. Only a draft with no usable citation left is
  refused, and then for citing nothing rather than for the formatting;
- an estimate on the covenant's scale (Linear silently adjusts an off-scale
  estimate to fit, which would land a number nobody chose; a team whose
  scale is `notUsed` gets no estimate and is not asked for one);
- a plan with ordered steps **and** a test story;
- for every assumption, what breaks if it is wrong and how expensive that is.

A misspelled field is a loud failure rather than a silently absent one.

### When the scout says the ticket is wrong

A scout that finds the ticket built on something untrue — work already done,
a mechanism that does not work the way the ticket says — reports that
instead of planning around it. Then nothing is written to the description and
no estimate is set: its account goes on the ticket verbatim, the ticket
moves to Needs Input, and the command exits `2`.

## The read-only promise

There is no worktree. The scout reads the checkout you ran the command
from — usually yours, with your uncommitted work in it — and is told not to
change it. wand records `git status` before and after every worker and
**parks** if anything moved, leaving the change in front of you rather than
writing a plan over a checkout somebody quietly edited. The handoff is
journaled before that check, so a park does not throw the research away.

## `--interactive`: the draft-then-grill interview

With `--interactive`, wand puts the draft to you before anything is written.
The questions are composed from the draft's own structure, worst consequence
first:

1. **the ticket itself** — the scout's reading of what you asked for,
   because everything below is wasted if the problem is wrong;
2. **the recommendation**, next to the approaches it passed over;
3. **the assumptions**, costliest first;
4. **the open questions**.

Every question quotes the draft verbatim: a paraphrase asks about a claim
the draft does not make. Answer over as many lines as you like; a blank line
ends an answer, and an empty answer means "as drafted". If you answer
nothing, nothing is revised and no extra model call is spent.

Your answers go to a **second, fresh session**, never back to the one that
wrote the draft — a session that has just argued for an approach defends it,
and what comes back is the same plan with your objections explained away.
The reviser produces a whole new plan, validated exactly as strictly.

The flag needs a terminal. An unattended caller must not pass it: it is
refused with exit `1` rather than left blocked on a read nobody will answer.
A covenant that turns the stage off (`toggles.plan_interview = false`)
refuses the flag too — the two settings answer different questions, and
resolving the contradiction silently would mean one of them was never
load-bearing.

## The critic

`toggles.plan_critic = true` in [`wand.toml`](../../covenant/) inserts a
cold critic between the draft and the interview: a fresh session prompted to
attack the draft, whose objections are validated like any other handoff and
handed to a reviser. An objection must state what it costs if the draft
ships as written; an objection with no consequence is a preference, and a
revision round is too expensive to spend on one. A critic that finds nothing
costs one call and changes nothing.

It is off by default. It spends a whole extra model call per plan run, and
unlike the rest of the covenant it is not a rule the reference system has
finished paying for.

The footer of the options comment says which stages ran, so you can tell a
first draft from a plan that survived a critic and an interview.

## One plan run per ticket

`wand plan` takes a per-ticket lock for the life of the process. `wand run`
does not need one — it claims its ticket out of Todo, so the board is its
mutex — but a plan run's ticket sits in Scoping from the first read to the
last write, so nothing on the board can keep a second plan run out. Two plan
runs over one ticket would write two plans into one fenced region and argue
two recommendations at a reader who cannot tell which the estimate belongs
to.

The lock is a file in wand's state directory, released by the operating
system when the process dies, however it dies. It is machine-local: it
serializes the processes on your machine and says nothing about anyone
else's.

## Flags

| Flag | Description |
|---|---|
| `--interactive` | Interview you over the draft before writing anything. Needs a terminal. |
| `--harness NAME` | Worker harness: `claude-code` (default) or `codex`. |
| `--model M` | Model for every worker. Default: the harness's own. |
| `--effort E` | Reasoning effort for every worker. Default: the harness's own. |

## Exit codes

A scheduler contract, and the one `wand run` publishes too.

| Code | Meaning |
|---|---|
| `0` | Planned. The plan, the options and the estimate are on the ticket, and it is in Scoped for a human to judge. |
| `1` | The run never started: no API key, the ticket is not in Scoping, `--interactive` with no terminal, another process holds the ticket. Nothing was written. |
| `2` | Handed back. The scout judged the ticket's premise wrong; its account is on the ticket, no plan was written, and it is in Needs Input for a human to answer. |
| `3` | Parked. The run stopped without deciding — an unusable handoff, a worker failure, a write that failed part-way. The journal says which, and how much reached the ticket; the ticket itself carries the reason as a comment and the `parked` label. |

## The journal

Every plan run is a journaled run: each phase recorded before it happens, the
scout's handoff kept as a note, and exactly one terminal record. A handoff
that fails validation is kept too, written to the run's `scratch/` as
`<phase>-<round>.rejected.json` before the park — the worker's own copy is
already gone by then, and a refusal you cannot read afterwards makes the
validator's one-line complaint the only surviving account of a research
pass that may have cost minutes and millions of tokens. Runs live
under `$XDG_STATE_HOME/wand/runs` (or `WAND_STATE_DIR`), outside every
repository. The journal is written before the ticket is, deliberately — a
park has to be reachable when Linear itself is what failed. Once it is
journaled, the ticket gets the same sentence as a comment and the `parked`
label, best-effort: a plan run never owns its ticket's status, so the mark is
a label and the ticket stays in Scoping where a human put it.

### The failure that retries instead

A scout that died of infrastructure — a provider error, a host suspended
mid-response — does not park on the first try. When the harness's own
report says the failure was about the machine rather than the research, the
phase respawns a fresh cold scout, up to `caps.worker_retries` times (one by
default; `0` switches it off). A scout costs a whole model call and produces
nothing at all until it hands off, so a provider error is the most
expensive possible thing to mistake for a verdict.

The rules are [`wand run`](../run/)'s, with one that is plan's own: no
retry ever happens into a checkout the scout changed. `plan` reads the
repository you ran it in — usually your own working copy, not a worktree the
run owns — so a stray edit is about to be handed back to you untouched.
Respawning a second scout into it first would write more into a directory
you are about to be asked to look at. Timeouts and interrupts are not
retried either, and a harness that cannot report transience parks exactly as
before.

## Requirements

`LINEAR_API_KEY` in the environment, the harness on `PATH`, and a git
repository to run in. The workers get none of that: the credential strip
runs before the harness starts, and the isolation is proven per adapter by
wand's conformance suite.

## Examples

```bash
wand plan WND-42
```

```
planning WND-42 (Scoping), journaling to ~/.local/state/wand/runs/WND-42-20260819T120000Z
phase scout: spawning a cold worker (claude-code)
wrote the plan into the ticket's description
posted the options comment
set the estimate to 3
run WND-42-20260819T120000Z ended: converged — scoped: …
```

Grilled first:

```bash
wand plan WND-42 --interactive
```

## See also

[`wand ticket`](../ticket/) renders the plan for a cold reader — the
description and every comment, in one piece.
