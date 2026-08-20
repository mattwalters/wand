---
title: Commands
---

Every command wand implements today, one page each. The pages are written
from the command definitions, not from memory: flags, help text and exit
codes match what the binary does.

`covenant` and `bless` are not here. They are stubs — they parse and print
and nothing else — and a reference page for a command that does not work
yet is worse than a missing one. They get pages in the change that makes
them real.

## What every command shares

**`LINEAR_API_KEY`.** Everything that talks to Linear reads a full-access
key from the environment, never from a file — a key in a checked-in file is
a key in the repo forever. Create one in Linear's security settings.
`wand ui` and `wand version` are the exceptions; they need no key.

**The covenant file.** A checked-in [`wand.toml`](../covenant/)
parameterizes the covenant — status names, caps, commands. Absent one, the
stock covenant applies. `init`, `doctor` and `queue` read it from the
working directory; the lifecycle verbs walk up from the working directory
to find it, so a verb run from a subdirectory sees the same covenant as one
run at the root. Run the first three from the repo root.

**Exit codes.** Unless a page says otherwise, a command exits `0` on
success and `1` on any failure, with the reason on stderr. Four commands
say otherwise, and each of their codes is a contract something else depends
on: `guard` (the harness hook), `doctor` (CI), `file` (a refusal to
file a duplicate is a failure, on purpose), and `run` (a scheduler reads
converged, handed-back and parked apart).
