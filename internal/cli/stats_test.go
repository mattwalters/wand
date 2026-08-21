package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/ledger"
)

func i64(v int64) *int64 { return &v }

func TestStatsReportRendersEveryReport(t *testing.T) {
	at := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	result := ledger.WalkResult{
		Runs: []ledger.RunSummary{
			{
				ID: "run-1", Ticket: "WND-1", Verb: "run", Harness: "claude-code",
				Outcome: journal.Converged,
				Rounds: []ledger.PhaseRound{
					{Phase: "implement", Round: 1, TokensIn: i64(100), TokensOut: i64(40), WallClock: time.Minute, At: at},
				},
			},
		},
		Skipped: []string{"broken-run"},
	}

	got := statsReport(result, time.Time{})

	for _, want := range []string{
		"token velocity", "2026-03-01",
		"harness x phase", "claude-code", "implement",
		"convergence (harness x verb)",
		"per-ticket totals", "WND-1",
		"skipped (could not read or replay): broken-run",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("statsReport output missing %q:\n%s", want, got)
		}
	}
}

// An empty store still prints a readable report, not an error — the same
// tolerance wand queue gives an empty Todo.
func TestStatsReportOnEmptyStore(t *testing.T) {
	got := statsReport(ledger.WalkResult{}, time.Time{})
	if strings.Contains(got, "skipped") {
		t.Errorf("statsReport on an empty store should not mention skipped runs:\n%s", got)
	}
	for _, want := range []string{"token velocity", "harness x phase", "convergence", "per-ticket totals"} {
		if !strings.Contains(got, want) {
			t.Errorf("statsReport output missing section %q:\n%s", want, got)
		}
	}
}

// --since is a plain Go duration cutoff applied to velocity only: a
// phase-round that closed before the cutoff drops out of that report, but
// still counts in the harness x phase and ticket totals, which read the
// whole history.
func TestStatsReportSinceFiltersVelocityOnly(t *testing.T) {
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	result := ledger.WalkResult{
		Runs: []ledger.RunSummary{
			{
				ID: "run-1", Ticket: "WND-1", Verb: "run", Harness: "claude-code",
				Outcome: journal.Converged,
				Rounds: []ledger.PhaseRound{
					{Phase: "implement", Round: 1, TokensIn: i64(5), WallClock: time.Minute, At: early},
				},
			},
		},
	}
	cutoff := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	got := statsReport(result, cutoff)
	if strings.Contains(got, "2026-01-01") {
		t.Errorf("velocity should exclude the pre-cutoff bucket:\n%s", got)
	}
	if !strings.Contains(got, "WND-1") {
		t.Errorf("ticket totals should still include the pre-cutoff run:\n%s", got)
	}
}
