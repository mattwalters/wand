package journal_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

// A record announces the action that is about to happen, so it must be on
// disk — durably, readable by another process — before the method returns.
// A record still in this process's buffers describes a phase that could
// start and then vanish.
func TestStartPhaseJournalsBeforeItReturns(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Close()

	if err := r.StartPhase("implement", 1); err != nil {
		t.Fatalf("StartPhase: %v", err)
	}
	// Read through the store, not the handle: this is what a resume, a
	// sweeper, or a human with `cat` would see at this instant.
	state, err := s.State(r.ID())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state.Phase != "implement" || state.Round != 1 || state.PhaseDone {
		t.Errorf("state = %q round %d done=%v, want an open implement round 1",
			state.Phase, state.Round, state.PhaseDone)
	}
}

func TestPhaseDiscipline(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Close()

	if err := r.EndPhase(nil); err == nil {
		t.Error("EndPhase closed a phase that never started")
	}
	if err := r.StartPhase("", 1); err == nil {
		t.Error("StartPhase accepted a phase with no name")
	}
	if err := r.StartPhase("implement", 1); err != nil {
		t.Fatalf("StartPhase: %v", err)
	}
	// Detail is passed through opaquely — this package knows nothing about
	// workers, and still ends up being where their results are kept.
	detail := struct {
		ExitCode int  `json:"exit_code"`
		TimedOut bool `json:"timed_out"`
	}{ExitCode: 1, TimedOut: true}
	if err := r.EndPhase(detail); err != nil {
		t.Fatalf("EndPhase: %v", err)
	}

	recs, err := s.Records(r.ID())
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	last := recs[len(recs)-1]
	if last.Kind != journal.KindPhaseEnded || last.Phase != "implement" || last.Round != 1 {
		t.Fatalf("last record = %+v, want the end of implement round 1", last)
	}
	if got := string(last.Detail); !strings.Contains(got, `"exit_code":1`) {
		t.Errorf("detail = %s, want the orchestrator's payload passed through", got)
	}
}

// The handoff path is per phase and round so that a file left behind by an
// earlier phase can never be read as this phase's report.
func TestHandoffPathIsPerPhase(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Close()

	before := r.HandoffPath()
	if filepath.Dir(before) != r.ScratchDir() {
		t.Errorf("handoff %s is outside the scratch directory %s", before, r.ScratchDir())
	}
	if err := r.StartPhase("review", 1); err != nil {
		t.Fatalf("StartPhase: %v", err)
	}
	first := r.HandoffPath()
	if err := r.StartPhase("review", 2); err != nil {
		t.Fatalf("StartPhase: %v", err)
	}
	second := r.HandoffPath()

	for _, pair := range [][2]string{{before, first}, {first, second}} {
		if pair[0] == pair[1] {
			t.Errorf("two phases share the handoff path %s", pair[0])
		}
	}
}

// Heartbeat is the periodic renewal a long single phase needs: it moves
// Lease.Renewed without spending a sequence number or leaving any other
// trace in the journal stream.
func TestHeartbeatRenewsTheLeaseWithoutJournaling(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Close()

	if err := r.StartPhase("implement", 1); err != nil {
		t.Fatalf("StartPhase: %v", err)
	}
	before, err := s.Records(r.ID())
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	later := epoch.Add(6 * time.Minute)
	s.Now = func() time.Time { return later }
	if err := r.Heartbeat(); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	after, err := s.Records(r.ID())
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("records = %d, want %d: a heartbeat is not a journal event", len(after), len(before))
	}
	report, err := s.Inspect(r.ID())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !report.Lease.Renewed.Equal(later) {
		t.Errorf("lease renewed = %v, want %v", report.Lease.Renewed, later)
	}
	if report.Lease.Phase != "implement" || report.Lease.Round != 1 {
		t.Errorf("lease phase = %q round %d, want the phase still open", report.Lease.Phase, report.Lease.Round)
	}
}

// A heartbeat after the run ended is refused the same way any other write
// to a finished run is: there is nothing left for it to keep alive.
func TestHeartbeatRefusesAnEndedRun(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Close()

	if err := r.Converged("done"); err != nil {
		t.Fatalf("Converged: %v", err)
	}
	if err := r.Heartbeat(); err == nil {
		t.Error("Heartbeat succeeded on an ended run")
	}
}

func TestTerminalRecordHappensExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(*journal.Run) error
		want journal.Outcome
	}{
		{"converged", func(r *journal.Run) error { return r.Converged("the PR merged") }, journal.Converged},
		{"handed back", func(r *journal.Run) error { return r.HandedBack("review rounds exhausted") }, journal.HandedBack},
		{"parked", func(r *journal.Run) error { return r.Parked("CI is down") }, journal.Parked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, meta := fixture(t)
			r, err := s.Create(meta)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			defer r.Close()

			if err := tc.end(r); err != nil {
				t.Fatalf("ending the run: %v", err)
			}
			if r.Outcome() != tc.want {
				t.Errorf("Outcome = %q, want %q", r.Outcome(), tc.want)
			}
			// A second ending is refused, not written. Two endings on one
			// run means nobody can afterwards say which was true.
			if err := r.Parked("second thoughts"); err == nil {
				t.Error("a second terminal record was accepted")
			}
			if err := r.StartPhase("implement", 1); err == nil {
				t.Error("a phase started after the run ended")
			}
			// Close must not add anything to a run that already ended.
			if err := r.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			state, err := s.State(r.ID())
			if err != nil {
				t.Fatalf("State: %v", err)
			}
			if state.Outcome != tc.want {
				t.Errorf("journal outcome = %q, want %q", state.Outcome, tc.want)
			}
		})
	}
}

func TestTerminalRecordsNeedAReason(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Close()
	for name, end := range map[string]func() error{
		"converged":   func() error { return r.Converged("") },
		"handed back": func() error { return r.HandedBack("  ") },
		"parked":      func() error { return r.Parked("") },
	} {
		if err := end(); err == nil {
			t.Errorf("%s: an ending with no reason on it was accepted", name)
		}
	}
}

// Close is the backstop: every path that reaches neither a decision nor an
// explicit park still leaves exactly one terminal record, and one that
// admits what it is.
func TestCloseParksAnUndecidedRun(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.StartPhase("implement", 1); err != nil {
		t.Fatalf("StartPhase: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	state, err := s.State(r.ID())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state.Outcome != journal.Parked {
		t.Fatalf("outcome = %q, want parked", state.Outcome)
	}
	if state.Reason != journal.ClosedReason {
		t.Errorf("reason = %q, want %q", state.Reason, journal.ClosedReason)
	}
	recs, err := s.Records(r.ID())
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	var endings int
	for _, rec := range recs {
		if rec.Kind == journal.KindEnded {
			endings++
		}
	}
	if endings != 1 {
		t.Errorf("%d terminal records, want exactly 1", endings)
	}
}

// A cause worth quoting is the whole reason this helper exists: "context
// canceled" in a park tells the next reader nothing.
func TestInterruptibleNamesItsCause(t *testing.T) {
	ctx, stop := journal.Interruptible(context.Background())
	stop()
	<-ctx.Done()
	if got := context.Cause(ctx); got != context.Canceled {
		t.Errorf("cause after stop = %v, want context.Canceled", got)
	}
}

func TestReopenRefusesALiveRun(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Close()

	_, err = s.Reopen(r.ID())
	if err == nil {
		t.Fatal("Reopen took a run another writer still holds")
	}
	if !strings.Contains(err.Error(), "one writer") {
		t.Errorf("error %q does not explain the single-writer rule", err)
	}
}

func TestReopenRefusesAFinishedRun(t *testing.T) {
	s, meta := fixture(t)
	r, err := s.Create(meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.Converged("the PR merged"); err != nil {
		t.Fatalf("Converged: %v", err)
	}
	_, err = s.Reopen(r.ID())
	if err == nil {
		t.Fatal("Reopen restarted a run that had already ended")
	}
	if !strings.Contains(err.Error(), "converged") {
		t.Errorf("error %q does not quote the recorded ending", err)
	}
}

func TestReopenUnknownRun(t *testing.T) {
	s, _ := fixture(t)
	if _, err := s.Reopen("WND-7-nope"); err == nil {
		t.Fatal("Reopen invented a run that does not exist")
	}
}
