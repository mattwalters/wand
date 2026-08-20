---
title: wand ui
weight: 200
summary: The cockpit — everything waiting on a human, and the one place blessing happens.
---

`ui` opens wand's cockpit: one screen answering one question — *what is
waiting on me?*

Bare `wand`, with a terminal attached and no flags, does the same thing:
`wand` and `wand ui` are equivalent. `ui` is the explicit spelling — the one
the flags below live on — and the one to reach for outside a terminal, where
bare `wand` falls back to printing help instead.

## Synopsis

```
wand
wand ui --team-key WND
wand ui --sample
wand ui --dump-screen [--script KEYS] [--width N] [--height N]
```

## The four queues

```
 wand cockpit · WND                                             6 waiting on you

  Triage  2 to judge
› WND-42  doctor prints an empty drift section on a clean board              Low
  WND-41  guard: a raw state UUID is not matched

  Needs Input  1 to answer
  WND-38  Second harness adapter: which one?                                High

  Ready for human  1 to look at
  WND-35  Run journal and lease store                                  In Review

  Lanes  2 to resolve
  stuck    WND-36  held by pid 48213 on studio.local, which is gone; the run …
  parked   WND-33  the worktree was dirty at handoff; refusing to park noise …

  judge  t ✦Todo  s ✦Scoping  b Backlog  u unranked  d duplicate  x cancel
  ↑/k ↓/j move • enter open • r refresh • q quit
```

Each of the four is a queue nothing drains on its own, and each is
invisible until something puts it on one screen:

* **Triage** — what agents filed, waiting to be judged. Ranked in the same
  order [`wand queue`](../queue/) ranks work, so the ticket you bless first
  is the one an agent starts first.
* **Needs Input** — runs parked on a question. This is where a scope ends
  and where a run hands back.
* **Ready for human** — every open issue carrying the `ready-for-human`
  label: a pull request to review, a merge to press. Closed issues are
  dropped, because the label outlives the merge that answered it.
* **Lanes** — runs the crash-only run journal says a person has to
  resolve. Four kinds, worst first: `stuck` (the journal says it is running
  and its holder is provably dead), `orphaned` (a live run whose ticket is
  not in a started status — nothing on the board claims the lane),
  `unclear` (a holder on a machine this one cannot see, which is never swept
  automatically), and `parked` (the run stopped and recorded why). Read-only:
  a lane is not a ticket, and resolving one means going to the machine.

The sections are always all four, even when empty. A queue that vanished
when it drained would teach you to stop looking for it, and the day it
refilled you would not notice.

**Backlog is deliberately absent.** A Backlog ticket is not waiting on you;
it is the pool, and browsing a pool is Linear's job, done better there. The
cockpit shows what has *stopped*, not what exists.

`enter` opens the row under the cursor; `r` re-reads the whole board. The
two compose: refreshing while a row is open re-resolves that row against
what came back, so the key you pressed to get current data cannot be the
key that hides how stale it is. If the row is gone from the re-read board —
somebody else judged it — the screen returns to the board and says so
rather than closing without a word. A refresh that *fails* says that too,
on whichever screen you were on.

## Blessing

Promoting a ticket to **Todo** or **Scoping** is the transition
[`wand guard`](../guard/) refuses everywhere else, because it hands out
authorization an agent does not have. The cockpit is the one place a person
grants it — and it is a moment, not a keystroke:

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

## The six judgments

They apply to Triage and Needs Input rows. Ready-for-human and lane rows
offer none, and the screen says why rather than leaving a key to do nothing.

| Key | Judgment | Asks for | Writes |
|---|---|---|---|
| `t` | Bless → Todo | — | status |
| `s` | Bless → Scoping | — | status |
| `b` | Backlog, ranked | a priority, `1`–`4` | status + priority |
| `u` | Backlog, unranked | — | status + priority `0` |
| `d` | Duplicate | the canonical issue | relation, **then** status |
| `x` | Canceled, with reason | the reason | comment, **then** status |

`enter` confirms, `esc` goes back. The unranked Backlog move sends an
explicit `0` rather than leaving the field alone: carrying the old rank into
the pool is the opposite of the judgment "worth keeping, not worth ranking".

Duplicate and Cancel write their evidence *before* the status moves, for the
same reason [`wand handback`](../handback/) posts its question first. The
status write is what ends the ticket's visibility, so anything a reader will
later need has to already be on it. A crash between the two leaves an open
ticket carrying its own argument for closure, which a person can finish; the
reverse leaves a closed ticket nobody can explain.

When the status move fails after that first write has landed, the
confirmation stays up so you can press `enter` again — and the retry does
**not** repeat the write that already succeeded. The screen says so, and the
field it came from goes read-only, because the text is on the ticket and
editing it could no longer reach anywhere. Retrying from the top would post
the same cancellation reason twice, and would re-issue a relation Linear
already holds and now refuses, which would leave the duplicate permanently
uncompletable through the only screen that can complete it.

Every status is named by what it *means*, so a covenant that renames `todo`
to `Ready` gets a screen that says `→ Ready`.

## Looking at the interface without a terminal

A terminal interface is the one kind of program you cannot observe by
reading its output, which makes it the one kind an agent cannot check its
own work on. So the renderer is exposed as a command:

```bash
wand ui --script "j,t" --dump-screen
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
| `0` | The cockpit exited cleanly, or the screen was printed. |
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
wand ui --script "j,t" --dump-screen
```

At a size that is not the default:

```bash
wand ui --dump-screen --width 120 --height 40
```
