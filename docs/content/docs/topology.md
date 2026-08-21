---
title: The single writer
weight: 40
summary: Two diagrams of the machine side of the lifecycle — the credential boundary, and each orchestrator's phase graph.
---

[The covenant](../covenant/) and [No daemon](../architecture/) are the
lifecycle's rules and its process shape. This page is the two structural
facts underneath both: who is allowed to write, and what order a run's
phases happen in. Both are drawn here because a picture makes them
checkable at a glance, where prose only makes them arguable.

## The single writer

The orchestrator — `wand run` or `wand plan` — holds the only Linear and
GitHub credentials in the system and makes every write to either. Every
worker it spawns is cold: a fresh process, no memory of any prior phase,
no credentials of its own. A worker receives a prompt, does its work
against the filesystem and git, and leaves exactly one JSON handoff file.
Nothing else it does can reach the orchestrator, and nothing it writes can
reach Linear or GitHub directly — the isolation conformance suite
(`internal/workertest`) instructs a real harness to try, per adapter, and
requires every attempt to fail.

The picture below draws that as a boundary, not a wall of prose: the
prompt is the only thing that crosses it going in, the handoff file is the
only thing that crosses it coming back, and nothing else — no credential,
no direct write — ever does.

```text
    ┌────────────────────────────────────────────────────────────┐
    │            orchestrator  (wand run / wand plan)            │
    │                                                            │
    │    the only process holding Linear + GitHub credentials    │
    └────────────────────────────────────────────────────────────┘
    │
    │  prompt in — no credentials, no state
    ▼
    ══════════════════════════════════════════════════════════════
    ════════════════════ credential boundary ═════════════════════
    ══════════════════════════════════════════════════════════════
    │
    ▼
    ┌────────────────────────────────────────────────────────────┐
    │           cold worker  (one phase, one process)            │
    │                                                            │
    │  stateless, mute — never calls Linear, never calls GitHub  │
    │               filesystem + git in the middle               │
    └────────────────────────────────────────────────────────────┘
    │
    │  one JSON handoff file — the only arrow that
    │  crosses this boundary back
    ▲
    ══════════════════════════════════════════════════════════════
    ════════════════════ credential boundary ═════════════════════
    ══════════════════════════════════════════════════════════════
    ▲
```

Two writers on one ticket cannot be reconciled afterwards, so the second
writer is not allowed to exist — this is the diagram of that conviction.

## One run's phase graph

Both orchestrators journal a phase's start before the phase runs, never
after (`internal/journal`'s crash-only rule: a record announces a
transition before it happens). That ordering is drawn explicitly below —
every phase is entered through its journal write, not the other way
round — because it is the property that lets a crash mid-phase resume from
what the journal already says, instead of leaving a ticket stuck with
nothing to explain it. See [the run journal](../journal/) for the record
schema itself.

### `wand run`: implement → CI → review → revise

```text
  claim ticket: Todo → In Progress

  journal: phase.started(implement) ──▶ implement (cold worker)

  journal: phase.started(ci) ──▶ ci: fix CI (cold worker)
    passes                                    ──▶ continue to review
    fails, retries left (< Caps.CIAttempts)   ──▶ back to journal: phase.started(ci)
    fails, CIAttempts exhausted               ──▶ HANDED BACK
                                                    Needs Input, CI cap reason

  journal: phase.started(review) ──▶ review (cold worker)
    approved, states what it verified   ──▶ CONVERGED
                                              In Review, ready-for-human
    no parseable handoff                ──▶ PARKED — reason
    findings                            ──▶ journal: phase.started(revise)
                                              ──▶ revise (cold worker)
        round left (< Caps.ReviewRounds)   ──▶ back to journal: phase.started(ci)
        ReviewRounds exhausted             ──▶ HANDED BACK
                                                 Needs Input, final round's
                                                 findings quoted whole

  any phase, any time: crash / interrupt / signal ──▶ PARKED — reason
```

Three ways out, and only three: **converged** (an approval that names what
it verified), **handed back** (a cap exhausted, or a worker reporting the
ticket's premise itself is wrong — always with a reason a human reads),
and **parked** (a run the orchestrator could not carry to either of the
other two, always with a reason). A cap running out is never silent
convergence — it is a hand-back that says so.

### `wand plan`: scout → critic → interview → four writes

```text
  claim ticket: To Plan → In Planning

  journal: phase.started(scout) ──▶ scout (cold, read-only worker)
    wrong premise   ──▶ HANDED BACK — Needs Input, premise reason
    otherwise       ──▶ continue to critic

  journal: phase.started(critic) ──▶ critic (cold worker)   [if the covenant enables it]
    objections   ──▶ journal: phase.started(revise) ──▶ reviser (cold worker)
                        wrong premise   ──▶ HANDED BACK
    otherwise    ──▶ continue to interview

  journal: phase.started(interview) ──▶ interview   [if a human is present]
    answers      ──▶ journal: phase.started(revise) ──▶ reviser (cold worker)
                        wrong premise   ──▶ HANDED BACK
    otherwise    ──▶ continue to the four writes

  four writes, in this fixed order:
    1. description — UpsertSection: the marker-fenced plan region only
    2. comment — the options and argued trade-offs
    3. issue update — the estimate, if the draft set one
    4. status — Plan Review
```

Every stage that can replace the draft (critic, interview) is followed by
the same wrong-premise check the scout's own draft gets: a human can be the
one who says the thing that makes a ticket's premise wrong, and the plan
does not get written over that. An invalid handoff at any stage parks the
run rather than writing a partial plan — a plan is read as a decision, and
a draft missing its trade-offs reads finished when it is not.
