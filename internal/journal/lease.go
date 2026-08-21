package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Lease is who holds a run right now: the process, the host it runs on, and
// where in the run it had got to when the lease was last written.
//
// The lease is a *readable* artifact, not the proof of anything. Proof is
// the lock file beside it, which the operating system releases when the
// holding process dies however it died — no heartbeat to go stale, no pid
// to be reused underneath us. The lease answers "who and what"; the lock
// answers "still alive", and only the lock's answer may be acted on.
type Lease struct {
	RunID  string `json:"run_id"`
	Ticket string `json:"ticket"`
	// Host and PID name the holder. Host is what makes an honest Unknown
	// possible: a lease written on another machine is one this process
	// cannot judge, and saying so is the whole point.
	Host string `json:"host"`
	PID  int    `json:"pid"`
	// Phase and Round are where the holder was as of Renewed. They are
	// for a human reading wand's report, and for a sweeper's message; the
	// journal, not the lease, is what a resume re-enters from.
	Phase string `json:"phase,omitempty"`
	Round int    `json:"round,omitempty"`
	// Taken is when this process took the run; Renewed is the last
	// transition it recorded.
	Taken   time.Time `json:"taken"`
	Renewed time.Time `json:"renewed"`
}

// Held reports whether a lease was found at all. A run directory with no
// lease is one nobody holds — either it was released cleanly or its holder
// died before writing one.
func (l Lease) Held() bool { return l.PID != 0 }

// Liveness is the answer to "is the holder still running?", and it has
// three values on purpose.
//
// Two-valued liveness is how a sweeper ends up killing a healthy run: the
// only honest answer about a process on another machine is that we cannot
// see it, and folding that into "dead" trades a stalled queue for two
// writers on one ticket. Only [Dead] is proof.
type Liveness string

const (
	// Unknown: the answer cannot be established from here — the holder is
	// on another host, or the lock could not be examined. Never sweep an
	// Unknown; report it and let a human look.
	Unknown Liveness = "unknown"
	// Alive: the lock is held, so the process exists.
	Alive Liveness = "alive"
	// Dead: the lock was free, so no process holds this run. This is the
	// only verdict a sweeper may act on.
	Dead Liveness = "dead"
)

const (
	journalFile     = "journal.jsonl"
	leaseFile       = "lease.json"
	lockFile        = "lock"
	scratchName     = "scratch"
	transcriptsName = "transcripts"
)

// writeLease replaces the lease atomically: a reader either sees the whole
// previous lease or the whole new one. A half-written lease would name a
// phase that never started, and the sweeper's report would quote it.
func writeLease(dir string, l Lease) error {
	raw, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(dir, leaseFile+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once the rename has taken it
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, leaseFile))
}

// readLease returns the lease, or a zero Lease when none is held. A lease
// that will not parse is an error: the file is small, written atomically
// and never appended to, so unreadable means something else wrote it.
func readLease(dir string) (Lease, error) {
	raw, err := os.ReadFile(filepath.Join(dir, leaseFile))
	if os.IsNotExist(err) {
		return Lease{}, nil
	}
	if err != nil {
		return Lease{}, err
	}
	var l Lease
	if err := json.Unmarshal(raw, &l); err != nil {
		return Lease{}, fmt.Errorf("journal: reading the lease in %s: %w", dir, err)
	}
	return l, nil
}

// liveness judges the holder of a run directory.
//
// The lock file is never deleted, only unlocked — deleting it would race a
// process that has just opened it and is about to lock, and on Windows a
// held file cannot be deleted at all.
func liveness(dir string, l Lease, host string) (Liveness, error) {
	if !l.Held() {
		// Nobody wrote a lease, or it was released. Either way no process
		// claims this run, and that is as provable as it gets.
		return Dead, nil
	}
	if l.Host != host {
		return Unknown, nil
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return Unknown, err
	}
	defer f.Close()
	taken, err := tryLock(f)
	if err != nil {
		return Unknown, err
	}
	if !taken {
		return Alive, nil
	}
	// Taking the lock proves nobody else holds it. Give it straight back:
	// this is an inspection, not a claim.
	if err := unlock(f); err != nil {
		return Unknown, err
	}
	return Dead, nil
}

// hostname is the identity a lease is compared against. A machine with no
// resolvable hostname gets a name no other machine will match, so its
// leases read as Unknown elsewhere — degraded, and honest about it.
func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown-host"
	}
	return h
}
