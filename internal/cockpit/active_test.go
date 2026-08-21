package cockpit

import (
	"errors"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

func activeReport(id, ticket, phase string, round int, started, renewed time.Time, live journal.Liveness) journal.Report {
	return journal.Report{
		State: journal.State{
			Meta:    journal.Meta{ID: id, Ticket: ticket, Verb: "run", Repo: "/repo", Harness: "claude-code"},
			Phase:   phase,
			Round:   round,
			Started: started,
		},
		Lease: journal.Lease{PID: 111, Host: "studio.local", Renewed: renewed},
		Live:  live,
	}
}

// A repository with no run store yet has nothing running on it, and a
// cockpit that refused to draw for want of one would be useless until the
// first orchestrator shipped — the same tolerance [ReadStalled] gives a nil
// store.
func TestActiveRunsToleratesNoStore(t *testing.T) {
	active, err := ActiveRuns(nil)
	if err != nil || active != nil {
		t.Errorf("ActiveRuns(nil) = %v, %v; want none and no error", active, err)
	}
}

// A run that recorded a terminal state is not running any more, whatever
// its lease still says.
func TestActiveRunsSkipsEndedRuns(t *testing.T) {
	at := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	ended := activeReport("done", "WND-1", "implement", 1, at, at, journal.Dead)
	ended.State.Outcome = journal.Converged

	runs := &fakeRuns{
		ids:     []string{"done"},
		reports: map[string]journal.Report{"done": ended},
	}
	active, err := ActiveRuns(runs)
	if err != nil {
		t.Fatalf("ActiveRuns: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("active = %+v, want none: the run already ended", active)
	}
}

// A run [Store.Inspect] cannot read is skipped here, not surfaced: that
// failure is already an unclear stalled run, and this strip is not a second place
// to report corruption.
func TestActiveRunsSkipsUnreadableRuns(t *testing.T) {
	at := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	runs := &fakeRuns{
		ids:  []string{"broken", "fine"},
		errs: map[string]error{"broken": errors.New("sequence gap at 4")},
		reports: map[string]journal.Report{
			"fine": activeReport("fine", "WND-2", "implement", 1, at, at, journal.Alive),
		},
	}
	active, err := ActiveRuns(runs)
	if err != nil {
		t.Fatalf("ActiveRuns: %v", err)
	}
	if len(active) != 1 || active[0].RunID != "fine" {
		t.Errorf("active = %+v, want just the readable run", active)
	}
}

// Oldest first: the run that has been going longest is the one most worth
// noticing.
func TestActiveRunsOrdersByStarted(t *testing.T) {
	early := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	late := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	runs := &fakeRuns{
		ids: []string{"b", "a"},
		reports: map[string]journal.Report{
			"b": activeReport("b", "WND-2", "review", 1, late, late, journal.Alive),
			"a": activeReport("a", "WND-1", "implement", 1, early, early, journal.Alive),
		},
	}
	active, err := ActiveRuns(runs)
	if err != nil {
		t.Fatalf("ActiveRuns: %v", err)
	}
	if len(active) != 2 || active[0].RunID != "a" || active[1].RunID != "b" {
		t.Errorf("active = %+v, want [a b] oldest first", active)
	}
}

// Every field a live-runs panel needs comes straight off the journal and
// the lease — nothing here is invented.
func TestActiveRunsCarriesJournalFacts(t *testing.T) {
	started := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	renewed := time.Date(2026, 3, 1, 9, 12, 0, 0, time.UTC)
	runs := &fakeRuns{
		ids: []string{"r"},
		reports: map[string]journal.Report{
			"r": activeReport("r", "WND-9", "implement", 2, started, renewed, journal.Alive),
		},
	}
	active, err := ActiveRuns(runs)
	if err != nil {
		t.Fatalf("ActiveRuns: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("active = %+v, want one run", active)
	}
	got := active[0]
	if got.Ticket != "WND-9" || got.Verb != "run" || got.Harness != "claude-code" {
		t.Errorf("run = %+v, want the journal's own ticket/verb/harness", got)
	}
	if got.Phase != "implement" || got.Round != 2 {
		t.Errorf("phase = %q round %d, want implement round 2", got.Phase, got.Round)
	}
	if !got.Started.Equal(started) || !got.Heartbeat.Equal(renewed) {
		t.Errorf("started = %v heartbeat = %v, want %v and %v", got.Started, got.Heartbeat, started, renewed)
	}
	if got.Live != journal.Alive {
		t.Errorf("live = %q, want alive", got.Live)
	}
}

// Stale carries the liveness [Store.Inspect] already judged — never a
// second verdict computed from how old the heartbeat looks.
func TestActiveStale(t *testing.T) {
	tests := []struct {
		live journal.Liveness
		want bool
	}{
		{journal.Alive, false},
		{journal.Dead, true},
		{journal.Unknown, true},
	}
	for _, tt := range tests {
		a := Active{Live: tt.live}
		if got := a.Stale(); got != tt.want {
			t.Errorf("Live=%q: Stale() = %v, want %v", tt.live, got, tt.want)
		}
	}
}

func TestActivePhaseLabel(t *testing.T) {
	tests := []struct {
		phase string
		round int
		want  string
	}{
		{"", 0, "no phase yet"},
		{"implement", 0, "implement"},
		{"implement", 2, "implement (round 2)"},
	}
	for _, tt := range tests {
		a := Active{Phase: tt.phase, Round: tt.round}
		if got := a.PhaseLabel(); got != tt.want {
			t.Errorf("phase=%q round=%d: PhaseLabel() = %q, want %q", tt.phase, tt.round, got, tt.want)
		}
	}
}

// A plan-style run picks its harness per phase rather than fixing one for
// the whole run, so an empty Harness is a fact, not a gap this package
// failed to read — and the label says so rather than printing nothing.
func TestActiveHarnessLabel(t *testing.T) {
	if got := (Active{Harness: "codex"}).HarnessLabel(); got != "codex" {
		t.Errorf("HarnessLabel() = %q, want codex", got)
	}
	if got := (Active{}).HarnessLabel(); got != "—" {
		t.Errorf("HarnessLabel() = %q, want the placeholder", got)
	}
}

// Running rides the board unchanged and does not count as waiting: a run
// under active drive is, by definition, not waiting on anyone.
func TestBuildCarriesRunningWithoutCountingItAsWaiting(t *testing.T) {
	b := Build(Snapshot{Team: "WND", Active: []Active{{RunID: "r", Ticket: "WND-9"}}})
	if len(b.Running) != 1 || b.Running[0].RunID != "r" {
		t.Errorf("running = %+v, want the one active run", b.Running)
	}
	if b.Waiting() != 0 {
		t.Errorf("waiting = %d, want 0: nothing running is waiting on a human", b.Waiting())
	}
}
