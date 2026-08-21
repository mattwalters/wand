package pm

import (
	"fmt"
	"strings"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
)

// scoutRules keep the scout's hands off everything but its own handoff. pm
// has no worktree at all — there is no ticket yet, let alone one to check
// out — so the rule here is simpler than plan's read-only promise: there
// is nothing to read from disk except the brief it was handed.
func scoutRules() []string {
	return []string{
		"You are not in a checkout of any particular repository, and none of your output is " +
			"implemented by you. Your entire output is the handoff.",
		"Propose tickets only for work implied by the brief. A brief that only partly " +
			"resolves is proposed as-is, with what remains unresolved inside each ticket's body " +
			"as open questions — it is not your job to invent scope the brief never asked for.",
	}
}

// draftSchema documents the Draft JSON, the same pairing plan.draftSchema
// keeps with handoff.go: every constraint stated here is enforced there.
func draftSchema() string {
	return `Write your handoff as this JSON object:
{"premise": "sound" | "wrong",
 "reason": "only when the premise is wrong: what you found, in your own words — it is quoted verbatim to the human",
 "projects": [{"name": "short project name", "description": "what it is for, and why this brief needs a new one rather than an existing project"}],
 "tickets": [{"title": "short imperative title", "body": "the goal, in a few sentences, then any open questions the brief leaves unresolved, in Markdown", "project": "an existing project's name, or one of the names in \"projects\" above, exactly — omit for no project", "blocked_by": ["the exact title of another ticket in this same \"tickets\" array"]}]}

No other fields. The whole object is validated before anything is written, and a
handoff that fails validation writes nothing at all — so:

- At least one ticket. A brief that resolves to nothing worth filing is a wrong
  premise, not an empty proposal.
- Every ticket needs a body: the goal, and what is still open. A title alone
  sends the human who blesses it right back to the brief you already read.
- "project" must name an existing project from the board summary below, or one
  of the names you put in "projects", spelled exactly the same both places.
- "blocked_by" may only name another title in this same "tickets" array, never
  a ticket already on the board — pm proposes a closed, self-contained set.
  No title may block itself, and the set of blocks may not form a cycle.
- Propose new projects sparingly: an existing project that already covers the
  work is the right answer far more often than a new one.
- "premise": "wrong" is for a brief that is already done, already tracked by
  existing tickets, or built on something untrue. It is not for a brief that
  is merely hard or under-specified — propose tickets that say what is open.
  When you use it, nothing else is required, and no tickets are written.`
}

// scoutPrompt is the propose stage's whole task: the brief and the board it
// must be proposed against.
func scoutPrompt(brief, boardSummary string, cov covenant.Covenant) string {
	var b strings.Builder
	b.WriteString("A human has decided the following product brief and wants it turned into " +
		"well-formed tickets. Read the brief and the board summary below, then propose the set " +
		"of tickets that should exist: titles, bodies stating the goal and what is still open, " +
		"project assignments, and blocking order between the tickets you propose.\n\n")
	b.WriteString("Do not propose a ticket for work the board summary shows already exists — " +
		"say so in \"reason\" and report the premise wrong instead. Do not propose a new project " +
		"the board summary shows already covers the work.\n\n")
	b.WriteString(draftSchema())
	b.WriteString("\n\n--- board summary ---\n\n")
	b.WriteString(boardSummary)
	b.WriteString("\n\n--- brief ---\n\n")
	b.WriteString(brief)
	return b.String()
}

// BoardSummary renders the board-read stage's result into what the scout is
// shown: existing projects and every open Triage/Backlog title, so the
// scout can recognize duplication and reuse a project rather than propose
// one that already exists.
func BoardSummary(projects []linear.Project, triage, backlog []linear.Issue) string {
	var b strings.Builder
	b.WriteString("Existing projects:\n")
	if len(projects) == 0 {
		b.WriteString("(none)\n")
	}
	for _, p := range projects {
		fmt.Fprintf(&b, "- %s — %s\n", p.Name, oneLine(p.Description))
	}

	b.WriteString("\nTriage titles:\n")
	writeTitles(&b, triage)
	b.WriteString("\nBacklog titles:\n")
	writeTitles(&b, backlog)
	return b.String()
}

func writeTitles(b *strings.Builder, issues []linear.Issue) {
	if len(issues) == 0 {
		b.WriteString("(none)\n")
		return
	}
	for _, i := range issues {
		fmt.Fprintf(b, "- %s: %s\n", i.Identifier, i.Title)
	}
}

func oneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return "(no description)"
	}
	return s
}
