// Package ledger reads the journal store's history into the aggregations
// `wand stats` reports: token velocity over time, a per-harness x per-phase
// breakdown, per-harness x per-verb convergence, and per-ticket totals.
//
// It is a reader, not a second store. Every number here is re-derived from
// the same run.jsonl files internal/journal already writes for the
// crash-only story — no cache, no index, no new dependency — so the only
// thing that can make wand stats wrong is a bug in this package, never a
// stale copy of the truth. That trade costs a full re-scan of every run on
// each invocation, which is the right price at the scale one operator's
// local run history reaches.
//
// Reading is split from replaying on purpose. [journal.Replay] validates a
// run's record stream and gives back its last position, which is all a
// resume needs — but a reader wanting every phase a run took, including
// ones a later phase started over, needs the raw records too, since replay
// keeps only the last phase and round. Walk does both: Replay for
// validation, Meta, Outcome and Started, and a second pass over the same
// records for the phase-by-phase detail.
package ledger

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

// Runs is the slice of the journal store this package uses. An interface so
// tests hold a fake instead of writing real journals to disk for every case
// — mirrors internal/home's own Runs interface.
type Runs interface {
	List() ([]string, error)
	Records(id string) ([]journal.Record, error)
}

// phaseDetail decodes the phase.ended `detail` payload documented on
// docs/content/docs/journal.md — plus `attempt`, which that table omits but
// internal/run and internal/plan both write. It is defined here rather than
// imported from either package because both structs are unexported: the
// ledger reads the wire contract, not either orchestrator's internals.
//
// TokensIn/TokensOut stay pointers so a reported zero and "the harness
// didn't say" remain distinguishable through the sums below — the same rule
// the writers' own phaseDetail follows, for the same reason.
type phaseDetail struct {
	Harness   string `json:"harness,omitempty"`
	TokensIn  *int64 `json:"tokens_in,omitempty"`
	TokensOut *int64 `json:"tokens_out,omitempty"`
	WallClock string `json:"wall_clock,omitempty"`
	Attempt   int    `json:"attempt,omitempty"`
}

// PhaseRound is one phase-and-round a run took, folded from every attempt at
// it. A transient failure retries a phase at the same round (see
// internal/run/run.go's work and internal/plan/plan.go's twin), so this is
// not one entry per phase.ended record: it is one entry per (phase, round),
// with every attempt's tokens and wall-clock summed into it. A retried
// attempt still burned real tokens and wall-clock, so it counts toward
// spend; but the phase-round happened once, however many attempts it took
// to close, so it counts once toward occurrence and review-round tallies.
type PhaseRound struct {
	Phase     string
	Round     int
	TokensIn  *int64
	TokensOut *int64
	WallClock time.Duration
	// At is the last attempt's record timestamp — when this phase-round
	// finally closed — used to bucket it into a velocity day.
	At time.Time
}

// RunSummary is one run's journal folded into what the reports need: its
// identity, how it ended, and every phase-round it took.
type RunSummary struct {
	ID      string
	Ticket  string
	Verb    string
	Harness string
	Outcome journal.Outcome
	Reason  string
	Started time.Time
	// Updated is when the run's journal last changed — state.Updated from
	// journal.Replay, which lands on the outcome-ending record for an ended
	// run. It is what windows outcome counts the same way PhaseRound.At
	// windows Velocity: a run's own last-touched time, not any one
	// phase-round inside it.
	Updated time.Time
	Rounds  []PhaseRound
}

// WalkResult is every run the store could read, plus the ids of any it
// could not.
type WalkResult struct {
	Runs []RunSummary
	// Skipped names runs whose journal failed to read or replay. Skipping
	// one run is not fatal to the report — the same tolerance
	// internal/home/active.go's ActiveRuns gives an unreadable run.
	Skipped []string
}

// Walk reads every run in the store and folds each into a [RunSummary]. A
// run whose journal fails to read or fails [journal.Replay] is dropped into
// Skipped rather than aborting the whole walk.
func Walk(runs Runs) (WalkResult, error) {
	if runs == nil {
		return WalkResult{}, nil
	}
	ids, err := runs.List()
	if err != nil {
		return WalkResult{}, fmt.Errorf("ledger: listing runs: %w", err)
	}
	var result WalkResult
	for _, id := range ids {
		recs, err := runs.Records(id)
		if err != nil {
			result.Skipped = append(result.Skipped, id)
			continue
		}
		state, err := journal.Replay(recs)
		if err != nil {
			result.Skipped = append(result.Skipped, id)
			continue
		}
		result.Runs = append(result.Runs, summarize(state, recs))
	}
	return result, nil
}

// summarize builds a RunSummary from a run's replayed state plus a raw pass
// over its records, grouping phase.ended records by (phase, round) so a
// retried attempt folds into the phase-round it retried rather than counting
// as a second one.
func summarize(state journal.State, recs []journal.Record) RunSummary {
	rs := RunSummary{
		ID:      state.Meta.ID,
		Ticket:  state.Meta.Ticket,
		Verb:    state.Meta.Verb,
		Harness: state.Meta.Harness,
		Outcome: state.Outcome,
		Reason:  state.Reason,
		Started: state.Started,
		Updated: state.Updated,
	}

	type key struct {
		Phase string
		Round int
	}
	index := make(map[key]int)
	for _, r := range recs {
		if r.Kind != journal.KindPhaseEnded {
			continue
		}
		// A detail that will not decode leaves pd zero-valued: its tokens
		// and wall-clock are absent rather than estimated, and it still
		// counts as an occurrence of the phase-round it closed. One
		// malformed record must not drop the run's other numbers.
		var pd phaseDetail
		if len(r.Detail) > 0 {
			_ = json.Unmarshal(r.Detail, &pd)
		}

		k := key{r.Phase, r.Round}
		i, ok := index[k]
		if !ok {
			i = len(rs.Rounds)
			index[k] = i
			rs.Rounds = append(rs.Rounds, PhaseRound{Phase: r.Phase, Round: r.Round})
		}
		g := &rs.Rounds[i]
		g.TokensIn = addTokens(g.TokensIn, pd.TokensIn)
		g.TokensOut = addTokens(g.TokensOut, pd.TokensOut)
		if d, err := time.ParseDuration(pd.WallClock); err == nil {
			g.WallClock += d
		}
		if r.At.After(g.At) {
			g.At = r.At
		}
	}
	return rs
}

// addTokens sums two optional token counts, staying nil when both are —
// absent plus absent is still absent, never a faked zero.
func addTokens(a, b *int64) *int64 {
	if a == nil && b == nil {
		return nil
	}
	var av, bv int64
	if a != nil {
		av = *a
	}
	if b != nil {
		bv = *b
	}
	sum := av + bv
	return &sum
}
