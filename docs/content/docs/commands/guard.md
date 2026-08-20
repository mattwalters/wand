---
title: wand guard
weight: 130
summary: The harness hook that blocks the ticket writes an agent may never make.
---

`guard` is the enforcement point of the covenant's authorization rules. It
runs as a `PreToolUse` hook: the harness hands it a pending tool call as
JSON on stdin, and its exit code decides whether that call happens.

You do not normally run it by hand. [`wand init`](../init/) installs the
hook entry that routes Linear `save_issue` calls here.

## Synopsis

```
wand guard < tool-call.json
```

## What it does

The lifecycle's prose already says an agent never promotes a ticket to Todo
and never closes one. Prose is advisory. In the production system wand is
extracted from, that rule was written down in two places and a run still
landed a ticket in Todo — a correctly written instruction that is only
sometimes followed looks, from the outside, exactly like one that was never
written. So the rule is a program instead.

`guard` reads the pending call, and blocks it when **all** of these hold:

* the tool name matches `mcp__.*__save_issue` — any Linear MCP connector,
  because the connector id changes when Linear is reconnected;
* the call sets a `state`;
* that state is one only a human may set.

### The forbidden states

| Blocked | Why |
|---|---|
| `Todo`, `To do` | Todo blesses work — the gate between "written down" and "a bot may act on this unattended". |
| `Scoping` | The same blessing one rung lower: a ticket in Scoping is one a dispatcher may spend a scout on unattended. |
| `Done`, `Canceled`, `Cancelled`, `Duplicate` | Closing a ticket is a human's call, however obsolete the ticket looks. |

Matching is on the trimmed, whitespace-collapsed, lowercased value, and
covers Linear's state *types* as well as its names: `completed` and
`canceled` block, and `unstarted` blocks with a message asking the caller
to name the status instead, since it covers Todo, Needs Input, Scoping and
Scoped alike.

Every status an agent legitimately sets passes: **In Progress**, **In
Review**, **Needs Input**, **Scoped**, **Backlog**, **Triage**. So does
every write that is not a status move — assigning, relabelling, editing a
description. A guard that blocked ordinary work would be routed around
within a day.

Note the direction. Moving a ticket *into* Scoping is a promotion and is
refused; moving one *out* of Scoping is what every scope ends with, either
way it ends — to Needs Input, a blocking question, or to Scoped, a
finished plan — and both are allowed. The guard sees only the destination.

### The block message is most of the value

A blocked agent that is not told where to go next improvises, which is how
tickets reach Todo in the first place. Each refusal names the correct
alternative with its verb — [`wand handback`](../handback/) for a question,
[`wand abandon`](../abandon/) for a ticket the evidence undoes — because
those encode orderings a raw status write skips.

### Known gap

Linear's `state` parameter accepts "a state type, name, or ID". Names and
types are matched; a raw UUID is not. The ids are per-workspace, and
hardcoding them would rot silently the first time a status is recreated.
In practice a model writes `state: "Todo"`, not a uuid it would have to
look up first.

## Flags

None.

## Exit codes

The exit code is the entire hook protocol, and it is pinned by a test in
`e2e/` that drives the compiled binary the way the harness does.

| Code | Meaning |
|---|---|
| `2` | **Blocked.** The reason is on stderr, and the harness shows it to the agent. |
| `0` | Allowed. |

Any exit code other than `2` allows the call — that is the harness's rule,
not wand's.

**`guard` fails open, deliberately.** Input that does not decode — garbage,
empty stdin, a `tool_input` that is not an object — exits `0`. A broken
guard must never wedge a session. The cost of that choice is that a
corrupted hook silently protects nothing, which is why the matcher is loose
and why [`wand doctor`](../doctor/) exists.

## Examples

The hook entry `wand init` writes into `.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "mcp__.*__save_issue",
        "hooks": [
          {
            "type": "command",
            "command": "wand guard"
          }
        ]
      }
    ]
  }
}
```

Check a verdict by hand:

```bash
echo '{"tool_name":"mcp__linear__save_issue","tool_input":{"id":"WND-1","state":"Todo"}}' \
  | wand guard; echo "exit $?"
```

```
Blocked by wand guard

Moving a ticket to **Todo** is the human's call. ...
exit 2
```

An allowed write:

```bash
echo '{"tool_name":"mcp__linear__save_issue","tool_input":{"id":"WND-1","state":"In Review"}}' \
  | wand guard; echo "exit $?"
```

```
exit 0
```
