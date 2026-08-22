---
title: wand ui
weight: 200
summary: Home — three views, one job each, and the one place blessing happens.
---

`ui` opens home: a landing screen naming three views — Decide, Review,
Unblock — one job each, rather than one flat list of everything waiting on
a human.

Bare `wand`, with a terminal attached and no flags, does the same thing:
`wand` and `wand ui` are equivalent. `ui` is the explicit spelling — the one
the flags below live on — and the one to reach for outside a terminal, where
bare `wand` falls back to printing help instead.

## Synopsis

```
wand
wand ui --team-key WND [--harness <name>] [--model <m>] [--effort <e>] [--interval <duration>]
wand ui --sample
wand ui --dump-screen [--script KEYS] [--width N] [--height N]
```

## Home: three views, one job each

A person sitting down at `wand ui` has come to do one of three jobs, not to
scan one flat list of everything waiting on them. Home opens on a landing
screen naming the three and how much is waiting in each:

```
 wand · WND                                                     7 waiting on you

  1  Decide   2 waiting  judge the pool, rank it, promote it
  2  Review   3 waiting  bless plans, answer questions, merge PRs
  3  Unblock  2 waiting  clear parks, resolve stuck lanes

  1 Decide • 2 Review • 3 Unblock • r refresh • q quit
```

A number key opens one view; `esc` from inside a view returns here, and a
number key pressed while already in a view jumps straight to another one.
The header's count is global here — across all three — and scoped to just
the active view once you are inside one.

* **Decide** (`1`) — judge the pool, rank it, promote it. Sourced from
  **Triage**: what agents filed, waiting to be judged, ranked in the same
  order [`wand queue`](../queue/) ranks work, so the ticket you bless first
  is the one an agent starts first. Blessing lives here — see
  [Blessing](#blessing) below.
* **Review** (`2`) — bless plans, answer questions, merge PRs: judgment on
  work that already exists, not on what to start. Three queues:
  * **Plan Review** — tickets carrying a finished plan, waiting on the
    judgment that either blesses it into Todo or sends it back. Opening a
    row shows the plan itself — the marker-fenced region
    [`wand plan`](../plan/) wrote into the description — not the ticket's
    original body. See [Blessing a plan](#blessing-a-plan) below.
  * **Needs Input** — runs parked on a question: a plan run that judged the
    ticket's premise wrong, or a run that hands back. It means one thing —
    answer me — never "a plan is ready to bless," which is Plan Review's
    job.
  * **Ready for human** — every open issue carrying the `ready-for-human`
    label: a pull request to review, a merge to press. Closed issues are
    dropped, because the label outlives the merge that answered it.
* **Unblock** (`3`) — clear parks, resolve stuck lanes: a repair of the
  tooling, not a judgment about the work. Sourced from **Stalled** — runs
  the crash-only run journal says a person has to resolve. Four kinds,
  worst first: `stuck` (the journal says it is running and its holder is
  provably dead), `orphaned` (a live run whose ticket is not in a started
  status — nothing on the board claims the run), `unclear` (a holder on a
  machine this one cannot see, which is never swept automatically), and
  `parked` (the run stopped and recorded why).

  A parked run is the one a person can act on: `clear parked` removes the
  `parked` label from its ticket, and the row goes with it. That is not a
  cosmetic dismissal — the label is what makes a park a live obligation, so
  clearing it is the act, and the row is a picture of it. The other three
  kinds are read-only: they describe a process misbehaving right now, and
  resolving one means going to the machine.

  These are runs, not the dispatch *lanes* of the same name that
  [`wand dispatch`](../dispatch/) counts. A lane is a concurrency slot; a
  stalled run is one that needs you, and a `stuck` one holds no lane at all
  — being provably dead is exactly what frees it.

Inside a view, every section is always shown, even when empty — a queue
that vanished when it drained would teach you to stop looking for it, and
the day it refilled you would not notice. A view that is *itself* fully
empty — the common case for two of the three on most visits — says what
would appear there rather than rendering nothing:

```
 wand · WND · Unblock                                     nothing waiting on you

  nothing to unblock right now — stalled runs a person has to resolve will land
  here.

  Stalled
    every run is either finished or being driven.

  ↑/k ↓/j move • enter open • r refresh • q quit
```

**Backlog is deliberately absent.** A Backlog ticket is not waiting on you;
it is the pool, and browsing a pool is Linear's job, done better there. Home
shows what has *stopped*, not what exists.

`enter` opens the row under the cursor; `r` re-reads the whole board. The
two compose: refreshing while a row is open re-resolves that row against
what came back, so the key you pressed to get current data cannot be the
key that hides how stale it is. If the row is gone from the re-read board —
somebody else judged it — the screen returns to the view and says so
rather than closing without a word. A refresh that *fails* says that too,
on whichever screen you were on.

## What is running right now

On the landing screen, when anything is running, sits a strip answering a
different question — not *what is waiting on me?* but *what is the machine
doing?* It belongs to none of the three jobs, so it renders there rather
than inside any one view:

```
 wand · WND                                                     7 waiting on you

  Running  2 in flight
  WND-12  implement (round 1) · claude-code · up 18m · hb 8s ago
  WND-7   possibly dead, sweep will confirm · scout · — · up 42m · hb 6m ago

  1  Decide   2 waiting  judge the pool, rank it, promote it
  ...
```

Each row is one run the crash-only run journal says has not ended: its
current phase (`implement`, `review`, `revise`, `scout`, …), its harness, how
long it has been going (`up`), and how long since its lease last renewed
(`hb`, for heartbeat — the periodic tick a long single phase writes so it
does not read as stale the moment it passes a minute). A harness shown as
`—` is a plan-style run, which picks its harness per phase rather than
fixing one for the whole run.

**Nothing here is waiting on you**, so nothing here counts toward the
header's count or takes a cursor: hand-launched `wand run` and `wand plan`
invocations journal identically to an engaged home's own, and appear here
identically, whether or not engage mode is on. The strip is read-only and
disappears entirely when nothing is running — a home screen nobody has
pointed at a live run still reads exactly as it did before this existed.

A run whose own liveness judgment says its holder is gone reads
`possibly dead, sweep will confirm` rather than being dropped or guessed at
a second time: that judgment is the same one [Unblock](#home-three-views-one-job-each)
uses for a `stuck` run, and only [`wand sweep`](../sweep/) ever turns it
into an action. A run that is actually stuck this way is *also* a `stuck`
row in that view — the strip says what the journal currently believes is
running, Unblock says a person has to do something about it, and a run can
be both at once until sweep reaps it.

## Blessing

Promoting a ticket to **Todo** or **To Plan** is the transition
[`wand guard`](../guard/) refuses everywhere else, because it hands out
authorization an agent does not have. Home is the one place a person
grants it — in the **Decide** view — and it is a moment, not a keystroke:

```
  ✦ Blessing

    WND-41  guard: a raw state UUID is not matched
    Triage → Todo

    Todo is the gate between written down and a bot may act on this unattended.
    Blessing it lets an agent claim the ticket, branch, write code and open a
    pull request without asking you again.

    WND-41 has no priority, and No priority sorts last in the queue. Blessing it
    authorizes the work without scheduling it.

  enter bless WND-41 → Todo • esc back
```

That last paragraph appears only when it applies. It is the one thing the
disposition itself cannot know: an unranked ticket promoted to Todo sorts
behind everything ranked, so the blessing works and the work still never
starts.

## Blessing a plan

Opening a Plan Review row, in the **Review** view, shows the plan
[`wand plan`](../plan/) wrote — the
marker-fenced region of the description, and nothing else. Not the ticket's
original body, and not the scout's argued-options comment (the approaches it
weighed, the trade-offs, the estimate): that stays in Linear, one click away
from a ticket you already have open, and this screen exists to put your
judgment on the plan itself rather than the argument for it.

```
 WND-44  home: a fifth queue for plans awaiting blessing

  status    Plan Review
  priority  Urgent
  assignee  Matt Walters

  ## Implementation plan

  **Add a Scoped section.** Triage and Scoped are both authorization judgments;
  putting them next to each other reads as one job, not two.

  ### Steps

  1. Read the Scoped state into the snapshot.
  2. Render the plan section on the detail screen.
  3. Wire Bless → Todo and Backlog, with reason.

  ### How it is proven

  Golden screens at two widths, plus the existing home test tiers.
  … 8 more lines; read it in Linear

  judge  t ✦Todo  b Backlog
  esc back • q quit
```

Like the description on every other row, the plan is clipped rather than
scrolled: a plan that needs more than a screenful to judge is one to read in
Linear.

Judging it has two answers, not six — there is no research left to
authorize here, and no title vague enough yet to belong to someone else's
ticket. `t` blesses it into Todo, exactly like Triage's. `b` sends it back to
Backlog with your reasoning posted as a comment first, the same ordering
Cancel and Duplicate use below, so a crash between the two leaves an open
ticket carrying its own argument rather than a status move nobody can
explain — and so the next plan run over this ticket starts from why the last
one did not land instead of guessing:

```
  Backlog, with reason

    WND-44  home: a fifth queue for plans awaiting blessing
    Plan Review → Backlog

    Backlog is the pool. The reason is posted as a comment before the status
    moves, because a rejected plan with no reason on it leaves the next plan run
    over this ticket guessing at what was wrong.

    reason       wrong

  enter move WND-44 → Backlog • esc back
```

## The seven judgments

Triage and Needs Input rows get all six of the first table below. A Plan
Review row gets only two — bless, or reject with a reason — which is `t`
and `b` from the same table, but pointed at a plan rather than a raw
ticket. Ready-for-human and stalled rows offer none, and the screen says why
rather than leaving a key to do nothing.

| Key | Judgment | Where | Asks for | Writes |
|---|---|---|---|---|
| `t` | Bless → Todo | Triage, Needs Input, Plan Review | — | status |
| `s` | Bless → To Plan | Triage, Needs Input | — | status |
| `b` | Backlog, ranked | Triage, Needs Input | a priority, `1`–`4` | status + priority |
| `u` | Backlog, unranked | Triage, Needs Input | — | status + priority `0` |
| `d` | Duplicate | Triage, Needs Input | the canonical issue | relation, **then** status |
| `x` | Canceled, with reason | Triage, Needs Input | the reason | comment, **then** status |
| `b` | Backlog, with reason | Plan Review | the reason | comment, **then** status |

`enter` confirms, `esc` goes back. The unranked Backlog move sends an
explicit `0` rather than leaving the field alone: carrying the old rank into
the pool is the opposite of the judgment "worth keeping, not worth ranking".

Duplicate, Cancel and the Plan Review row's Backlog write their evidence *before*
the status moves, for the same reason [`wand handback`](../handback/) posts
its question first. The status write is what ends the ticket's visibility,
so anything a reader will later need has to already be on it. A crash
between the two leaves an open ticket carrying its own argument, which a
person can finish; the reverse leaves a ticket nobody can explain.

When the status move fails after that first write has landed, the
confirmation stays up so you can press `enter` again — and the retry does
**not** repeat the write that already succeeded. The screen says so, and the
field it came from goes read-only, because the text is on the ticket and
editing it could no longer reach anywhere. Retrying from the top would post
the same reason twice, and would re-issue a relation Linear already holds
and now refuses, which would leave the duplicate permanently uncompletable
through the only screen that can complete it.

Every status is named by what it *means*, so a covenant that renames `todo`
to `Ready` gets a screen that says `→ Ready`.

## Engage mode

Press `e` to engage. While engaged, home polls Todo and To Plan on
`--interval` (default `1m`) exactly the way `wand dispatch --watch` (see
[`wand dispatch`](../dispatch/)) does — because it *is* that mechanism,
reused rather than reimplemented: each poll runs a sweep pass first — the
same one `wand sweep` runs standalone — reclaiming a dead lease, handing
back a re-plan or re-review label, or draining an unresolved PR thread on a
converged ticket, then counts lanes, ranks and vets Todo and To Plan, and
when a winner exists spawns it as a **detached child process** through
`wand run` or `wand scope`, the same way `--watch` does. That child
survives home: closing the UI, or the whole terminal it runs in,
never kills work already spawned. Press `e` again to disengage.

The header names what engage mode is doing:

```
 wand · WND                                                     7 waiting on you
  engaged · idle · next poll in 40s
```

```
 wand · WND                                                     7 waiting on you
  engaged · dispatched WND-9 (run) · next poll in 60s
```

```
 wand · WND                                                     7 waiting on you
  engaged · reaped WND-1 · next poll in 60s
```

A tick that both swept and dispatched reads as one line: `engaged · reaped
WND-1, dispatched WND-9 (run) · next poll in 60s`.

**Engaging is always a deliberate key press, never a default.** Bare
`wand` opens home read-for-work but never pre-engaged — opening a
dashboard must not start spending money. Nothing engages it for you.

**Multiple engaged homes, and an engaged home alongside a standalone
`wand dispatch`, are all safe together.** Engaging acquires the same
per-repo dispatch lock `wand dispatch` itself arbitrates: the loser of a
pass — whichever process asks for the lock second — simply cannot toggle
on, and the `e` key says why. Disengaging, or quitting home while
engaged, releases the lock; a killed terminal releases it the same way
`--watch` does, through the lock's own dead-holder reclaim rather than
through any code in home.

Engage mode needs the same dependencies `wand dispatch` needs to run a
winner — `LINEAR_API_KEY`, an authenticated `gh`, `commands.verify` in
`wand.toml`, and a resolvable team key — plus whichever of `--harness`,
`--model` and `--effort` you want a spawned winner to run with. Missing any
of them does not stop home from opening: it opens exactly as it
always has, read and judge still work, and only the `e` key refuses, saying
why.

`wand dispatch`'s own conformance gate, wherever it lands, applies here
unchanged — engage mode selects a winner through the same `dispatch.Select`
every standalone pass uses, so a gate placed there covers both without
engage mode needing its own copy of it.

Left out, deliberately: engage mode does not act on the `parked` label — a
parked lane is a decision only a human at home makes, by pressing
`c`, and an engaged home is by definition in front of a person who can
press it. The poll interval is a flag, not a covenant parameter; that
remains a natural extension if engage mode earns it.

## Looking at the interface without a terminal

A terminal interface is the one kind of program you cannot observe by
reading its output, which makes it the one kind an agent cannot check its
own work on. So the renderer is exposed as a command:

```bash
wand ui --script "1,j,t" --dump-screen
```

**The dump and the test suite share one renderer.** The output is
byte-identical to the golden screen files the tests compare against. It is
not an approximation of the interface — it is the interface, replayed
through a virtual terminal.

`--dump-screen` renders a **built-in sample board**, not your team. Two
reasons, and the second is the important one:

1. A screen built from whatever happened to be in a live Triage is one no
   two people can diff, and it would need an API key to look at a user
   interface.
2. That path constructs the screen **with no writer behind it**. An agent
   can walk to every screen — the blessing screen included, which is the one
   most worth being able to inspect — and reach none of the transitions the
   guard forbids it. The refusal is structural rather than a check: there is
   nothing for the confirm key to call.

`--sample` opens that same read-only board interactively, for a person who
wants to walk the interface without a team.

### Scripts

`--script` takes a comma-separated key sequence, applied in order before the
screen is rendered. Each key is:

* a named key — `enter`, `return`, `esc`, `escape`, `tab`, `space`,
  `backspace`, `delete`, `up`, `down`, `left`, `right`, `home`, `end`,
  `pgup`, `pgdown`;
* a single character — `j`, `t`, `/`;
* either of those with `ctrl+`, `alt+` or `shift+` prefixes — `ctrl+c`,
  `shift+tab`.

The names are the ones Bubble Tea's own `Key.String()` produces, so a
binding declared as `key.WithKeys("esc")` is driven by writing `esc`. The
same parser backs the test harness, so a reproduction typed at a shell is
the same input a test replays. Printable keys type into a text field, which
is how a scripted `x,o,b,s,o,l,e,t,e` reaches the cancellation screen with a
reason on it.

Every script that reaches a queue row starts with `1`, `2` or `3` — the
landing screen's own key, opening the view that row lives in — before the
usual `j`/`k`/`enter` and disposition keys. A script that opens with a
disposition or move key straight away, as if the landing screen were not
there, does nothing on the first key: none of them mean anything on home
itself.

`--script` without `--dump-screen` is an error rather than a silent no-op.

## Flags

| Flag | Default | Description |
|---|---|---|
| `--team-key KEY` | — | Linear team key. Falls back to `[team] key` in the nearest `wand.toml`. Not read at all, and an error if passed, with `--sample` or `--dump-screen`; required (from a flag or a file) otherwise. |
| `--sample` | off | Open the built-in read-only sample board instead of reading Linear. |
| `--dump-screen` | off | Render the sample board to plain text and print it. |
| `--script KEYS` | — | Comma-separated keys to apply before rendering. Requires `--dump-screen`. |
| `--width N` | `80` | Terminal width to render at. Requires `--dump-screen`. |
| `--height N` | `24` | Terminal height to render at. Requires `--dump-screen`. |
| `--harness` | `claude-code` | Worker harness engage mode runs a winner through: `claude-code` or `codex`. Does not apply with `--sample` or `--dump-screen`. |
| `--model` | — | Model engage mode gives every worker. Default: the harness's default. Does not apply with `--sample` or `--dump-screen`. |
| `--effort` | — | Reasoning effort engage mode gives every worker. Default: the harness's default. Does not apply with `--sample` or `--dump-screen`. |
| `--interval` | `1m` | How often engage mode polls once engaged. Does not apply with `--sample` or `--dump-screen`. |

The size flags are explicit rather than read from the ambient terminal: a
screen whose width depends on whoever ran it is a screen no two people can
compare.

They apply to `--dump-screen` only, and passing either one without it is an
error rather than a silent no-op. An interactive board is sized by the
terminal it runs in — the program's first message is the real window size,
which overrules anything given here — so accepting the flag and then
ignoring it would say "this is the width I asked for" on a screen that is
whatever width the window happens to be.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Home exited cleanly, or the screen was printed. |
| `1` | No resolvable team key (neither `--team-key` nor `[team] key` in `wand.toml`), an unreachable Linear, an unparseable `--script`, `--script` or `--width`/`--height` without `--dump-screen`, or a failure in the program itself. |

The board is read *before* the alternate screen opens, deliberately: a
Linear failure is then an ordinary error on stderr rather than a message
trapped inside a full-screen app you have to quit out of to read.

## Examples

Your board:

```bash
wand ui --team-key WND
```

The interface, with no team and no API key:

```bash
wand ui --sample
```

Look at the opening screen without a terminal:

```bash
wand ui --dump-screen
```

Walk to the blessing screen and print it:

```bash
wand ui --script "1,j,t" --dump-screen
```

At a size that is not the default:

```bash
wand ui --dump-screen --width 120 --height 40
```
