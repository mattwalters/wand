@AGENTS.md

## Claude Code setup

The shared ticket-transition rules are enforced for Claude Code:
`.claude/settings.json` routes every `save_issue` through `wand guard`, and
the verdicts live in `internal/guard`. Ensure `wand` is on `PATH` before
working here:

```bash
make install
```

## You cannot see the UI. Here is how you look at it.

A TUI is the one kind of program you cannot observe by reading its output. So
the repo renders screens to plain text on demand:

```bash
go run . ui --script "j,enter" --dump-screen
```

`--dump-screen` renders the built-in sample board with no writer behind it, so
every screen is reachable and none of them can write. That is deliberate:
home is the one place in wand that performs the transitions `wand guard`
forbids, and the command an agent uses to *look* at it must not be a way to
*use* it.

`--script` is a comma-separated key sequence (`j`, `enter`, `esc`, `ctrl+c`,
`down`, …) applied before rendering. No terminal is required; the output is
plain text you can read directly.

**That command and the test suite share one renderer**, so its output is
byte-identical to the golden files in `internal/tui/testdata/screens/`. If you
want to know what a change did to the screen, run it. Do not guess, and do not
ask the user to look.

`make screen SCRIPT=j,enter` is the same thing.

`go run . ui --sample` needs a real terminal, so it is not something you can
drive from a tool call — use `--dump-screen` above to see the UI, and leave the
interactive run to the user.

## Golden-file changes

**Never run `-update` to make a failing test pass.** Regenerate, read the diff,
and confirm the change was intended before committing. A golden an agent can
blindly refresh is worse than no test: it reports success unconditionally.

```bash
make update-goldens
git diff -- '*/testdata/screens/*.txt'   # then actually read it
```
