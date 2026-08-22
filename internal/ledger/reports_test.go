package ledger

import (
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

func run(id, ticket, verb, harness string, outcome journal.Outcome, rounds ...PhaseRound) RunSummary {
	return RunSummary{ID: id, Ticket: ticket, Verb: verb, Harness: harness, Outcome: outcome, Rounds: rounds}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestVelocityBucketsByDayOldestFirst(t *testing.T) {
	d1 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 3, 2, 23, 0, 0, 0, time.UTC)
	runs := []RunSummary{
		run("r1", "WND-1", "run", "claude-code", journal.Converged,
			PhaseRound{Phase: "implement", Round: 1, TokensIn: i64(100), TokensOut: i64(50), At: d2},
			PhaseRound{Phase: "review", Round: 1, TokensIn: i64(10), TokensOut: i64(5), At: d1},
		),
	}
	buckets := Velocity(runs, time.Time{})
	if len(buckets) != 2 {
		t.Fatalf("buckets = %+v, want 2 days", buckets)
	}
	if !buckets[0].Day.Equal(day(2026, 3, 1)) || !buckets[1].Day.Equal(day(2026, 3, 2)) {
		t.Errorf("buckets not oldest-first: %+v", buckets)
	}
	if *buckets[1].TokensIn != 100 || *buckets[1].TokensOut != 50 {
		t.Errorf("2026-03-02 bucket = %+v, want 100/50", buckets[1])
	}
}

// --since excludes phase-rounds that closed before the cutoff.
func TestVelocitySinceCutoff(t *testing.T) {
	early := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	late := time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC)
	runs := []RunSummary{
		run("r1", "WND-1", "run", "claude-code", journal.Converged,
			PhaseRound{Phase: "implement", Round: 1, TokensIn: i64(1), At: early},
			PhaseRound{Phase: "review", Round: 1, TokensIn: i64(2), At: late},
		),
	}
	cutoff := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
	buckets := Velocity(runs, cutoff)
	if len(buckets) != 1 || !buckets[0].Day.Equal(day(2026, 3, 5)) {
		t.Errorf("buckets = %+v, want only the 2026-03-05 bucket past the cutoff", buckets)
	}
}

// Two runs against one ticket sum their tokens and wall-clock, and both
// runs' outcomes are listed.
func TestTicketsSumsAcrossRuns(t *testing.T) {
	runs := []RunSummary{
		run("r1", "WND-7", "plan", "claude-code", journal.HandedBack,
			PhaseRound{Phase: "scout", Round: 1, TokensIn: i64(10), TokensOut: i64(5), WallClock: 2 * time.Minute}),
		run("r2", "WND-7", "run", "codex", journal.Converged,
			PhaseRound{Phase: "implement", Round: 1, TokensIn: i64(20), TokensOut: i64(8), WallClock: 3 * time.Minute}),
	}
	tickets := Tickets(runs)
	if len(tickets) != 1 {
		t.Fatalf("tickets = %+v, want one", tickets)
	}
	ts := tickets[0]
	if ts.Ticket != "WND-7" {
		t.Errorf("ticket = %q, want WND-7", ts.Ticket)
	}
	if *ts.TokensIn != 30 || *ts.TokensOut != 13 {
		t.Errorf("tokens = %v/%v, want 30/13", ts.TokensIn, ts.TokensOut)
	}
	if ts.WallClock != 5*time.Minute {
		t.Errorf("wall clock = %v, want 5m", ts.WallClock)
	}
	if len(ts.Runs) != 2 {
		t.Fatalf("runs = %+v, want both listed", ts.Runs)
	}
}

func TestTicketsSortsNumerically(t *testing.T) {
	runs := []RunSummary{
		run("r1", "WND-10", "run", "claude-code", journal.Converged),
		run("r2", "WND-9", "run", "claude-code", journal.Converged),
		run("r3", "WND-2", "run", "claude-code", journal.Converged),
	}
	tickets := Tickets(runs)
	got := []string{tickets[0].Ticket, tickets[1].Ticket, tickets[2].Ticket}
	want := []string{"WND-2", "WND-9", "WND-10"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Tickets order = %v, want %v (numeric, not lexical)", got, want)
			break
		}
	}
}

// A phase retried across two attempts folds into one PhaseRound upstream
// (see ledger_test.go), so HarnessPhase's occurrence tally — and by
// extension the review-round count, since "review" is just the row where
// Phase == "review" — must count that one round, not two records.
func TestHarnessPhaseCountsDistinctRoundsNotRecords(t *testing.T) {
	runs := []RunSummary{
		run("r1", "WND-1", "run", "claude-code", journal.Converged,
			PhaseRound{Phase: "review", Round: 1, TokensIn: i64(300), TokensOut: i64(130), WallClock: 90 * time.Second}),
	}
	rows := HarnessPhase(runs)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one", rows)
	}
	if rows[0].Rounds != 1 {
		t.Errorf("Rounds = %d, want 1 (one round, whatever the attempt count that closed it)", rows[0].Rounds)
	}
}

// HarnessPhase orders by phase pipeline position then harness name — never
// by a "best" metric — so the table cannot be read as a leaderboard.
func TestHarnessPhaseOrdersByPhaseThenHarnessNotByMetric(t *testing.T) {
	runs := []RunSummary{
		// zulu's review round moves a huge number of tokens; if sorting ever
		// regresses to ranking by a metric, this row would float to the top.
		run("r1", "WND-1", "run", "zulu", journal.Converged,
			PhaseRound{Phase: "review", Round: 1, TokensIn: i64(999999)}),
		run("r2", "WND-2", "run", "alpha", journal.Converged,
			PhaseRound{Phase: "review", Round: 1, TokensIn: i64(1)}),
		run("r3", "WND-3", "run", "alpha", journal.Converged,
			PhaseRound{Phase: "implement", Round: 1, TokensIn: i64(1)}),
	}
	rows := HarnessPhase(runs)
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want three", rows)
	}
	// implement precedes review in phasePipelineOrder; within review, alpha
	// precedes zulu alphabetically, regardless of token count.
	got := []string{rows[0].Phase + "/" + rows[0].Harness, rows[1].Phase + "/" + rows[1].Harness, rows[2].Phase + "/" + rows[2].Harness}
	want := []string{"implement/alpha", "review/alpha", "review/zulu"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("order = %v, want %v", got, want)
			break
		}
	}
}

// Two runs on two different harnesses, one converged and one parked each,
// must land in separate harness rows with the run/verb axis intact — a
// regression to dropping the harness axis would merge them into one count.
func TestConvergenceBreaksOutByHarnessAndVerb(t *testing.T) {
	runs := []RunSummary{
		run("r1", "WND-1", "run", "claude-code", journal.Converged),
		run("r2", "WND-2", "run", "codex", journal.Parked),
		run("r3", "WND-3", "plan", "claude-code", journal.Converged),
		run("r4", "WND-4", "run", "claude-code", journal.Parked),
		// Still in progress: no outcome yet, must not count.
		run("r5", "WND-5", "run", "claude-code", ""),
	}
	rows := Convergence(runs)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two harness rows (claude-code, codex)", rows)
	}
	if rows[0].Harness != "claude-code" || rows[1].Harness != "codex" {
		t.Errorf("harness order = %+v, want alphabetical", rows)
	}
	cc := rows[0].ByVerb["run"]
	if cc.Converged != 1 || cc.Total != 2 {
		t.Errorf("claude-code/run = %+v, want 1/2 (r5 excluded, still in progress)", cc)
	}
	ccPlan := rows[0].ByVerb["plan"]
	if ccPlan.Converged != 1 || ccPlan.Total != 1 {
		t.Errorf("claude-code/plan = %+v, want 1/1", ccPlan)
	}
	codex := rows[1].ByVerb["run"]
	if codex.Converged != 0 || codex.Total != 1 {
		t.Errorf("codex/run = %+v, want 0/1", codex)
	}
}

// runEnded is [run] plus Updated, for the two windowed-by-Updated report
// functions below — CountOutcomes filters on it the way Velocity filters
// PhaseRound.At, so the tests need to set it explicitly rather than relying
// on the zero value [run] leaves it at.
func runEnded(id, ticket, verb, harness string, outcome journal.Outcome, updated time.Time, rounds ...PhaseRound) RunSummary {
	rs := run(id, ticket, verb, harness, outcome, rounds...)
	rs.Updated = updated
	return rs
}

func TestCountOutcomes(t *testing.T) {
	early := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	late := time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		runs  []RunSummary
		since time.Time
		want  OutcomeCounts
	}{
		{name: "empty input", runs: nil, since: time.Time{}, want: OutcomeCounts{}},
		{
			name: "since cutoff excludes everything",
			runs: []RunSummary{
				runEnded("r1", "WND-1", "run", "claude-code", journal.Converged, early),
				runEnded("r2", "WND-2", "run", "claude-code", journal.Parked, early),
			},
			since: late.Add(time.Hour),
			want:  OutcomeCounts{},
		},
		{
			name: "mixed outcomes past and before the cutoff",
			runs: []RunSummary{
				runEnded("r1", "WND-1", "run", "claude-code", journal.Converged, late),
				runEnded("r2", "WND-2", "run", "codex", journal.Parked, late),
				runEnded("r3", "WND-3", "run", "claude-code", journal.HandedBack, late),
				runEnded("r4", "WND-4", "run", "claude-code", journal.Converged, late),
				// Before the cutoff: must not count.
				runEnded("r5", "WND-5", "run", "claude-code", journal.Converged, early),
				// Still in progress: no outcome yet, must not count regardless of Updated.
				runEnded("r6", "WND-6", "run", "claude-code", "", late),
			},
			since: cutoff,
			want:  OutcomeCounts{Converged: 2, Parked: 1, HandedBack: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CountOutcomes(tt.runs, tt.since); got != tt.want {
				t.Errorf("CountOutcomes = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestHarnessTokens(t *testing.T) {
	early := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	late := time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		runs  []RunSummary
		since time.Time
		want  []HarnessTotal
	}{
		{name: "empty input", runs: nil, since: time.Time{}, want: nil},
		{
			name: "since cutoff excludes everything",
			runs: []RunSummary{
				run("r1", "WND-1", "run", "claude-code", journal.Converged,
					PhaseRound{Phase: "implement", Round: 1, TokensIn: i64(10), TokensOut: i64(5), At: early}),
			},
			since: late.Add(time.Hour),
			want:  nil,
		},
		{
			name: "mixed harnesses past and before the cutoff",
			runs: []RunSummary{
				run("r1", "WND-1", "run", "claude-code", journal.Converged,
					PhaseRound{Phase: "implement", Round: 1, TokensIn: i64(100), TokensOut: i64(50), At: late},
					// Before the cutoff: must not count.
					PhaseRound{Phase: "review", Round: 1, TokensIn: i64(999), TokensOut: i64(999), At: early},
				),
				run("r2", "WND-2", "run", "codex", journal.Parked,
					PhaseRound{Phase: "implement", Round: 1, TokensIn: i64(20), TokensOut: i64(8), At: late}),
			},
			since: cutoff,
			want: []HarnessTotal{
				{Harness: "claude-code", TokensIn: i64(100), TokensOut: i64(50)},
				{Harness: "codex", TokensIn: i64(20), TokensOut: i64(8)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HarnessTokens(tt.runs, tt.since)
			if len(got) != len(tt.want) {
				t.Fatalf("HarnessTokens = %+v, want %+v", got, tt.want)
			}
			for i := range tt.want {
				g, w := got[i], tt.want[i]
				if g.Harness != w.Harness || tokenEq(g.TokensIn, w.TokensIn) == false || tokenEq(g.TokensOut, w.TokensOut) == false {
					t.Errorf("row %d = %+v, want %+v", i, g, w)
				}
			}
		})
	}
}

func tokenEq(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func TestAddTokensStaysNilWhenBothAbsent(t *testing.T) {
	if got := addTokens(nil, nil); got != nil {
		t.Errorf("addTokens(nil, nil) = %v, want nil, not a faked zero", *got)
	}
	if got := addTokens(i64(3), nil); got == nil || *got != 3 {
		t.Errorf("addTokens(3, nil) = %v, want 3", got)
	}
}
