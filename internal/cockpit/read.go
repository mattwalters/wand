package cockpit

import (
	"context"
	"fmt"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
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
}

// Runs is the slice of the journal store this package uses.
//
// Optional: a repository with no runs yet has no store on disk, and a
// cockpit that refused to draw for want of one would be useless until the
// first orchestrator shipped. [Read] treats a nil Runs as "no lanes".
type Runs interface {
	List() ([]string, error)
	Inspect(id string) (journal.Report, error)
}

// Read fetches the whole board. Four reads and a walk of the run store, in
// that order, with no interleaving: this runs once per refresh and the
// simplicity is worth more than the round trips.
//
// The started-status read exists only to classify lanes — see [Classify].
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

	needsInput, err := cl.TeamIssuesByState(ctx, teamKey, cov.StatusName("needs_input"))
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading Needs Input: %w", err)
	}
	snap.NeedsInput = needsInput

	ready, err := cl.TeamIssuesByLabel(ctx, teamKey, ReadyForHumanLabel)
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading %s: %w", ReadyForHumanLabel, err)
	}
	snap.ReadyForHuman = ready

	started, err := cl.TeamIssuesByStateType(ctx, teamKey, "started")
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading started work: %w", err)
	}

	lanes, err := ReadLanes(runs, started)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Lanes = lanes
	return snap, nil
}

// ReadLanes walks the run store and keeps the runs waiting on a person.
//
// A run whose journal will not replay is not skipped: it becomes an
// unclear lane carrying the parse error. That is the point of the section
// — an unreadable run is exactly a thing only a person can resolve, and
// dropping it would hide the one state the journal itself calls refused.
func ReadLanes(runs Runs, started []linear.Issue) ([]Lane, error) {
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

	var lanes []Lane
	for _, id := range ids {
		report, err := runs.Inspect(id)
		if err != nil {
			lanes = append(lanes, Lane{
				Kind:   LaneUnclear,
				RunID:  id,
				Reason: "the journal will not replay: " + err.Error(),
			})
			continue
		}
		if lane, ok := Classify(report, inStarted); ok {
			lanes = append(lanes, lane)
		}
	}
	return lanes, nil
}
