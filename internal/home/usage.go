package home

import (
	"time"

	"github.com/mattwalters/wand/internal/ledger"
)

// Usage is what the landing screen's usage panel needs: recent token
// velocity, how runs recently ended, and where the tokens went by harness —
// derived by the same internal/ledger aggregation `wand stats` reports, over
// one shared window, so the panel and the drill-down command can never
// disagree about a number.
type Usage struct {
	// Since is the window's cutoff — the same value passed to [BuildUsage].
	Since    time.Time
	Velocity []ledger.VelocityBucket
	Outcomes ledger.OutcomeCounts
	Harness  []ledger.HarnessTotal
}

// Empty reports whether the window has nothing to show: no phase-round
// closed and no run ended in it, so every part of the panel would otherwise
// render as a row of zeroes rather than the honest "nothing recorded" ledger's
// own Render* functions give the same situation.
func (u Usage) Empty() bool {
	return len(u.Velocity) == 0 && u.Outcomes == (ledger.OutcomeCounts{}) && len(u.Harness) == 0
}

// BuildUsage walks the run store once and folds it into a [Usage] over the
// window starting at since — the same Runs-interface read, pure fold, no I/O
// beyond the store shape [ActiveRuns] already follows. Runs is also a
// ledger.Runs (see [Runs]'s own doc), so the walk is ledger's, not a second
// one home invents.
func BuildUsage(runs Runs, since time.Time) (Usage, error) {
	result, err := ledger.Walk(runs)
	if err != nil {
		return Usage{}, err
	}
	return Usage{
		Since:    since,
		Velocity: ledger.Velocity(result.Runs, since),
		Outcomes: ledger.CountOutcomes(result.Runs, since),
		Harness:  ledger.HarnessTokens(result.Runs, since),
	}, nil
}
