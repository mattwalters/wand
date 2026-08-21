// Package sweep is everything that happens after a run exits.
//
// wand run and wand plan each end in exactly one terminal state, and each
// terminal state is where their own responsibility stops. Three things can
// still be true of a ticket after that:
//
//   - a human labeled a converged ticket [ReReviewLabel]: another cycle,
//     asked for explicitly;
//   - a human labeled a Plan Review ticket [verbs.RePlanLabel]: the
//     planning-side twin of the above, another planning cycle asked for
//     explicitly — sweep hands it back into In Planning rather than Needs
//     Input, because the human's comments already answered the question a
//     re-plan resumes over;
//   - a converged ticket's PR carries an unresolved human review thread —
//     necessarily left *after* convergence, because run.Execute's own
//     check at the moment of converging would have caught one standing
//     before that; a structural fact, not something inferred from who
//     posted it;
//   - a run's lease says its holder is provably dead — the zombie, a
//     ticket In Progress with nothing behind it, which looks healthy and
//     which nothing else drains.
//
// A sweep pass acts on at most one of these, ranked ([Rank]) and vetted the
// same way dispatch picks a winner: the first candidate whose preflight
// does not refuse it. Skipping past a refusal in the same pass — never
// retrying the same refused candidate twice in one pass — is what keeps an
// unstartable candidate from starving every other one forever; the next
// pass re-evaluates everything fresh.
//
// Sweep also *reports*, read-only, tickets sitting In Progress with no
// journal run behind them at all — not even a dead one ([ZombieReports]).
// There is nothing there to reap, only something that looks stuck for a
// person to judge; sweep never acts on it.
package sweep

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/run"
	"github.com/mattwalters/wand/internal/verbs"
)

// ActedKind is how a pass's one action ended.
type ActedKind string

const (
	ActedNothing  ActedKind = "nothing"
	ActedHandback ActedKind = "handed_back"
	ActedReaped   ActedKind = "reaped"
)

// Result is everything one sweep pass found and did.
type Result struct {
	// Acted is the one candidate the pass wrote against, if any.
	Acted     ActedKind
	Candidate Candidate
	// Skipped lists every higher-ranked candidate a preflight refused
	// before Acted was reached — printed so a skip never reads as silence.
	Skipped []SkipReason
	// Zombies are In Progress tickets with no run behind them at all,
	// reported and never acted on.
	Zombies []ZombieReport
}

// SkipReason is one candidate a preflight refused, and why.
type SkipReason struct {
	Candidate Candidate
	Reason    string
}

// Execute runs one sweep pass over repo.
func Execute(ctx context.Context, d Deps, store *journal.Store) (Result, error) {
	if err := d.validate(); err != nil {
		return Result{}, err
	}
	if d.Out == nil {
		d.Out = io.Discard
	}

	ids, err := d.Runs.List()
	if err != nil {
		return Result{}, fmt.Errorf("sweep: listing runs: %w", err)
	}
	reports := make(map[string]journal.Report, len(ids))
	for _, id := range ids {
		r, err := d.Runs.Inspect(id)
		if err != nil {
			fmt.Fprintf(d.Out, "sweep: run %s could not be inspected: %v\n", id, err)
			continue
		}
		reports[id] = r
	}

	reReview, err := d.Board.TeamIssuesByLabel(ctx, d.TeamKey, ReReviewLabel)
	if err != nil {
		return Result{}, fmt.Errorf("sweep: reading %s: %w", ReReviewLabel, err)
	}
	rePlan, err := d.Board.TeamIssuesByLabel(ctx, d.TeamKey, verbs.RePlanLabel)
	if err != nil {
		return Result{}, fmt.Errorf("sweep: reading %s: %w", verbs.RePlanLabel, err)
	}
	readyForHuman, err := d.Board.TeamIssuesByLabel(ctx, d.TeamKey, run.ReadyForHumanLabel)
	if err != nil {
		return Result{}, fmt.Errorf("sweep: reading %s: %w", run.ReadyForHumanLabel, err)
	}
	inProgress, err := d.Board.TeamIssuesByState(ctx, d.TeamKey, d.Cov.StatusName("in_progress"))
	if err != nil {
		return Result{}, fmt.Errorf("sweep: reading %s: %w", d.Cov.StatusName("in_progress"), err)
	}

	unresolved := d.unresolvedThreadCandidates(ctx, openIssues(readyForHuman))

	res := Result{
		Zombies: ZombieReports(openIssues(inProgress), reports, d.Repo),
	}
	for _, z := range res.Zombies {
		fmt.Fprintf(d.Out, "sweep: %s is In Progress with no run behind it — looks stuck, reported only\n", z.Ticket)
	}

	candidates := Rank(append(append(append(
		DeadLeaseCandidates(reports, d.Repo),
		ReReviewCandidates(openIssues(reReview))...),
		RePlanCandidates(openIssues(rePlan))...),
		unresolved...))

	for _, c := range candidates {
		acted, err := d.act(ctx, store, c)
		if err != nil {
			res.Skipped = append(res.Skipped, SkipReason{Candidate: c, Reason: err.Error()})
			fmt.Fprintf(d.Out, "sweep: skipped %s (%s): %v\n", c.Ticket, c.Kind, err)
			continue
		}
		if acted {
			res.Acted = actedKindFor(c.Kind)
			res.Candidate = c
			fmt.Fprintf(d.Out, "sweep: %s on %s: %s\n", res.Acted, c.Ticket, c.Reason)
			return res, nil
		}
		// The condition it was found for no longer holds — resolved
		// between the read and here. Nothing to skip loudly about; try
		// the next candidate.
	}
	res.Acted = ActedNothing
	return res, nil
}

func actedKindFor(k Kind) ActedKind {
	if k == KindDeadLease {
		return ActedReaped
	}
	return ActedHandback
}

// act performs the one write a candidate calls for, after re-checking that
// the condition still holds. It returns acted=false, err=nil when the
// condition already resolved itself (nothing wrong, nothing to do), and a
// non-nil err for anything a preflight would refuse — the caller moves on
// to the next candidate either way.
func (d Deps) act(ctx context.Context, store *journal.Store, c Candidate) (acted bool, err error) {
	switch c.Kind {
	case KindDeadLease:
		return d.reap(ctx, store, c)
	case KindReReview:
		return d.actReReview(ctx, c)
	case KindRePlan:
		return d.actRePlan(ctx, c)
	case KindUnresolvedThreads:
		return d.actUnresolvedThreads(ctx, c)
	default:
		return false, fmt.Errorf("sweep: candidate has no kind; this is a wand bug")
	}
}

// reap reopens a dead-lease run, parks its journal with the lease's own
// account, and hands the ticket back to a human — in that order, so the
// journal is never left saying "still going" once a person has been told
// otherwise would be worse than the reverse: a park that never reaches
// Linear at least leaves the journal honest.
//
// Reopen is itself the preflight: it takes the run's lock the same way a
// resume would, so a holder that revived between the read and here is
// found here, atomically, and reported as a refusal rather than reaped out
// from under it.
func (d Deps) reap(ctx context.Context, store *journal.Store, c Candidate) (bool, error) {
	r, err := store.Reopen(c.RunID)
	if err != nil {
		return false, fmt.Errorf("could not reopen the run: %w", err)
	}
	defer r.Close()
	if err := r.Parked(c.Reason); err != nil {
		return false, fmt.Errorf("could not park the reopened run: %w", err)
	}
	// Best-effort past this point: the journal is already honest, which is
	// the property this action exists to restore. A failed hand-back is
	// worth a line, not a reason to try this candidate again — the next
	// pass will find the run already ended and will not re-select it.
	if _, err := verbs.Handback(ctx, d.Board, d.Cov, c.Ticket, deadLeaseComment(c)); err != nil {
		fmt.Fprintf(d.Out, "sweep: run %s parked, but handing %s back failed: %v\n", c.RunID, c.Ticket, err)
	}
	return true, nil
}

func deadLeaseComment(c Candidate) string {
	return fmt.Sprintf(
		"wand sweep found this ticket's run stopped without a live process behind it: %s. "+
			"The run's journal has been parked; nothing is driving this ticket, so it needs a fresh look before it runs again.",
		c.Reason)
}

// actReReview re-checks the label is still there, then hands the ticket
// back. verbs.Handback is its own preflight: a ticket closed in the
// interim refuses there, cleanly, before anything is written.
func (d Deps) actReReview(ctx context.Context, c Candidate) (bool, error) {
	issue, err := d.Board.IssueByIdentifier(ctx, c.Ticket)
	if err != nil {
		return false, fmt.Errorf("could not re-read the ticket: %w", err)
	}
	if !hasLabel(issue, ReReviewLabel) {
		return false, nil // resolved since the read: the label is gone
	}
	comment := fmt.Sprintf(
		"This ticket is labeled %s: a human asked for another cycle. wand sweep is handing it back so it can be picked up again.",
		ReReviewLabel)
	if _, err := verbs.Handback(ctx, d.Board, d.Cov, c.Ticket, comment); err != nil {
		return false, err
	}
	return true, nil
}

// actRePlan re-checks the label is still there, then hands the ticket back
// into In Planning — the planning-side twin of [Deps.actReReview]. Unlike a
// re-review, this does not go through [verbs.Handback]: a re-plan resumes a
// cycle the human's comments on Plan Review already answered, so it returns
// to the started status a live plan run claims, via [verbs.ReturnToPlanning],
// rather than asking a fresh question through Needs Input.
func (d Deps) actRePlan(ctx context.Context, c Candidate) (bool, error) {
	issue, err := d.Board.IssueByIdentifier(ctx, c.Ticket)
	if err != nil {
		return false, fmt.Errorf("could not re-read the ticket: %w", err)
	}
	if !hasLabel(issue, verbs.RePlanLabel) {
		return false, nil // resolved since the read: the label is gone
	}
	comment := fmt.Sprintf(
		"This ticket is labeled %s: a human asked for another planning cycle. wand sweep is handing it back into %s so the cycle can resume.",
		verbs.RePlanLabel, d.Cov.StatusName("in_planning"))
	if _, err := verbs.ReturnToPlanning(ctx, d.Board, d.Cov, c.Ticket, comment); err != nil {
		return false, err
	}
	return true, nil
}

// actUnresolvedThreads re-reads the PR's threads — a human may have
// resolved them since the candidate was gathered — then hands the ticket
// back if any still stand.
func (d Deps) actUnresolvedThreads(ctx context.Context, c Candidate) (bool, error) {
	issue, err := d.Board.IssueByIdentifier(ctx, c.Ticket)
	if err != nil {
		return false, fmt.Errorf("could not re-read the ticket: %w", err)
	}
	if issue.BranchName == "" {
		return false, nil
	}
	pr, found, err := d.Hub.PRForBranch(ctx, d.Repo, issue.BranchName)
	if err != nil {
		return false, fmt.Errorf("could not look up the PR: %w", err)
	}
	// Open only. A thread left unresolved on a PR that has since merged is
	// not a person still waiting: merging is the answer.
	if !found || pr.State != run.PRStateOpen {
		return false, nil
	}
	n, err := d.Hub.UnresolvedThreads(ctx, d.Repo, pr.Number)
	if err != nil {
		return false, fmt.Errorf("could not read the PR's review threads: %w", err)
	}
	if n == 0 {
		return false, nil // resolved since the read
	}
	comment := fmt.Sprintf(
		"%d unresolved review thread(s) stand on %s. This was left after the ticket converged — "+
			"wand run's own check for this happens only at the moment of converging, so a thread opened "+
			"afterward is exactly what nothing else catches. Please resolve them, or label the ticket %s for another cycle.",
		n, pr.URL, ReReviewLabel)
	if _, err := verbs.Handback(ctx, d.Board, d.Cov, c.Ticket, comment); err != nil {
		return false, err
	}
	return true, nil
}

// unresolvedThreadCandidates checks every ready-for-human issue's PR for
// unresolved threads. One Hub round trip per issue — the same cost `wand
// run`'s own convergence check pays for one ticket, paid here for the
// whole ready-for-human queue.
func (d Deps) unresolvedThreadCandidates(ctx context.Context, issues []linear.Issue) []Candidate {
	var out []Candidate
	for _, issue := range issues {
		if issue.BranchName == "" {
			continue
		}
		pr, found, err := d.Hub.PRForBranch(ctx, d.Repo, issue.BranchName)
		if err != nil || !found || pr.State != run.PRStateOpen {
			continue
		}
		n, err := d.Hub.UnresolvedThreads(ctx, d.Repo, pr.Number)
		if err != nil || n == 0 {
			continue
		}
		out = append(out, Candidate{
			Kind:   KindUnresolvedThreads,
			Ticket: issue.Identifier,
			Reason: fmt.Sprintf("%d unresolved review thread(s) on %s", n, pr.URL),
			Since:  issue.CreatedAt,
		})
	}
	return out
}

// openIssues drops closed work, the same discipline [cockpit.openIssues]
// applies to ready-for-human: a label outlives the merge that answered it.
func openIssues(issues []linear.Issue) []linear.Issue {
	var open []linear.Issue
	for _, issue := range issues {
		switch issue.State.Type {
		case "completed", "canceled":
		default:
			open = append(open, issue)
		}
	}
	return open
}

func hasLabel(issue linear.Issue, name string) bool {
	for _, l := range issue.Labels {
		if strings.EqualFold(l, name) {
			return true
		}
	}
	return false
}
