// Package guard decides what an agent may never do to a Linear ticket.
//
// The lifecycle's prose already says an agent never promotes a ticket to Todo
// or Scoping and never closes one. Prose is advisory: the reference system
// this port comes from had the rule written down in two places and a run
// still landed a ticket in Todo. A correctly written instruction that is only
// sometimes followed looks, from the outside, exactly like one that was never
// written. So the forbidden transitions are enforced here instead.
//
// Why these five statuses: they are the ones that hand out authorization the
// agent does not have.
//
//	Todo       blesses work — the gate between "written down" and "a bot may
//	           act on this unattended". A ticket that lands there carrying no
//	           comment and no branch rejoins the queue looking startable.
//	Scoping    blesses research, the same shape one rung lower: a ticket in
//	           Scoping is one a dispatcher may spend a scout on unattended.
//	Done       )
//	Canceled   ) close a ticket. Closing is a human's call however obsolete
//	Duplicate  ) the ticket looks; an agent recommends it in a comment.
//
// Every status an agent legitimately sets is left alone: In Progress,
// In Review, Needs Input, Scoped, Backlog, Triage. Backlog is the one
// downward move an agent may make, and it is safe because it removes
// authorization rather than granting it. Note which direction is blocked:
// moving a ticket *into* Scoping is a promotion and is refused; moving one
// *out* of Scoping is what every plan run ends with, and both ways it can
// end are allowed — to Needs Input, the scout has a blocking question, or to
// Scoped, the plan is finished — the guard sees only the destination.
//
// Scoped is the research-side analog of In Review: it is the plan
// orchestrator's terminal write, the plan-review equivalent of "this code is
// ready to look at". An agent may set it unattended, the same as In Review,
// and it may never move a ticket *out* of Scoped — that destination is
// already covered by the Todo rule above (Scoped -> Todo is blessing a plan,
// which is the same human act as blessing anything else into Todo) and by
// the four-close-statuses rule (Scoped -> Done/Canceled/Duplicate is closing
// a ticket, forbidden regardless of where it is closed from). No status
// besides Todo, Scoping, Done, Canceled and Duplicate is ever a forbidden
// destination, so leaving Scoped needs no rule of its own: it is already
// covered by the same five.
//
// WND-17: the maps below match the covenant's default English display
// names ("Todo", "Scoping", ...), not whatever a covenant-file rename might
// call them. A team that renames a guarded status in its covenant file
// silently falls out of this guard's coverage — including Scoped and its
// neighbors, added here. That gap is real and is not fixed by this change;
// it is tracked separately as WND-17, deliberately, rather than carried
// forward with no comment saying so.
//
// Known gap, accepted rather than overlooked: Linear's state parameter takes
// "a state type, name, or ID". Names and types are matched below; a raw UUID
// is not, because the ids are per-workspace and hardcoding them would rot
// silently the first time a status is recreated. In practice a model writes
// `state: "Todo"`, not a uuid it would have to look up first.
//
// This is the one verdict function. The PreToolUse hook (Run, in run.go) and
// wand's own lifecycle verbs (internal/verbs) both call it, so the forbidden
// list cannot drift between enforcement paths.
//
// One caller deliberately does not: internal/cockpit, the TUI's write path.
// The subject of every sentence above is an *agent acting unattended*, and
// none of them is true of a person at a terminal pressing a key on a screen
// that has just told them what the transition authorizes. Blessing has to
// happen somewhere, and a system that forbids it everywhere is one whose
// board never moves. The guard is not a lock on the statuses; it is a lock
// on who may reach them, and the cockpit is the door with a human on the
// other side. See cockpit.Apply, where the absence of the call is
// documented as the load-bearing detail it is.
package guard

import (
	"regexp"
	"strings"
)

// saveIssue matches the Linear MCP tool that writes an issue.
//
// Matched on the tool's own name rather than the connector id in the middle:
// reconnecting Linear changes the id, and a rule pinned to today's id would
// go dead silently — which for a guard means allowing everything with no
// sign of it.
var saveIssue = regexp.MustCompile(`^mcp__.+__save_issue$`)

// The reason texts are most of the value of a block: an agent that is
// stopped without being told where to go next improvises, which is how
// tickets reach Todo in the first place. Each names the correct alternative,
// verb included — `wand handback` and `wand abandon` encode the orderings
// (comment before status, description corrected with the demotion) that a
// raw status write skips.
const (
	reasonTodo = "Moving a ticket to **Todo** is the human's call. Todo is the gate between " +
		"\"written down\" and \"a bot may act on this unattended\", so an agent never " +
		"promotes one — its own included.\n\n" +
		"If you got here because a transition to **Needs Input** failed, Todo is the " +
		"worst available answer: the ticket rejoins the queue looking startable, " +
		"carrying no comment and no branch, and the next dispatcher pass spends a " +
		"slot rediscovering the same wall. Leave the issue **In Progress**, keep the " +
		"comment you wrote, and stop — saying that the status is missing and that a " +
		"human has to add it in Linear's team settings.\n\n" +
		"If the ticket turns out to be wrong rather than blocked, hand it back to " +
		"**Backlog** with `wand abandon` — the one downward move an agent may make, " +
		"safe because it removes authorization rather than granting it."

	reasonClose = "Closing a ticket is the human's call, however obsolete it looks. An agent " +
		"never sets Done, Canceled or Duplicate.\n\n" +
		"Recommend it in a comment instead, with what you found and why. If the " +
		"ticket is wrong rather than finished, hand it back to **Backlog** " +
		"unassigned with `wand abandon` — it posts your evidence, corrects the " +
		"description in the same act, and makes the one downward move an agent " +
		"may make, safe because it removes authorization rather than granting " +
		"it.\n\n" +
		"Done also arrives on its own: Linear's on-PR-merge automation sets it " +
		"within seconds of the human merging."

	reasonScoping = "Moving a ticket to **Scoping** is the human's call, for the same reason " +
		"Todo is. Scoping blesses research: a ticket sitting in it is one a " +
		"dispatcher may spend a scout on unattended, so putting work there " +
		"authorizes it rather than describing it.\n\n" +
		"If you are a scout finishing a plan, the move you want is **Needs " +
		"Input** — post the approaches, the recommendation and the estimate, then " +
		"transition there (`wand handback` does both, in the order that cannot " +
		"strand a question-less ticket). That is allowed, and it is what hands " +
		"the decision back to a person.\n\n" +
		"If you have found something that ought to be planned, say so in a comment " +
		"and leave the status alone. A human promotes it."

	reasonAmbiguousUnstarted = "`unstarted` is a state *type*, not a status, and this team has four of " +
		"them — **Todo**, **Needs Input**, **Scoping** and **Scoped**. Linear " +
		"resolves the type to whichever the team defaults to, which is Todo, so " +
		"this reads as a hand-back and lands as a promotion. Two of the four are " +
		"statuses you may never set, which is why the type is refused rather than " +
		"resolved.\n\n" +
		"Name the status you actually mean: `state: \"Needs Input\"`."
)

// forbiddenNames are the statuses an agent may never set, by normalized name.
//
// "cancelled" sits alongside "canceled" because Linear spells it with one `l`
// and half the English-speaking world spells it with two; a guard that
// matched only Linear's spelling would be bypassed by a typo that Linear
// itself would then reject — leaving the agent improvising, which is the
// failure this whole package exists to stop.
//
// These are literal default display names, not covenant-file configuration —
// see the WND-17 ruling in the package doc above.
var forbiddenNames = map[string]string{
	"todo":      reasonTodo,
	"to do":     reasonTodo,
	"scoping":   reasonScoping,
	"done":      reasonClose,
	"canceled":  reasonClose,
	"cancelled": reasonClose,
	"duplicate": reasonClose,
}

// forbiddenTypes are the state *types* that resolve to a forbidden status.
//
// `completed` and `canceled` are unambiguous — one status each. `unstarted`
// covers Todo, Needs Input, Scoping and Scoped, so it gets its own message
// telling the caller to name the status rather than the type. (`duplicate` needs no
// entry: the string is already forbidden as a name.)
var forbiddenTypes = map[string]string{
	"completed": reasonClose,
	"canceled":  reasonClose,
	"unstarted": reasonAmbiguousUnstarted,
}

var whitespace = regexp.MustCompile(`\s+`)

// normalize prepares a state argument for comparison: trim, collapse
// whitespace, lowercase.
func normalize(s string) string {
	return strings.ToLower(whitespace.ReplaceAllString(strings.TrimSpace(s), " "))
}

// CheckState judges a bare state value: the reason it must not be set, and
// whether it is blocked. This is the function wand's own Linear write path
// calls before every transition, so its verdicts cannot drift from the
// hook's.
func CheckState(state string) (reason string, blocked bool) {
	key := normalize(state)
	if key == "" {
		return "", false
	}
	// Names first. `canceled` is both a name and a type and means the same
	// thing either way, so the order only matters for the message.
	if r, ok := forbiddenNames[key]; ok {
		return r, true
	}
	if r, ok := forbiddenTypes[key]; ok {
		return r, true
	}
	return "", false
}

// Evaluate judges a whole pending tool call, the hook's shape: which tool,
// with what input. Anything that is not a save_issue setting a forbidden
// state passes — assigning, relabelling and editing descriptions are
// ordinary work, and a guard that blocked them would be routed around
// within a day.
func Evaluate(toolName string, input map[string]any) (reason string, blocked bool) {
	if !saveIssue.MatchString(toolName) {
		return "", false
	}
	state, ok := input["state"].(string)
	if !ok {
		return "", false
	}
	return CheckState(state)
}
