# wand

A Go CLI and TUI, built on Cobra + [fang](https://github.com/charmbracelet/fang)
and [Bubble Tea v2](https://charm.land/bubbletea/v2). Public, MIT licensed.

`init`, `guard`, `doctor`, `scope`, `run`, `dispatch`, `sweep` and `ui` are
real: `init` bootstraps a Linear team to the covenant (parameterized by a
checked-in `wand.toml` when present) and installs the guard's hook shim;
`guard` is the status verdict oracle the shim routes Linear writes through;
`doctor` diffs the live team against the covenant and reports drift (exit 0
clean, 1 drift, 2 could not check); `scope` is the first orchestrator — a
cold read-only scout over one Scoping ticket, whose hard-validated handoff
becomes a plan in the ticket body and argued options in a comment, ending
at Needs Input; `run` is the core orchestrator — implement → CI → review →
revise over cold workers, exit 0 converged, 2 handed back, 3 parked, 1
never started; `dispatch` is the selector over that loop — a thin,
read-mostly pass that picks the one ticket to run next through `run` or
`scope` and runs it, one ticket per pass, with `--watch` to poll and spawn
detached children; `sweep` is everything that happens after a run exits —
a re-review label, an unresolved PR thread on a ready-for-human ticket, or
a lease whose owner is provably dead, one action per pass; and `ui` is the
cockpit: the five queues waiting on a human, and the only surface in wand
that performs the transitions the guard forbids — blessing is a human act,
so it has a human door. [PLAN.md](./PLAN.md) is the build order and the
reasoning — a deliberately mortal document; the Linear tickets are the
authoritative version of the work. The TUI's verification layer is described
below; read that before changing anything under `internal/tui`.

## Layout

```
main.go              at the repo root, per Go CLI convention; hands off to internal/cli
internal/cli/        cobra commands, fang wiring, the --dump-screen path
internal/linear/     the Linear GraphQL client — raw net/http, no GraphQL library, on purpose
internal/covenant/   the process contract: fixed topology, parameterized covenant
internal/bootstrap/  planner/executor over the covenant; all decisions in the pure Plan
internal/guard/      the one verdict function: which ticket writes an agent may never make
internal/cockpit/    what is waiting on a human: the five queues, the seven judgments,
                     and the one write path that deliberately does not call the guard
internal/doctor/     read-only drift report: bootstrap.Plan as the diff, plus what Plan cannot express
internal/shim/       generates the PreToolUse hook entry that routes save_issue to wand guard
internal/worker/     the harness seam: an Adapter turns a Spec into one headless invocation;
                     the runner owns the timeout, credential strip, prompt contract and handoff
internal/workertest/ the isolation conformance suite every adapter must pass
internal/journal/    the crash-only run journal, lease and lock: journal before you act,
                     exactly one terminal record, and a dead holder provably dead
internal/scope/      the research orchestrator: cold scout -> hard handoff validation ->
                     optional critic and interview -> four writes in a fixed order
internal/run/        the core orchestrator behind `wand run`: implement → CI → review →
                     revise, a cold worker per phase, every external write the
                     orchestrator's, exactly one journaled terminal state per run
internal/dispatch/   the selector behind `wand dispatch`: lock, gc dead leases, rank
                     and vet Todo and Scoping, run the winner through run/scope
internal/sweep/      everything behind `wand sweep`: re-review labels, unresolved PR
                     threads and dead leases, ranked, one write per pass
internal/tui/        Bubble Tea models — the cockpit itself
  testdata/screens/  golden screens (plain text pictures of the UI)
internal/theme/      every lipgloss style, in one place
internal/screen/     the renderer: model -> real program -> vt -> text
internal/tuitest/    test-facing layer over internal/screen
e2e/                 pty smoke test + the exit-code contracts, behind the `e2e` build tag
docs/                the versioned docs site: Hugo, hand-rolled theme. The root is the
                     splash and the latest build (main); the documentation is under
                     /docs; every release tag also freezes a /vX.Y.Z copy
```

`internal/screen` must never import `testing`. Both the test harness and the
shipped binary depend on it, and that shared path is what makes the dump and
the goldens identical.

## The four test tiers

| Tier | Where | What it catches |
|---|---|---|
| 0 | `internal/tui/*_test.go` | `Update` transitions. Pure, instant. **Most tests belong here.** |
| 1 | `tuitest.FinalModel` | Wiring: keys reach the right branch, commands fire |
| 2 | `tuitest.AssertScreen` | What the user actually sees, as a golden screen |
| 3 | `e2e/` | TTY detection, alt-screen, signals, exit codes. One pty smoke test — keep it that way — plus the exit-code contracts of the guard hook, doctor, scope and run (plain exec, no pty). |

Off to the side of the tiers sits the **isolation conformance suite**
(`internal/workertest`): its structural half runs with `make test`, and its
live half — spawn the real harness, instruct it to write Linear and GitHub,
require every attempt to fail — is behind the `conformance` build tag
(`make test-conformance`) because it spends a real model call and needs the
harness installed and authenticated. It never runs in CI; it is run
deliberately, per adapter, whenever an adapter or its isolation flags change.

Also off to the side: `internal/journal`'s **crash tests** spawn the test
binary as a child, let it take a run, and kill it outright. They run with
`make test` and need nothing installed. Do not replace them with a fake that
unlocks on request — the property under test is that the *kernel* releases
the lock when a process dies in a way the process never gets to handle, and
a fake would be testing the fake. (A child that waits on `select {}` trips
Go's deadlock detector and ends itself, which is not the death under test;
`internal/journal/crash_test.go` sleeps instead and asserts the kill is what
ended it.)

Tier 2 golden-files the **rendered screen**, not the ANSI byte stream. The byte
stream contains every intermediate frame plus color escapes: brittle, and
unreadable in a diff. Screens are replayed through a virtual terminal
(`x/vt`) and stored as text. Keep it that way — the readability is the point.

## Rules

**Determinism.** Every one of these is a flaky golden waiting to happen:

- No `time.Now()` in `Update` — inject a clock.
- No unseeded randomness — inject the seed.
- Animation must respect `theme.Static`, which the renderer sets.
- Never read the ambient terminal size in a test; pass it explicitly.
- All I/O (git, Linear, filesystem, network) goes behind an interface with a
  fake. `Update` does no I/O.
- `View` is a pure function of model state. No logic lives there.

**Styles go in `internal/theme`**, not inline in a view.

**The determinism rules are not TUI-only.** Non-TUI packages follow the same
shape: I/O behind an interface with a fake, decisions in pure functions a
test can hold (`bootstrap.Plan` is the pattern), no ambient time or
randomness. When a live-API behavior costs an incident to learn, pin it in a
test named for the lesson.

## Process

Work lives in Linear, in the **Wand** team (key `WND`). wand runs its own
lifecycle — the one it ships — so sessions here follow it:

- **Take work only from Todo**, highest priority first, oldest first to
  break ties. Skip `human-only` and anything with an unresolved blocker.
  An empty Todo means: nothing for you right now.
- **Never move a ticket to Todo, Scoping, Done, Canceled or Duplicate.**
  Those grant or revoke authorization, and that is a human's act. Agents
  may set In Progress, In Review, Needs Input, Backlog and Triage.
- **Found something that isn't your ticket?** File it into Triage with the
  `agent-filed` label and carry on. Search for a duplicate first.
- **Branch from the ticket's own branch name** (Linear provides it), and
  lead the PR title with the identifier in brackets: `[WND-12] …`. This
  repo squash-merges, so the PR title is the commit subject on `main`
  forever.
- **Never push to `main`.** Open a PR and drive it to green.
- **Docs change with the code.** A change to behavior that README, PLAN.md
  or the docs site (`docs/`) describes carries the doc change in the same
  PR. Docs rot is a code defect.

## Commands

```bash
make test            # tiers 0-2, fast
make test-e2e        # tier 3, needs a pty
make test-conformance # worker isolation against the real harness (spends a model call)
make check           # what CI runs: gofmt, vet, test
make build           # bin/wand
make help            # every target
```

## Dependency notes

- Charm v2 packages import from `charm.land/...`, not `github.com/charmbracelet/...`.
  **`fang` is the exception** — its module is still declared as
  `github.com/charmbracelet/fang`.
- `x/vt`, `x/exp/teatest/v2` and `x/exp/golden` are untagged pseudo-versions
  from one `charmbracelet/x` monorepo commit. Keep them pinned to the *same*
  commit; mixing commits across that monorepo breaks the build. `internal/tuitest`
  exists partly to absorb their churn — tests should not import them directly.
- Two gotchas already paid for, worth not rediscovering:
  - Bubble Tea writes bare `\n`; a real tty expands it via `ONLCR`. Captured
    output has no tty, so `internal/screen` puts the emulator in LNM mode
    instead. Without it every screen renders as a diagonal cascade.
  - `vt.Emulator` writes query replies into an unbuffered pipe. Drain them or
    its `Write` blocks and the session deadlocks (see `e2e/`).

## Scope

This is a standalone single-repo tool. Nothing here may depend on the
surrounding workspace, and no product may depend on `wand`'s internals.
