package run

import (
	"fmt"
	"strings"
)

// The prompts hand the worker everything it needs stated, per the PW-174
// rule the worker package enforces: nothing about the run is left to
// inference. worker.Compose prepends the always-true contract (mode,
// scratch, handoff path, no external writes); these builders supply the
// phase's task and its mode-dependent rules.

// workRules are the rules every tree-writing phase (implement, fix-CI,
// revise) carries.
func workRules(identifier, verify string) []string {
	return []string{
		"Work only in this worktree. Commit your changes with git, in coherent commits " +
			"whose messages start with [" + identifier + "]. Do not push and do not open a PR — " +
			"you have no credentials for either; the orchestrator pushes and opens the PR.",
		"Leave the working tree clean: every change committed, nothing untracked. " +
			"Scratch output goes in the scratch directory. A dirty tree parks the run.",
		fmt.Sprintf("Before writing your handoff, run the repository's verify command (%s) "+
			"in the worktree and make it pass. The orchestrator re-runs it and every failure costs a capped attempt.", verify),
	}
}

// workHandoffSchema documents the WorkHandoff JSON the worker must write.
// The field names here and in handoff.go must change together.
const workHandoffSchema = `Write your handoff as this JSON object:
{"status": "done" | "blocked",
 "summary": "what you did and why, a few sentences (required when done)",
 "reason": "only when blocked: why you stopped, in your own words — it is quoted verbatim to the human who picks this up",
 "title": "suggested PR title for the change, without the ticket identifier (it is added for you)",
 "description_corrections": [{"old": "exact ticket-description wording your work disproved", "new": "what it should say instead (empty string deletes it)"}],
 "plan_deviations": ["each place you deviated from the ticket's stated plan, and why"]}
No other fields. Omit optional fields you have nothing for. "blocked" means you hit
something a human must decide; a hard task you are still equipped to do is not blocked.`

// implementPrompt is the first phase's task: the ticket, whole.
//
// The description-is-spec, comments-are-trail line is not throat-clearing:
// a ticket's comments routinely carry approaches that were proposed and
// then rejected, and a cold worker that treats the whole ticket as equally
// authoritative can build the rejected one. Only the description is
// checked against; the comments are read for how it got there.
func implementPrompt(ticketText string) string {
	var b strings.Builder
	b.WriteString("Implement the following ticket in this worktree. The ticket's description is ")
	b.WriteString("the specification: build what it says. Its comments are the record of how it ")
	b.WriteString("got there — the reasoning, the open questions, the answers, approaches raised ")
	b.WriteString("and rejected along the way. Read them for context, not instructions: never ")
	b.WriteString("implement an approach the comments show was rejected in favor of what the ")
	b.WriteString("description states.\n\n")
	b.WriteString(workHandoffSchema)
	b.WriteString("\n\n--- ticket ---\n\n")
	b.WriteString(ticketText)
	return b.String()
}

// fixCIPrompt hands the failing verify output to a cold fixer.
func fixCIPrompt(verify, failure string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The verify command (%s) fails in this worktree. Diagnose and fix the failure, "+
		"then commit the fix. Do not weaken or delete tests to get to green; if the only fix you can "+
		"find is one a human should judge, report blocked instead.\n\n", verify)
	b.WriteString(workHandoffSchema)
	b.WriteString("\n\n--- failing output ---\n\n")
	b.WriteString(failure)
	return b.String()
}

// reviewRules keep the reviewer's hands off the tree: review is a process
// boundary, not a fixing pass.
func reviewRules() []string {
	return []string{
		"Do not modify the working tree in any way — no edits, no formatting, no test " +
			"scaffolding left behind. You are reviewing the change, not fixing it. " +
			"Running read-only commands (build, tests, linters) is fine.",
	}
}

// reviewPrompt asks a cold reviewer for a verdict on the branch.
func reviewPrompt(ticketText, base string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review the change on this branch against its ticket. The base branch is %s; "+
		"see the change with `git log %s..HEAD` and `git diff %s...HEAD`.\n\n", base, base, base)
	b.WriteString(`Approve only on positive evidence: say what you verified and why it is enough.
Raise a finding only when you can state a concrete failure scenario — the inputs or
state under which the code misbehaves, and what goes wrong. A finding without a
failure scenario is dropped in code without being read, so do not pad the list.

Docs change with the code: check whether this diff touches behavior that README.md,
PLAN.md, or docs/ describes, and if so, whether the docs moved with it in this change.
If they did not, raise a finding — same schema as any other, with the failure scenario
being the specific documented claim the diff now leaves stale. This is a diff-shape
check, not a semantic audit: you are looking for documented behavior the diff changed
without a matching doc change, not auditing docs for accuracy in general.

Write your handoff as this JSON object:
{"verdict": "approve" | "revise",
 "summary": "when approving: what you verified and why it is enough (required for approve)",
 "findings": [{"summary": "what is wrong", "failure_scenario": "the concrete inputs or state, and what goes wrong", "location": "file:line"}]}
No other fields.`)
	b.WriteString("\n\n--- ticket ---\n\n")
	b.WriteString(ticketText)
	return b.String()
}

// revisePrompt hands the surviving findings to a cold reviser.
func revisePrompt(ticketText string, findings []Finding) string {
	var b strings.Builder
	b.WriteString("A reviewer raised the findings below against the change on this branch. " +
		"Address each one: fix what is real, and where a finding is wrong, make the code's " +
		"correctness evident instead (a test, an assertion, a comment stating the invariant). " +
		"Commit your changes.\n\n")
	b.WriteString(workHandoffSchema)
	b.WriteString("\n\n--- findings ---\n\n")
	b.WriteString(renderFindings(findings))
	b.WriteString("\n--- ticket ---\n\n")
	b.WriteString(ticketText)
	return b.String()
}

// renderFindings prints findings for prompts and comments alike: numbered,
// with the failure scenario spelled out — the scenario is the finding.
func renderFindings(findings []Finding) string {
	var b strings.Builder
	for i, f := range findings {
		fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(f.Summary))
		if f.Location != "" {
			fmt.Fprintf(&b, "   at: %s\n", strings.TrimSpace(f.Location))
		}
		fmt.Fprintf(&b, "   failure scenario: %s\n", strings.TrimSpace(f.FailureScenario))
	}
	return b.String()
}
