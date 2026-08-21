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
	// The lane is held; nothing on the board claims it. This now fires for
	// a plan run exactly as it does for a build one: `wand plan` claims
	// In Planning (a started status, WND-79) before it does anything else,
	// so a live plan run whose ticket has drifted off it is genuine drift,
	// not a design invariant to carve out.
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
// the board — In Progress, In Review and In Planning all three. It is what
// distinguishes a held lane from an orphaned one, and it is passed in
// rather than looked up so this stays a pure function.
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
	// disagreeing with the journal — and that now applies to a plan run the
	// same as a build one: `wand plan` claims In Planning (a started
	// status, WND-79) before it does anything else, so started has exactly
	// as much to say about a live plan run as it does about a live build
	// one. The verb-specific exemption this package used to carry here (see
	// WND-66) is gone along with the topology gap it was patching.
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
// sentence.
func phaseName(s journal.State) string { return phaseLabel(s.Phase, s.Round) }

// phaseLabel renders a phase and round, shared by [Lane]'s prose and
// [Active]'s own column. A run with no phase at all — killed before its
// first — reads as "no phase yet" rather than an empty pair of quotes.
func phaseLabel(phase string, round int) string {
	if phase == "" {
		return "no phase yet"
	}
	if round > 0 {
		return fmt.Sprintf("%s (round %d)", phase, round)
	}
	return phase
}

// Reconcile drops parked lanes whose ticket a later run already resolved.
//
// A park is a permanent record of one run's failure to decide, but it says
// nothing about whether the ticket stayed unresolved — a later run for the
// same ticket may have converged or handed back cleanly, and once that
// happened the park is history, not a live obligation. Only parked lanes
// are ever superseded this way: a stuck, orphaned, or unclear lane means a
// process is misbehaving right now, and a later run finishing for the same
// ticket does not explain that away.
//
// reports is every run this walk read, not just the ones that became
// lanes, because the run that resolves a park is exactly the one Classify
// drops as needing nobody.
//
// parked is the set of ticket identifiers presently carrying the parked
// label, and it is what makes a park *resolvable*. The journal records
// what happened and is append-only, so a run that parked says so forever;
// the board records what is still outstanding, and a person clearing the
// label is how they say it no longer is. Deriving the lane from the
// journal alone meant clearing the label changed nothing a person could
// see — the lane came straight back on the next read, and a ticket that
// reached Done kept a lane no action could ever shift.
//
// A lane with no ticket at all — a pm run, which works no ticket — can
// carry no label, so it is never dropped this way.
func Reconcile(lanes []Lane, reports []journal.Report, parked map[string]bool) []Lane {
	resolved := make(map[string]time.Time)
	for _, r := range reports {
		if !r.State.Ended() || r.State.Outcome == journal.Parked {
			continue
		}
		ticket := r.State.Meta.Ticket
		if t, ok := resolved[ticket]; !ok || r.State.Updated.After(t) {
			resolved[ticket] = r.State.Updated
		}
	}

	kept := make([]Lane, 0, len(lanes))
	for _, lane := range lanes {
		if lane.Kind == LaneParked {
			if t, ok := resolved[lane.Ticket]; ok && t.After(lane.Since) {
				continue
			}
			// The label is the live obligation. Both rules stay: a later
			// run that converged does not remove the label, so neither
			// suppression subsumes the other.
			if lane.Ticket != "" && !parked[lane.Ticket] {
				continue
			}
		}
		kept = append(kept, lane)
	}
	return collapseParked(kept)
}

// collapseParked keeps only the most recent parked lane per ticket.
//
// The operative state is one parked label per ticket, written by
// verbs.ReportPark and cleared by a person — but a ticket that has parked
// more than once still has one run per park in the journal, and each of
// those becomes its own [LaneParked] lane. A ticket does not have several
// parks waiting on a human; it has one current park and the rest history,
// so only the newest reason belongs on the board. Other lane kinds are
// per-process, not per-ticket, and pass through untouched.
func collapseParked(lanes []Lane) []Lane {
	newest := make(map[string]Lane, len(lanes))
	for _, lane := range lanes {
		if lane.Kind != LaneParked {
			continue
		}
		if cur, ok := newest[lane.Ticket]; !ok || lane.Since.After(cur.Since) {
			newest[lane.Ticket] = lane
		}
	}

	kept := make([]Lane, 0, len(lanes))
	seen := make(map[string]bool, len(newest))
	for _, lane := range lanes {
		if lane.Kind != LaneParked {
			kept = append(kept, lane)
			continue
		}
		if seen[lane.Ticket] {
			continue
		}
		seen[lane.Ticket] = true
		kept = append(kept, newest[lane.Ticket])
	}
	return kept
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
