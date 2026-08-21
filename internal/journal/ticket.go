package journal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The per-ticket lock, kept deliberately separate from the run lock.
//
// The run lock (run.go) answers "is this run alive?" and nothing else. Two
// runs against one ticket are two directories and two locks, and for
// `wand run` that is enough: it claims the ticket out of Todo before it
// does anything, so the board itself is the mutex — the second process
// finds the ticket already In Progress and refuses.
//
// `wand plan` has no such move. Its ticket sits in Scoping from the first
// read to the last write, because Scoping is the blessing it works under
// and an agent may not revoke a blessing to take a lock. Nothing on the
// board changes until the plan run is finished, so nothing on the board can
// keep a second plan run out — and two plan runs over one ticket produce two
// plans written into the same fenced region, the second silently replacing
// the first, plus two options comments arguing different recommendations at
// a human who cannot tell which one the estimate belongs to.
//
// So an orchestrator that cannot use the board as its mutex takes this
// lock explicitly. It is the same mechanism as the run lock, for the same
// reason: the kernel drops a flock when its holder dies, however it died,
// so a crashed plan run does not wedge its ticket until someone deletes a
// file. The plan run, not the ticket, is the unit — the lock lives only as
// long as the process holding it.
//
// The lock is machine-local: it is a file in this machine's state
// directory, so it serializes the processes on one developer's machine and
// says nothing about another's. That is the honest boundary of a
// filesystem lock, and the reason cross-machine exclusion stays the
// dispatcher's problem, decided on the board before a run exists.

// ticketLocksDir is where per-ticket locks live, beside the runs they
// serialize.
func (s *Store) ticketLocksDir() string { return filepath.Join(s.Root, "tickets") }

// TicketLock is an exclusive hold on one ticket for the life of a process.
type TicketLock struct {
	f      *os.File
	ticket string
}

// LockTicket takes the exclusive lock on one ticket, without blocking.
//
// It refuses rather than waits: a second plan run over the same ticket is a
// mistake to report, not a job to queue behind a run that may take half an
// hour. The refusal names the holder, because "somebody else has it" is
// only actionable if you can go and look at them.
//
// Pair it immediately, before anything else the caller does:
//
//	lock, err := store.LockTicket("WND-9")
//	if err != nil { return err }
//	defer lock.Release()
func (s *Store) LockTicket(ticket string) (*TicketLock, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(ticket) == "" {
		return nil, fmt.Errorf("journal: locking a ticket needs the ticket")
	}
	if err := os.MkdirAll(s.ticketLocksDir(), 0o755); err != nil {
		return nil, fmt.Errorf("journal: creating the ticket lock directory: %w", err)
	}
	path := filepath.Join(s.ticketLocksDir(), slug(ticket)+".lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("journal: opening the ticket lock: %w", err)
	}
	taken, err := tryLock(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("journal: locking ticket %s: %w", ticket, err)
	}
	if !taken {
		holder := readHolder(f)
		f.Close()
		return nil, fmt.Errorf(
			"journal: ticket %s is already being worked by another wand process on this machine (%s); "+
				"two processes writing one ticket cannot be reconciled afterwards, so the second refuses rather than waits",
			ticket, holder)
	}
	// Who holds it, for the next process's refusal message. It is a
	// courtesy, not the lock: the lock is the flock, and a torn or missing
	// line here costs a helpful sentence, never correctness.
	f.Truncate(0)
	f.Seek(0, 0)
	fmt.Fprintf(f, "pid %d on %s since %s\n", os.Getpid(), s.host(), s.now().UTC().Format(time.RFC3339))
	f.Sync()
	return &TicketLock{f: f, ticket: ticket}, nil
}

// Release drops the lock. It is idempotent, and it is only a tidiness: the
// kernel releases the lock when the process exits by any route, which is
// what makes a dead holder's ticket immediately workable again.
func (l *TicketLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	err := unlock(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// readHolder returns whatever the holder wrote about itself, or a stand-in
// when there is nothing readable there.
func readHolder(f *os.File) string {
	buf := make([]byte, 256)
	n, _ := f.ReadAt(buf, 0)
	if line := strings.TrimSpace(string(buf[:n])); line != "" {
		return strings.SplitN(line, "\n", 2)[0]
	}
	return "holder unknown"
}
