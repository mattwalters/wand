package journal_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

func TestLockTicketRefusesASecondHolder(t *testing.T) {
	s, _ := fixture(t)

	first, err := s.LockTicket("WND-9")
	if err != nil {
		t.Fatalf("LockTicket: %v", err)
	}
	defer first.Release()

	// A second open of the same file description is refused exactly as
	// another process would be: a plan run cannot mistake itself for a
	// vacancy.
	_, err = s.LockTicket("WND-9")
	if err == nil {
		t.Fatal("a second lock on the same ticket was granted")
	}
	// The refusal has to name the holder, or "somebody has it" is not
	// something a human can act on.
	if !strings.Contains(err.Error(), "pid "+strconv.Itoa(os.Getpid())) {
		t.Errorf("refusal does not name the holder: %v", err)
	}

	// Another ticket is another lock.
	other, err := s.LockTicket("WND-10")
	if err != nil {
		t.Fatalf("LockTicket on a different ticket: %v", err)
	}
	other.Release()
}

func TestLockTicketIsRetakenAfterRelease(t *testing.T) {
	s, _ := fixture(t)

	first, err := s.LockTicket("WND-9")
	if err != nil {
		t.Fatalf("LockTicket: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Errorf("a second Release should be a no-op, got %v", err)
	}

	second, err := s.LockTicket("WND-9")
	if err != nil {
		t.Fatalf("LockTicket after release: %v", err)
	}
	second.Release()
}

// The property under test is the kernel's, not the code's: a process killed
// outright never runs a deferred Release, and the ticket must be workable
// again anyway. A fake that unlocks on request would be testing the fake —
// see crash_test.go for the same argument at run granularity.
func TestLockTicketSurvivesTheHoldersDeath(t *testing.T) {
	if os.Getenv("WAND_TICKET_LOCK_CHILD") != "" {
		holdTicketLockForever()
		return
	}
	root := t.TempDir()

	cmd := exec.Command(os.Args[0], "-test.run=TestLockTicketSurvivesTheHoldersDeath", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), "WAND_TICKET_LOCK_CHILD=1", "WAND_TICKET_LOCK_ROOT="+root)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("StderrPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the child: %v", err)
	}
	defer cmd.Wait()

	// The child prints one line once it holds the lock.
	buf := make([]byte, 64)
	if _, err := stderr.Read(buf); err != nil {
		t.Fatalf("waiting for the child to take the lock: %v", err)
	}

	s := journal.New(root)
	if _, err := s.LockTicket("WND-9"); err == nil {
		t.Fatal("the live child's lock was granted to this process")
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("killing the child: %v", err)
	}
	cmd.Wait()

	lock, err := waitForTicketLock(s)
	if err != nil {
		t.Fatalf("the killed holder's lock was never released: %v", err)
	}
	lock.Release()
}

// holdTicketLockForever is the child half: take the lock, say so, and sleep
// until killed. It sleeps rather than blocking on a channel — a child parked
// on `select {}` trips Go's deadlock detector and ends itself, which is not
// the death under test.
func holdTicketLockForever() {
	s := journal.New(os.Getenv("WAND_TICKET_LOCK_ROOT"))
	if _, err := s.LockTicket("WND-9"); err != nil {
		os.Stderr.WriteString("child could not lock: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Stderr.WriteString("held\n")
	time.Sleep(time.Minute)
}

// waitForTicketLock retries briefly: the kernel drops the lock as the
// process is reaped, which Wait has usually but not always observed.
func waitForTicketLock(s *journal.Store) (*journal.TicketLock, error) {
	var err error
	for i := 0; i < 100; i++ {
		var lock *journal.TicketLock
		lock, err = s.LockTicket("WND-9")
		if err == nil {
			return lock, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, err
}

func TestLockTicketWritesOneFilePerTicket(t *testing.T) {
	s, _ := fixture(t)
	lock, err := s.LockTicket("WND-9")
	if err != nil {
		t.Fatalf("LockTicket: %v", err)
	}
	defer lock.Release()

	// The slug keeps the identifier readable, and a ticket whose
	// identifier is not a safe filename must not escape the directory.
	if _, err := os.Stat(filepath.Join(s.Root, "tickets", "WND-9.lock")); err != nil {
		t.Errorf("no lock file for the ticket: %v", err)
	}
	odd, err := s.LockTicket("../../escape")
	if err != nil {
		t.Fatalf("LockTicket: %v", err)
	}
	defer odd.Release()
	entries, err := os.ReadDir(filepath.Join(s.Root, "tickets"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("locks directory holds %d files, want 2 — a lock escaped it", len(entries))
	}
}
