---
title: Commands
weight: 20
summary: Every command wand implements, one page each, with its flags and its exit codes.
# Command pages are titled with the command itself, so they are set in the
# face you type it in — in the rail, in the index and in their own heading.
# One cascade beats the same flag repeated on thirteen files.
cascade:
  cmd: true
---

Every command wand implements today, one page each. The pages are written
from the command definitions, not from memory: flags, help text and exit
codes match what the binary does.

There is no standalone `bless` page, because promoting a ticket is a human
act with a human door: it lives in the cockpit, [`wand ui`](../ui/). A
one-shot CLI verb that promoted a ticket to Todo would be a verb an agent
could call, which is the whole thing the guard exists to prevent.
[`wand pm bless`](../pm/) is not that verb: it never sets a guarded status,
only files a validated, human-reviewed proposal into Triage — the same
status [`wand file`](../file/) already lands an agent's own findings in —
so it is documented on `pm`'s own page rather than getting one of its own.

## What every command shares

**`LINEAR_API_KEY`.** Everything that talks to Linear reads a full-access
key from the environment, never from a file — a key in a checked-in file is
a key in the repo forever. Create one in Linear's security settings.
`wand version` is the exception, along with `wand ui --sample` and
`wand ui --dump-screen`, which render a built-in board.

**The covenant file.** A checked-in [`wand.toml`](../covenant/)
parameterizes the covenant — status names, caps, commands. Absent one, the
stock covenant applies. `init`, `doctor` and `queue` read it from the
working directory; the lifecycle verbs walk up from the working directory
to find it, so a verb run from a subdirectory sees the same covenant as one
run at the root. Run the first three from the repo root.

**Exit codes.** Unless a page says otherwise, a command exits `0` on
success and `1` on any failure, with the reason on stderr. Several commands
say otherwise, and each of their codes is a contract something else depends
on: `guard` (the harness hook), `doctor` (CI), `file` (a refusal to file a
duplicate is a failure, on purpose), the orchestrators `scope`, `pm` and
`run`, whose `0`/`2`/`3` tell a scheduler how a run ended — scoped or
converged (or proposed), handed back, or parked — leaving `1` for a run
that never started; `pm bless`, whose own `0`/`1`/`3` mark a batched write
rather than a single one — filed, refused before any write, or a write
that failed partway through, reporting what had already landed; and
[`dispatch`](../dispatch/), the selector over the orchestrators, whose
`0`–`5` add locked, nothing-to-do and Linear-unreachable to that same
vocabulary — a scheduler's whole view of a pass is a status and a log.
