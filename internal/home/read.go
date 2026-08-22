package home

import (
	"context"
	"fmt"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/queue"
)

// Linear is the slice of the Linear client this package uses. An interface
// so tests hold a fake: the orderings in apply.go are the point of that
// file, and a test has to be able to watch them.
type Linear interface {
	TeamIssuesByState(ctx context.Context, teamKey, stateName string) ([]linear.Issue, error)
	TeamIssuesByStateType(ctx context.Context, teamKey, stateType string) ([]linear.Issue, error)
	TeamIssuesByLabel(ctx context.Context, teamKey, label string) ([]linear.Issue, error)
	IssueByIdentifier(ctx context.Context, identifier string) (linear.Issue, error)
	TeamStates(ctx context.Context, teamID string) ([]linear.WorkflowState, error)
	UpdateIssue(ctx context.Context, issueID string, u linear.IssueUpdate) error
	CreateComment(ctx context.Context, issueID, body string) error
	CreateRelation(ctx context.Context, issueID, relatedID, relType string) error
	LabelByName(ctx context.Context, name string) (linear.Label, bool, error)
	RemoveLabel(ctx context.Context, issueID, labelID string) error
}

// Runs is the slice of the journal store this package uses.
//
// Optional: a repository with no runs yet has no store on disk, and a home
// screen that refused to draw for want of one would be useless until the
// first orchestrator shipped. [Read] treats a nil Runs as "nothing stalled".
type Runs interface {
	List() ([]string, error)
	Inspect(id string) (journal.Report, error)
}

// Read fetches the whole board. Five reads and a walk of the run store, in
// that order, with no interleaving: this runs once per refresh and the
// simplicity is worth more than the round trips.
//
// The started-status read exists only to classify runs — see [Classify].
// It is a read of the board rather than a per-run lookup because one query
// answers it for every run at once, and because a run whose ticket has been
// deleted then reads as orphaned, which is exactly right.
func Read(ctx context.Context, cl Linear, runs Runs, cov covenant.Covenant, teamKey string) (Snapshot, error) {
	snap := Snapshot{Team: teamKey}

	triage, err := cl.TeamIssuesByState(ctx, teamKey, cov.StatusName("triage"))
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading Triage: %w", err)
	}
	snap.Triage = triage

	planReview, err := cl.TeamIssuesByState(ctx, teamKey, cov.StatusName("plan_review"))
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading Plan Review: %w", err)
	}
	snap.PlanReview = planReview

	needsInput, err := cl.TeamIssuesByState(ctx, teamKey, cov.StatusName("needs_input"))
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading Needs Input: %w", err)
	}
	snap.NeedsInput = needsInput

	inReview, err := cl.TeamIssuesByState(ctx, teamKey, cov.StatusName("in_review"))
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading In Review: %w", err)
	}
	snap.InReview = inReview

	started, err := cl.TeamIssuesByStateType(ctx, teamKey, "started")
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading started work: %w", err)
	}

	parked, err := cl.TeamIssuesByLabel(ctx, teamKey, queue.ParkedLabel)
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading %s: %w", queue.ParkedLabel, err)
	}

	stalled, err := ReadStalled(runs, started, parked)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Stalled = stalled

	active, err := ActiveRuns(runs)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Active = active
	return snap, nil
}

// ReadStalled walks the run store and keeps the runs waiting on a person.
//
// A run whose journal will not replay is not skipped: it becomes an
// unclear row carrying the parse error. That is the point of the section
// — an unreadable run is exactly a thing only a person can resolve, and
// dropping it would hide the one state the journal itself calls refused.
//
// A parked run whose ticket a later run resolved is dropped by
// [Reconcile] before this returns — see there for why.
func ReadStalled(runs Runs, started, parked []linear.Issue) ([]StalledRun, error) {
	if runs == nil {
		return nil, nil
	}
	ids, err := runs.List()
	if err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}

	inStarted := make(map[string]bool, len(started))
	for _, issue := range started {
		inStarted[issue.Identifier] = true
	}
	stillParked := make(map[string]bool, len(parked))
	for _, issue := range parked {
		stillParked[issue.Identifier] = true
	}

	var stalled []StalledRun
	var reports []journal.Report
	for _, id := range ids {
		report, err := runs.Inspect(id)
		if err != nil {
			stalled = append(stalled, StalledRun{
				Kind:   StallUnclear,
				RunID:  id,
				Reason: "the journal will not replay: " + err.Error(),
			})
			continue
		}
		reports = append(reports, report)
		if st, ok := Classify(report, inStarted); ok {
			stalled = append(stalled, st)
		}
	}
	return Reconcile(stalled, reports, stillParked), nil
}
