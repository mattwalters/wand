package dispatch

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
)

// TestWatchSpawnsAWinnerAndStopsRedispatchingItBeforeTheJournalCatchesUp
// pins the reason `pending` exists: a poll interval short enough to fire
// again before the spawned child has had a chance to open its own run must
// still not double-dispatch the same lane.
func TestWatchSpawnsAndTracksPending(t *testing.T) {
	store := journal.New(t.TempDir())
	board := &fakeBoard{todo: []linear.Issue{
		{Identifier: "WND-1", Title: "one", State: linear.IssueState{Name: "Todo"}, CreatedAt: time.Now()},
	}}
	cov := covenant.Default()
	cov.Caps.Lanes = 1

	w := WatchDeps{
		Deps: Deps{
			Board: board, Cov: cov, Runs: &fakeRuns{reports: map[string]journal.Report{}},
			Git: unimplementedGit{}, Hub: unimplementedHub{}, Shell: unimplementedShell{},
			Tree: unimplementedTree{}, Workers: unimplementedWorkers{},
			TeamKey: "WND", Repo: t.TempDir(),
		},
		Bin:      sleeperBinary(t),
		Interval: time.Hour, // never fires again inside this test
	}

	p := &pending{children: map[string]pendingChild{}}
	logDir := t.TempDir()

	summary, err := w.tick(context.Background(), store, p, logDir)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !strings.Contains(summary, "WND-1") {
		t.Errorf("summary = %q, want it to name WND-1", summary)
	}
	if p.count() != 1 {
		t.Fatalf("pending.count() = %d, want 1 right after spawning", p.count())
	}

	// A second tick, immediately: the journal has nothing new yet (the
	// fake sleeper hasn't run.Execute'd anything — it is a stand-in
	// process, not wand), but pending must still report the lane in use.
	summary2, err := w.tick(context.Background(), store, p, logDir)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if !strings.Contains(summary2, "idle") {
		t.Errorf("second tick summary = %q, want idle: the lane pending already claims must not be dispatched into twice", summary2)
	}

	// Let the spawned process finish and confirm pending drains.
	waitForPending(t, p, 0)
}

// TestWatchScopingWinnerDoesNotOccupyALane pins the addendum's invariant
// down to pending, not just LanesUsed: a Scoping winner spawned this session
// must not inflate the lane count while its child is still running, or a
// long-lived scope would starve every Todo ticket behind it for the length
// of its own pass — exactly backwards from "a scope needs no lane."
func TestWatchScopingWinnerDoesNotOccupyALane(t *testing.T) {
	store := journal.New(t.TempDir())
	board := &fakeBoard{scoping: []linear.Issue{
		{Identifier: "WND-2", Title: "needs scoping", State: linear.IssueState{Name: "Scoping"}, CreatedAt: time.Now()},
	}}
	cov := covenant.Default()
	cov.Caps.Lanes = 1

	w := WatchDeps{
		Deps: Deps{
			Board: board, Cov: cov, Runs: &fakeRuns{reports: map[string]journal.Report{}},
			Git: unimplementedGit{}, Hub: unimplementedHub{}, Shell: unimplementedShell{},
			Tree: unimplementedTree{}, Workers: unimplementedWorkers{},
			TeamKey: "WND", Repo: t.TempDir(),
		},
		Bin:      sleeperBinary(t),
		Interval: time.Hour,
	}

	p := &pending{children: map[string]pendingChild{}}
	logDir := t.TempDir()

	summary, err := w.tick(context.Background(), store, p, logDir)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !strings.Contains(summary, "WND-2") {
		t.Errorf("summary = %q, want it to name WND-2", summary)
	}
	if got := p.count(); got != 0 {
		t.Fatalf("pending.count() = %d, want 0: a scope child holds no lane", got)
	}

	// A Todo ticket now shows up while the scope child is still (per
	// sleeperBinary, briefly) running. With one lane and zero run winners
	// pending, it must still be free to dispatch.
	board.todo = []linear.Issue{
		{Identifier: "WND-1", Title: "one", State: linear.IssueState{Name: "Todo"}, CreatedAt: time.Now()},
	}
	summary2, err := w.tick(context.Background(), store, p, logDir)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if !strings.Contains(summary2, "WND-1") {
		t.Errorf("second tick summary = %q, want it to dispatch WND-1: the in-flight scope must not have starved the lane", summary2)
	}

	waitForPending(t, p, 0)
}

func waitForPending(t *testing.T, p *pending, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p.count() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pending.count() never reached %d, stuck at %d", want, p.count())
}

// sleeperBinary builds a tiny helper binary that exits immediately, so
// spawn() has something real to os/exec.Start without depending on wand's
// own binary or touching Linear.
func sleeperBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := dir + "/main.go"
	if err := os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := dir + "/sleeper"
	cmd := exec.Command("go", "build", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the test helper binary: %v\n%s", err, out)
	}
	return bin
}
