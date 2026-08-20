---
title: wand version
weight: 210
summary: Report the build and the covenant schema this binary speaks.
---

`version` reports which wand this is, and which covenant schema it can
read.

## Synopsis

```
wand version
```

## What it does

Prints four lines: the version, the commit, the build date, and the
**covenant schema version** this binary speaks.

The schema line is the one that matters operationally. A repo's `wand.toml`
declares the schema it was written against; compare the two to learn
whether this binary can read that file. A file older than the binary is
read as-is — but it still gets the binary's topology, because the state
graph ships centrally rather than per repo. [The covenant](../../covenant/)
spells out what a bump does and does not change. The docs are versioned the same
way, so a repo pinned to wand vX.Y.Z can read the doctrine matching its
covenant schema — pick the version in the site header.

The build fields are stamped at release time. An unstamped build — one
built straight from a clone — reports `dev`, `none` and `unknown`, which is
the honest answer to "which wand am I running?" rather than a version
number nobody can trace.

`wand --version` prints the version alone; `wand version` prints the whole
report.

## Flags

None.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Always. |

## Examples

A release build:

```bash
wand version
```

```
wand v0.2.0
  commit:          8a63311
  built:           2026-08-19T20:11:04Z
  covenant schema: 2
```

A build from a clone:

```
wand dev
  commit:          none
  built:           unknown
  covenant schema: 2
```
