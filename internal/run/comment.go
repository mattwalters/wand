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

// workState is what a hand-back can truthfully say about where the work
// sits, read from git at hand-back time — never assumed. known false means
// git could not be read, and the note says less rather than guessing.
type workState struct {
	known  bool
	ahead  int  // commits on the branch beyond base
	dirty  bool // uncommitted changes in the worktree
	pushed bool // the branch has reached origin at least once
}

// workNote says where the work sits, for every hand-back: what the branch
// actually holds, whether it ever reached origin, the PR when one exists,
// and the preserved worktree. Composed from checked facts — a comment that
// tells a human "the work is committed" when nothing is would send them
// deleting the one copy (the reason requireClean exists).
func workNote(branch, prURL, treeDir string, s workState) string {
	var b strings.Builder
	switch {
	case !s.known:
		fmt.Fprintf(&b, "The run's branch is `%s`", branch)
	case s.ahead == 0:
		fmt.Fprintf(&b, "Branch `%s` has no commits yet", branch)
	default:
		fmt.Fprintf(&b, "The work so far is committed on branch `%s` (%d commit(s))", branch, s.ahead)
	}
	if prURL != "" {
		fmt.Fprintf(&b, " (PR: %s)", prURL)
	}
	b.WriteString(".")
	if s.known && s.ahead > 0 && !s.pushed {
		b.WriteString(" The branch has not been pushed; it exists only in the preserved worktree.")
	}
	if s.known && s.dirty {
		b.WriteString(" Uncommitted changes remain in the worktree — they exist nowhere else.")
	}
	if treeDir != "" {
		fmt.Fprintf(&b, " The run's worktree is preserved at `%s`.", treeDir)
	}
	return b.String()
}

// deviationList renders the plan deviations a run collected. One renderer,
// because the PR body, the converged comment and every hand-back are all
// reporting the same thing and should not drift apart in wording.
func deviationList(deviations []string) string {
	var b strings.Builder
	b.WriteString("Deviations from the ticket's plan:\n")
	for _, d := range deviations {
		fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(d))
	}
	return b.String()
}

// withDeviations appends the run's deviations to a hand-back comment.
// [loop.handback] applies it to every hand-back rather than each builder
// doing it, so none can forget: the PR body carries only the deviations
// known when it was composed, and a run that hands back at the review cap
// would otherwise let every revise round's account die in a transcript
// (the PW-191 lesson) on the one ending a human is about to read.
func withDeviations(comment string, deviations []string) string {
	if len(deviations) == 0 {
		return comment
	}
	return strings.TrimRight(comment, "\n") + "\n\n" + deviationList(deviations)
}

// blockedComment is the hand-back for a worker that reported blocked. The
// reason is the worker's own account, verbatim (the PW-190 lesson) — never a
// guess inferred from the phase.
func blockedComment(phase, reason, branch, prURL, treeDir string, s workState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The %s worker stopped on something it judged a human must decide. Its own account:\n\n", phase)
	b.WriteString(blockquote(reason))
	b.WriteString("\n\n")
	b.WriteString(workNote(branch, prURL, treeDir, s))
	return b.String()
}

// ciCapComment is the hand-back for an exhausted fix-CI cap. Exhaustion is
// named as exhaustion: a cap running out is never allowed to read as
// convergence or as anything else it is not.
func ciCapComment(attempts int, verify, failure, branch, prURL, treeDir string, s workState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The verify command (`%s`) is still failing after the fix-CI cap of %d attempt(s) ran out. "+
		"Handing back rather than converging on exhaustion. The last failure:\n\n", verify, attempts)
	fmt.Fprintf(&b, "```\n%s\n```\n\n", strings.TrimSpace(failure))
	b.WriteString(workNote(branch, prURL, treeDir, s))
	return b.String()
}

// reviewCapComment is the hand-back for an exhausted review-round cap with
// findings still standing. The findings are quoted whole: a final-round
// finding is a real finding, not "a disagreement" to be waved through (the
// PW-176 lesson), and the human deciding needs to read exactly what stood.
func reviewCapComment(rounds int, findings []Finding, branch, prURL, treeDir string, s workState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The review-round cap of %d ran out with findings still standing. "+
		"These are the final round's findings, unresolved — not a disagreement that cancels itself out. "+
		"A human owns the call now:\n\n", rounds)
	b.WriteString(renderFindings(findings))
	b.WriteString("\n")
	b.WriteString(workNote(branch, prURL, treeDir, s))
	return b.String()
}

// vagueReviewComment is the hand-back for a cap whose final round withheld
// approval without one concrete finding surviving the filter. It speaks
// only for what it knows — the final round — because earlier rounds may
// have raised real findings that a revision already addressed, and telling
// a human the whole history was noise would mislabel it (the same honesty
// PW-176 demands of the cap itself).
func vagueReviewComment(rounds int, branch, prURL, treeDir string, s workState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The review-round cap of %d ran out. The final reviewer withheld approval, "+
		"but none of its findings stated a concrete failure scenario, so all were dropped before posting. "+
		"The change needs a human's judgement rather than another round.\n\n", rounds)
	b.WriteString(workNote(branch, prURL, treeDir, s))
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
		b.WriteString("\n")
		b.WriteString(deviationList(deviations))
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
