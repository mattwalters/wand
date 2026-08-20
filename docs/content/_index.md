---
title: wand
tagline: An agent toolkit for repositories.
lede: |
  wand sets up and maintains a repo's agent machinery — its covenant, and
  the blessing path work travels along. It wires a repository to a
  [Linear](https://linear.app) team and runs an opinionated lifecycle
  through it, built for the reality that much of the work in a repo is now
  done by coding agents — and that the dangerous part of agent work is not
  the code. It is the authorization: what a bot may start, what it may
  close, and what only a human may decide.
install: brew install mattwalters/wand/wand
---

## What it is

```console
$ wand ui --sample

 wand cockpit · WND                                             6 waiting on you
  sample board — reads no Linear team, and writes none

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
  ↑/k ↓/j move • enter open • q quit
```

One screen answering one question — *what is waiting on me?* — and the one
place in wand where a human blesses work. Everything on it is a decision an
agent is not allowed to make on its own.

That is the built-in sample board: no Linear key, no setup, nothing
written. `wand ui --team-key WND` shows your team's.

> **Status: early.** `init` bootstraps a Linear team to the covenant and
> installs the guard hook, `guard` enforces the covenant's authorization
> rules, `queue` and `ticket` are the read layer, `doctor` reports drift,
> and `scope` and `run` are the first two orchestrators. `dispatch`, the
> last one, is not built yet.

**A covenant, not a config file.** The state graph — Triage → Backlog →
Scoping → Scoped → Todo → Needs Input → In Progress → In Review → Done — is
wand's opinion, gofmt-style. A checked-in `wand.toml` carries the *parameters* of
the machine — status names, caps, commands — never its shape.
[The covenant](docs/covenant/) is the reasoning behind every state in it.

**A guard that outranks the agent.** Promoting to Todo blesses building,
promoting to Scoping blesses research, and Done, Canceled and Duplicate
close a ticket. Those are a human's call, so
[`wand guard`](docs/commands/guard/) refuses them — as a harness hook, on the
tool call, before the write happens.

**Verbs instead of raw API calls.** [`claim`](docs/commands/claim/),
[`handback`](docs/commands/handback/), [`abandon`](docs/commands/abandon/) and
[`file`](docs/commands/file/) each encode an ordering rule that used to live in
prose, where it was followed only sometimes: the question is posted before
the ticket parks on it, the evidence before the demotion, the duplicate
search before the filing.

**Orchestrators that hand back.** [`wand scope`](docs/commands/scope/) sends
a cold, read-only scout over one ticket and writes a plan plus the argued
options a human blesses it on — ending at Needs Input, never at Todo.
[`wand run`](docs/commands/run/) takes a ticket a human *has* blessed and
drives it through implement → CI → review → revise. Neither ever blesses
its own work.

## Install

```bash
brew install mattwalters/wand/wand
```

Or with a shell script, on macOS or Linux — this is the one to put on a
Linux box where the cask doesn't reach:

```bash
curl -fsSL https://wandcli.com/install.sh | sh
```

It downloads the release archive matching your OS and arch, verifies it
against the published checksums, and installs to `/usr/local/bin` (falling
back to `~/.local/bin` if that isn't writable — override either with
`WAND_INSTALL_DIR`). Pin a version with `WAND_VERSION=v0.1.0`.

Or with Go:

```bash
go install github.com/mattwalters/wand@latest
```

That puts `wand` in `$(go env GOPATH)/bin`, which needs to be on your
`PATH`. Prebuilt binaries for macOS, Linux and Windows (amd64 and arm64)
are on the [releases page](https://github.com/mattwalters/wand/releases);
the `v1` tag always points at the latest `v1.x.y`.

## Start here

[The documentation](docs/) — the doctrine and the reference, versioned with
the tool. It is also the Docs link in the header, on every page.

[The cockpit](docs/commands/ui/) — `wand ui`: one screen answering one
question, *what is waiting on me?*, and the one place in wand a human
blesses work.

[The covenant](docs/covenant/) — the process contract wand maintains, and
the reasoning behind every state and rule in it. The covenant file carries
the parameters; this page carries the *why*, which deliberately does not
live in the file.

[Commands](docs/commands/) — every command wand implements, one page each,
with its flags, its exit codes and what it is for.
