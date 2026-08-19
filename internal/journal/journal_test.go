package journal_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

var epoch = time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

// stream builds a well-formed record slice, numbering and timestamping it,
// so a test states only the shape it is about.
func stream(recs ...journal.Record) []journal.Record {
	for i := range recs {
		recs[i].Seq = i + 1
		recs[i].At = epoch.Add(time.Duration(i) * time.Minute)
	}
	return recs
}

func opening() journal.Record {
	return journal.Record{
		Kind: journal.KindStarted,
		Run:  &journal.Meta{ID: "WND-7-x", Ticket: "WND-7", Verb: "run", Repo: "/repo"},
	}
}

func TestReplayReportsTheResumePoint(t *testing.T) {
	recs := stream(
		opening(),
		journal.Record{Kind: journal.KindPhaseStarted, Phase: "implement", Round: 1},
		journal.Record{Kind: journal.KindPhaseEnded, Phase: "implement", Round: 1},
		journal.Record{Kind: journal.KindPhaseStarted, Phase: "review", Round: 1},
	)
	state, err := journal.Replay(recs)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if state.Meta.Ticket != "WND-7" {
		t.Errorf("ticket = %q, want WND-7", state.Meta.Ticket)
	}
	if state.Phase != "review" || state.Round != 1 {
		t.Errorf("resume point = %q round %d, want review round 1", state.Phase, state.Round)
	}
	// The record announces the phase before it runs, so an unfinished
	// phase is the normal state of a run that died — and the resume point.
	if state.PhaseDone {
		t.Error("PhaseDone is true for a phase that never recorded its end")
	}
	if state.Ended() {
		t.Error("Ended is true for a run with no terminal record")
	}
	if ok, why := state.Resumable(); !ok {
		t.Errorf("Resumable = false (%s), want true", why)
	}
	if state.Seq != 4 {
		t.Errorf("Seq = %d, want 4", state.Seq)
	}
	if !state.Updated.Equal(epoch.Add(3 * time.Minute)) {
		t.Errorf("Updated = %v, want the last record's time", state.Updated)
	}
}

func TestReplayFinishedPhaseIsNotReRun(t *testing.T) {
	state, err := journal.Replay(stream(
		opening(),
		journal.Record{Kind: journal.KindPhaseStarted, Phase: "implement", Round: 2},
		journal.Record{Kind: journal.KindPhaseEnded, Phase: "implement", Round: 2},
	))
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !state.PhaseDone {
		t.Error("PhaseDone = false after the phase recorded its end")
	}
	if state.Phase != "implement" || state.Round != 2 {
		t.Errorf("phase = %q round %d, want implement round 2", state.Phase, state.Round)
	}
}

// A resume happens because the holder died mid-phase. Nothing ever closes
// that phase, so replay must not report it as finished — an orchestrator
// reading PhaseDone would advance past work that never happened.
func TestReplayResumeLeavesTheInterruptedPhaseOpen(t *testing.T) {
	state, err := journal.Replay(stream(
		opening(),
		journal.Record{Kind: journal.KindPhaseStarted, Phase: "implement", Round: 1},
		journal.Record{Kind: journal.KindPhaseEnded, Phase: "implement", Round: 1},
		journal.Record{Kind: journal.KindResumed, Reason: "resumed"},
	))
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if state.PhaseDone {
		t.Error("PhaseDone survived a resume; the phase after a resume is always re-entered")
	}
	if state.Resumes != 1 {
		t.Errorf("Resumes = %d, want 1", state.Resumes)
	}
}

func TestReplayEndedRunIsNotResumable(t *testing.T) {
	state, err := journal.Replay(stream(
		opening(),
		journal.Record{Kind: journal.KindEnded, Outcome: journal.HandedBack, Reason: "review rounds exhausted"},
	))
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !state.Ended() || state.Outcome != journal.HandedBack {
		t.Fatalf("outcome = %q, want handed_back", state.Outcome)
	}
	ok, why := state.Resumable()
	if ok {
		t.Fatal("a run that already ended reported itself resumable")
	}
	if !strings.Contains(why, "handed_back") || !strings.Contains(why, "review rounds exhausted") {
		t.Errorf("refusal does not quote the recorded ending: %s", why)
	}
}

// Replay refuses anything it cannot reason over. A wrong resume decision
// costs a second writer on a live ticket; a refusal costs a human two
// minutes.
func TestReplayRefusesUnreadableStreams(t *testing.T) {
	cases := map[string]struct {
		recs []journal.Record
		want string
	}{
		"empty": {
			recs: nil,
			want: "never started",
		},
		"does not open with run.started": {
			recs: stream(journal.Record{Kind: journal.KindNote, Reason: "hello"}),
			want: "the first record is",
		},
		"opened twice": {
			recs: stream(opening(), opening()),
			want: "a second time",
		},
		"opening record carries no metadata": {
			recs: stream(journal.Record{Kind: journal.KindStarted}),
			want: "no run metadata",
		},
		"sequence gap": {
			recs: []journal.Record{
				{Seq: 1, Kind: journal.KindStarted, Run: &journal.Meta{Ticket: "WND-7"}},
				{Seq: 3, Kind: journal.KindNote},
			},
			want: "out of sequence",
		},
		"record after the terminal": {
			recs: stream(
				opening(),
				journal.Record{Kind: journal.KindEnded, Outcome: journal.Converged, Reason: "merged"},
				journal.Record{Kind: journal.KindNote, Reason: "and then some"},
			),
			want: "a run ends exactly once",
		},
		"unknown kind": {
			recs: stream(opening(), journal.Record{Kind: "phase.retried"}),
			want: "newer wand",
		},
		"unknown outcome": {
			recs: stream(opening(), journal.Record{Kind: journal.KindEnded, Outcome: "gave_up"}),
			want: "unknown outcome",
		},
		"unnamed phase": {
			recs: stream(opening(), journal.Record{Kind: journal.KindPhaseStarted}),
			want: "no name",
		},
		"phase ended that never started": {
			recs: stream(opening(), journal.Record{Kind: journal.KindPhaseEnded, Phase: "implement"}),
			want: "never started",
		},
		"phase ended out of step": {
			recs: stream(
				opening(),
				journal.Record{Kind: journal.KindPhaseStarted, Phase: "implement", Round: 1},
				journal.Record{Kind: journal.KindPhaseEnded, Phase: "review", Round: 1},
			),
			want: "while",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := journal.Replay(tc.recs)
			if err == nil {
				t.Fatal("Replay accepted a stream it cannot reason over")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// Interleaved phases are a fact about a sloppy orchestrator, not a
// corrupted stream: the last phase started is still the resume point, and
// refusing to replay would strand a recoverable run.
func TestReplayToleratesAnUnclosedPhase(t *testing.T) {
	state, err := journal.Replay(stream(
		opening(),
		journal.Record{Kind: journal.KindPhaseStarted, Phase: "implement", Round: 1},
		journal.Record{Kind: journal.KindPhaseStarted, Phase: "review", Round: 1},
	))
	if err != nil {
		t.Fatalf("Replay refused a readable stream: %v", err)
	}
	if state.Phase != "review" {
		t.Errorf("phase = %q, want review", state.Phase)
	}
}
