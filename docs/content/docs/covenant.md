---
title: The covenant
weight: 10
summary: The process contract wand maintains, and the reasoning behind every state and rule in it.
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

The state graph — Triage → Backlog → To Plan → In Planning → Plan Review →
Todo → Needs Input → In Progress → In Review → Done — is wand's opinion,
gofmt-style. A repo's
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

That number is narrower than it looks, and the two halves of it pull in
opposite directions, so it is worth saying exactly:

- It gates **reading**. A file declaring a schema newer than the binary
  speaks is refused outright — wand cannot know what those keys mean. An
  older one is read unchanged: schema bumps only add optional keys, so a
  file that sets none of them says the same thing under either number.
- It does **not** gate topology. The state graph ships centrally, so every
  file gets the current states, whatever number it declares. That is the
  point rather than a leak: it is what makes a team bootstrapped before a
  new state show up as drift under [`wand doctor`](../commands/doctor/)
  instead of quietly keeping the old board.

So the number in a file describes the file, not a pin on the lifecycle.
Raising it is a migration — it asserts that the states exist on your board
— and like every other blessing, a human's act.

One schema exists so far: **1**, which includes every state below —
[Plan Review](#the-states-and-why-each-exists) included. The number holds
at 1 until wand is distributed: an increment is a promise to other
people's covenant files, and there are no other people yet.

[WND-79](https://linear.app/prosewell/issue/WND-79/topology-the-planning-track-gets-three-states-and-claims-the-middle)
is the one topology change so far that reversed an earlier decision rather
than adding to it: `wand plan` used to hold its ticket in a single
unstarted status (Scoping) for the whole research phase and take a
machine-local per-ticket lock instead of a claim, because it had no board
move to lose a race on. A board claim is the more robust mechanism — it is
what stops two *machines* from planning one ticket, which a machine-local
lock never could — and the dead-claim objection that justified the lock
over a claim is gone now that dead-lease reaping exists. So the research
track claims a started status of its own (In Planning) before it touches
anything, the same way `wand run` claims In Progress, and it is symmetric
with the build track for the first time.

## Two doors out of Backlog

Every ticket leaves Backlog through one of two doors, and a human opens
both. The cheap door is `Backlog → Todo`: a ticket small enough that
writing a plan and reviewing it would cost more than just building it, so
a person blesses it straight into Todo. The deliberate door is
`Backlog → To Plan → In Planning → Plan Review → Todo`: a person blesses
the *research* first, an agent claims it and researches it and writes a
plan, and a person blesses that plan onward. Both doors end at the same
gate — a human choosing Todo — the deliberate one just puts a plan in
front of that choice instead of asking for it blind.

Plan Review is what makes the deliberate door's last step first-class. A
ticket sitting there is exactly a PR sitting in In Review: finished,
argued, and waiting on nothing but a human's judgment. Reviewing a plan is
not lesser work than reviewing the code that follows from it, and giving
it its own state — rather than folding it into Needs Input, which means
something else entirely — is how the board says so. In Planning is the
same idea one step earlier: a live plan run is work in flight the same way
a live build is, not a blessing sitting idle, and giving it its own
`started` state is what lets the cockpit recognize a live plan run without
a carve-out written just for it.

## The states, and why each exists

The board is a map of *authorization*, not just progress. Reading the
states as "how done is this?" misses their point; read them as "who has
decided what may happen to this next?"

| State | What it authorizes |
|---|---|
| Triage | Nothing. The inbox agents file into. |
| Backlog | Nothing. The undifferentiated pool. |
| To Plan | A scout may research this unattended. |
| In Planning | Claimed; a scout is (or should be) behind it. |
| Plan Review | Nothing — a finished plan is parked on a human to judge. |
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

**To Plan** blesses research. A ticket sitting in To Plan is one a
dispatcher may spend a scout on unattended — real tokens, real time. That
is an authorization, the same shape as Todo one rung lower, which is why
agents may not put work there either. `wand plan` claims a To Plan ticket
into In Planning before it does anything else, the research-side mirror of
`wand run` claiming Todo into In Progress — the board is the mutex on both
sides now, where research used to take a machine-local lock instead
because it had no board move to claim.

**In Planning** is a live plan run in progress: a scout is (or should be)
behind it, the same way In Progress means a worker is behind a build. It is
the state that makes the cockpit's generic started-type read do the right
thing for a plan run without a carve-out written just for it — a live plan
run whose ticket is not in In Planning is genuine drift, the same as it
would be for a build. A plan run ends one of two ways, and both move the
ticket *out* of In Planning: a scout with a blocking question moves it to
Needs Input, posting the question; a scout with a finished plan moves it
to Plan Review, posting approaches, a recommendation and an estimate.
Moving out is what every plan run ends with, either way, and it hands the
decision back to a person.

**Plan Review** is the research side of In Review: the plan is written, the
approaches are argued, and all that remains is a human's judgment. It exists
because Needs Input was carrying two different jobs after a plan run — "the
scout has a question" and "the plan is ready to bless" — and those want
different things from the person reading the board, on different clocks. A
cockpit whose whole purpose is to sort work by the job it needs from you
cannot do that while one queue holds both. Building already had the pair, In
Progress and In Review; research now has the full triple — To Plan, In
Planning, Plan Review mirroring Todo, In Progress, In Review exactly. Like
Needs Input, Plan Review is `unstarted`: blessing a plan promotes it to
Todo, which re-authorizes the work rather than resuming it.

Plan Review is the agent's terminal write for a plan run the same way In
Review is for a build — an agent may set it unattended, and the guard
treats the two identically. Neither may an agent move a ticket *out* of
Plan Review: that destination is Todo (blessing) or one of the three close
statuses, all five already forbidden regardless of where the ticket is
moving from.

`wand plan`'s happy path ends there: the plan, the argued options and the
estimate land on the ticket, then the move to Plan Review is the last
write, advertising that all three are there to read. Needs Input is the
scout's other ending, and only that one now — a blocking question, nothing
else, with the question as the comment. A team bootstrapped before this
topology existed will see the missing states reported by
[`wand doctor`](../commands/doctor/) and created by `wand init`. (It ships
inside schema 1: wand is undistributed, so topology changes do not yet
spend schema increments.)

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
the work, it does not resume it automatically. It means exactly one thing —
answer me — never "review this," which is Plan Review's job and In Review's;
a queue that means two things is a queue a person has to open before they
know what it is asking of them.

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
agent may never set them: **Todo**, **To Plan**, **Done**, **Canceled**,
**Duplicate**. Everything an agent legitimately sets is left alone:
In Progress, In Review, Needs Input, **In Planning**, **Plan Review**,
Backlog, Triage.

Note which *direction* is blocked. Moving a ticket into To Plan is a
promotion and is refused; moving one out of To Plan — the claim into In
Planning `wand plan` makes before anything else — is allowed, the same
WND-47 rule restated: agents may enter a state they do not bless, only
humans may leave one that authorizes something. From In Planning, a plan
run ends either way it ends — to Needs Input (a blocking question) or to
Plan Review (a finished plan). The guard sees only the destination.

Leaving Plan Review toward Todo needs no rule of its own: it is the same
promotion the Todo rule above already forbids from every status, Plan
Review included. Setting Plan Review is new with this status; blocking the
way out of it is not — it falls out of a rule that already existed.

One gap these statuses inherit rather than fix: the guard matches statuses
by their default English display name (`"Plan Review"`, `"Todo"`, ...), not
whatever a covenant-file rename might call them. A team that renames a
guarded status in its covenant file silently falls out of the guard's
coverage. That gap predates this topology, applies to it and its
neighbors alike, and is tracked separately rather than fixed here — see
[WND-17](https://linear.app/prosewell/issue/WND-17/guards-forbidden-status-list-ignores-covenant-file-status-renames).

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
outright, because a cap of nothing is a request to loop forever — with the
single exception of `caps.worker_retries`, below, which counts retries
rather than attempts and so has a meaningful zero.

`caps.lanes` (default `1`) is a different kind of limit: how many
`wand run` loops [`wand dispatch`](../commands/dispatch/) runs against this
repository at once. A To Plan ticket never counts against it — research
needs no lane — so raising it only ever buys more concurrent building.

`caps.worker_retries` (default `1`) is the one cap whose floor is zero, and
the one whose zero is a real answer rather than a refusal. It counts
*retries* rather than attempts: every phase gets its one worker regardless,
and this is how many extra times that phase may respawn after a failure the
harness itself reported as infrastructure — a provider error, a host that
suspended mid-response. So `0` does not mean "loop forever"; it means
"never retry", which is what wand did before the cap existed. Nothing else
is ever retried: a failure that might be about the work, a timeout, and an
interrupt all park on the first attempt. See
[`wand run`](../commands/run/) for the rules that keep it narrow.

`caps.worker_timeout_minutes` (default `60`) is a different kind of cap
from `review_rounds` and `ci_attempts`, worth naming so the three are not
tuned by the same instinct. The round caps bound *semantic looping* — they
exist so a run cannot converge by exhausting a counter, and changing them
changes what a run is willing to conclude. The worker timeout bounds *a
wedged process* — a liveness backstop only, and changing it changes
nothing about correctness.

One number applied to every phase asks a reviewer to justify the
implementer's budget, or asks an implementer to make do with the
reviewer's. `caps.phase_timeout_minutes` overrides the global timeout for
specific phases, falling back to it for any phase left unmentioned:

```toml
[caps]
worker_timeout_minutes = 60

[caps.phase_timeout_minutes]
review = 20
fix-ci = 20
```

The stock covenant already ships `review` and `fix-ci` at 20 minutes, over
a 60-minute global default that `implement` and scope's research phases
(`scout`, `critic`, `revise`) fall back to. These are early defaults —
sized from this machine's own run journals (an `implement` phase
legitimately running into the 40s of minutes on a real multi-package
ticket; `review` and `fix-ci` never observed past a few) rather than from
guessing, but from a small sample, and subject to revision as more runs
accumulate. Every key is validated the same way as the other caps: an
unknown phase name is refused loudly, the same as any other misspelled key
in this file, and an explicit zero or negative is refused for the same
reason a bare cap of zero is — a cap of nothing is a request to loop
forever.

## Toggles

Two optional stages of the plan orchestrator are the covenant's to switch,
because whether they run is a property of a team's process rather than of
one operator's afternoon:

- `toggles.plan_interview` (on by default) allows `wand plan
  --interactive` to grill a human over its draft before writing anything.
  The toggle and the flag answer different questions — the toggle says
  whether this repo's lifecycle has an interview at all, the flag says
  whether this invocation has a human to hold one with — so passing the flag
  against a covenant that turned the stage off is refused rather than
  quietly resolved.
- `toggles.plan_critic` (off by default) inserts a cold critic between the
  draft and the interview: a fresh session prompted to attack the draft,
  whose objections are validated like any other handoff. It is off by
  default because it spends a whole extra model call per plan run and, unlike
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
