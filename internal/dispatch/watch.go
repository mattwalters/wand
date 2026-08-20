package dispatch

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

// WatchDeps is what a poll loop needs beyond one pass's own Deps: the
// wand binary to re-invoke for a winner, and how long to sleep between
// polls.
type WatchDeps struct {
	Deps

	// Bin is the wand binary a winner is spawned through — normally
	// os.Args[0]. A winner runs as `<Bin> run <id>` or `<Bin> scope <id>`,
	// never in-process: a detached child is what survives the watcher, and
	// a goroutine cannot.
	Bin string
	// Interval is how long a poll sleeps between passes.
	Interval time.Duration
	// LogDir is where a spawned winner's stdout/stderr goes — one file per
	// child, since `wand run`'s own narration is useful for debugging a
	// watch session and the journal only carries the structured half of it.
	// Defaults to <store root>/dispatch/logs.
	LogDir string
}

func (w WatchDeps) validate() error {
	if w.Bin == "" {
		return fmt.Errorf("dispatch: WatchDeps.Bin is required")
	}
	if w.Interval <= 0 {
		return fmt.Errorf("dispatch: WatchDeps.Interval must be positive")
	}
	return nil
}

// Watch polls until ctx is done: each tick it gc's dead leases, counts
// lanes (including children this session has already spawned but that may
// not have registered a run yet), and — when a winner exists and either a
// lane is free or the winner needs none — spawns it as a detached child
// that survives this process. Logging is state-change-only: a tick that
// changes nothing prints nothing, so a long-running watch does not drown
// its own signal in a poll interval's worth of silence restated forever.
//
// The dispatch lock is held once, for Watch's whole lifetime — see the
// package doc for why concurrency across lanes comes from here, spawning
// children, rather than from multiple dispatch processes racing selection.
func Watch(ctx context.Context, w WatchDeps, store *journal.Store) error {
	if err := w.validate(); err != nil {
		return err
	}
	if err := w.Deps.validate(); err != nil {
		return err
	}
	if w.Out == nil {
		w.Out = io.Discard
	}
	logDir := w.LogDir
	if logDir == "" {
		logDir = filepath.Join(store.Root, "dispatch", "logs")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("dispatch: creating the watch log directory: %w", err)
	}

	lock, err := Acquire(store.Root, w.Repo)
	if err != nil {
		return err
	}
	defer lock.Release()

	p := &pending{children: map[string]*exec.Cmd{}}
	var lastState string
	fmt.Fprintf(w.Out, "dispatch: watching %s (poll every %s, %d lane(s))\n", w.Repo, w.Interval, w.Cov.Caps.Lanes)

	for {
		state, err := w.tick(ctx, store, p, logDir)
		if err != nil {
			fmt.Fprintf(w.Out, "dispatch: %v\n", err)
		} else if state != lastState {
			fmt.Fprintln(w.Out, state)
			lastState = state
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(w.Interval):
		}
	}
}

// pending tracks children this watch session has spawned but that have not
// yet exited, so a poll immediately after a spawn does not re-read the
// journal, see no run registered yet, and dispatch a second winner into a
// lane the first has already claimed.
type pending struct {
	mu       sync.Mutex
	children map[string]*exec.Cmd
}

func (p *pending) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.children)
}

func (p *pending) add(ticket string, cmd *exec.Cmd) {
	p.mu.Lock()
	p.children[ticket] = cmd
	p.mu.Unlock()
	go func() {
		cmd.Wait()
		p.mu.Lock()
		delete(p.children, ticket)
		p.mu.Unlock()
	}()
}

// tick is one poll: read, select, maybe spawn. It returns a short summary
// for state-change-only logging.
func (w WatchDeps) tick(ctx context.Context, store *journal.Store, p *pending, logDir string) (string, error) {
	ids, err := w.Runs.List()
	if err != nil {
		return "", fmt.Errorf("listing runs: %w", err)
	}
	var reports []journal.Report
	for _, id := range ids {
		r, err := w.Runs.Inspect(id)
		if err != nil {
			continue
		}
		reports = append(reports, r)
	}
	used := LanesUsed(reports, w.Repo) + p.count()
	laneFree := used < w.Cov.Caps.Lanes

	todo, err := w.Board.TeamIssuesByState(ctx, w.TeamKey, w.Cov.StatusName("todo"))
	if err != nil {
		return "", fmt.Errorf("reading Todo: %w", err)
	}
	scoping, err := w.Board.TeamIssuesByState(ctx, w.TeamKey, w.Cov.StatusName("scoping"))
	if err != nil {
		return "", fmt.Errorf("reading Scoping: %w", err)
	}

	winner, ok, _, _ := Select(todo, scoping, laneFree)
	if !ok {
		return fmt.Sprintf("dispatch: idle (%d/%d lanes in use)", used, w.Cov.Caps.Lanes), nil
	}

	cmd, err := w.spawn(winner, logDir)
	if err != nil {
		return "", fmt.Errorf("spawning %s: %w", winner.Issue.Identifier, err)
	}
	p.add(winner.Issue.Identifier, cmd)
	return fmt.Sprintf("dispatch: dispatched %s %s (pid %d, %d/%d lanes now in use)",
		winner.Verb, winner.Issue.Identifier, cmd.Process.Pid, used+1, w.Cov.Caps.Lanes), nil
}

// spawn starts the winner's loop as a detached child: its own session, so
// neither a signal to this process's controlling terminal nor this
// process's own context cancellation reaches it, and no context is used to
// start it in the first place. Surviving the watcher is the point.
func (w WatchDeps) spawn(winner Winner, logDir string) (*exec.Cmd, error) {
	args := []string{string(winner.Verb), winner.Issue.Identifier}
	if w.Harness != "" {
		args = append(args, "--harness", w.Harness)
	}
	if w.Model != "" {
		args = append(args, "--model", w.Model)
	}
	if w.Effort != "" {
		args = append(args, "--effort", w.Effort)
	}

	cmd := exec.Command(w.Bin, args...)
	cmd.Dir = w.Repo

	logPath := filepath.Join(logDir, fmt.Sprintf("%s-%s.log", winner.Issue.Identifier, time.Now().UTC().Format("20060102T150405Z")))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", logPath, err)
	}
	cmd.Stdout, cmd.Stderr = f, f
	cmd.Stdin = nil
	detach(cmd)

	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, err
	}
	// The child has its own copy of the fd now; this process's is no
	// longer needed. Closing it here, not on Wait, is what lets the child
	// keep writing to it after this process exits.
	f.Close()
	return cmd, nil
}
