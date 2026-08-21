package plan

import (
	"fmt"
	"strings"

	"github.com/mattwalters/wand/internal/covenant"
)

// The prompts. worker.Compose prepends the always-true contract — mode,
// scratch directory, handoff path, no external writes — per the rule that
// nothing about a run is left for a worker to infer; these supply the
// phase's task and its mode-dependent rules.
//
// Every stage here is a separate cold process, and that is the design
// rather than an implementation detail: a session that has just argued for
// an approach defends it. The critic must be able to call the draft wrong
// without contradicting itself, and the reviser must be able to throw the
// draft away.

// scoutRules keep the scout's hands off the tree. There is no worktree, no
// branch and no CI in a plan run: the repository the command was run from
// is the code being read, which is a human's checkout as often as not.
//
// This is the prose tier for what no adapter flag enforces today, backed
// by a code gate: the orchestrator diffs the tree around every phase and
// parks if anything moved, so the rule is checked rather than trusted.
func scoutRules() []string {
	return []string{
		"Read only. Do not create, modify or delete anything in the working tree — " +
			"no edits, no scratch files, no test scaffolding, no `git` command that writes. " +
			"You are reading this repository to plan work, not doing the work. " +
			"Running read-only commands (grep, build, tests, git log, git diff) is fine.",
		"There is no branch, no pull request and no CI in this run, and nothing you " +
			"say will be implemented by you. Your entire output is the handoff.",
		"Prefer the code to the documentation where they disagree, and say so in your " +
			"handoff when they do.",
		"Your plan becomes the marker-fenced plan region of the ticket's description, " +
			"and nothing else. The ticket's goals, problem statement and title are the " +
			"human's and are never rewritten by a plan — if your plan deliberately narrows " +
			"or excludes something the ticket asked for, say so inside the plan itself " +
			"rather than proposing a change to the ticket above it. If you believe the " +
			"ticket is asking for the wrong thing, that is a wrong premise, not a plan.",
	}
}

// draftSchema documents the Draft JSON. The field names here and in
// handoff.go must change together, and every constraint stated here is
// enforced there — a schema that asks for less than the validator demands
// wastes a whole model call per mismatch.
func draftSchema(cov covenant.Covenant) string {
	var b strings.Builder
	b.WriteString(`Write your handoff as this JSON object:
{"premise": "sound" | "wrong",
 "reason": "only when the premise is wrong: what you found, in your own words — it is quoted verbatim to the human",
 "understanding": "what you take the ticket to be asking, in a few sentences",
 "approaches": [{"name": "short name", "sketch": "what it is, a few sentences", "tradeoff": "what it costs, gives up or risks"}],
 "recommendation": {"approach": "the name of one of the approaches above, exactly", "why": "the argument for it over the others"},
 "files": [{"location": "path/to/file.go:42", "note": "why this location matters to the plan"}],
`)
	if values := cov.EstimateValues(); len(values) > 0 {
		fmt.Fprintf(&b, " \"estimate\": one of %s (this team's %s scale),\n", joinInts(values), cov.IssueEstimationType)
	} else {
		b.WriteString(" (no \"estimate\": this team does not use estimates, so omit the field),\n")
	}
	b.WriteString(` "plan": {"steps": ["each step of the work, in the order it should be done"], "tests": "how the work is proven — what tests exist or must exist, and what they would catch"},
 "assumptions": [{"statement": "something the plan rests on that you could not verify", "if_wrong": "what has to change if it does not hold", "cost": "high" | "medium" | "low"}],
 "open_questions": ["what you could not answer from the code"]}

No other fields. The whole object is validated before anything is written, and a
handoff that fails validation writes nothing at all — so:

- One to three approaches, each with a real trade-off. Listing every option you
  can think of hands the choosing back to the human you were sent to spare.
- The recommendation must name one of your approaches, spelled the same way.
- At least one file citation, every one of them path:line, path:line-line for a
  range, or a comma-separated list of either when a thing lives in several
  places in one file, e.g. path/to/file.go:42,58,103-110. A bare filename
  sends the next reader back to the search you already ran.
- The plan needs ordered steps and a test story; "how it is proven" is the half
  of a plan that goes missing when nobody asks for it.
- "premise": "wrong" is for a ticket built on something untrue — work already
  done, a file that does not exist, a mechanism that does not work the way the
  ticket says. It is not for a hard ticket. When you use it, nothing else is
  required, and nothing is written to the ticket but your account.`)
	return b.String()
}

// scoutPrompt is the first phase's task: the ticket, whole.
func scoutPrompt(ticketText string, cov covenant.Covenant) string {
	var b strings.Builder
	b.WriteString("Research the following ticket in this repository and produce a plan: what the " +
		"work actually is, the ways it could be done, which one you recommend, and a plan a cold " +
		"implementer could follow without repeating your reading.\n\n")
	b.WriteString("Read the code before you decide anything. The ticket is a request, not a survey " +
		"of the codebase, and the whole value of this pass is that somebody looked.\n\n")
	b.WriteString("The ticket below may already carry comments. If it does, that is the record of " +
		"how it got here — a prior plan's argument, what a human said against it, questions and " +
		"answers — and if this is a re-plan, it is the whole reason you are running again. Read it " +
		"for that; it does not change where your plan is written.\n\n")
	b.WriteString(draftSchema(cov))
	b.WriteString("\n\n--- ticket ---\n\n")
	b.WriteString(ticketText)
	return b.String()
}

// criticRules let the critic read the draft and the code and nothing else.
func criticRules() []string {
	return append(scoutRules(),
		"You did not write this draft and you are not defending it. Your job is to find what "+
			"is wrong with it before a human acts on it.")
}

// criticPrompt hands the rendered draft to a fresh session and asks it to
// attack. It is given what a human would be shown rather than the raw
// handoff: the draft's failures are failures of the artifact a person will
// act on.
func criticPrompt(ticketText, rendered string) string {
	var b strings.Builder
	b.WriteString("Below is a draft plan for the ticket that follows it, written by another " +
		"session from the same code you can read. Attack it. Look for: a premise that is wrong, " +
		"an approach that cannot work here, a recommendation that does not follow from its own " +
		"trade-offs, a plan with a step that is impossible or a missing step that is load-bearing, " +
		"a test story that would not catch the thing most likely to break, an assumption ranked " +
		"cheap that is expensive, and anything the draft asserts about this codebase that is not " +
		"true.\n\n")
	b.WriteString("Verify claims against the code rather than judging the prose. Raise an " +
		"objection only when you can say what it costs if the draft ships as written; an " +
		"objection with no consequence is a preference, and acting on it costs a whole revision " +
		"round.\n\n")
	b.WriteString(`Write your handoff as this JSON object:
{"verdict": "sound" | "flawed",
 "objections": [{"target": "which part of the draft — the premise, an approach by name, the recommendation, the plan, the estimate, an assumption", "summary": "what is wrong", "consequence": "what it costs if the draft ships as written"}]}
No other fields. "sound" means you tried and it held; it does not mean you had
nothing to say, and it is a real answer.`)
	b.WriteString("\n\n--- draft plan ---\n\n")
	b.WriteString(rendered)
	b.WriteString("\n\n--- ticket ---\n\n")
	b.WriteString(ticketText)
	return b.String()
}

// revisePrompt hands a draft and what was said against it to a fresh
// session. It produces a whole new draft rather than a patch: a revision
// expressed as edits invites the reviser to keep everything it was not
// explicitly told to change, which is exactly how a defended draft
// survives an interview.
func revisePrompt(ticketText, rendered, objections string, cov covenant.Covenant, source string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Below is a draft plan for the ticket that follows it, written by another "+
		"session, and %s. Produce the plan that should stand now.\n\n", source)
	b.WriteString("You did not write the draft and you owe it nothing. Where what follows is " +
		"right, change the plan — including the recommendation, the steps, the estimate or the " +
		"premise, if that is what it takes. Where it is wrong, say why in the part of the plan " +
		"it bears on, and leave that part standing. Read the code again for anything you change: " +
		"a revision argued from the draft alone inherits whatever the draft got wrong.\n\n")
	b.WriteString("Your handoff replaces the draft whole, and is validated the same way, so " +
		"carry forward everything that still holds.\n\n")
	b.WriteString(draftSchema(cov))
	b.WriteString("\n\n--- what was said against the draft ---\n\n")
	b.WriteString(objections)
	b.WriteString("\n\n--- draft plan ---\n\n")
	b.WriteString(rendered)
	b.WriteString("\n\n--- ticket ---\n\n")
	b.WriteString(ticketText)
	return b.String()
}

// reviseAfterCritiquePrompt hands the critic's objections to a fresh
// session and asks for both a whole revised plan and an accounting of what
// happened to each objection. The accounting is what lets the run route a
// resolved objection into the comment's reasoning trail and one the
// reviser could not answer into an open question a human sees — a
// distinction the revised plan's prose alone does not carry, because a
// revision that changed nothing about an objection looks identical to one
// that never saw it.
func reviseAfterCritiquePrompt(ticketText, rendered string, c Critique, cov covenant.Covenant) string {
	var b strings.Builder
	b.WriteString("Below is a draft plan for the ticket that follows it, written by another " +
		"session, and a cold critic's objections to it. Produce the plan that should stand now.\n\n")
	b.WriteString("You did not write the draft and you owe it nothing. Where an objection is " +
		"right, change the plan — including the recommendation, the steps, the estimate or the " +
		"premise, if that is what it takes. Where it is wrong, say why, and leave that part of " +
		"the plan standing. Read the code again for anything you change: a revision argued from " +
		"the draft alone inherits whatever the draft got wrong.\n\n")
	b.WriteString("Your handoff replaces the draft whole, and is validated the same way, so " +
		"carry forward everything that still holds.\n\n")
	b.WriteString(draftSchema(cov))
	b.WriteString(`

Add one more field to that same object:
 "resolutions": [{"resolved": true | false, "explanation": "if resolved: what changed in the plan and why; if not: why the objection still stands and what a human should decide"}]

Exactly one resolution per objection below, in the same order — the run pairs them
positionally with the objections, not by matching text. When your premise is "wrong",
omit "resolutions" entirely: the whole draft is being withdrawn and there is nothing
left to resolve.`)
	b.WriteString("\n\n--- the critic's objections ---\n\n")
	b.WriteString(renderObjections(c))
	b.WriteString("\n\n--- draft plan ---\n\n")
	b.WriteString(rendered)
	b.WriteString("\n\n--- ticket ---\n\n")
	b.WriteString(ticketText)
	return b.String()
}

// replanPrompt hands the plan currently on the ticket and everything a
// human said about it in comments since to a fresh session, and asks for
// the plan that should stand now plus a diff-shaped account of what
// changed — the re-plan cycle's own revision, distinct from
// [revisePrompt]'s interview-answers round in what it is revising against
// (comments on the board, not an interactive transcript) and in what it
// asks back (an account for a comment, not a resolution per objection).
func replanPrompt(ticketText, rendered, transcript string, cov covenant.Covenant) string {
	var b strings.Builder
	b.WriteString("Below is the plan currently on this ticket, and everything a human has said " +
		"about it in comments since it was posted. This is a re-plan: converge on the plan " +
		"already here to reflect what they said, rather than starting over.\n\n")
	b.WriteString("You did not write the plan and you owe it nothing. Where the human is right, " +
		"change the plan — including the recommendation, the steps, the estimate or the premise, " +
		"if that is what it takes. Where they are asking for something the plan already does, or " +
		"something that does not hold up, say why and leave that part standing. Read the code " +
		"again for anything you change: a revision argued from the plan alone inherits whatever " +
		"it got wrong.\n\n")
	b.WriteString("Your handoff replaces the plan whole, and is validated the same way, so carry " +
		"forward everything that still holds.\n\n")
	b.WriteString(draftSchema(cov))
	b.WriteString(`

Add one more field to that same object:
 "changes": "a diff-shaped account for the ticket's own comment stream: what the human asked, what moved in this revision and why, and what you deliberately left alone and why — an account of what changed, not a verdict on the plan"

Omit "changes" only when your premise is "wrong": the whole draft is being withdrawn and there is nothing to account for.`)
	b.WriteString("\n\n--- comments since the last plan ---\n\n")
	b.WriteString(transcript)
	b.WriteString("\n\n--- the current plan ---\n\n")
	b.WriteString(rendered)
	b.WriteString("\n\n--- ticket ---\n\n")
	b.WriteString(ticketText)
	return b.String()
}

// renderObjections prints a critique for the reviser, in the critic's own
// terms.
func renderObjections(c Critique) string {
	var b strings.Builder
	for i, o := range c.Objections {
		fmt.Fprintf(&b, "%d. (%s) %s\n   consequence: %s\n", i+1,
			strings.TrimSpace(o.Target), strings.TrimSpace(o.Summary), strings.TrimSpace(o.Consequence))
	}
	return b.String()
}

// renderDraft is the draft as a human would be shown it: the plan that
// would go in the ticket body, then the argument that would go in the
// comment. Both the critic and the reviser see this rather than the raw
// JSON — what they are judging is the artifact, not the encoding.
func renderDraft(d Draft, cov covenant.Covenant) string {
	return PlanMarkdown(d) + "\n" + Argument(d, cov.IssueEstimationType)
}
