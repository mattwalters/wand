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
$ wand ui

   wand

 │ init
 │ Set up wand in this repository

   covenant
   Read and edit the repository covenant

   bless
   Promote work along the blessing path

   ↑/k up • ↓/j down • enter select • q quit
```

> **Status: early.** `init` bootstraps a Linear team to the covenant and
> installs the guard hook, `guard` enforces the covenant's authorization
> rules, `queue` and `ticket` are the read layer, `doctor` reports drift,
> and `scope` is the first orchestrator. `covenant` and `bless` are stubs.

**A covenant, not a config file.** The state graph — Triage → Backlog →
Scoping → Todo → Needs Input → In Progress → In Review → Done — is wand's
opinion, gofmt-style. A checked-in `wand.toml` carries the *parameters* of
the machine — status names, caps, commands — never its shape.
[The covenant](covenant/) is the reasoning behind every state in it.

**A guard that outranks the agent.** Promoting to Todo blesses building,
promoting to Scoping blesses research, and Done, Canceled and Duplicate
close a ticket. Those are a human's call, so
[`wand guard`](commands/guard/) refuses them — as a harness hook, on the
tool call, before the write happens.

**Verbs instead of raw API calls.** [`claim`](commands/claim/),
[`handback`](commands/handback/), [`abandon`](commands/abandon/) and
[`file`](commands/file/) each encode an ordering rule that used to live in
prose, where it was followed only sometimes: the question is posted before
the ticket parks on it, the evidence before the demotion, the duplicate
search before the filing.

**Orchestrators that hand back.** [`wand scope`](commands/scope/) sends a
cold, read-only scout over one ticket and writes a plan plus the argued
options a human blesses it on — ending at Needs Input, never at Todo. An
agent does not bless its own plan.

## Install

```bash
brew install mattwalters/wand/wand
```

Or with Go:

```bash
go install github.com/mattwalters/wand@latest
```

That puts `wand` in `$(go env GOPATH)/bin`, which needs to be on your
`PATH`. Prebuilt binaries for macOS, Linux and Windows are on the
[releases page](https://github.com/mattwalters/wand/releases).

## Start here

[The covenant](covenant/) — the process contract wand maintains, and the
reasoning behind every state and rule in it. The covenant file carries the
parameters; this page carries the *why*, which deliberately does not live
in the file.

[The cockpit](commands/ui/) — `wand ui`: one screen answering one question,
*what is waiting on me?*, and the one place in wand a human blesses work.

[Commands](commands/) — the reference: every command wand implements, one
page each, with its flags, its exit codes and what it is for.

## Versions

These docs are versioned with the tool. This site's root is the latest
build — it tracks `main` — and every release also publishes a frozen copy
at `/vX.Y.Z/`, so a repo pinned to wand vX.Y.Z can read the doctrine
matching its covenant schema. Pick the version in the header.
