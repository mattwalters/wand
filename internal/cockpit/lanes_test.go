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

// planReport builds a live plan-verb report, the shape TestClassify's
// plan-verb cases need: report() hardcodes Verb: "run", and adding a verb
// parameter there would touch every existing case for one that needs it.
func planReport(live journal.Liveness) journal.Report {
	r := report("", "", live)
	r.State.Meta.Verb = "plan"
	return r
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
			name:   "a live plan run claimed into In Planning needs nobody",
			report: planReport(journal.Alive), started: started,
			// `wand plan` claims In Planning (a started status, WND-79)
			// before it does anything else, so a live plan run whose
			// ticket is in the started set reads exactly like a live build
			// run's does: nobody has to resolve it.
		},
		{
			name:   "a live plan run whose ticket drifted off In Planning is orphaned",
			report: planReport(journal.Alive), started: map[string]bool{},
			// The WND-66 carve-out this used to be exempt under is gone:
			// the planning track has a started state of its own now, so a
			// live plan run the board does not claim is genuine drift, the
			// same as it would be for a build run.
			want: LaneOrphaned, wantAny: true,
		},
		{
			name:   "a plan run with a dead holder is still stuck",
			report: planReport(journal.Dead), started: map[string]bool{},
			want: LaneStuck, wantAny: true,
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
	for _, row := range b.Sections[4].Rows {
		got = append(got, row.Lane.RunID)
	}
	want := []string{"a", "b", "c", "d"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lane order = %v, want %v", got, want)
		}
	}
}

// --- supersession ----------------------------------------------------------

func reportFor(ticket string, outcome journal.Outcome, updated time.Time) journal.Report {
	return journal.Report{
		State: journal.State{
			Meta:    journal.Meta{ID: ticket + "-run", Ticket: ticket, Verb: "run", Repo: "/repo"},
			Outcome: outcome,
			Updated: updated,
		},
		Live: journal.Dead,
	}
}

func TestReconcileDropsAParkResolvedByALaterRun(t *testing.T) {
	early := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

	parked := Lane{Kind: LaneParked, Ticket: "WND-37", RunID: "old", Since: early}
	reports := []journal.Report{
		reportFor("WND-37", journal.Parked, early),
		reportFor("WND-37", journal.HandedBack, late),
	}

	got := Reconcile([]Lane{parked}, reports, labelled("WND-37"))
	if len(got) != 0 {
		t.Errorf("lanes = %+v, want the park dropped: a later run for the same ticket handed back cleanly", got)
	}
}

func TestReconcileKeepsAParkNothingLaterResolved(t *testing.T) {
	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	parked := Lane{Kind: LaneParked, Ticket: "WND-59", RunID: "still-parked", Since: at}
	reports := []journal.Report{reportFor("WND-59", journal.Parked, at)}

	got := Reconcile([]Lane{parked}, reports, labelled("WND-59"))
	if len(got) != 1 {
		t.Errorf("lanes = %+v, want the park kept: nothing later resolved this ticket", got)
	}
}

func TestReconcileIgnoresAnEarlierResolution(t *testing.T) {
	early := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

	// The park happened *after* the handoff — a fresh problem, not a stale
	// record of one already fixed.
	parked := Lane{Kind: LaneParked, Ticket: "WND-11", RunID: "new-park", Since: late}
	reports := []journal.Report{
		reportFor("WND-11", journal.HandedBack, early),
		reportFor("WND-11", journal.Parked, late),
	}

	got := Reconcile([]Lane{parked}, reports, labelled("WND-11"))
	if len(got) != 1 {
		t.Errorf("lanes = %+v, want the park kept: it postdates the only resolution on record", got)
	}
}

// A ticket parked more than once has one current park and the rest history:
// the cockpit shows the newer reason, not the one that happened to be
// classified first.
func TestReconcileCollapsesRepeatedParksToTheNewestReason(t *testing.T) {
	early := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

	stale := Lane{Kind: LaneParked, Ticket: "WND-66", RunID: "old-park", Since: early, Reason: "citation gate"}
	current := Lane{Kind: LaneParked, Ticket: "WND-66", RunID: "new-park", Since: late, Reason: "timeout"}

	got := Reconcile([]Lane{stale, current}, nil, labelled("WND-66"))
	if len(got) != 1 {
		t.Fatalf("lanes = %+v, want one: repeated parks on the same ticket collapse", got)
	}
	if got[0].RunID != "new-park" || got[0].Reason != "timeout" {
		t.Errorf("lane = %+v, want the newer park's run and reason", got[0])
	}
}

// The collapse is per-ticket, not a blanket dedup of every parked lane: two
// different tickets each parked once must both survive.
func TestReconcileKeepsParksForDifferentTickets(t *testing.T) {
	early := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

	older := Lane{Kind: LaneParked, Ticket: "WND-1", RunID: "a", Since: early}
	newer := Lane{Kind: LaneParked, Ticket: "WND-2", RunID: "b", Since: late}

	got := Reconcile([]Lane{newer, older}, nil, labelled("WND-1", "WND-2"))
	if len(got) != 2 {
		t.Fatalf("lanes = %+v, want both: different tickets, not a blanket dedup", got)
	}

	rows := laneRows(got)
	if len(rows) != 2 || rows[0].Lane.RunID != "a" || rows[1].Lane.RunID != "b" {
		t.Errorf("rows = %+v, want oldest-first across tickets", rows)
	}
}

func TestReconcileLeavesOtherKindsAlone(t *testing.T) {
	early := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

	stuck := Lane{Kind: LaneStuck, Ticket: "WND-11", RunID: "stuck", Since: early}
	reports := []journal.Report{reportFor("WND-11", journal.Converged, late)}

	got := Reconcile([]Lane{stuck}, reports, nil)
	if len(got) != 1 || got[0].Kind != LaneStuck {
		t.Errorf("lanes = %+v, want the stuck lane kept: a later run finishing does not explain away a live process misbehaving now", got)
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
	lanes, err := ReadLanes(nil, nil, nil)
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
	lanes, err := ReadLanes(runs, []linear.Issue{{Identifier: "WND-9"}}, nil)
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

// The scenario off the live board: a ticket parked more than once, then a
// later run for the same ticket handed back cleanly. None of the parks
// should survive the walk.
func TestReadLanesSuppressesParksALaterRunResolved(t *testing.T) {
	early := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)

	runs := &fakeRuns{
		ids: []string{"park-1", "park-2", "resolved"},
		reports: map[string]journal.Report{
			"park-1":   reportFor("WND-37", journal.Parked, early),
			"park-2":   reportFor("WND-37", journal.Parked, mid),
			"resolved": reportFor("WND-37", journal.HandedBack, late),
		},
	}

	lanes, err := ReadLanes(runs, nil, []linear.Issue{{Identifier: "WND-37"}})
	if err != nil {
		t.Fatalf("ReadLanes: %v", err)
	}
	if len(lanes) != 0 {
		t.Errorf("lanes = %+v, want none: both parks predate the run that handed back", lanes)
	}
}

// labelled is the set of tickets presently carrying the parked label.
func labelled(tickets ...string) map[string]bool {
	m := make(map[string]bool, len(tickets))
	for _, t := range tickets {
		m[t] = true
	}
	return m
}

// WND-85. A park is resolved by a person clearing the label, and the lane
// has to go when they do. The journal is append-only, so the run that
// parked says so forever; deriving the lane from it alone meant clearing
// the label changed nothing anyone could see — the lane returned on the
// next read, and a ticket that reached Done kept a lane no action could
// ever shift.
func TestReconcileDropsAParkWhoseLabelAPersonCleared(t *testing.T) {
	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	parked := Lane{Kind: LaneParked, Ticket: "WND-78", RunID: "cleared", Since: at}
	reports := []journal.Report{reportFor("WND-78", journal.Parked, at)}

	// Nothing later resolved it and it is still parked in the journal —
	// the only thing that changed is that a person cleared the label.
	got := Reconcile([]Lane{parked}, reports, labelled())
	if len(got) != 0 {
		t.Errorf("lanes = %+v, want the park dropped: a person cleared the label", got)
	}
}

// A run that works no ticket — pm — can carry no label, so the label can
// never be the thing that retires its lane.
func TestReconcileKeepsAParkedRunWithNoTicket(t *testing.T) {
	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	parked := Lane{Kind: LaneParked, Ticket: "", RunID: "pm-abc", Since: at}

	got := Reconcile([]Lane{parked}, nil, labelled())
	if len(got) != 1 {
		t.Errorf("lanes = %+v, want the park kept: no ticket means no label to clear", got)
	}
}
