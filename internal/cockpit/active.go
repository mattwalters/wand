package cockpit

import (
	"fmt"
	"sort"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

// Active is one run a live process is presently driving — the cockpit's
// answer to "what is the machine doing right now?", which is a different
// question from the one the four queues answer. A run belongs here for as
// long as its journal has not recorded a terminal state, whether or not a
// process can still be proven to hold it: the same run can be an Active row
// now and a stuck [StalledRun] a moment later, once [Classify] (or a real sweep
// pass) decides its holder is gone. Nothing here is waiting on a human, so
// nothing here counts toward [Board.Waiting] or is reachable by the cursor
// — see [Board.Running].
type Active struct {
	RunID   string
	Ticket  string
	Verb    string
	Harness string
	Phase   string
	Round   int
	// Started is when the run began — Started, not journal seq 1's At,
	// because a resumed run's first process is not the one on screen now.
	Started time.Time
	// Heartbeat is the lease's own Renewed timestamp: the last time any
	// process holding this run proved it was still working, whether that
	// was a phase transition or a mid-phase heartbeat tick (journal.Run's
	// own Heartbeat method — see internal/worker's OnHeartbeat).
	Heartbeat time.Time
	// Live is the liveness judgment [Store.Inspect] already makes for this
	// run — the same one [Classify] uses for the stalled section. It is carried
	// through rather than re-derived, because inventing a second liveness
	// judgment from the heartbeat's own age is exactly what this package
	// must not do: a stale heartbeat is what sweep's dead-lease logic
	// exists to confirm.
	Live journal.Liveness
}

// Stale reports whether this run's own liveness judgment says its heartbeat
// cannot be trusted — a signal to render "possibly dead, sweep will
// confirm", never a second verdict computed from the heartbeat's age.
func (a Active) Stale() bool { return a.Live != journal.Alive }

// PhaseLabel renders where the run has got to, in the form used across the
// board: bare for a phase with no second round, parenthesized otherwise.
func (a Active) PhaseLabel() string { return phaseLabel(a.Phase, a.Round) }

// HarnessLabel is the harness a person reads, or an explicit placeholder
// when there is none — a plan-style run picks its harness per phase rather
// than fixing one for the whole run, so an empty Harness is an honest
// "not fixed", not a fact this package failed to read.
func (a Active) HarnessLabel() string {
	if a.Harness == "" {
		return "—"
	}
	return a.Harness
}

// ActiveRuns walks the run store for every run whose journal has not
// recorded a terminal state — the whole of what "running right now" means
// from the journal's own point of view, live holder or not.
//
// A run [Store.Inspect] cannot read is skipped rather than surfaced here:
// [ReadStalled] already turns that failure into an unclear row, which is the
// queue a person acts from. This one only shows what it could actually
// read.
func ActiveRuns(runs Runs) ([]Active, error) {
	if runs == nil {
		return nil, nil
	}
	ids, err := runs.List()
	if err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}

	var out []Active
	for _, id := range ids {
		report, err := runs.Inspect(id)
		if err != nil {
			continue
		}
		if report.State.Ended() {
			continue
		}
		out = append(out, Active{
			RunID:     report.State.Meta.ID,
			Ticket:    report.State.Meta.Ticket,
			Verb:      report.State.Meta.Verb,
			Harness:   report.State.Meta.Harness,
			Phase:     report.State.Phase,
			Round:     report.State.Round,
			Started:   report.State.Started,
			Heartbeat: report.Lease.Renewed,
			Live:      report.Live,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out, nil
}
