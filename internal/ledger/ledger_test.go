package ledger

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

// fakeRuns is an in-memory Runs a test can hold without a filesystem —
// mirrors internal/cockpit/active_test.go's fakeRuns.
type fakeRuns struct {
	ids     []string
	records map[string][]journal.Record
	errs    map[string]error
}

func (f *fakeRuns) List() ([]string, error) { return f.ids, nil }

func (f *fakeRuns) Records(id string) ([]journal.Record, error) {
	if err, ok := f.errs[id]; ok {
		return nil, err
	}
	return f.records[id], nil
}

func detail(t *testing.T, tokensIn, tokensOut *int64, wallClock string, attempt int) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(struct {
		TokensIn  *int64 `json:"tokens_in,omitempty"`
		TokensOut *int64 `json:"tokens_out,omitempty"`
		WallClock string `json:"wall_clock,omitempty"`
		Attempt   int    `json:"attempt,omitempty"`
	}{tokensIn, tokensOut, wallClock, attempt})
	if err != nil {
		t.Fatalf("marshaling detail: %v", err)
	}
	return raw
}

func i64(v int64) *int64 { return &v }

// A store with no runs, or a nil Runs, has nothing to report — the same
// tolerance internal/cockpit.ActiveRuns gives a repository with no run
// store yet.
func TestWalkToleratesNoStore(t *testing.T) {
	result, err := Walk(nil)
	if err != nil || result.Runs != nil || result.Skipped != nil {
		t.Errorf("Walk(nil) = %+v, %v; want a zero result and no error", result, err)
	}
}

// A run whose journal fails to read or replay is skipped, not fatal to the
// whole walk.
func TestWalkSkipsUnreadableRuns(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	good := []journal.Record{
		{Seq: 1, At: t0, Kind: journal.KindStarted, Run: &journal.Meta{ID: "fine", Ticket: "WND-1", Verb: "run", Repo: "/repo", Harness: "claude-code"}},
		{Seq: 2, At: t0, Kind: journal.KindEnded, Outcome: journal.Converged, Reason: "done"},
	}
	runs := &fakeRuns{
		ids:     []string{"broken", "unreplayable", "fine"},
		records: map[string][]journal.Record{"fine": good, "unreplayable": {{Seq: 2, Kind: journal.KindStarted}}},
		errs:    map[string]error{"broken": errors.New("torn line")},
	}
	result, err := Walk(runs)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].ID != "fine" {
		t.Errorf("Runs = %+v, want just the readable run", result.Runs)
	}
	if len(result.Skipped) != 2 {
		t.Errorf("Skipped = %v, want both broken and unreplayable", result.Skipped)
	}
}

// A retried phase — a transient failure at attempt 1, success at attempt 2,
// both at the same round — folds into one PhaseRound: the round happened
// once, but both attempts' tokens and wall-clock count toward spend.
func TestWalkFoldsRetriedAttemptsIntoOneRound(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	recs := []journal.Record{
		{Seq: 1, At: t0, Kind: journal.KindStarted, Run: &journal.Meta{ID: "r1", Ticket: "WND-1", Verb: "run", Repo: "/repo", Harness: "claude-code"}},
		{Seq: 2, At: t0, Kind: journal.KindPhaseStarted, Phase: "review", Round: 1},
		{Seq: 3, At: t0.Add(30 * time.Second), Kind: journal.KindPhaseEnded, Phase: "review", Round: 1,
			Detail: detail(t, i64(100), i64(50), "30s", 1)},
		{Seq: 4, At: t0.Add(30 * time.Second), Kind: journal.KindPhaseStarted, Phase: "review", Round: 1},
		{Seq: 5, At: t1, Kind: journal.KindPhaseEnded, Phase: "review", Round: 1,
			Detail: detail(t, i64(200), i64(80), "1m0s", 2)},
		{Seq: 6, At: t1, Kind: journal.KindEnded, Outcome: journal.Converged, Reason: "done"},
	}
	runs := &fakeRuns{ids: []string{"r1"}, records: map[string][]journal.Record{"r1": recs}}
	result, err := Walk(runs)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(result.Runs) != 1 {
		t.Fatalf("Runs = %+v, want one", result.Runs)
	}
	rounds := result.Runs[0].Rounds
	if len(rounds) != 1 {
		t.Fatalf("Rounds = %+v, want one folded round, not one per attempt", rounds)
	}
	g := rounds[0]
	if g.Phase != "review" || g.Round != 1 {
		t.Errorf("round = %+v, want review round 1", g)
	}
	if g.TokensIn == nil || *g.TokensIn != 300 {
		t.Errorf("TokensIn = %v, want 300 (100+200 across both attempts)", g.TokensIn)
	}
	if g.TokensOut == nil || *g.TokensOut != 130 {
		t.Errorf("TokensOut = %v, want 130 (50+80 across both attempts)", g.TokensOut)
	}
	if g.WallClock != 90*time.Second {
		t.Errorf("WallClock = %v, want 1m30s (30s+1m across both attempts)", g.WallClock)
	}
}

// Tokens present on one record and absent (both nil) on another must not
// collapse to a reported zero: absent stays absent unless something
// actually reported a number.
func TestWalkKeepsAbsentTokensDistinctFromZero(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	recs := []journal.Record{
		{Seq: 1, At: t0, Kind: journal.KindStarted, Run: &journal.Meta{ID: "r1", Ticket: "WND-1", Verb: "run", Repo: "/repo", Harness: "codex"}},
		{Seq: 2, At: t0, Kind: journal.KindPhaseStarted, Phase: "implement", Round: 1},
		{Seq: 3, At: t0, Kind: journal.KindPhaseEnded, Phase: "implement", Round: 1, Detail: detail(t, nil, nil, "5s", 1)},
		{Seq: 4, At: t0, Kind: journal.KindEnded, Outcome: journal.HandedBack, Reason: "needs a call"},
	}
	runs := &fakeRuns{ids: []string{"r1"}, records: map[string][]journal.Record{"r1": recs}}
	result, err := Walk(runs)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	g := result.Runs[0].Rounds[0]
	if g.TokensIn != nil || g.TokensOut != nil {
		t.Errorf("tokens = %v/%v, want both nil: the harness reported no usage", g.TokensIn, g.TokensOut)
	}
}
