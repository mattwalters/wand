---
title: The covenant
---

The covenant is the process contract wand maintains in Linear: the states a
team's board must have, the rules about who may move work between them, and
the parameters of the lifecycle wand runs through it. This page is the
rationale — the *why* behind each state and rule, which deliberately does
not live in the covenant file. The file carries values; values without
their reasons rot into cargo cult.

The lifecycle was not designed on a whiteboard. It is extracted from a
private production system where it ran for months, agents and humans
working the same board, and where every rule below was paid for by a
specific failure.

## Why the shape is fixed

The state graph — Triage → Backlog → Scoping → Todo → Needs Input →
In Progress → In Review → Done — is wand's opinion, gofmt-style. A repo's
covenant file (`wand.toml`) customizes the parameters of the machine:
status *names* over the fixed semantics, caps, the estimate scale, toggles,
the pluggable commands, ticket templates. Never its shape.

Every state exists because of a failure mode every team running agents
will eventually hit, and the rules below only hold if the states they
refer to mean the same thing everywhere. A configurable topology would
turn each rule back into a per-repo negotiation — exactly the prose-only
enforcement that failed in the reference system. Topology changes ship as
wand upgrades, for everyone, versioned by the covenant file's `schema`
field: the file declares the schema it was written against, `wand version`
reports the schema the binary speaks, and comparing the two is how you
learn whether a given binary can read a given file.

## The states, and why each exists

The board is a map of *authorization*, not just progress. Reading the
states as "how done is this?" misses their point; read them as "who has
decided what may happen to this next?"

| State | What it authorizes |
|---|---|
| Triage | Nothing. The inbox agents file into. |
| Backlog | Nothing. The undifferentiated pool. |
| Scoping | A scout may research this unattended. |
| Todo | A bot may build this unattended. |
| Needs Input | Nothing — a question is parked on a human. |
| In Progress | Claimed; a worker is (or should be) behind it. |
| In Review | The work is under review. |
| Done / Canceled / Duplicate | Closed. Terminal, and human-granted. |

**Triage** is the inbox. Agents constantly notice work that is not their
ticket — a bug in passing, a missing test, a doc gone stale. The rule is:
file it into Triage with the `agent-filed` label and carry on. Without a
designated inbox those observations either get lost or, worse, get acted
on mid-task, and the session that was fixing one thing comes back having
"improved" five. Nothing in Triage is authorized; it is raw material for a
human to sort.

**Backlog** is the undifferentiated pool: real work, not yet blessed.
It is also the one state agents may move work *down* into. Demotion is
safe where promotion is not, because it removes authorization rather than
granting it — a ticket that turns out to be wrong mid-flight gets handed
back to Backlog with a comment, never quietly closed.

**Scoping** blesses research. A ticket sitting in Scoping is one a
dispatcher may spend a scout on unattended — real tokens, real time. That
is an authorization, the same shape as Todo one rung lower, which is why
agents may not put work there either. A scout finishing its scope moves
the ticket *out* of Scoping to Needs Input, posting approaches, a
recommendation and an estimate; moving out is what every scope ends with,
and it hands the decision back to a person.

**Todo** is the gate between "written down" and "a bot may act on this
unattended". It is the single most consequential state on the board, and
promotion into it is a human act — the blessing. The failure it guards
against is concrete: in the reference system the no-promotion rule was
written down in two places, in prose, and a run still landed a ticket in
Todo — where it rejoined the queue looking startable, carrying no comment
and no branch, and the next dispatcher pass spent a slot rediscovering the
same wall. A correctly written instruction that is only sometimes followed
looks, from the outside, exactly like one that was never written.

So the blessing has one door, and it is a human one:
[`wand ui`](../commands/ui/), the cockpit. Every other write path in wand
runs through the guard's verdict function first and is refused; the cockpit
is the one that does not, because the thing on the other side of it is a
person who has just been told what the transition authorizes.

**Needs Input** parks a question on a human. Without it, a blocked agent
has two bad options: guess, or stall in In Progress looking healthy. Needs
Input makes being blocked *visible and cheap* — the ticket says what it is
waiting for, and surfacing that queue is one quarter of the whole job of
[the cockpit](../commands/ui/).
It is deliberately an `unstarted` state: answering the question re-blesses
the work, it does not resume it automatically.

**In Progress** and **In Review** track claimed and reviewed work, and
Linear's PR automations mirror them: opening a PR (draft included) targets
In Progress — a claimed ticket is already there, so the write changes
nothing — review targets In Review, and merge closes the ticket.

**Done, Canceled and Duplicate** close a ticket, and closing is a human's
call however obsolete the ticket looks. An agent that decides mid-task
that a ticket is moot might be right — but "might be right" is exactly the
confidence level that recommends in a comment rather than acts. Done also
arrives on its own: the on-merge automation sets it within seconds of a
human merging, so the human act it encodes (the merge) has already
happened.

## The forbidden transitions

Five statuses grant or revoke authorization an agent does not have, so an
agent may never set them: **Todo**, **Scoping**, **Done**, **Canceled**,
**Duplicate**. Everything an agent legitimately sets is left alone:
In Progress, In Review, Needs Input, Backlog, Triage.

Note which *direction* is blocked. Moving a ticket into Scoping is a
promotion and is refused; moving one out of Scoping to Needs Input is how
every scope ends, and is allowed. The guard sees only the destination.

This rule is enforced, not requested. Every rule in wand lives in exactly
one of four tiers, and being honest about which tier a rule occupies is
itself a rule:

| Tier | Mechanism |
|---|---|
| Structural | The violation cannot be expressed |
| Code gate | The attempt is refused with a reason |
| Harness hook | The agent's tool call is intercepted |
| Prose | Written down, hoped for, audited |

The forbidden transitions live at the harness-hook tier: `wand init`
installs a PreToolUse shim that routes every Linear issue write through
`wand guard`, and the same verdict function backs wand's own write path,
so the two cannot drift. When the guard blocks a write it says where to go
instead — a stopped agent that is not redirected improvises, which is how
tickets reached Todo in the first place.

## Caps

The run loop's limits — review rounds, CI attempts, worker timeout — are
hard caps, and a cap running out is never silent convergence. The failure
they guard against is false "Done": a terminal state reached by exhaustion
rather than evidence — a round cap quietly running out, a crashed
reviewer, a review thread outdated by a partial revision. Under the
covenant, terminal success is reached only on positive evidence;
exhaustion is a hand-back that says so. A cap set to zero is refused
outright, because a cap of nothing is a request to loop forever.

## Toggles

Two optional stages of the scope orchestrator are the covenant's to switch,
because whether they run is a property of a team's process rather than of
one operator's afternoon:

- `toggles.scope_interview` (on by default) allows `wand scope
  --interactive` to grill a human over its draft before writing anything.
  The toggle and the flag answer different questions — the toggle says
  whether this repo's lifecycle has an interview at all, the flag says
  whether this invocation has a human to hold one with — so passing the flag
  against a covenant that turned the stage off is refused rather than
  quietly resolved.
- `toggles.scope_critic` (off by default) inserts a cold critic between the
  draft and the interview: a fresh session prompted to attack the draft,
  whose objections are validated like any other handoff. It is off by
  default because it spends a whole extra model call per scope and, unlike
  the rest of this covenant, is not yet a rule the reference system has
  finished paying for.

Both stages are separate cold processes for the same reason the reviser is:
a session that has just argued for a plan defends it.

## What belongs in the covenant file

The file is checked in, and it carries parameters only. The test for
whether a value belongs there: could two clones of the repo legitimately
differ on it? If they could — a secret, a machine path, a harness name —
it is machine config and belongs elsewhere. If a difference between two
clones would mean two different *processes*, it is covenant. TOML, so the
rationale for a value survives as a comment next to the value it
justifies.

A missing file means the stock covenant applies unchanged. A present but
broken file is a loud error, never a quiet fall-back to defaults — a
misspelled key silently defaulting is the failure mode the parser exists
to prevent.
