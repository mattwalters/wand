# wand

An agent toolkit for repositories: it sets up and maintains a repo's agent
machinery — its covenant, and the blessing path work travels along.

> **Status: early.** The scaffold and its verification layer are in place. The
> `init`, `covenant` and `bless` verbs are stubs.

## Install

```bash
go install github.com/mattwalters/wand/cmd/wand@latest
```

Or build from source:

```bash
make build   # -> bin/wand
```

## Usage

```bash
wand          # help
wand ui       # the interactive interface
```

## Testing a TUI

Terminal UIs are awkward to test and nearly impossible for an AI agent to
review, because the thing under test is a picture. wand renders screens to
plain text instead:

```console
$ wand ui --script "j,enter" --dump-screen
 wand covenant

  Read and edit the repository covenant

  "covenant" is not implemented yet.

  esc back • q quit
```

`--script` applies a key sequence first, so any screen is reachable from one
command, and no terminal is needed.

The test suite renders through the same code path, so those snapshots are
stored as golden files that read as pictures of the UI:

```go
tuitest.AssertScreen(t, "detail", tui.New(80, 24), "j,enter")
```

When a screen changes, the failure is a unified diff of the screen itself
rather than a wall of escape codes. Under the hood a real `tea.Program` runs
with its output captured, and those ANSI bytes are replayed through a virtual
terminal emulator to recover the grid of characters a user would see.

Four tiers cover the app: pure `Update` tests, runtime wiring tests, golden
screens, and one end-to-end test that drives the compiled binary through a real
pseudo-terminal. See [CLAUDE.md](CLAUDE.md) for the details and the rules that
keep it deterministic.

## Development

```bash
make test            # fast suite
make test-e2e        # pty smoke test
make check           # gofmt + vet + test
make update-goldens  # regenerate screens (then read the diff)
```

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Lip Gloss](https://github.com/charmbracelet/lipgloss),
[Bubbles](https://github.com/charmbracelet/bubbles) and
[fang](https://github.com/charmbracelet/fang).

## License

MIT — see [LICENSE](LICENSE).
