# wand

A Go CLI and TUI, built on Cobra + [fang](https://github.com/charmbracelet/fang)
and [Bubble Tea v2](https://charm.land/bubbletea/v2). Public, MIT licensed.

`init` and `guard` are real: `init` bootstraps a Linear team to the covenant
(parameterized by a checked-in `wand.toml` when present) and installs the
guard's hook shim; `guard` is the status verdict oracle the shim routes
Linear writes through. `covenant` and `bless` are stubs today. [PLAN.md](./PLAN.md) is the build order and the
reasoning — a deliberately mortal document; the Linear tickets are the
authoritative version of the work. The TUI's verification layer is described
below; read that before changing anything under `internal/tui`.

## You cannot see the UI. Here is how you look at it.

A TUI is the one kind of program you cannot observe by reading its output. So
the repo renders screens to plain text on demand:

```bash
go run . ui --script "j,enter" --dump-screen
```

`--script` is a comma-separated key sequence (`j`, `enter`, `esc`, `ctrl+c`,
`down`, …) applied before rendering. No terminal is required; the output is
plain text you can read directly.

**That command and the test suite share one renderer**, so its output is
byte-identical to the golden files in `internal/tui/testdata/screens/`. If you
want to know what a change did to the screen, run it. Do not guess, and do not
ask the user to look.

`make screen SCRIPT=j,enter` is the same thing.

## Layout

```
main.go              at the repo root, per Go CLI convention; hands off to internal/cli
internal/cli/        cobra commands, fang wiring, the --dump-screen path
internal/linear/     the Linear GraphQL client — raw net/http, no GraphQL library, on purpose
internal/covenant/   the process contract: fixed topology, parameterized covenant
internal/bootstrap/  planner/executor over the covenant; all decisions in the pure Plan
internal/guard/      the one verdict function: which ticket writes an agent may never make
internal/shim/       generates the PreToolUse hook entry that routes save_issue to wand guard
internal/tui/        Bubble Tea models — the app itself
  testdata/screens/  golden screens (plain text pictures of the UI)
internal/theme/      every lipgloss style, in one place
internal/screen/     the renderer: model -> real program -> vt -> text
internal/tuitest/    test-facing layer over internal/screen
e2e/                 pty smoke test + guard exit-code contract, behind the `e2e` build tag
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
| 3 | `e2e/` | TTY detection, alt-screen, signals, exit codes. One pty smoke test — keep it that way — plus the guard hook's exit-code contract (plain exec, no pty). |

Tier 2 golden-files the **rendered screen**, not the ANSI byte stream. The byte
stream contains every intermediate frame plus color escapes: brittle, and
unreadable in a diff. Screens are replayed through a virtual terminal
(`x/vt`) and stored as text. Keep it that way — the readability is the point.

## Rules

**Never run `-update` to make a failing test pass.** Regenerate, read the diff,
and confirm the change was intended before committing. A golden an agent can
blindly refresh is worse than no test: it reports success unconditionally.

```bash
make update-goldens
git diff -- '*/testdata/screens/*.txt'   # then actually read it
```

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
  may set In Progress, In Review, Needs Input, Backlog and Triage. This is
  enforced, not requested: `.claude/settings.json` routes every `save_issue`
  through `wand guard` (needs `wand` on PATH — `make install`), and the
  verdicts live in `internal/guard`.
- **Found something that isn't your ticket?** File it into Triage with the
  `agent-filed` label and carry on. Search for a duplicate first.
- **Branch from the ticket's own branch name** (Linear provides it), and
  lead the PR title with the identifier in brackets: `[WND-12] …`. This
  repo squash-merges, so the PR title is the commit subject on `main`
  forever.
- **Never push to `main`.** Open a PR and drive it to green.
- **Docs change with the code.** A change to behavior that README, PLAN.md
  or the docs describe carries the doc change in the same PR. Docs rot is
  a code defect.

## Commands

```bash
go run . ui          # run the TUI interactively (needs a terminal)
make test            # tiers 0-2, fast
make test-e2e        # tier 3, needs a pty
make check           # what CI runs: gofmt, vet, test
make update-goldens  # then read the diff
make build           # bin/wand
make install         # put wand on the user's PATH
make help            # every target
```

`go run . ui` needs a real terminal, so it is not something you can drive from
a tool call — use `--dump-screen` above to see the UI, and leave the
interactive run to the user.

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
