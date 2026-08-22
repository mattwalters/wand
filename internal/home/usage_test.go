package home

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/ledger"
)

func usageDetail(t *testing.T, tokensIn, tokensOut *int64) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(struct {
		TokensIn  *int64 `json:"tokens_in,omitempty"`
		TokensOut *int64 `json:"tokens_out,omitempty"`
	}{tokensIn, tokensOut})
	if err != nil {
		t.Fatalf("marshaling detail: %v", err)
	}
	return raw
}

func usageI64(v int64) *int64 { return &v }

// A run store with no history renders the panel's honest "nothing
// recorded" state rather than a row of zeroes — the same tolerance
// [ActiveRuns] and [ReadStalled] give a nil or empty store.
func TestBuildUsageOnEmptyHistory(t *testing.T) {
	u, err := BuildUsage(&fakeRuns{}, time.Time{})
	if err != nil {
		t.Fatalf("BuildUsage: %v", err)
	}
	if !u.Empty() {
		t.Errorf("Usage = %+v, want Empty() with no runs recorded", u)
	}
}

// BuildUsage(nil, ...) tolerates a repository with no run store on disk yet,
// the same way ActiveRuns(nil) and ReadStalled(nil, ...) do.
func TestBuildUsageToleratesNoStore(t *testing.T) {
	u, err := BuildUsage(nil, time.Time{})
	if err != nil {
		t.Fatalf("BuildUsage(nil): %v", err)
	}
	if !u.Empty() {
		t.Errorf("Usage = %+v, want Empty() with no store", u)
	}
}

// A mix of harnesses and outcomes folds into the same three ledger
// aggregations wand stats itself calls — BuildUsage is a thin wire-up, and
// this proves it hands ledger.Walk's result to all three rather than
// silently dropping one.
func TestBuildUsageOverMixedHarnessesAndOutcomes(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC)

	claude := []journal.Record{
		{Seq: 1, At: t1, Kind: journal.KindStarted, Run: &journal.Meta{ID: "r1", Ticket: "WND-1", Verb: "run", Repo: "/repo", Harness: "claude-code"}},
		{Seq: 2, At: t1, Kind: journal.KindPhaseStarted, Phase: "implement", Round: 1},
		{Seq: 3, At: t1, Kind: journal.KindPhaseEnded, Phase: "implement", Round: 1, Detail: usageDetail(t, usageI64(100), usageI64(50))},
		{Seq: 4, At: t1, Kind: journal.KindEnded, Outcome: journal.Converged, Reason: "done"},
	}
	codex := []journal.Record{
		{Seq: 1, At: t1, Kind: journal.KindStarted, Run: &journal.Meta{ID: "r2", Ticket: "WND-2", Verb: "run", Repo: "/repo", Harness: "codex"}},
		{Seq: 2, At: t1, Kind: journal.KindPhaseStarted, Phase: "implement", Round: 1},
		{Seq: 3, At: t1, Kind: journal.KindPhaseEnded, Phase: "implement", Round: 1, Detail: usageDetail(t, usageI64(20), usageI64(8))},
		{Seq: 4, At: t1, Kind: journal.KindEnded, Outcome: journal.Parked, Reason: "worktree dirty"},
	}
	// Before the window: must not contribute to any of the three
	// aggregations.
	early := []journal.Record{
		{Seq: 1, At: t0, Kind: journal.KindStarted, Run: &journal.Meta{ID: "r3", Ticket: "WND-3", Verb: "run", Repo: "/repo", Harness: "claude-code"}},
		{Seq: 2, At: t0, Kind: journal.KindPhaseStarted, Phase: "implement", Round: 1},
		{Seq: 3, At: t0, Kind: journal.KindPhaseEnded, Phase: "implement", Round: 1, Detail: usageDetail(t, usageI64(999), usageI64(999))},
		{Seq: 4, At: t0, Kind: journal.KindEnded, Outcome: journal.Converged, Reason: "done"},
	}

	runs := &fakeRuns{
		ids: []string{"r1", "r2", "r3"},
		records: map[string][]journal.Record{
			"r1": claude, "r2": codex, "r3": early,
		},
	}

	since := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
	u, err := BuildUsage(runs, since)
	if err != nil {
		t.Fatalf("BuildUsage: %v", err)
	}
	if u.Empty() {
		t.Fatalf("Usage = %+v, want not Empty() with two runs in the window", u)
	}
	if !u.Since.Equal(since) {
		t.Errorf("Since = %v, want %v", u.Since, since)
	}

	if len(u.Velocity) != 1 || !u.Velocity[0].Day.Equal(t1.UTC().Truncate(24*time.Hour)) {
		t.Fatalf("Velocity = %+v, want one bucket on 2026-03-05", u.Velocity)
	}
	if got := *u.Velocity[0].TokensIn; got != 120 {
		t.Errorf("Velocity tokens in = %d, want 120 (100+20, r3 excluded by the cutoff)", got)
	}

	if want := (ledger.OutcomeCounts{Converged: 1, Parked: 1}); u.Outcomes != want {
		t.Errorf("Outcomes = %+v, want %+v", u.Outcomes, want)
	}

	if len(u.Harness) != 2 {
		t.Fatalf("Harness = %+v, want claude-code and codex", u.Harness)
	}
	if u.Harness[0].Harness != "claude-code" || *u.Harness[0].TokensIn != 100 {
		t.Errorf("Harness[0] = %+v, want claude-code/100", u.Harness[0])
	}
	if u.Harness[1].Harness != "codex" || *u.Harness[1].TokensIn != 20 {
		t.Errorf("Harness[1] = %+v, want codex/20", u.Harness[1])
	}
}
