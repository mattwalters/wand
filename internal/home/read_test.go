package home

import (
	"context"
	"github.com/mattwalters/wand/internal/queue"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
)

// readFake answers each of the five board reads with a marker issue, and
// records what it was asked for.
type readFake struct {
	fakeLinear
	states []string
	types  []string
	labels []string
}

func (f *readFake) TeamIssuesByState(_ context.Context, _, state string) ([]linear.Issue, error) {
	f.states = append(f.states, state)
	return []linear.Issue{{Identifier: "in-" + state}}, nil
}

func (f *readFake) TeamIssuesByStateType(_ context.Context, _, t string) ([]linear.Issue, error) {
	f.types = append(f.types, t)
	return []linear.Issue{{Identifier: "WND-9"}}, nil
}

func (f *readFake) TeamIssuesByLabel(_ context.Context, _, label string) ([]linear.Issue, error) {
	f.labels = append(f.labels, label)
	return []linear.Issue{{Identifier: "in-" + label}}, nil
}

// The board is read by what statuses *mean*, not by what a stock team calls
// them: a repo whose blessed column is called Ready still has a Triage and a
// Needs Input under whatever names its covenant gives them.
func TestReadFollowsTheCovenantsNames(t *testing.T) {
	cov := covenant.Default()
	for i := range cov.Statuses {
		switch cov.Statuses[i].Key {
		case "triage":
			cov.Statuses[i].Name = "Inbox"
		case "needs_input":
			cov.Statuses[i].Name = "Blocked on me"
		}
	}

	cl := &readFake{}
	snap, err := Read(context.Background(), cl, nil, cov, "WND")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if len(cl.states) != 4 || cl.states[0] != "Inbox" || cl.states[1] != "Plan Review" ||
		cl.states[2] != "Blocked on me" || cl.states[3] != "In Review" {
		t.Errorf("statuses read = %v, want the covenant's own names", cl.states)
	}
	// One label read: parked decides which parked stalled runs are still a
	// live obligation rather than a run that happened (WND-85). Covenant
	// topology, so not renamed by a covenant file. ready-for-human no longer
	// drives a read of its own — In Review's rows carry their labels for
	// free, and Build sorts on them.
	if len(cl.labels) != 1 || cl.labels[0] != queue.ParkedLabel {
		t.Errorf("labels read = %v, want %q", cl.labels, queue.ParkedLabel)
	}
	// The started read exists only to tell a held stalled run from an orphaned
	// one; it is by type because a covenant may name two started columns.
	if len(cl.types) != 1 || cl.types[0] != "started" {
		t.Errorf("state types read = %v, want [started]", cl.types)
	}
	if snap.Team != "WND" {
		t.Errorf("team = %q, want WND", snap.Team)
	}
	if len(snap.Triage) != 1 || len(snap.PlanReview) != 1 || len(snap.NeedsInput) != 1 || len(snap.InReview) != 1 {
		t.Errorf("snapshot = %+v, want one issue in each queue", snap)
	}
	if snap.Stalled != nil {
		t.Errorf("stalled = %v, want none without a run store", snap.Stalled)
	}
	if snap.Active != nil {
		t.Errorf("active = %v, want none without a run store", snap.Active)
	}
}

// Read's walk of the run store fills both Stalled and Active from the same
// journal — a live, started run is not a stalled run (nobody has to resolve it),
// but it is what the machine is doing right now.
func TestReadFillsActiveAlongsideStalled(t *testing.T) {
	at := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	runs := &fakeRuns{
		ids: []string{"r"},
		reports: map[string]journal.Report{
			"r": activeReport("r", "WND-9", "implement", 1, at, at, journal.Alive),
		},
	}
	cl := &readFake{}
	snap, err := Read(context.Background(), cl, runs, covenant.Default(), "WND")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(snap.Active) != 1 || snap.Active[0].RunID != "r" {
		t.Errorf("active = %+v, want the one live run", snap.Active)
	}
	// readFake's started read answers WND-9 as started, so the same run is
	// not also a stalled run — Classify already covers this; this test is only
	// about Active being wired into Read at all.
	if len(snap.Stalled) != 0 {
		t.Errorf("stalled = %+v, want none: a live run on a started ticket needs nobody", snap.Stalled)
	}
}
