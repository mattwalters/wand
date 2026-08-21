# Development plan

> **This document is scaffolding.** It records the build order and the
> reasoning while wand goes from one verb to a working system. The Linear
> tickets are the authoritative version of the work; this file is the
> sequencing and the brain dump behind them. Expect it to drift as tickets
> evolve, and expect it to be deleted once the main thrust has landed.

## What wand is

wand is a Go CLI that wires a repository to a [Linear](https://linear.app)
team and runs an opinionated software development lifecycle through it — one
built for the reality that much of the work in a repo is now done by coding
agents, and that the dangerous part of agent work is not the code, it is the
authorization: what a bot may start, what it may close, and what only a
human may decide.

The lifecycle wand ships was not designed on a whiteboard. It is extracted
from a private production system where it ran for months, agents and humans
working the same board, and where every rule below was paid for by a
specific failure. wand is the productization: the rules move from prose and
one-off scripts into a tool any repo can adopt.

## Convictions

These are settled positions, not open questions. Changing one is a big deal
and should feel like one.

1. **Linear-only.** The status model, branch-name integration, relations and
   PR automations do real load-bearing work. Abstracting over ticketing
   backends would flatten exactly the features that make the system good.
   If wand ever grows a second backend, it will be because wand won, not as
   a hedge.
2. **Fixed topology, parameterized covenant.** The state graph — Triage →
   Backlog → Scoping → Scoped → Todo → Needs Input → In Progress →
   In Review → Done — is wand's opinion, gofmt-style. Every state exists because of a failure
   mode every team running agents will eventually hit. A repo's covenant
   file customizes the parameters of the machine (names, caps, commands,
   toggles), never its shape. Topology changes ship as wand upgrades, for
   everyone, versioned by a schema field.
3. **Single writer.** The orchestrator holds the only API credentials and
   makes every Linear and GitHub write. Spawned workers are cold, stateless
   and mute: prompt in, filesystem and git in the middle, one JSON handoff
   file out. Two writers on one ticket cannot be reconciled afterwards, so
   the second writer is not allowed to exist.
4. **Humans hold the blessing.** Promotion to Todo (build this) and Scoping
   (research this), and every closing status, are human acts. Agents can
   file, demote, hand back and recommend — never authorize. This is
   enforced, not requested (see the tiers below).
5. **Low dependency.** wand is one binary. The Linear client is raw
   `net/http`; the docs site generator is a single Go binary; every
   dependency is weight to be justified.
6. **The harness seam is the one deliberately generic seam.** Workers are
   spawned through a small adapter interface (spawn, prompt, handoff,
   isolation), so different agent CLIs can run the same lifecycle under the
   same guardrails — which also makes results comparable across them.
   Everything else stays opinionated. What a *particular* harness says about
   itself — its token accounting, its own verdict on why a turn ended —
   arrives through optional interfaces an adapter may implement, and every
   one of them fails soft: an adapter that cannot answer leaves the
   orchestrator exactly where it was without one. That is what keeps the
   seam generic while still letting the orchestrator act on what only a
   harness can know.

## How rules are enforced

Every rule lives in exactly one of four tiers, and the tier is part of the
design. Being honest about which tier a rule occupies is itself a rule.

| Tier | Mechanism | Example |
|---|---|---|
| Structural | The violation cannot be expressed | Workers have no credentials; reviewer coldness is a process boundary |
| Code gate | The attempt is refused with a reason | Status verdicts, queue vetting, handoff validation |
| Harness hook | The agent's tool call is intercepted | `wand guard` behind a generated PreToolUse shim |
| Prose | Written down, hoped for, audited | Description-pairing doctrine, reference glosses |

The guard logic lives once, in the wand binary, and every enforcement path —
the orchestrator's own writes and each harness's hook shim — calls the same
verdict function. That is what keeps the tiers from drifting apart, and what
makes cross-harness comparisons honest.

## The build order

Built outside-in: reads before writes, guards before verbs, verbs before
orchestrators, the loop last. Each phase is immediately useful to the
humans and hand-driven agent sessions developing wand itself — the tool
bootstraps its own development a stage at a time, without pretending the
full loop exists before it does.

**Phase 0 — bootstrap (done).** `wand init` creates or adopts a Linear team
and brings it to the covenant: statuses, labels, PR automations, verified by
readback, idempotent. Its first production act was creating wand's own team.

**Phase 1 — the guard (WAND-1).** `wand guard` as the status verdict
oracle, plus generated harness shims. From here, sessions developing wand
are under the same protections the lifecycle promises everyone else.

**Phase 2 — reads and foundations (WAND-2, 3, 4, 5, 14).** The covenant
file; `wand queue` and `wand ticket` (ranked, vetted, rendered for cold
readers); marker-fenced description sections; `wand doctor` diffing the
live team against the covenant; goreleaser and a `wand version` that
reports the covenant schema it speaks. All reads, all low-risk, all
immediately usable.

**Phase 3 — the single-writer verbs (WAND-6).** `claim`, `handback`,
`abandon`, `file`: the lifecycle actions interactive sessions perform, with
their ordering rules (comment before status, description corrected in the
same act as the comment that contradicts it) encoded rather than remembered.

**Phase 4 — orchestration foundations (WAND-7, 8).** The run journal and
lease store — crash-only from day one, every transition journaled before it
happens, a killed holder provably dead, and reopening a run as the one
recovery act — and the worker adapter interface with its isolation
conformance test: a worker instructed to write Linear must fail, proven per
harness, never assumed. `wand resume` is the verb over the store, and it
ships with the first orchestrator that has a phase graph to re-enter
(phases 5 and 6): the store answers *where the run was*, the orchestrator
owns *what comes next*, and a verb shipped before the second half exists
would only be able to refuse.

**Phase 5 — the first orchestrator (WAND-9).** `wand plan`: a cold scout
researches one blessed ticket, a validated handoff becomes a plan in the
ticket body and argued options in a comment, ending at Needs Input. Lowest
risk (read-only worker, no branch, no CI), yet it exercises every new
mechanism exactly once. Optional critic and interview stages sit between
draft and write.

**Phase 6 — the loop (WAND-10, 11).** `wand run`: implement → CI → review →
revise, cold worker per phase, hard caps, exactly one journaled terminal
state per run. Then `wand dispatch` (the selector, with its exit-code
contract and `--watch`) and `wand sweep` (everything that happens after a
run exits). wand's own repo is the first customer — a Go CLI needs nothing
but a worktree and `make check`, so the default covenant gets exercised
before any complex infrastructure does.

**Phase 7 — the cockpit and the second harness (WAND-12, 13, 15).** The
TUI answering one question — *what is waiting on me?* — with blessing as a
deliberate human act; a second harness adapter behind the same conformance
test; the versioned docs site, whose first real content is the covenant's
rationale.

Distribution (WAND-14, 15) runs parallel to the phases rather than after
them; releases start as soon as there is a verb worth installing.

## Failure modes this is built against

The state machine's known bad states, each answered by design rather than
by vigilance:

- **The zombie.** A ticket In Progress with no living process behind it
  looks healthy, and nothing drains that queue. Answer: the journal and
  lease store (phase 4) make death provable, `resume` makes it cheap, and
  the sweep reports what it cannot prove.
- **False convergence.** "Done" reached by exhaustion — a round cap running
  out, a crashed reviewer, a human thread outdated by a partial revision.
  Answer: terminal success states are reached only on positive evidence;
  cap exhaustion is a hand-back that says so; a crashed reviewer parks.
- **Automation racing intent.** Ticket-closes-on-merge is correct and
  still closes tickets whose remaining deliverable was a human's. Answer:
  ticket templates that split deliverables by owner, so the automation
  closes exactly what merging finished.
- **Queues nothing drains.** Needs Input, Triage and parked runs wait on
  people and are invisible unless surfaced. Answer: that is the cockpit's
  entire job — and, for parks specifically, the board's too. A park writes
  its reason to the ticket and marks it `parked`, because a queue you have
  to run a command to see is one you find out about an hour late. The
  cheapest park is the one that never happens: a failure the harness itself
  blames on infrastructure retries instead, so a closed laptop lid does not
  become a queue entry at all.

## Risks worth naming

- **Rewrite fidelity.** The reference implementation's test suites encode
  invariants that each cost a real incident. Port the assertions, not the
  code; the failure mode is a 95%-faithful rewrite that re-learns the
  missing 5% the expensive way.
- **Infrastructure temptation.** A tool like this can absorb unlimited
  effort. The phases are deliberately small, each lands something usable,
  and the not-list below is load-bearing.
- **Isolation is per-harness work.** "Workers cannot write Linear" is a
  designed property, not an inherited one — some harnesses leak configured
  tools into child sessions. The conformance test exists so the guarantee
  is proven wherever it is claimed.
- **The TUI is the fun part.** It ships in phase 7 anyway. The value for
  months is in the guard, the verbs and the orchestrators.

## Deliberately not building

- A teardown verb. wand's writes are additive and convergent; deleting a
  team is a human act, done in Linear, with Linear's own confirmations.
- Other ticketing backends. See conviction 1.
- User-editable state topology. See conviction 2.
- An LLM docs-drift checker. The docs-change-with-code rule is prose plus
  a standing review dimension, and that is the whole product.
- Benchmark leaderboards. Cross-harness comparison data falls out of the
  run journal for free; charts are someone else's product.

## When this document dies

When phases 1–6 are landed and wand is running its own board end to end,
this file has done its job: the covenant, the docs site and the tickets
will say everything it says, with authority. Delete it then.
