# wand

A Go CLI and TUI, built on Cobra + [fang](https://github.com/charmbracelet/fang)
and [Bubble Tea v2](https://charm.land/bubbletea/v2). Public, MIT licensed.

The verbs — `init`, `covenant`, `bless` — are stubs today. The machinery that
exists is the verification layer, described below. Read that before changing
anything under `internal/tui`.

## You cannot see the UI. Here is how you look at it.

A TUI is the one kind of program you cannot observe by reading its output. So
the repo renders screens to plain text on demand:

```bash
go run ./cmd/wand ui --script "j,enter" --dump-screen
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
cmd/wand/            main; hands off to internal/cli
internal/cli/        cobra commands, fang wiring, the --dump-screen path
internal/tui/        Bubble Tea models — the app itself
  testdata/screens/  golden screens (plain text pictures of the UI)
internal/theme/      every lipgloss style, in one place
internal/screen/     the renderer: model -> real program -> vt -> text
internal/tuitest/    test-facing layer over internal/screen
e2e/                 pty smoke test, behind the `e2e` build tag
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
| 3 | `e2e/` | TTY detection, alt-screen, signals, exit codes. One smoke test — keep it that way. |

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

## Commands

```bash
make test            # tiers 0-2, fast
make test-e2e        # tier 3, needs a pty
make check           # what CI runs: gofmt, vet, test
make update-goldens  # then read the diff
make build           # bin/wand
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
