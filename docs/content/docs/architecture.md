---
title: No daemon
# Between journal.md (15) and commands/_index.md (20): this page's own
# .Pages.ByWeight position in the auto-generated /docs/ index (list.html)
# depends on it, separately from its hand-curated hugo.toml menu weight.
weight: 17
summary: The decision that wand runs no resident server, and what would reopen it.
---

wand runs no resident server. There is no daemon listening on a socket, no
process that has to be started once and kept alive for the system to work.
Durability, multi-client attach and scheduling all fall out of the
crash-only journal instead; "managed mode" is the UI spawning **detached**
dispatch children, never a server dispatching on its own clock; and
multiple UIs or dispatchers on one machine are arbitrated by the dispatch
lock. The shared state between them all is the filesystem journal plus
Linear itself — nothing else.

This was argued once, deliberately, and this page exists so a future
session that wants a server for some feature finds the argument instead of
re-having it from nothing. The daemon question will recur — every
process-manager feature invites it.

## What a daemon would buy, and what the journal buys instead

A resident server is the obvious way to get three things: durability
across crashes, several clients sharing one view of the world, and
scheduling — something deciding when the next unit of work runs. wand gets
all three from the crash-only journal (`internal/journal`), and the
decision not to build a daemon holds only because that substitution is
real:

- **Durability.** A record announces a transition before it happens, never
  after: `Run.StartPhase` writes and syncs before the phase runs, so a
  crash mid-phase leaves a journal a resume can read, not a ticket stuck
  In Progress with nothing to explain it. A daemon buys durability by
  staying up; the journal buys it by making every write outlive the
  process that made it, which holds even if nothing is running at all.
- **Who holds a run, and whether they're still alive.** A lease
  (`internal/journal/lease.go`) records who and where; the proof that they
  are still running is a separate OS-released file lock, not the lease
  itself. Liveness is three-valued — Unknown, Alive, Dead — and a sweeper
  may act only on Dead, never on a guess. A lease written on another host
  reads Unknown forever. A daemon would answer the same question with a
  heartbeat, which is weaker: a heartbeat can go stale while its process is
  fine, or keep beating past a hang.
- **Recovery.** `internal/sweep` acts on at most one candidate per pass —
  ranked, and vetted the way dispatch picks a winner — and the only
  verdict it may act on is a lease's liveness reading Dead. There is no
  daemon reconciling state in the background; sweep is a cold pass invoked
  the same way any other verb is, touching only what the journal already
  proved dead.
- **Resume.** A run re-enters through the journal's own replay — a pure
  function from records to state, no I/O, nothing live to ask. Whatever
  restarts a run gets back exactly where the journal says it was, because
  that is the only place it was ever recorded.
- **Multi-client attach and scheduling.** These are the two that most
  invite "just run a server." Several UIs or dispatchers on one machine
  are arbitrated by the dispatch lock (`internal/dispatch/lock.go`) — one
  mkdir-based lock per repository, proven dead by a liveness probe on this
  host, deliberately not the journal's own flock: a lock directory is
  something a person can `ls` and remove by hand, the way a scheduler's
  own idiom (wrapping a cron job in `flock(1)`) is, and that command does
  not ship on macOS. Scheduling is `--watch` polling and spawning detached
  children (see Managed mode, below) — no listener, nothing that has to be
  kept alive by anything but the OS's own process table.

Against all of that, what a daemon would cost: a socket protocol to define
and version, a lifecycle to manage (start, stop, restart-on-crash, who is
responsible for that), version skew between a long-lived server and
whatever CLI invocations talk to it, and an auth story for anything that
isn't strictly local. None of that is impossible to build. It is the
failure mode of building infrastructure the system does not yet need,
before the need is real — and every one of the guarantees above already
exists without it.

## Managed mode

"Managed mode" is the shape scheduling takes without a daemon: the UI
spawning detached dispatch children, rather than a server dispatching on
its own clock. `wand dispatch --watch` already does the spawning half —
each tick that finds capacity spawns the winner as a detached process that
survives the watcher, so several lanes can be in flight at once without
anything staying resident between ticks except the OS's own process table.

What doesn't exist yet: the UI is not itself what spawns `--watch` or a
dispatch child today — `--watch` is invoked from the CLI directly, by a
person. Managed mode, the cockpit spawning and supervising detached
dispatch children, is the shape this decision commits to, not a shipped
feature. When it is built it inherits the same constraint: whatever it
spawns must be detached, surviving the process that spawned it — a UI that
had to stay open for a dispatch loop to keep running would already be most
of the daemon this decision rejects.

## What would reopen this

The decision is "not now, and here is what 'now' would look like." Any of
the following is a reason to come back to this page and re-derive the
trade-off, not a reason to quietly build around it:

- **Push-based Linear webhooks replacing polling.** A webhook needs
  something listening for the push, which means a long-lived process by
  definition — the one requirement this decision exists to avoid.
- **A web or remote UI.** Everything above — the dispatch lock, a lease's
  host field, sweep's single-machine reach — assumes the UI and the run it
  is arbitrating are on the same machine. A remote UI breaks that
  assumption at the root.
- **Multi-machine orchestration.** The dispatch lock and leases are
  per-machine by construction; Linear status transitions are the only
  cross-machine signal, and they give coarse arbitration at best. There is
  a real race window between a dispatcher vetting a winner and the later,
  separate write that claims it by flipping its Linear status — a second
  machine reading the same board in between sees no reason not to pick the
  same ticket.
- **Shared team analytics.** [The run ledger](../journal/) lives per-run,
  per-machine, with no aggregation across runs today, let alone across a
  team's laptops. Anything wanting a team-wide view fragments across
  machines until something aggregates it, and that something is exactly
  the kind of long-lived shared service this decision defers.

None of these are hypothetical forever — they are the specific shapes of
work that would make "no daemon" the wrong call. Until one of them is
real, the journal plus Linear is the whole system of record, and it does
not need anything else to hold it up.
