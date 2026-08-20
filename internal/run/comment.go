package run

import (
	"fmt"
	"strings"
)

// The terminal comments. Each is composed pure, and each is posted before
// the status move it explains (comment-before-status): a failure between the
// two leaves a ticket whose status still matches what is written on it,
// never a Needs Input ticket that asks nothing.

// blockquote renders text as a Markdown quote, the form every verbatim
// worker account takes in a comment — quoted, so a reader can tell the
// worker's words from the orchestrator's.
func blockquote(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("> "+l, " ")
	}
	return strings.Join(lines, "\n")
}

// workNote says where the work sits, for every hand-back: the branch, the PR
// when one exists, and the preserved worktree.
func workNote(branch, prURL, treeDir string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The work so far is committed on branch `%s`", branch)
	if prURL != "" {
		fmt.Fprintf(&b, " (PR: %s)", prURL)
	}
	b.WriteString(".")
	if treeDir != "" {
		fmt.Fprintf(&b, " The run's worktree is preserved at `%s`.", treeDir)
	}
	return b.String()
}

// blockedComment is the hand-back for a worker that reported blocked. The
// reason is the worker's own account, verbatim (the PW-190 lesson) — never a
// guess inferred from the phase.
func blockedComment(phase, reason, branch, prURL, treeDir string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The %s worker stopped on something it judged a human must decide. Its own account:\n\n", phase)
	b.WriteString(blockquote(reason))
	b.WriteString("\n\n")
	b.WriteString(workNote(branch, prURL, treeDir))
	return b.String()
}

// ciCapComment is the hand-back for an exhausted fix-CI cap. Exhaustion is
// named as exhaustion: a cap running out is never allowed to read as
// convergence or as anything else it is not.
func ciCapComment(attempts int, verify, failure, branch, prURL, treeDir string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The verify command (`%s`) is still failing after the fix-CI cap of %d attempt(s) ran out. "+
		"Handing back rather than converging on exhaustion. The last failure:\n\n", verify, attempts)
	fmt.Fprintf(&b, "```\n%s\n```\n\n", strings.TrimSpace(failure))
	b.WriteString(workNote(branch, prURL, treeDir))
	return b.String()
}

// reviewCapComment is the hand-back for an exhausted review-round cap with
// findings still standing. The findings are quoted whole: a final-round
// finding is a real finding, not "a disagreement" to be waved through (the
// PW-176 lesson), and the human deciding needs to read exactly what stood.
func reviewCapComment(rounds int, findings []Finding, branch, prURL, treeDir string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The review-round cap of %d ran out with findings still standing. "+
		"These are the final round's findings, unresolved — not a disagreement that cancels itself out. "+
		"A human owns the call now:\n\n", rounds)
	b.WriteString(renderFindings(findings))
	b.WriteString("\n")
	b.WriteString(workNote(branch, prURL, treeDir))
	return b.String()
}

// vagueReviewComment is the hand-back for a cap that ran out because the
// reviewer kept withholding approval without one concrete finding surviving
// the filter. Saying so honestly beats inventing a cleaner story.
func vagueReviewComment(rounds int, branch, prURL, treeDir string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The review-round cap of %d ran out. The reviewer withheld approval each round, "+
		"but none of its findings stated a concrete failure scenario, so all were dropped before posting. "+
		"The change needs a human's judgement rather than another round.\n\n", rounds)
	b.WriteString(workNote(branch, prURL, treeDir))
	return b.String()
}

// humanThreadsComment is the hand-back for a run whose reviewer approved
// while human review threads still stand on the PR. Outdated is not
// answered (the PW-177 lesson): a revision that moved the code a human
// commented on did not thereby answer the human, so the run does not
// converge over them.
func humanThreadsComment(threads int, reviewSummary, prURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The reviewer approved this change, but %d human review thread(s) on the PR are "+
		"unresolved — counting any a revision has outdated, because outdated is not answered. "+
		"Your threads own the next move.\n\n", threads)
	b.WriteString("The reviewer's approval, for context:\n\n")
	b.WriteString(blockquote(reviewSummary))
	fmt.Fprintf(&b, "\n\nPR: %s\n", prURL)
	return b.String()
}

// convergedComment announces the terminal success on the ticket: what the
// reviewer verified (the positive evidence), any plan deviations, and the
// PR a human should now read.
func convergedComment(round int, reviewSummary, prURL string, deviations []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Implemented, verify green, and the reviewer approved on round %d:\n\n", round)
	b.WriteString(blockquote(reviewSummary))
	b.WriteString("\n")
	if len(deviations) > 0 {
		b.WriteString("\nDeviations from the ticket's plan:\n")
		for _, d := range deviations {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(d))
		}
	}
	fmt.Fprintf(&b, "\nPR: %s — ready for a human.\n", prURL)
	return b.String()
}

// correctionsComment quotes each description wording being corrected, in the
// same act as the edit. The quote is the record: Linear surfaces description
// history poorly, so the comment is where the disproven claim survives.
func correctionsComment(phase string, corrections []Correction) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The %s worker's findings correct the description. It asserted:\n", phase)
	for _, c := range corrections {
		b.WriteString("\n")
		b.WriteString(blockquote(c.Old))
		b.WriteString("\n\n")
		if strings.TrimSpace(c.New) == "" {
			b.WriteString("That wording is removed in the same action as this comment.\n")
		} else {
			b.WriteString("Corrected in the same action as this comment to:\n\n")
			b.WriteString(blockquote(c.New))
			b.WriteString("\n")
		}
	}
	return b.String()
}
