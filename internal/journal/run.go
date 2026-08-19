package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Run is a live handle on one run: the open journal, the held lock, and the
// lease that says where the run has got to.
//
// The methods are ordered by the discipline they enforce, not by
// convenience. [Run.StartPhase] writes and syncs its record before it
// returns, so the caller's next line is the action the record announced.
// The three terminal methods each write exactly once and refuse a second.
// [Run.Close] is the backstop for every path that reaches neither.
//
// A Run is safe for concurrent use, though an orchestrator has no reason
// to need that: the point of the mutex is that "exactly one terminal
// record" stays true even when a signal handler and the main loop both
// decide the run is over.
type Run struct {
	store *Store
	dir   string
	meta  Meta
	taken time.Time

	// lock is held for the run's lifetime. Its release — by unlock, by
	// process exit, or by the kernel reaping a killed process — is what
	// makes a dead holder provably dead.
	lock *os.File
	jf   *os.File

	mu       sync.Mutex
	seq      int
	phase    string
	round    int
	outcome  Outcome
	released bool
}

// open takes the lock and the journal handle for a run directory. It is
// the only way a Run comes into existence, so the lock is always held
// before any other write to the directory happens.
func (s *Store) open(dir string, m Meta, now time.Time) (*Run, error) {
	// The lock comes first. Everything after it — appending to the
	// journal, replacing the lease — is a write only the holder may make,
	// so nothing may happen before the holding is established.
	lock, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("journal: opening the run lock: %w", err)
	}
	taken, err := tryLock(lock)
	if err != nil {
		lock.Close()
		return nil, fmt.Errorf("journal: locking the run: %w", err)
	}
	if !taken {
		lock.Close()
		held, _ := readLease(dir)
		return nil, fmt.Errorf(
			"journal: run %s is held by pid %d on %s and is still running; a run has one writer, and a second one cannot be reconciled with the first afterwards",
			m.ID, held.PID, held.Host)
	}
	if err := os.MkdirAll(filepath.Join(dir, scratchName), 0o755); err != nil {
		unlock(lock)
		lock.Close()
		return nil, fmt.Errorf("journal: creating the scratch directory: %w", err)
	}
	jf, err := os.OpenFile(filepath.Join(dir, journalFile), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		unlock(lock)
		lock.Close()
		return nil, fmt.Errorf("journal: opening the journal: %w", err)
	}
	return &Run{store: s, dir: dir, meta: m, taken: now, lock: lock, jf: jf}, nil
}

// adopt takes on a journal already on disk: the run's identity, the next
// sequence number, and the phase that was under way. Only Reopen calls it,
// and only once the lock is held — before that, the state it would adopt is
// still being written by somebody else.
func (r *Run) adopt(state State) {
	r.meta = state.Meta
	r.seq, r.phase, r.round = state.Seq, state.Phase, state.Round
}

// ID is the run's identity, and what `wand resume` is given.
func (r *Run) ID() string { return r.meta.ID }

// Meta is what the run is.
func (r *Run) Meta() Meta { return r.meta }

// Dir is the run's directory.
func (r *Run) Dir() string { return r.dir }

// ScratchDir is the per-run write grant handed to every worker: a real
// directory, outside the repository, stated in the worker's contract rather
// than left to it to invent. A worker that has been told where to scratch
// does not write its CI log into the worktree, and the run does not park on
// a tree it dirtied itself.
func (r *Run) ScratchDir() string { return filepath.Join(r.dir, scratchName) }

// HandoffPath is where the current phase's worker writes its handoff —
// named for the phase and round, so an earlier phase's file cannot be
// mistaken for this one's even if something failed to clean it up. Before
// any phase has started it is the run's own handoff.json.
func (r *Run) HandoffPath() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase == "" {
		return filepath.Join(r.ScratchDir(), "handoff.json")
	}
	return filepath.Join(r.ScratchDir(), fmt.Sprintf("%s-%d.handoff.json", slug(r.phase), r.round))
}

// StartPhase journals a phase and renews the lease, then returns. The
// caller spawns the worker afterwards — never before.
//
// The order is the whole design. A crash between this record and the spawn
// leaves a run that looks like it was in this phase, which is true and
// which resume handles: workers are cold and stateless, so re-running a
// phase costs time and nothing else. The opposite order leaves a run that
// did something its journal never mentions, and nothing recovers that.
//
// An error here means the transition was not recorded. Do not proceed:
// an unjournaled phase is exactly the state this package exists to prevent.
func (r *Run) StartPhase(name string, round int) error {
	if name == "" {
		return errors.New("journal: a phase needs a name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.usable(); err != nil {
		return err
	}
	if err := r.append(Record{Kind: KindPhaseStarted, Phase: name, Round: round}); err != nil {
		return err
	}
	r.phase, r.round = name, round
	return r.renew()
}

// EndPhase records that the current phase returned, with whatever the
// orchestrator wants kept about it — exit code, timeout, rounds, tokens.
// detail is passed through opaquely, which is what keeps this package
// ignorant of workers and harnesses while still being where their results
// land; cross-harness comparison data falls out of that field for free.
func (r *Run) EndPhase(detail any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.usable(); err != nil {
		return err
	}
	if r.phase == "" {
		return errors.New("journal: no phase is open")
	}
	raw, err := marshalDetail(detail)
	if err != nil {
		return err
	}
	return r.append(Record{Kind: KindPhaseEnded, Phase: r.phase, Round: r.round, Detail: raw})
}

// Note records something worth reading later that is not a transition.
func (r *Run) Note(message string, detail any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.usable(); err != nil {
		return err
	}
	raw, err := marshalDetail(detail)
	if err != nil {
		return err
	}
	return r.append(Record{Kind: KindNote, Phase: r.phase, Round: r.round, Reason: message, Detail: raw})
}

// Converged ends the run having reached its goal. summary is the positive
// evidence — what was proven, not what stopped happening. A terminal
// success reached because a cap ran out is the false convergence this
// lifecycle is built against; that is a hand-back, and it says so.
func (r *Run) Converged(summary string) error { return r.end(Converged, summary) }

// HandedBack ends the run with a human owning the next move: a question, a
// judgement call, or a cap that ran out.
func (r *Run) HandedBack(reason string) error { return r.end(HandedBack, reason) }

// Parked ends the run without deciding — interrupted, blocked, or stopped
// by something it could not resolve. The reason is required: a park nobody
// can explain is a park nobody clears.
func (r *Run) Parked(reason string) error { return r.end(Parked, reason) }

// end writes the one terminal record and releases the run.
//
// A second attempt refuses rather than overwriting. Two endings recorded
// for one run is worse than either ending alone, because afterwards nobody
// can say which one was true — and the whole point of the terminal record
// is that a reader never has to guess.
func (r *Run) end(o Outcome, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("journal: ending a run %s needs a reason — an ending nobody can explain is one nobody acts on", o)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outcome != "" {
		return fmt.Errorf("journal: run %s already ended (%s); a run ends exactly once", r.meta.ID, r.outcome)
	}
	if r.released {
		return fmt.Errorf("journal: run %s is closed", r.meta.ID)
	}
	if err := r.append(Record{Kind: KindEnded, Outcome: o, Reason: reason}); err != nil {
		return err
	}
	r.outcome = o
	return r.release()
}

// ClosedReason is what Close records when the process unwound without
// deciding anything. It names the backstop rather than inventing a cause,
// because a park whose reason is a guess is worse than one that admits it.
const ClosedReason = "the run ended without recording an outcome; the process unwound through Close"

// Close releases the run, parking it first if nothing terminal was
// recorded. It is idempotent, and it is the reason `defer r.Close()` on the
// line after Create is the whole of the exactly-one-terminal-state
// guarantee for every death this process can observe.
//
// It cannot cover the deaths it cannot observe — SIGKILL, a power cut. Those
// are what the lock and [Store.Reopen] are for: not prevented, but provable
// and cheap to recover.
func (r *Run) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.released {
		return nil
	}
	if r.outcome == "" {
		if err := r.append(Record{Kind: KindEnded, Outcome: Parked, Reason: ClosedReason}); err != nil {
			r.release()
			return err
		}
		r.outcome = Parked
	}
	return r.release()
}

// Outcome is how the run ended, or "" while it is still going.
func (r *Run) Outcome() Outcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.outcome
}

// usable rejects writes to a run that has already ended. Caller holds mu.
func (r *Run) usable() error {
	if r.outcome != "" {
		return fmt.Errorf("journal: run %s already ended (%s)", r.meta.ID, r.outcome)
	}
	if r.released {
		return fmt.Errorf("journal: run %s is closed", r.meta.ID)
	}
	return nil
}

// append writes one record and syncs it. Caller holds mu.
//
// The sync is the point: a record that is only in the page cache when the
// machine loses power announced an action that then happened anyway, which
// is precisely the ordering this package promises does not occur.
func (r *Run) append(rec Record) error {
	rec.Seq = r.seq + 1
	if rec.At.IsZero() {
		rec.At = r.store.now()
	}
	switch rec.Kind {
	case KindStarted, KindResumed:
		rec.Host, rec.PID = r.store.host(), os.Getpid()
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("journal: encoding a %s record: %w", rec.Kind, err)
	}
	if _, err := r.jf.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("journal: writing a %s record: %w", rec.Kind, err)
	}
	if err := r.jf.Sync(); err != nil {
		return fmt.Errorf("journal: syncing a %s record: %w", rec.Kind, err)
	}
	r.seq = rec.Seq
	return nil
}

// renew rewrites the lease at the run's current position. Caller holds mu.
func (r *Run) renew() error {
	return writeLease(r.dir, Lease{
		RunID:   r.meta.ID,
		Ticket:  r.meta.Ticket,
		Host:    r.store.host(),
		PID:     os.Getpid(),
		Phase:   r.phase,
		Round:   r.round,
		Taken:   r.taken,
		Renewed: r.store.now(),
	})
}

// release drops the lease and the lock. Caller holds mu.
//
// The lease is deleted rather than rewritten: a lease is a claim, and an
// ended run has no claimant. The lock file itself stays — unlocked, but
// present — because deleting it would race a process that has just opened
// it and is about to lock, and on Windows a held file cannot be deleted at
// all.
func (r *Run) release() error {
	if r.released {
		return nil
	}
	r.released = true
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	if err := os.Remove(filepath.Join(r.dir, leaseFile)); err != nil && !os.IsNotExist(err) {
		keep(err)
	}
	keep(r.jf.Close())
	keep(unlock(r.lock))
	keep(r.lock.Close())
	return first
}

func marshalDetail(detail any) (json.RawMessage, error) {
	if detail == nil {
		return nil, nil
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("journal: encoding record detail: %w", err)
	}
	return raw, nil
}
