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
	"github.com/mattwalters/wand/internal/queue"
	"github.com/mattwalters/wand/internal/sweep"
)

// WatchDeps is what a poll loop needs beyond one pass's own Deps: the
// wand binary to re-invoke for a winner, and how long to sleep between
// polls.
type WatchDeps struct {
	Deps

	// Bin is the wand binary a winner is spawned through — normally
	// os.Args[0]. A winner runs as `<Bin> run <id>` or `<Bin> plan <id>`,
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
	// Sweep, when set, is run once at the top of every Tick, before
	// dispatch reads Todo/To Plan or counts lanes — nil means no sweep,
	// preserving today's dispatch-only behavior for any caller that does
	// not wire it (including every existing test). Sweep-first is chosen
	// for narration simplicity, not because it changes lane math:
	// LanesUsed already excludes a dead-lease report by its lease's
	// live-computed liveness regardless of whether sweep has reaped it yet
	// (see select.go's LanesUsed), and none of sweep's own targets
	// (re-plan/re-review labeled tickets, unresolved-thread tickets, or a
	// just-reaped dead-lease ticket handed to Needs Input) land in
	// Todo/To Plan as a same-pass side effect — so dispatch's read in the
	// same tick cannot pick up a ticket sweep just touched, whichever order
	// the two run in.
	Sweep *sweep.Deps
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

	p := NewPending()
	var lastState string
	fmt.Fprintf(w.Out, "dispatch: watching %s (poll every %s, %d lane(s))\n", w.Repo, w.Interval, w.Cov.Caps.Lanes)

	for {
		res, err := w.Tick(ctx, store, p, logDir)
		if err != nil {
			fmt.Fprintf(w.Out, "dispatch: %v\n", err)
		} else if res.Summary != lastState {
			fmt.Fprintln(w.Out, res.Summary)
			lastState = res.Summary
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(w.Interval):
		}
	}
}

// pendingChild is one spawned-but-not-yet-exited child, along with the verb
// it was dispatched as: count() needs the verb to know whether the child
// occupies a lane.
type pendingChild struct {
	cmd  *exec.Cmd
	verb Verb
}

// Pending tracks children one polling session — `dispatch --watch`'s own
// loop, or home's engage-mode loop driving [WatchDeps.Tick] itself —
// has spawned but that have not yet exited, so a poll immediately after a
// spawn does not re-read the journal, see no run registered yet, and
// dispatch a second winner into a lane the first has already claimed.
//
// Exported so a caller outside this package can hold one across repeated
// calls to Tick: the bookkeeping is session-scoped, not tick-scoped, and a
// caller that made a new one every tick would lose exactly the protection
// this type exists to provide.
type Pending struct {
	mu       sync.Mutex
	children map[string]pendingChild
}

// NewPending returns an empty session, ready for repeated Tick calls.
func NewPending() *Pending {
	return &Pending{children: map[string]pendingChild{}}
}

// count returns the number of lanes this session's spawned-but-unregistered
// children occupy. A plan child never counts here — a plan run needs no
// lane, same as [LanesUsed] excludes a registered plan run — otherwise a
// dispatched To Plan ticket would starve Todo for the length of its own
// pass, the exact starvation the lane split exists to prevent.
func (p *Pending) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.children {
		if c.verb == VerbRun {
			n++
		}
	}
	return n
}

func (p *Pending) add(ticket string, verb Verb, cmd *exec.Cmd) {
	p.mu.Lock()
	p.children[ticket] = pendingChild{cmd: cmd, verb: verb}
	p.mu.Unlock()
	go func() {
		cmd.Wait()
		p.mu.Lock()
		delete(p.children, ticket)
		p.mu.Unlock()
	}()
}

// TickResult is one poll's outcome: enough for both --watch's own
// state-change-only logging and a caller like home's engage mode to
// describe precisely what happened, without either re-deriving it from a
// log line.
type TickResult struct {
	// Dispatched reports whether a winner was spawned this tick.
	Dispatched bool
	// Winner is the spawned winner. Only meaningful when Dispatched.
	Winner Winner
	// PID is the spawned child's process id. Only meaningful when Dispatched.
	PID int
	// LanesUsed and LanesCap are lane occupancy as of this tick.
	LanesUsed, LanesCap int
	// Summary is the one-line, human-readable report `wand dispatch
	// --watch` prints on a state change.
	Summary string

	// Swept reports whether sweep acted this tick. Always false when
	// w.Sweep is nil.
	Swept bool
	// SweptKind, SweptTicket and SweptReason describe what sweep did. Only
	// meaningful when Swept.
	SweptKind                sweep.ActedKind
	SweptTicket, SweptReason string
}

// Tick is one poll: read, select, maybe spawn. It is non-blocking — every
// wait this method itself performs is an ordinary network or process-start
// call, never a sleep — which is what lets a caller drive its own polling
// schedule around it instead of being driven by Watch's own loop. Watch
// calls this in its for-loop; home's engage mode calls it from a
// tea.Cmd fired on its own interval, holding the same *Pending across ticks
// the way Watch holds one across its own.
func (w WatchDeps) Tick(ctx context.Context, store *journal.Store, p *Pending, logDir string) (TickResult, error) {
	var swept bool
	var sweptKind sweep.ActedKind
	var sweptTicket, sweptReason string
	if w.Sweep != nil {
		sres, err := sweep.Execute(ctx, *w.Sweep, store)
		if err != nil {
			return TickResult{}, fmt.Errorf("sweep: %w", err)
		}
		if sres.Acted != sweep.ActedNothing {
			swept = true
			sweptKind = sres.Acted
			sweptTicket = sres.Candidate.Ticket
			sweptReason = sres.Candidate.Reason
		}
	}

	ids, err := w.Runs.List()
	if err != nil {
		return TickResult{}, fmt.Errorf("listing runs: %w", err)
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
		return TickResult{}, fmt.Errorf("reading Todo: %w", err)
	}
	toPlan, err := w.Board.TeamIssuesByState(ctx, w.TeamKey, w.Cov.StatusName("to_plan"))
	if err != nil {
		return TickResult{}, fmt.Errorf("reading To Plan: %w", err)
	}

	winner, ok, todoSkips, toPlanSkips := Select(todo, toPlan, laneFree)
	if !ok {
		skips := append(append([]queue.Skip{}, todoSkips...), toPlanSkips...)
		return TickResult{
			Swept:       swept,
			SweptKind:   sweptKind,
			SweptTicket: sweptTicket,
			SweptReason: sweptReason,
			LanesUsed:   used,
			LanesCap:    w.Cov.Caps.Lanes,
			Summary: sweptSummary(swept, sweptKind, sweptTicket, sweptReason,
				IdleReason(used, w.Cov.Caps.Lanes, laneFree, skips)),
		}, nil
	}

	cmd, err := w.spawn(winner, logDir)
	if err != nil {
		return TickResult{}, fmt.Errorf("spawning %s: %w", winner.Issue.Identifier, err)
	}
	p.add(winner.Issue.Identifier, winner.Verb, cmd)
	// Only a build winner takes a lane. Reporting used+1 for a plan winner
	// would claim an occupancy the very next tick contradicts, since
	// neither [Pending.count] nor [LanesUsed] counts a plan run.
	now := used
	if winner.Verb == VerbRun {
		now++
	}
	return TickResult{
		Dispatched:  true,
		Winner:      winner,
		PID:         cmd.Process.Pid,
		Swept:       swept,
		SweptKind:   sweptKind,
		SweptTicket: sweptTicket,
		SweptReason: sweptReason,
		LanesUsed:   now,
		LanesCap:    w.Cov.Caps.Lanes,
		Summary: sweptSummary(swept, sweptKind, sweptTicket, sweptReason,
			fmt.Sprintf("dispatched %s %s (pid %d, %d/%d lanes in use)",
				winner.Verb, winner.Issue.Identifier, cmd.Process.Pid, now, w.Cov.Caps.Lanes)),
	}, nil
}

// sweptSummary folds sweep's outcome, if any, into the same one-line
// summary dispatch's own idle/dispatched report already is — a tick that
// both swept and dispatched reads as one line, not two.
func sweptSummary(swept bool, kind sweep.ActedKind, ticket, reason, dispatchTail string) string {
	if !swept {
		return "dispatch: " + dispatchTail
	}
	return fmt.Sprintf("dispatch: swept %s %s (%s); %s", kind, ticket, reason, dispatchTail)
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
