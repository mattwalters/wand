package cockpit

import (
	"context"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
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

	if len(cl.states) != 3 || cl.states[0] != "Inbox" || cl.states[1] != "Scoped" || cl.states[2] != "Blocked on me" {
		t.Errorf("statuses read = %v, want the covenant's own names", cl.states)
	}
	if len(cl.labels) != 1 || cl.labels[0] != ReadyForHumanLabel {
		t.Errorf("labels read = %v, want %q", cl.labels, ReadyForHumanLabel)
	}
	// The started read exists only to tell a held lane from an orphaned
	// one; it is by type because a covenant may name two started columns.
	if len(cl.types) != 1 || cl.types[0] != "started" {
		t.Errorf("state types read = %v, want [started]", cl.types)
	}
	if snap.Team != "WND" {
		t.Errorf("team = %q, want WND", snap.Team)
	}
	if len(snap.Triage) != 1 || len(snap.Scoped) != 1 || len(snap.NeedsInput) != 1 || len(snap.ReadyForHuman) != 1 {
		t.Errorf("snapshot = %+v, want one issue in each queue", snap)
	}
	if snap.Lanes != nil {
		t.Errorf("lanes = %v, want none without a run store", snap.Lanes)
	}
}
