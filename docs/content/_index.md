# wand

An agent toolkit for repositories: it sets up and maintains a repo's agent
machinery — its covenant, and the blessing path work travels along.

wand wires a repository to a [Linear](https://linear.app) team and runs an
opinionated software development lifecycle through it — one built for the
reality that much of the work in a repo is now done by coding agents, and
that the dangerous part of agent work is not the code, it is the
authorization: what a bot may start, what it may close, and what only a
human may decide.

These docs are versioned with the tool. Each release publishes its own
frozen copy, so a repo pinned to wand vX.Y.Z can read the doctrine matching
its covenant schema — pick the version in the header.

## Install

```bash
brew install mattwalters/wand/wand
```

Or with Go:

```bash
go install github.com/mattwalters/wand@latest
```

Prebuilt binaries for macOS, Linux and Windows are on the
[releases page](https://github.com/mattwalters/wand/releases).

## Start here

[The covenant](covenant/) — the process contract wand maintains, and the
reasoning behind every state and rule in it. The covenant file (`wand.toml`)
carries the parameters; this page carries the *why*, which deliberately does
not live in the file.

[Commands](commands/) — the reference: every command wand implements, one
page each, with its flags, its exit codes and what it is for.
