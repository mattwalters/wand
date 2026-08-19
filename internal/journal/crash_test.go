package journal_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

// The crash tests spawn this test binary as a child, let it take a run, and
// then kill it outright. Nothing softer proves the property: the point of
// the lock is that it is released by the kernel when a process dies in a
// way the process itself never gets to handle, and a fake that unlocked on
// request would be testing the fake.
const (
	holderEnv = "WAND_JOURNAL_TEST_HOLDER"
	rootEnv   = "WAND_JOURNAL_TEST_ROOT"
	repoEnv   = "WAND_JOURNAL_TEST_REPO"
	tornEnv   = "WAND_JOURNAL_TEST_TORN"
)

// notKilled is the exit code the holder uses if it ever stops waiting on
// its own. Signal exit codes differ across platforms, so the test asserts
// on this rather than on how a kill happens to be reported.
const notKilled = 9

func TestMain(m *testing.M) {
	if os.Getenv(holderEnv) != "" {
		holdForever()
	}
	os.Exit(m.Run())
}

// holdForever is the child: it takes a run, announces its id, and waits to
// be killed. It never returns.
func holdForever() {
	s := journal.New(os.Getenv(rootEnv))
	r, err := s.Create(journal.Meta{Ticket: "WND-7", Verb: "run", Repo: os.Getenv(repoEnv)})
	if err != nil {
		fmt.Fprintln(os.Stderr, "holder:", err)
		os.Exit(1)
	}
	if err := r.StartPhase("implement", 1); err != nil {
		fmt.Fprintln(os.Stderr, "holder:", err)
		os.Exit(1)
	}
	if os.Getenv(tornEnv) != "" {
		// The half-written record of a process that died mid-append.
		f, err := os.OpenFile(filepath.Join(s.Dir(r.ID()), "journal.jsonl"), os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, "holder:", err)
			os.Exit(1)
		}
		f.WriteString(`{"seq":3,"kind":"phase.end`)
		f.Sync()
		f.Close()
	}
	fmt.Println(r.ID())
	// Wait to be killed. A bare `select {}` would trip Go's deadlock
	// detector and end the process by its own hand, which is not the death
	// under test; a pending timer keeps the runtime waiting quietly.
	time.Sleep(time.Hour)
	fmt.Fprintln(os.Stderr, "holder: nobody killed me")
	os.Exit(notKilled)
}

// spawnHolder starts a holder and waits until it has taken the run. The
// returned kill function ends it without warning and waits for the reap:
// until the process is gone its file descriptors are still open, and the
// lock is still held.
func spawnHolder(t *testing.T, root, repo string, torn bool) (id string, kill func()) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		holderEnv+"=1",
		rootEnv+"="+root,
		repoEnv+"="+repo,
	)
	if torn {
		cmd.Env = append(cmd.Env, tornEnv+"=1")
	}
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the holder: %v", err)
	}
	var once sync.Once
	kill = func() {
		once.Do(func() {
			if err := cmd.Process.Kill(); err != nil {
				t.Errorf("killing the holder: %v", err)
				return
			}
			err := cmd.Wait()
			// The kill has to be what ended it. A child that exited on
			// its own would still release the lock, and the test would
			// pass while proving nothing about a process dying without
			// warning.
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ProcessState.ExitCode() == notKilled {
				t.Errorf("the holder was not killed; it exited on its own (%v)", err)
			}
		})
	}
	t.Cleanup(kill)

	id, err = firstLine(stdout, 30*time.Second)
	if err != nil {
		kill()
		t.Fatalf("waiting for the holder to take a run: %v", err)
	}
	return id, kill
}

// spawnAndKill starts a holder, waits until it has taken the run, and then
// kills it without warning. It returns the run's id.
func spawnAndKill(t *testing.T, root, repo string, torn bool) string {
	t.Helper()
	id, kill := spawnHolder(t, root, repo, torn)
	kill()
	return id
}

func firstLine(r io.Reader, within time.Duration) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		ch <- result{strings.TrimSpace(line), err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			return "", res.err
		}
		return res.line, nil
	case <-time.After(within):
		return "", fmt.Errorf("the holder printed nothing within %s", within)
	}
}

// The zombie: a run whose journal says it is mid-phase and whose holder is
// gone. It is the state machine's most dangerous state because it looks
// healthy from Linear — the ticket sits In Progress and nothing drains it.
// The lock is what makes the death provable rather than suspected.
func TestKilledHolderIsProvablyDead(t *testing.T) {
	root, repo := t.TempDir(), t.TempDir()
	id := spawnAndKill(t, root, repo, false)

	s := journal.New(root)
	rep, err := s.Inspect(id)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if rep.Live != journal.Dead {
		t.Errorf("Live = %s after the holder was killed, want dead", rep.Live)
	}
	if !rep.Zombie() {
		t.Error("a mid-phase run with a dead holder is not reported as a zombie")
	}
	if rep.Lease.PID == 0 {
		t.Error("the lease of a killed holder is gone; nothing names who died")
	}
	if rep.State.Phase != "implement" || rep.State.PhaseDone {
		t.Errorf("state = %q done=%v, want the interrupted phase left open",
			rep.State.Phase, rep.State.PhaseDone)
	}
}

// Recovery is one command, not archaeology: the journal already says where
// the run was, and re-running a phase is safe because workers are cold.
func TestReopenRecoversAKilledRun(t *testing.T) {
	root, repo := t.TempDir(), t.TempDir()
	id := spawnAndKill(t, root, repo, false)

	s := journal.New(root)
	before, err := s.State(id)
	if err != nil {
		t.Fatalf("State: %v", err)
	}

	r, err := s.Reopen(id)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer r.Close()

	if r.ID() != id || r.Meta().Ticket != "WND-7" {
		t.Errorf("reopened run = %s/%s, want %s/WND-7", r.ID(), r.Meta().Ticket, id)
	}
	after, err := s.State(id)
	if err != nil {
		t.Fatalf("State after Reopen: %v", err)
	}
	if after.Resumes != before.Resumes+1 {
		t.Errorf("Resumes = %d, want %d", after.Resumes, before.Resumes+1)
	}
	if after.Phase != "implement" || after.PhaseDone {
		t.Errorf("resume point = %q done=%v, want the interrupted phase re-entered",
			after.Phase, after.PhaseDone)
	}
	// The resume record explains the gap to whoever reads the journal next.
	recs, err := s.Records(id)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	var resumed journal.Record
	for _, rec := range recs {
		if rec.Kind == journal.KindResumed {
			resumed = rec
		}
	}
	if !strings.Contains(resumed.Reason, "left open") {
		t.Errorf("resume record reads %q; it should say what was interrupted", resumed.Reason)
	}
	if resumed.PID != os.Getpid() {
		t.Errorf("resume record names pid %d, want this process (%d)", resumed.PID, os.Getpid())
	}

	// A resumed run still ends exactly once.
	if err := r.Converged("finished on the second pass"); err != nil {
		t.Fatalf("Converged: %v", err)
	}
	final, err := s.State(id)
	if err != nil {
		t.Fatalf("State after ending: %v", err)
	}
	if final.Outcome != journal.Converged {
		t.Errorf("outcome = %q, want converged", final.Outcome)
	}
}

// Dropping a torn line on read is not enough once we are about to append:
// the fragment would end up mid-stream, where it is loss rather than
// interruption, and every later read of the run would refuse.
func TestReopenTruncatesATornTail(t *testing.T) {
	root, repo := t.TempDir(), t.TempDir()
	id := spawnAndKill(t, root, repo, true)

	s := journal.New(root)
	r, err := s.Reopen(id)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer r.Close()

	if err := r.StartPhase("implement", 2); err != nil {
		t.Fatalf("StartPhase: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir(id), "journal.jsonl"))
	if err != nil {
		t.Fatalf("reading the journal: %v", err)
	}
	if strings.Contains(string(raw), `"phase.end`) {
		t.Errorf("the torn fragment survived the resume:\n%s", raw)
	}
	if _, err := s.State(id); err != nil {
		t.Errorf("the journal no longer replays after a resume over a torn tail: %v", err)
	}
}

// Reopen takes the lock before it reads, and this is why: a refused reopen
// must not have touched the journal. If it read first and truncated after,
// a holder that appended in between — and only then died — would lose the
// record it had legitimately written.
func TestRefusedReopenLeavesTheJournalAlone(t *testing.T) {
	root, repo := t.TempDir(), t.TempDir()
	id, kill := spawnHolder(t, root, repo, true)

	s := journal.New(root)
	path := filepath.Join(s.Dir(id), "journal.jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the journal: %v", err)
	}
	if !strings.Contains(string(before), `"phase.end`) {
		t.Fatalf("the holder did not leave a torn fragment, so this test proves nothing:\n%s", before)
	}
	if _, err := s.Reopen(id); err == nil {
		t.Fatal("Reopen took a run its holder was still running")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the journal: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("a refused Reopen rewrote the journal:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	kill()
}
