package cockpit

import (
	"fmt"
	"sort"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

// StallKind is why a run is stalled. Each of these is a state only a person
// can resolve, which is the whole test for whether a run belongs in this
// section: a run being driven by a living process is not waiting on anyone,
// and showing it would train people to skim the section.
type StallKind string

const (
	// StallParked: the run ended without deciding and recorded why.
	// Nothing will pick it up on its own — a park is a hand-back to a
	// human by definition.
	StallParked StallKind = "parked"
	// StallStuck: the journal says the run is still going and its holder
	// is provably gone. The zombie — a ticket In Progress with nothing
	// behind it, which looks healthy and which nothing drains.
	StallStuck StallKind = "stuck"
	// StallOrphaned: a live run whose ticket is not in a started status.
	// The run holds a dispatch lane; nothing on the board claims it. This
	// now fires for
	// a plan run exactly as it does for a build one: `wand plan` claims
	// In Planning (a started status, WND-79) before it does anything else,
	// so a live plan run whose ticket has drifted off it is genuine drift,
	// not a design invariant to carve out.
	StallOrphaned StallKind = "orphaned"
	// StallUnclear: the journal says the run is still going and its holder
	// is on another machine, or the lock could not be examined. Never
	// swept automatically — see journal.Report.Zombie — so it is surfaced
	// for a person instead of guessed at.
	StallUnclear StallKind = "unclear"
)

// StalledRun is one run needing a person, with the sentence saying why.
type StalledRun struct {
	Kind   StallKind
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
// distinguishes a healthy run from an orphaned one, and it is passed in
// rather than looked up so this stays a pure function.
//
// The order of the checks is the order of severity, and it matters: a dead
// holder whose ticket also fell out of In Progress is reported stuck, not
// orphaned, because the death is the thing to act on and the board drift is
// its consequence.
func Classify(r journal.Report, started map[string]bool) (StalledRun, bool) {
	st := StalledRun{
		RunID:  r.State.Meta.ID,
		Ticket: r.State.Meta.Ticket,
		Verb:   r.State.Meta.Verb,
		Repo:   r.State.Meta.Repo,
		Since:  r.State.Updated,
	}

	if r.State.Ended() {
		if r.State.Outcome != journal.Parked {
			return StalledRun{}, false // converged or handed back: finished, and nobody is waiting
		}
		st.Kind = StallParked
		st.Reason = r.State.Reason
		return st, true
	}

	switch r.Live {
	case journal.Dead:
		st.Kind = StallStuck
		st.Reason = fmt.Sprintf(
			"held by pid %d on %s, which is gone; the run stopped in %s and nothing is driving it",
			r.Lease.PID, r.Lease.Host, phaseName(r.State))
		return st, true
	case journal.Unknown:
		st.Kind = StallUnclear
		st.Reason = fmt.Sprintf(
			"held by pid %d on %s, which this machine cannot see; stopped in %s",
			r.Lease.PID, r.Lease.Host, phaseName(r.State))
		return st, true
	}

	// Alive. The only thing left that needs a person is the board
	// disagreeing with the journal — and that now applies to a plan run the
	// same as a build one: `wand plan` claims In Planning (a started
	// status, WND-79) before it does anything else, so started has exactly
	// as much to say about a live plan run as it does about a live build
	// one. The verb-specific exemption this package used to carry here (see
	// WND-66) is gone along with the topology gap it was patching.
	if !started[st.Ticket] {
		st.Kind = StallOrphaned
		st.Reason = fmt.Sprintf(
			"running %s in %s, but %s is not in a started status: nothing on the board claims this run",
			st.Verb, phaseName(r.State), st.Ticket)
		return st, true
	}
	return StalledRun{}, false
}

// phaseName renders where a run stopped, in a form that reads inside a
// sentence.
func phaseName(s journal.State) string { return phaseLabel(s.Phase, s.Round) }

// phaseLabel renders a phase and round, shared by [StalledRun]'s prose and
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

// Reconcile drops parked runs whose ticket a later run already resolved.
//
// A park is a permanent record of one run's failure to decide, but it says
// nothing about whether the ticket stayed unresolved — a later run for the
// same ticket may have converged or handed back cleanly, and once that
// happened the park is history, not a live obligation. Only parked runs are
// ever superseded this way: a stuck, orphaned, or unclear run means a
// process is misbehaving right now, and a later run finishing for the same
// ticket does not explain that away.
//
// reports is every run this walk read, not just the ones that came back
// stalled, because the run that resolves a park is exactly the one Classify
// drops as needing nobody.
//
// parked is the set of ticket identifiers presently carrying the parked
// label, and it is what makes a park *resolvable*. The journal records
// what happened and is append-only, so a run that parked says so forever;
// the board records what is still outstanding, and a person clearing the
// label is how they say it no longer is. Deriving the row from the journal
// alone meant clearing the label changed nothing a person could see — the
// row came straight back on the next read, and a ticket that reached Done
// kept a row no action could ever shift.
//
// A stalled run with no ticket at all — a pm run, which works no ticket —
// can carry no label, so it is never dropped this way.
func Reconcile(stalled []StalledRun, reports []journal.Report, parked map[string]bool) []StalledRun {
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

	kept := make([]StalledRun, 0, len(stalled))
	for _, st := range stalled {
		if st.Kind == StallParked {
			if t, ok := resolved[st.Ticket]; ok && t.After(st.Since) {
				continue
			}
			// The label is the live obligation. Both rules stay: a later
			// run that converged does not remove the label, so neither
			// suppression subsumes the other.
			if st.Ticket != "" && !parked[st.Ticket] {
				continue
			}
		}
		kept = append(kept, st)
	}
	return collapseParked(kept)
}

// collapseParked keeps only the most recent parked run per ticket.
//
// The operative state is one parked label per ticket, written by
// verbs.ReportPark and cleared by a person — but a ticket that has parked
// more than once still has one run per park in the journal, and each of
// those becomes its own [StallParked] row. A ticket does not have several
// parks waiting on a human; it has one current park and the rest history,
// so only the newest reason belongs on the board. The other kinds are
// per-process, not per-ticket, and pass through untouched.
func collapseParked(stalled []StalledRun) []StalledRun {
	newest := make(map[string]StalledRun, len(stalled))
	for _, st := range stalled {
		if st.Kind != StallParked {
			continue
		}
		if cur, ok := newest[st.Ticket]; !ok || st.Since.After(cur.Since) {
			newest[st.Ticket] = st
		}
	}

	kept := make([]StalledRun, 0, len(stalled))
	seen := make(map[string]bool, len(newest))
	for _, st := range stalled {
		if st.Kind != StallParked {
			kept = append(kept, st)
			continue
		}
		if seen[st.Ticket] {
			continue
		}
		seen[st.Ticket] = true
		kept = append(kept, newest[st.Ticket])
	}
	return kept
}

// stallSeverity orders the kinds. Stuck first because a dead holder is the
// only one of the four that is actively lying — the board says the work is
// under way. Parked last because a park is an orderly stop that already
// said what it needed.
var stallSeverity = map[StallKind]int{
	StallStuck:    0,
	StallOrphaned: 1,
	StallUnclear:  2,
	StallParked:   3,
}

// stalledRows orders stalled runs and wraps them as rows: worst kind first,
// and within a kind the one that has been waiting longest.
func stalledRows(stalled []StalledRun) []Row {
	ordered := make([]StalledRun, len(stalled))
	copy(ordered, stalled)
	sort.SliceStable(ordered, func(i, j int) bool {
		if a, b := stallSeverity[ordered[i].Kind], stallSeverity[ordered[j].Kind]; a != b {
			return a < b
		}
		if !ordered[i].Since.Equal(ordered[j].Since) {
			return ordered[i].Since.Before(ordered[j].Since)
		}
		return ordered[i].RunID < ordered[j].RunID
	})
	rows := make([]Row, 0, len(ordered))
	for _, st := range ordered {
		rows = append(rows, Row{Kind: KindStalled, Stalled: st})
	}
	return rows
}
