package cockpit

import (
	"fmt"
	"sort"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

// LaneKind is why a lane is on the board. Each of these is a state only a
// person can resolve, which is the whole test for whether a run belongs
// here: a run being driven by a living process is not waiting on anyone,
// and showing it would train people to skim the section.
type LaneKind string

const (
	// LaneParked: the run ended without deciding and recorded why.
	// Nothing will pick it up on its own — a park is a hand-back to a
	// human by definition.
	LaneParked LaneKind = "parked"
	// LaneStuck: the journal says the run is still going and its holder
	// is provably gone. The zombie — a ticket In Progress with nothing
	// behind it, which looks healthy and which nothing drains.
	LaneStuck LaneKind = "stuck"
	// LaneOrphaned: a live run whose ticket is not in a started status.
	// The lane is held; nothing on the board claims it.
	LaneOrphaned LaneKind = "orphaned"
	// LaneUnclear: the journal says the run is still going and its holder
	// is on another machine, or the lock could not be examined. Never
	// swept automatically — see journal.Report.Zombie — so it is surfaced
	// for a person instead of guessed at.
	LaneUnclear LaneKind = "unclear"
)

// Lane is one run needing a person, with the sentence saying why.
type Lane struct {
	Kind   LaneKind
	RunID  string
	Ticket string
	Verb   string
	Repo   string
	// Reason is the recorded park reason, or the sentence this package
	// composed for a state the journal only implies.
	Reason string
	// Since is the last thing the journal recorded about the run.
	Since time.Time
}

// Classify decides whether one run is waiting on a person, and why.
//
// started is the set of ticket identifiers currently in a started status on
// the board — In Progress and In Review both. It is what distinguishes a
// held lane from an orphaned one, and it is passed in rather than looked up
// so this stays a pure function.
//
// The order of the checks is the order of severity, and it matters: a dead
// holder whose ticket also fell out of In Progress is reported stuck, not
// orphaned, because the death is the thing to act on and the board drift is
// its consequence.
func Classify(r journal.Report, started map[string]bool) (Lane, bool) {
	lane := Lane{
		RunID:  r.State.Meta.ID,
		Ticket: r.State.Meta.Ticket,
		Verb:   r.State.Meta.Verb,
		Repo:   r.State.Meta.Repo,
		Since:  r.State.Updated,
	}

	if r.State.Ended() {
		if r.State.Outcome != journal.Parked {
			return Lane{}, false // converged or handed back: finished, and nobody is waiting
		}
		lane.Kind = LaneParked
		lane.Reason = r.State.Reason
		return lane, true
	}

	switch r.Live {
	case journal.Dead:
		lane.Kind = LaneStuck
		lane.Reason = fmt.Sprintf(
			"held by pid %d on %s, which is gone; the run stopped in %s and nothing is driving it",
			r.Lease.PID, r.Lease.Host, phaseName(r.State))
		return lane, true
	case journal.Unknown:
		lane.Kind = LaneUnclear
		lane.Reason = fmt.Sprintf(
			"held by pid %d on %s, which this machine cannot see; stopped in %s",
			r.Lease.PID, r.Lease.Host, phaseName(r.State))
		return lane, true
	}

	// Alive. The only thing left that needs a person is the board
	// disagreeing with the journal.
	if !started[lane.Ticket] {
		lane.Kind = LaneOrphaned
		lane.Reason = fmt.Sprintf(
			"running %s in %s, but %s is not in a started status: nothing on the board claims this lane",
			lane.Verb, phaseName(r.State), lane.Ticket)
		return lane, true
	}
	return Lane{}, false
}

// phaseName renders where a run stopped, in a form that reads inside a
// sentence. A run killed before its first phase has no phase at all, and
// saying so beats printing an empty pair of quotes.
func phaseName(s journal.State) string {
	if s.Phase == "" {
		return "no phase yet"
	}
	if s.Round > 0 {
		return fmt.Sprintf("%s (round %d)", s.Phase, s.Round)
	}
	return s.Phase
}

// laneSeverity orders the lane kinds. Stuck first because a dead holder is
// the only one of the four that is actively lying — the board says the work
// is under way. Parked last because a park is an orderly stop that already
// said what it needed.
var laneSeverity = map[LaneKind]int{
	LaneStuck:    0,
	LaneOrphaned: 1,
	LaneUnclear:  2,
	LaneParked:   3,
}

// laneRows orders lanes and wraps them as rows: worst kind first, and
// within a kind the one that has been waiting longest.
func laneRows(lanes []Lane) []Row {
	ordered := make([]Lane, len(lanes))
	copy(ordered, lanes)
	sort.SliceStable(ordered, func(i, j int) bool {
		if a, b := laneSeverity[ordered[i].Kind], laneSeverity[ordered[j].Kind]; a != b {
			return a < b
		}
		if !ordered[i].Since.Equal(ordered[j].Since) {
			return ordered[i].Since.Before(ordered[j].Since)
		}
		return ordered[i].RunID < ordered[j].RunID
	})
	rows := make([]Row, 0, len(ordered))
	for _, lane := range ordered {
		rows = append(rows, Row{Kind: KindLanes, Lane: lane})
	}
	return rows
}
