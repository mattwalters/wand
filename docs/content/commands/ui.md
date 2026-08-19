---
title: wand ui
weight: 200
summary: Open the interactive interface — or render it to plain text.
---

`ui` opens wand's interactive interface. With `--dump-screen` it renders
that interface to plain text and prints it instead, which needs no terminal
at all.

## Synopsis

```
wand ui
wand ui --dump-screen [--script KEYS] [--width N] [--height N]
```

## What it does

Run bare, it takes over the terminal in the usual way.

`--dump-screen` is the interesting half. A terminal interface is the one
kind of program you cannot observe by reading its output, which makes it
the one kind an agent cannot check its own work on. So the renderer is
exposed as a command: combined with `--script`, any screen reachable by
keystrokes is reachable from a single non-interactive invocation.

**The dump and the test suite share one renderer.** The output of
`--dump-screen` is byte-identical to the golden screen files the tests
compare against. It is not an approximation of the interface — it is the
interface, replayed through a virtual terminal.

### Scripts

`--script` takes a comma-separated key sequence, applied in order before
the screen is rendered. Each key is:

* a named key — `enter`, `return`, `esc`, `escape`, `tab`, `space`,
  `backspace`, `delete`, `up`, `down`, `left`, `right`, `home`, `end`,
  `pgup`, `pgdown`;
* a single character — `j`, `q`, `/`;
* either of those with `ctrl+`, `alt+` or `shift+` prefixes — `ctrl+c`,
  `shift+tab`.

The names are the ones Bubble Tea's own `Key.String()` produces, so a
binding declared as `key.WithKeys("esc")` is driven by writing `esc`. The
same parser backs the test harness, so a reproduction typed at a shell is
the same input a test replays.

`--script` without `--dump-screen` is an error rather than a silent no-op.

## Flags

| Flag | Default | Description |
|---|---|---|
| `--dump-screen` | off | Render the interface to plain text and print it. |
| `--script KEYS` | — | Comma-separated keys to apply before rendering. Requires `--dump-screen`. |
| `--width N` | `80` | Terminal width to render at. |
| `--height N` | `24` | Terminal height to render at. |

The size flags are explicit rather than read from the ambient terminal: a
screen whose width depends on whoever ran it is a screen no two people
can compare.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | The interface exited cleanly, or the screen was printed. |
| `1` | An unparseable `--script`, `--script` without `--dump-screen`, or a failure in the program itself. |

## Examples

Run it:

```bash
wand ui
```

Look at the opening screen without a terminal:

```bash
wand ui --dump-screen
```

Press `j` then `enter`, and print what that leaves on screen:

```bash
wand ui --script "j,enter" --dump-screen
```

At a size that is not the default:

```bash
wand ui --dump-screen --width 120 --height 40
```
