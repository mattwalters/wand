package sweep

import (
	"fmt"
	"sort"
	"time"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/verbs"
)

// Kind names the condition a candidate was found for.
type Kind string

const (
	// KindDeadLease: the journal says a run is still going and its lease's
	// holder is provably dead — the zombie run, still lying that it holds
	// the ticket's lane.
	KindDeadLease Kind = "dead_lease"
	// KindReReview: a human labeled a converged ticket for another cycle.
	KindReReview Kind = "re_review"
	// KindRePlan: a human labeled a Plan Review ticket for another
	// planning cycle — the planning-side twin of KindReReview.
	KindRePlan Kind = "re_plan"
	// KindUnresolvedThreads: a converged ticket's PR carries an unresolved
	// human review thread — necessarily left after the run itself ended,
	// because run.Execute's own convergence check would have caught one
	// standing before that.
	KindUnresolvedThreads Kind = "unresolved_threads"
)

// Candidate is one thing a sweep pass could act on.
type Candidate struct {
	Kind Kind
	// Ticket is the Linear identifier a hand-back would target. Set for
	// every kind — a dead lease's run carries its own ticket in Meta.
	Ticket string
	// RunID is set only for KindDeadLease: the journal run to reopen and
	// park.
	RunID string
	// Reason is the human sentence a hand-back comment is built from.
	Reason string
	// Since orders candidates within a kind, oldest first — the same
	// fairness the queue package's age tiebreak gives Todo.
	Since time.Time
}

// severity ranks kinds: a dead lease first, because it is the one state
// actively lying about itself on the board — the ticket reads In Progress
// and nothing is behind it — while a re-review or an unresolved thread is
// an orderly, already-visible request for another look. Mirrors
// [cockpit.laneSeverity]'s own reasoning for the same ordering problem.
var severity = map[Kind]int{
	KindDeadLease:         0,
	KindReReview:          1,
	KindRePlan:            1,
	KindUnresolvedThreads: 2,
}

// Rank orders every candidate a pass could act on: by kind severity, oldest
// first within a kind. Pure: a test can hold the whole priority without a
// board or a store. Execute walks this order and acts on the first
// candidate whose preflight does not refuse it — see the package doc for
// why skipping past a refusal, in the same pass, is what keeps one
// unstartable candidate from starving every other forever.
func Rank(candidates []Candidate) []Candidate {
	ranked := make([]Candidate, len(candidates))
	copy(ranked, candidates)
	sort.SliceStable(ranked, func(i, j int) bool {
		if a, b := severity[ranked[i].Kind], severity[ranked[j].Kind]; a != b {
			return a < b
		}
		return ranked[i].Since.Before(ranked[j].Since)
	})
	return ranked
}

// DeadLeaseCandidates finds every run of repo the journal says is still
// going whose lease's holder is provably dead. Read-only, same as
// dispatch's own gc: [journal.Dead] is the only liveness a sweeper may act
// on — see [journal.Report.Zombie].
func DeadLeaseCandidates(reports map[string]journal.Report, repo string) []Candidate {
	var out []Candidate
	for id, r := range reports {
		if r.State.Meta.Repo != repo || r.State.Ended() || r.Live != journal.Dead {
			continue
		}
		out = append(out, Candidate{
			Kind:   KindDeadLease,
			Ticket: r.State.Meta.Ticket,
			RunID:  id,
			Reason: deadLeaseReason(id, r),
			Since:  r.State.Updated,
		})
	}
	return out
}

func deadLeaseReason(runID string, r journal.Report) string {
	return fmt.Sprintf("the run's holder (pid %d on %s) is gone; nothing is driving %s (run %s)",
		r.Lease.PID, r.Lease.Host, r.State.Meta.Ticket, runID)
}

// ReReviewCandidates turns every issue labeled [ReReviewLabel] into a
// candidate. Filtering to open, in-review work is the caller's — the same
// openIssues discipline [cockpit.Build] applies to ready-for-human.
func ReReviewCandidates(issues []linear.Issue) []Candidate {
	out := make([]Candidate, 0, len(issues))
	for _, issue := range issues {
		out = append(out, Candidate{
			Kind:   KindReReview,
			Ticket: issue.Identifier,
			Reason: "labeled " + ReReviewLabel + ": a human asked for another cycle",
			Since:  issue.CreatedAt,
		})
	}
	return out
}

// RePlanCandidates turns every issue labeled [verbs.RePlanLabel] into a
// candidate — the planning-side twin of [ReReviewCandidates]. Filtering to
// open work is the caller's, the same discipline ReReviewCandidates leaves
// to it.
func RePlanCandidates(issues []linear.Issue) []Candidate {
	out := make([]Candidate, 0, len(issues))
	for _, issue := range issues {
		out = append(out, Candidate{
			Kind:   KindRePlan,
			Ticket: issue.Identifier,
			Reason: "labeled " + verbs.RePlanLabel + ": a human asked for another planning cycle",
			Since:  issue.CreatedAt,
		})
	}
	return out
}

// ZombieReport is one In Progress ticket with no run behind it at all —
// not even a dead one — for sweep's read-only report. Distinct from
// [KindDeadLease]: there the journal has a run to reap; here there is
// nothing to act on, only something for a person to look at.
type ZombieReport struct {
	Ticket string
	Title  string
}

// ZombieReports finds every In Progress issue with no journal run —
// dead or alive — for repo. Never acted on: only reported, because
// "looks stuck" versus "is stuck" is a human's call once there is nothing
// provable to reap.
func ZombieReports(inProgress []linear.Issue, reports map[string]journal.Report, repo string) []ZombieReport {
	backed := map[string]bool{}
	for _, r := range reports {
		if r.State.Meta.Repo == repo && !r.State.Ended() {
			backed[r.State.Meta.Ticket] = true
		}
	}
	var out []ZombieReport
	for _, issue := range inProgress {
		if !backed[issue.Identifier] {
			out = append(out, ZombieReport{Ticket: issue.Identifier, Title: issue.Title})
		}
	}
	return out
}
