// Package journal is the crash-only record of what a wand run did and of
// who holds it.
//
// It exists before any orchestrator does, and deliberately so. The
// reference system grew its loop first and its journal afterwards, and the
// gap between the two was a real bug: a loop killed mid-phase left its
// ticket In Progress with nothing to resume from and no way to tell a
// working run from a dead one. Retrofitting a journal onto a loop means
// guessing at what the loop was doing; writing the loop against a journal
// means it never has to be guessed.
//
// Three properties carry that weight, and each shapes the API:
//
//   - **Journal before you act.** A record announces the action that is
//     about to happen, never the one that just did. A crash between the
//     record and the action leaves a run that looks like it was in that
//     phase — which is true, and which is safe, because workers are cold
//     and stateless and re-running a phase costs only time. The reverse
//     order — act, then record — leaves a run that did something the
//     journal does not mention, and no amount of later reading recovers
//     it. So [Run.StartPhase] writes and syncs before returning, and its
//     error means: do not proceed.
//
//   - **Exactly one terminal record.** Every run ends converged, handed
//     back, or parked with a reason. [Run.Close] is the backstop: if the
//     process unwinds without recording an outcome, Close parks and says
//     so. A second terminal attempt refuses rather than overwriting,
//     because a run with two endings is a run whose ending is unknown.
//
//   - **Death is provable or it is unknown, never assumed.** The zombie —
//     a ticket In Progress with no living process behind it — is the
//     state machine's most dangerous state, because it looks healthy and
//     nothing drains it. A holder is proven alive by an exclusive lock the
//     operating system releases when the process dies, whatever killed it
//     (see lease.go). A lease from another host answers [Unknown], not
//     [Dead], and a sweeper may act only on [Dead]. Guessing here means
//     two writers on one ticket, which is the one failure this system
//     cannot reconcile afterwards.
//
// The record stream is append-only JSON Lines, one object per line, synced
// per record. Reading tolerates a torn final line — that is a crash caught
// mid-append, which is exactly the case this file format exists for — but
// never a torn line in the middle, which is loss rather than interruption.
//
// [Replay] is the pure half: records in, [State] out, no I/O. Every
// decision about a run — is it resumable, where does it re-enter, did it
// end and how — is made there, so a test can hold the whole state machine
// without a filesystem.
//
// What is deliberately *not* durable: record contents are synced, but the
// directory entries holding them are not — Go has no portable directory
// fsync, and buying one would mean a build-tagged path per platform for a
// window measured in milliseconds. The trade is only acceptable because of
// where it fails. A power loss in that window can lose a whole run
// directory, a lease rename, or a truncation; each of those makes a run
// unreadable, and an unreadable run is refused. None of them can make a
// readable journal say the wrong thing. Refusing to resume costs a human
// two minutes; resuming a run that already ended costs two writers on one
// ticket, which is the one failure this system cannot reconcile
// afterwards.
package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// Kind names what a record announces. The set is closed: a kind this
// binary does not know is an error rather than a skip, because the two
// records that decide a resume — the terminal and the phase — are the ones
// a silent skip would lose.
type Kind string

const (
	// KindStarted opens a run. Exactly one, always first, and the only
	// record carrying the run's Meta.
	KindStarted Kind = "run.started"
	// KindResumed records that a new process picked the run back up. It
	// closes any phase left open by the death that made the resume
	// necessary.
	KindResumed Kind = "run.resumed"
	// KindPhaseStarted announces a phase about to run. Written before the
	// worker is spawned, never after.
	KindPhaseStarted Kind = "phase.started"
	// KindPhaseEnded records that the phase returned, with whatever the
	// orchestrator wants kept about it in Detail — exit code, timeout,
	// tokens. Cross-harness comparison falls out of that field for free.
	KindPhaseEnded Kind = "phase.ended"
	// KindNote is anything worth reading later that is not a transition.
	KindNote Kind = "note"
	// KindEnded closes a run. Exactly one, always last.
	KindEnded Kind = "run.ended"
)

func (k Kind) known() bool {
	switch k {
	case KindStarted, KindResumed, KindPhaseStarted, KindPhaseEnded, KindNote, KindEnded:
		return true
	}
	return false
}

// Outcome is how a run ended. There are exactly three, and cap exhaustion
// is not one of them: a loop that ran out of rounds hands back and says so,
// because "converged" claimed by exhaustion is the false convergence this
// lifecycle is built against.
type Outcome string

const (
	// Converged: the run reached its goal on positive evidence.
	Converged Outcome = "converged"
	// HandedBack: the run stopped and a human owns the next move.
	HandedBack Outcome = "handed_back"
	// Parked: the run stopped without deciding — interrupted, crashed,
	// or blocked on something it could not resolve. A park always states
	// why, because a park nobody can explain is one nobody clears.
	Parked Outcome = "parked"
)

func (o Outcome) known() bool {
	switch o {
	case Converged, HandedBack, Parked:
		return true
	}
	return false
}

// Meta is what the run is, fixed at creation and never restated. It rides
// the KindStarted record rather than a sidecar file so that one append-only
// stream is the whole truth about a run.
type Meta struct {
	// ID is the run's identity and its directory name. The store assigns
	// it; Create ignores whatever is here.
	ID string `json:"id"`
	// Ticket is the Linear identifier the run works, e.g. "WND-7".
	Ticket string `json:"ticket"`
	// Verb names the orchestrator: "run", "plan" ("scope" in a run
	// journaled before this rename — home and dispatch.LanesUsed
	// both read the two as synonyms rather than migrating old runs).
	Verb string `json:"verb"`
	// Repo is the absolute path of the repository the run acts on. A
	// sweeper needs it to find the worktree of a run whose process is
	// gone.
	Repo string `json:"repo"`
	// Harness names the worker adapter, when one is chosen. Optional:
	// plan-style runs pick their harness per phase.
	Harness string `json:"harness,omitempty"`
}

func (m Meta) validate() error {
	switch {
	case m.Ticket == "":
		return errors.New("journal: Meta.Ticket is required — a run that does not name its ticket cannot be swept")
	case m.Verb == "":
		return errors.New("journal: Meta.Verb is required — it names which orchestrator to re-enter on resume")
	case m.Repo == "":
		return errors.New("journal: Meta.Repo is required — a sweeper needs the worktree of a run whose process is gone")
	case !filepath.IsAbs(m.Repo):
		return errors.New("journal: Meta.Repo must be absolute — a relative path resolves against whichever process reads it later")
	}
	return nil
}

// Record is one line of the journal.
//
// The shape is deliberately flat and self-describing: someone reading a
// journal by hand, months later, during an incident, is the reader this
// format is for.
type Record struct {
	// Seq numbers records from 1, contiguously. A gap is loss, and Replay
	// refuses it rather than reasoning over a stream with a hole in it.
	Seq int `json:"seq"`
	// At is when the record was written.
	At time.Time `json:"at"`
	// Kind is what the record announces.
	Kind Kind `json:"kind"`

	// Run carries the run's identity. Set on KindStarted only.
	Run *Meta `json:"run,omitempty"`

	// Host and PID name the process that opened this segment of the run.
	// Set on KindStarted and KindResumed. The lease is deleted when a run
	// ends, so this is where "which machine actually ran it" survives —
	// and a resumed run has more than one answer.
	Host string `json:"host,omitempty"`
	PID  int    `json:"pid,omitempty"`

	// Phase and Round identify the phase a phase record is about.
	Phase string `json:"phase,omitempty"`
	Round int    `json:"round,omitempty"`

	// Outcome is set on KindEnded.
	Outcome Outcome `json:"outcome,omitempty"`

	// Reason is the human sentence: why the run ended, what the note
	// says, what made the resume necessary.
	Reason string `json:"reason,omitempty"`

	// Detail is the orchestrator's own structured payload, passed through
	// opaquely. Keeping it opaque is what lets this package know nothing
	// about workers, harnesses or CI while still being where their
	// results are recorded.
	Detail json.RawMessage `json:"detail,omitempty"`
}

// State is the whole of what a journal says about a run: pure, derived,
// and the only thing callers should make decisions from.
type State struct {
	Meta Meta

	// Phase and Round are where the run last was — the resume point. A
	// phase record is written before its action, so this names the phase
	// that was under way when the run stopped, whether or not it finished.
	Phase string
	Round int
	// PhaseDone reports whether that phase recorded its end. The
	// orchestrator owns the phase graph, so it decides what follows: a
	// finished phase means advance, an unfinished one means re-run —
	// always safe, because workers are cold.
	PhaseDone bool

	// Resumes counts how many times a new process picked this run up. A
	// run resuming over and over is a run failing in a way resume cannot
	// fix, and that is worth seeing.
	Resumes int

	// Outcome and Reason are set once the run ended.
	Outcome Outcome
	Reason  string

	Started time.Time
	Updated time.Time
	// Seq is the last sequence number in the stream; the next record
	// written continues from here.
	Seq int
}

// Ended reports whether the run recorded a terminal state.
func (s State) Ended() bool { return s.Outcome != "" }

// Resumable reports whether a new process may pick this run up, and when it
// may not, the sentence explaining it. Liveness is a separate question with
// a separate answer — see [Store.Inspect]; this one is only about what the
// journal says.
func (s State) Resumable() (bool, string) {
	if s.Ended() {
		return false, fmt.Sprintf("run %s already ended (%s: %s); a finished run is not resumed, it is read",
			s.Meta.ID, s.Outcome, s.Reason)
	}
	return true, ""
}

// Replay folds a record stream into a State, validating it on the way.
//
// It refuses a stream it cannot reason over — a missing or misplaced
// opening record, a sequence gap, anything after the terminal, a kind this
// binary does not know — because a wrong resume decision costs more than a
// refusal. It does not police phase interleaving: an orchestrator that
// starts a phase without ending the last one has left a fact, not a
// corruption, and the last phase started is still the resume point.
func Replay(records []Record) (State, error) {
	var s State
	if len(records) == 0 {
		return s, errors.New("journal: empty — a run with no records was never started")
	}
	for i, r := range records {
		if !r.Kind.known() {
			return s, fmt.Errorf("journal: record %d has unknown kind %q; this journal was written by a newer wand", r.Seq, r.Kind)
		}
		if r.Seq != i+1 {
			return s, fmt.Errorf("journal: record %d is out of sequence (expected %d); the stream has a hole in it and cannot be replayed", r.Seq, i+1)
		}
		if s.Ended() {
			return s, fmt.Errorf("journal: record %d follows the run's terminal record; a run ends exactly once", r.Seq)
		}
		if i == 0 && r.Kind != KindStarted {
			return s, fmt.Errorf("journal: the first record is %q, not %q", r.Kind, KindStarted)
		}
		if i > 0 && r.Kind == KindStarted {
			return s, fmt.Errorf("journal: record %d opens the run a second time", r.Seq)
		}

		switch r.Kind {
		case KindStarted:
			if r.Run == nil {
				return s, errors.New("journal: the opening record carries no run metadata")
			}
			s.Meta = *r.Run
			s.Started = r.At
		case KindResumed:
			s.Resumes++
			// The death that made the resume necessary is what closed the
			// phase; nothing else ever will. Reopening it here would make
			// PhaseDone report a phase that finished when it did not.
			s.PhaseDone = false
		case KindPhaseStarted:
			if r.Phase == "" {
				return s, fmt.Errorf("journal: record %d starts a phase with no name", r.Seq)
			}
			s.Phase, s.Round, s.PhaseDone = r.Phase, r.Round, false
		case KindPhaseEnded:
			if s.Phase == "" {
				return s, fmt.Errorf("journal: record %d ends a phase that never started", r.Seq)
			}
			if r.Phase != s.Phase || r.Round != s.Round {
				return s, fmt.Errorf("journal: record %d ends phase %q round %d while %q round %d is the open one",
					r.Seq, r.Phase, r.Round, s.Phase, s.Round)
			}
			s.PhaseDone = true
		case KindEnded:
			if !r.Outcome.known() {
				return s, fmt.Errorf("journal: record %d ends the run with unknown outcome %q", r.Seq, r.Outcome)
			}
			s.Outcome, s.Reason = r.Outcome, r.Reason
		}
		s.Updated = r.At
		s.Seq = r.Seq
	}
	return s, nil
}
