package cockpit

import (
	"errors"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
)

func report(outcome journal.Outcome, reason string, live journal.Liveness) journal.Report {
	return journal.Report{
		State: journal.State{
			Meta:    journal.Meta{ID: "run-1", Ticket: "WND-9", Verb: "run", Repo: "/repo"},
			Phase:   "implement",
			Round:   2,
			Outcome: outcome,
			Reason:  reason,
			Updated: time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC),
		},
		Lease: journal.Lease{PID: 4821, Host: "studio.local"},
		Live:  live,
	}
}

func TestClassify(t *testing.T) {
	started := map[string]bool{"WND-9": true}

	tests := []struct {
		name    string
		report  journal.Report
		started map[string]bool
		want    LaneKind
		wantAny bool
	}{
		{
			name: "a converged run needs nobody",
			// Nothing is waiting: the run reached its goal on positive
			// evidence, which is what converged means.
			report: report(journal.Converged, "green", journal.Dead), started: started,
		},
		{
			name:   "a handed-back run needs nobody here",
			report: report(journal.HandedBack, "asked a question", journal.Dead), started: started,
			// The ticket itself is in Needs Input, which is its own section.
			// Listing the lane too would double-count one thing to do.
		},
		{
			name:   "a parked run is waiting",
			report: report(journal.Parked, "the worktree was dirty", journal.Dead), started: started,
			want: LaneParked, wantAny: true,
		},
		{
			name:   "an unfinished run with a dead holder is stuck",
			report: report("", "", journal.Dead), started: started,
			want: LaneStuck, wantAny: true,
		},
		{
			name:   "an unfinished run on another machine is unclear",
			report: report("", "", journal.Unknown), started: started,
			want: LaneUnclear, wantAny: true,
		},
		{
			name:   "a live run on a started ticket needs nobody",
			report: report("", "", journal.Alive), started: started,
		},
		{
			name:   "a live run nothing claims is orphaned",
			report: report("", "", journal.Alive), started: map[string]bool{},
			want: LaneOrphaned, wantAny: true,
		},
		{
			name: "death outranks board drift",
			// A dead holder whose ticket also fell out of In Progress is
			// stuck, not orphaned: the death is the thing to act on and the
			// drift is its consequence.
			report: report("", "", journal.Dead), started: map[string]bool{},
			want: LaneStuck, wantAny: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lane, ok := Classify(tt.report, tt.started)
			if ok != tt.wantAny {
				t.Fatalf("waiting = %v, want %v", ok, tt.wantAny)
			}
			if !ok {
				return
			}
			if lane.Kind != tt.want {
				t.Errorf("kind = %q, want %q", lane.Kind, tt.want)
			}
			if lane.Reason == "" {
				t.Error("no reason; a lane nobody can explain is one nobody clears")
			}
			if lane.Ticket != "WND-9" || lane.RunID != "run-1" {
				t.Errorf("lane = %+v, want the run's own identity", lane)
			}
		})
	}
}

// A parked run reports the reason the run itself recorded, not one this
// package composed.
func TestParkedLaneCarriesTheRecordedReason(t *testing.T) {
	lane, ok := Classify(report(journal.Parked, "CI never reported", journal.Dead), nil)
	if !ok {
		t.Fatal("a parked run is not waiting on anyone")
	}
	if lane.Reason != "CI never reported" {
		t.Errorf("reason = %q, want the recorded one", lane.Reason)
	}
}

// Worst first, and within a kind the one that has been waiting longest.
func TestLanesAreOrderedBySeverityThenAge(t *testing.T) {
	at := func(day int) time.Time { return time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC) }
	b := Build(Snapshot{Lanes: []Lane{
		{Kind: LaneParked, RunID: "d", Since: at(1)},
		{Kind: LaneUnclear, RunID: "c", Since: at(1)},
		{Kind: LaneStuck, RunID: "b", Since: at(2)},
		{Kind: LaneStuck, RunID: "a", Since: at(1)},
	}})

	var got []string
	for _, row := range b.Sections[3].Rows {
		got = append(got, row.Lane.RunID)
	}
	want := []string{"a", "b", "c", "d"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lane order = %v, want %v", got, want)
		}
	}
}

// --- the run store walk --------------------------------------------------

type fakeRuns struct {
	ids     []string
	reports map[string]journal.Report
	errs    map[string]error
}

func (f *fakeRuns) List() ([]string, error) { return f.ids, nil }

func (f *fakeRuns) Inspect(id string) (journal.Report, error) {
	if err := f.errs[id]; err != nil {
		return journal.Report{}, err
	}
	return f.reports[id], nil
}

// A repository with no runs yet has no store on disk, and a cockpit that
// refused to draw for want of one would be useless until the first
// orchestrator shipped.
func TestReadLanesToleratesNoStore(t *testing.T) {
	lanes, err := ReadLanes(nil, nil)
	if err != nil || lanes != nil {
		t.Errorf("ReadLanes(nil) = %v, %v; want no lanes and no error", lanes, err)
	}
}

// An unreadable run is not skipped. It is exactly a thing only a person can
// resolve, and dropping it would hide the one state the journal itself
// calls refused.
func TestReadLanesSurfacesAnUnreadableRun(t *testing.T) {
	runs := &fakeRuns{
		ids:  []string{"broken", "fine"},
		errs: map[string]error{"broken": errors.New("sequence gap at 4")},
		reports: map[string]journal.Report{
			"fine": report(journal.Converged, "green", journal.Dead),
		},
	}
	lanes, err := ReadLanes(runs, []linear.Issue{{Identifier: "WND-9"}})
	if err != nil {
		t.Fatalf("ReadLanes: %v", err)
	}
	if len(lanes) != 1 {
		t.Fatalf("lanes = %v, want just the unreadable one", lanes)
	}
	if lanes[0].Kind != LaneUnclear || lanes[0].RunID != "broken" {
		t.Errorf("lane = %+v, want the broken run reported unclear", lanes[0])
	}
	if lanes[0].Reason == "" {
		t.Error("no reason; the parse error is the only thing that explains this row")
	}
}
