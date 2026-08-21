package dispatch

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/sweep"
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

	p := NewPending()
	logDir := t.TempDir()

	res, err := w.Tick(context.Background(), store, p, logDir)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !strings.Contains(res.Summary, "WND-1") {
		t.Errorf("summary = %q, want it to name WND-1", res.Summary)
	}
	if p.count() != 1 {
		t.Fatalf("pending.count() = %d, want 1 right after spawning", p.count())
	}

	// A second tick, immediately: the journal has nothing new yet (the
	// fake sleeper hasn't run.Execute'd anything — it is a stand-in
	// process, not wand), but pending must still report the lane in use.
	res2, err := w.Tick(context.Background(), store, p, logDir)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if !strings.Contains(res2.Summary, "idle") {
		t.Errorf("second tick summary = %q, want idle: the lane pending already claims must not be dispatched into twice", res2.Summary)
	}

	// Let the spawned process finish and confirm pending drains.
	waitForPending(t, p, 0)
}

// TestWatchToPlanWinnerDoesNotOccupyALane pins the addendum's invariant
// down to pending, not just LanesUsed: a To Plan winner spawned this session
// must not inflate the lane count while its child is still running, or a
// long-lived plan run would starve every Todo ticket behind it for the length
// of its own pass — exactly backwards from "a plan run needs no lane."
func TestWatchToPlanWinnerDoesNotOccupyALane(t *testing.T) {
	store := journal.New(t.TempDir())
	board := &fakeBoard{toPlan: []linear.Issue{
		{Identifier: "WND-2", Title: "needs planning", State: linear.IssueState{Name: "To Plan"}, CreatedAt: time.Now()},
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

	p := NewPending()
	logDir := t.TempDir()

	res, err := w.Tick(context.Background(), store, p, logDir)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !strings.Contains(res.Summary, "WND-2") {
		t.Errorf("summary = %q, want it to name WND-2", res.Summary)
	}
	if got := p.count(); got != 0 {
		t.Fatalf("pending.count() = %d, want 0: a plan child holds no lane", got)
	}
	// The reported occupancy has to agree with p.count(), not with "one more
	// than before": a summary claiming 1/1 for a plan winner is one the very
	// next tick contradicts by dispatching into the lane it just claimed was
	// taken.
	if res.LanesUsed != 0 {
		t.Errorf("LanesUsed = %d, want 0: a plan winner takes no lane", res.LanesUsed)
	}
	if strings.Contains(res.Summary, "1/1") {
		t.Errorf("summary = %q, want it not to claim an occupied lane for a plan winner", res.Summary)
	}

	// A Todo ticket now shows up while the plan child is still (per
	// sleeperBinary, briefly) running. With one lane and zero run winners
	// pending, it must still be free to dispatch.
	board.todo = []linear.Issue{
		{Identifier: "WND-1", Title: "one", State: linear.IssueState{Name: "Todo"}, CreatedAt: time.Now()},
	}
	res2, err := w.Tick(context.Background(), store, p, logDir)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if !strings.Contains(res2.Summary, "WND-1") {
		t.Errorf("second tick summary = %q, want it to dispatch WND-1: the in-flight plan run must not have starved the lane", res2.Summary)
	}

	waitForPending(t, p, 0)
}

// TestTickIdlesWhenNoLaneAndNoToPlanWinner pins Tick's own idle path,
// independent of Watch's loop: a full lane and an empty To Plan queue must
// report Dispatched=false with an idle summary, not merely fail to find a
// Todo winner. This is the case an engage-mode caller needs to distinguish
// from a real dispatch, since it renders as "idle" rather than "dispatched".
func TestTickIdlesWhenNoLaneAndNoToPlanWinner(t *testing.T) {
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
		Interval: time.Hour,
	}

	p := NewPending()
	logDir := t.TempDir()

	// Fill the one lane, then confirm a second tick with no To Plan winner
	// idles rather than spawning a second run into the same lane.
	if _, err := w.Tick(context.Background(), store, p, logDir); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	board.todo = []linear.Issue{
		{Identifier: "WND-3", Title: "another", State: linear.IssueState{Name: "Todo"}, CreatedAt: time.Now()},
	}
	res, err := w.Tick(context.Background(), store, p, logDir)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if res.Dispatched {
		t.Fatalf("res.Dispatched = true, want false: no lane is free and To Plan is empty")
	}
	if !strings.Contains(res.Summary, "idle") {
		t.Errorf("summary = %q, want it to say idle", res.Summary)
	}

	waitForPending(t, p, 0)
}

// TestTickReapsADeadLeaseAndDispatchesATodoWinnerInTheSameTick proves the
// property WND-88 is actually about: sweep and dispatch coexist correctly
// within one Tick call, each carrying its own outcome without clobbering
// the other's fields or Summary. This is not a lane-freeing-order test —
// LanesUsed already excludes a dead-lease report by its lease's own
// live-computed liveness whether or not sweep has reaped it yet (see
// select.go's LanesUsed and select_test.go's pin of the same), so the one
// lane here is free before Tick runs at all, sweep or no sweep.
func TestTickReapsADeadLeaseAndDispatchesATodoWinnerInTheSameTick(t *testing.T) {
	store := journal.New(t.TempDir())
	repo := t.TempDir()

	board := &fakeBoard{
		todo: []linear.Issue{
			{Identifier: "WND-2", Title: "todo winner", State: linear.IssueState{Name: "Todo"}, CreatedAt: time.Now()},
		},
		issues: map[string]*linear.Issue{
			"WND-1": {ID: "id-1", Identifier: "WND-1", State: linear.IssueState{Name: "In Progress", Type: "started"}},
		},
		states: []linear.WorkflowState{
			{ID: "st-needs-input", Name: "Needs Input", Type: "unstarted"},
		},
		comments: map[string][]string{},
	}
	cov := covenant.Default()
	cov.Caps.Lanes = 1

	fabricateDeadRun(t, store, "run-1", journal.Meta{Ticket: "WND-1", Verb: "run", Repo: repo})

	w := WatchDeps{
		Deps: Deps{
			Board: board, Cov: cov, Runs: store,
			Git: unimplementedGit{}, Hub: unimplementedHub{}, Shell: unimplementedShell{},
			Tree: unimplementedTree{}, Workers: unimplementedWorkers{},
			TeamKey: "WND", Repo: repo,
		},
		Bin:      sleeperBinary(t),
		Interval: time.Hour,
		Sweep: &sweep.Deps{
			Board:   board,
			Hub:     unimplementedHub{},
			Runs:    store,
			Cov:     cov,
			TeamKey: "WND",
			Repo:    repo,
		},
	}

	p := NewPending()
	logDir := t.TempDir()

	res, err := w.Tick(context.Background(), store, p, logDir)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}

	if !res.Swept || res.SweptKind != sweep.ActedReaped || res.SweptTicket != "WND-1" {
		t.Fatalf("res = %+v, want the dead lease reaped in the same tick", res)
	}
	if got := board.issues["WND-1"].State.Name; got != "Needs Input" {
		t.Errorf("WND-1 state = %q, want Needs Input: sweep's hand-back", got)
	}
	if len(board.comments["WND-1"]) != 1 {
		t.Errorf("WND-1 comments = %v, want exactly one from the reap", board.comments["WND-1"])
	}
	rep, err := store.Inspect("run-1")
	if err != nil {
		t.Fatalf("inspecting run-1: %v", err)
	}
	if !rep.State.Ended() {
		t.Error("run-1's journal was not ended by the reap")
	}

	if !res.Dispatched || res.Winner.Issue.Identifier != "WND-2" {
		t.Fatalf("res = %+v, want WND-2 dispatched in the same tick", res)
	}
	if !strings.Contains(res.Summary, "WND-1") || !strings.Contains(res.Summary, "WND-2") {
		t.Errorf("summary = %q, want it to name both the swept WND-1 and the dispatched WND-2", res.Summary)
	}

	waitForPending(t, p, 0)
}

// fabricateDeadRun writes a run directory by hand, the way a crashed
// process would leave one: one run.started record and a lease, but no
// terminal record and no held lock — the same fixture
// internal/sweep/sweep_test.go uses to pin the same fact.
func fabricateDeadRun(t *testing.T, store *journal.Store, id string, m journal.Meta) {
	t.Helper()
	dir := store.Dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	rec := journal.Record{Seq: 1, At: time.Now(), Kind: journal.KindStarted, Run: &m, Host: host, PID: 999999}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "journal.jsonl"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	lease := journal.Lease{RunID: id, Ticket: m.Ticket, Host: host, PID: 999999, Taken: time.Now(), Renewed: time.Now()}
	raw, err = json.Marshal(lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lease.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitForPending(t *testing.T, p *Pending, want int) {
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
