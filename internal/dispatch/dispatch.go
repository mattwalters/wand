// Package dispatch is the selector over the loop: a thin, read-mostly pass
// that picks the one ticket a repository works next and runs it through the
// same orchestrators `wand run` and `wand plan` already ship.
//
// The Todo gate lives here, deliberately, and not in run.Execute or
// plan.Execute themselves: a human typing `wand run WND-9` has made the
// decision that WND-9 is the ticket to work; an unattended selector has
// not; the covenant's ranking and vetting is where that decision is made
// instead, the same read layer `wand queue` prints.
//
// A pass is: take the repo's own lock (lock.go — one selector at a time),
// gc dead leases out of the lane count (read-only; see [LanesUsed]), read
// Todo, To Plan and the re-plan-labeled slice of In Planning (a ticket wand
// sweep has already handed back for another planning cycle — see
// [rePlanEligible]), rank and vet each through the read layer, pick the one
// winner ([Select]), and run its loop to a terminal state — one ticket per
// pass. The lock is held for the whole of it: a single-shot pass commits to
// running one loop to completion, and a second concurrent pass against the
// same repo is a mistake to refuse, not a job to queue behind the first.
// Concurrency across lanes comes from [Watch] instead, which holds the lock
// once for its own lifetime and fans work out to detached children.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/plan"
	"github.com/mattwalters/wand/internal/queue"
	"github.com/mattwalters/wand/internal/run"
	"github.com/mattwalters/wand/internal/verbs"
)

// Kind is how a dispatch pass ended, for the caller and the scheduler.
type Kind string

const (
	KindConverged   Kind = "converged"
	KindHandedBack  Kind = "handed_back"
	KindParked      Kind = "parked"
	KindRefused     Kind = "refused"
	KindLocked      Kind = "locked"
	KindNothingToDo Kind = "nothing_to_do"
	KindUnreachable Kind = "unreachable"
)

// Exit codes are a contract a scheduler can read: a status and a log is a
// scheduler's whole view of a pass, so every ending it needs to tell apart
// gets its own code. 0 and 1 keep the meaning `wand run` and `wand plan`
// already gave them; the rest are new to dispatch.
const (
	ExitConverged    = 0
	ExitRefused      = 1
	ExitNonConverged = 2 // handed back or parked; the journal has the detail
	ExitLocked       = 3
	ExitNothingToDo  = 4
	ExitUnreachable  = 5
)

// Result is how one pass ended.
type Result struct {
	Kind   Kind
	Winner Winner
	Reason string
	RunID  string
}

// ExitCode maps the result onto the contract.
func (r Result) ExitCode() int {
	switch r.Kind {
	case KindConverged:
		return ExitConverged
	case KindLocked:
		return ExitLocked
	case KindNothingToDo:
		return ExitNothingToDo
	case KindUnreachable:
		return ExitUnreachable
	case KindRefused:
		return ExitRefused
	default: // handed back, parked
		return ExitNonConverged
	}
}

// Execute runs one dispatch pass: lock, gc, read, select, run the winner's
// loop. An error return means a wand bug or a misconfiguration that made
// the pass unable to even try — Deps validation, a store the lock directory
// could not be created under. Every other ending, including one nothing
// picked a winner for and one raced to a refused claim, is a [Result].
func Execute(ctx context.Context, d Deps, store *journal.Store) (Result, error) {
	if err := d.validate(); err != nil {
		return Result{}, err
	}
	if d.Out == nil {
		d.Out = io.Discard
	}

	lock, err := Acquire(store.Root, d.Repo)
	if err != nil {
		var locked *LockedError
		if errors.As(err, &locked) {
			return Result{Kind: KindLocked, Reason: locked.Error()}, nil
		}
		return Result{}, err
	}
	defer lock.Release()

	winner, ok, err := d.selectWinner(ctx, store)
	if err != nil {
		if unreachable(err) {
			return Result{Kind: KindUnreachable, Reason: err.Error()}, nil
		}
		return Result{}, err
	}
	if !ok {
		return Result{Kind: KindNothingToDo, Reason: "Todo and To Plan are both empty or fully vetted out"}, nil
	}
	fmt.Fprintf(d.Out, "dispatch: selected %s %s (%s)\n", winner.Verb, winner.Issue.Identifier, winner.Issue.Title)

	return d.runWinner(ctx, store, winner)
}

// selectWinner reads Todo and To Plan and gc's dead leases out of the lane
// count, then hands the read to [Select]. All reads, and the one place a
// Linear transport failure is told apart from every other error — the
// caller needs that distinction, and nowhere past this point does.
func (d Deps) selectWinner(ctx context.Context, store *journal.Store) (Winner, bool, error) {
	ids, err := d.Runs.List()
	if err != nil {
		return Winner{}, false, fmt.Errorf("dispatch: listing runs: %w", err)
	}
	var reports []journal.Report
	for _, id := range ids {
		r, err := d.Runs.Inspect(id)
		if err != nil {
			// A run whose journal will not replay cannot be attributed to
			// this repo at all — there is nothing here to read Meta.Repo
			// from — so it cannot be counted against this repo's lanes.
			// The cockpit's own reading of the store is where an
			// unreadable run is surfaced for a person.
			fmt.Fprintf(d.Out, "dispatch: run %s could not be inspected: %v\n", id, err)
			continue
		}
		reports = append(reports, r)
	}
	used := LanesUsed(reports, d.Repo)
	laneFree := used < d.Cov.Caps.Lanes

	todo, err := d.Board.TeamIssuesByState(ctx, d.TeamKey, d.Cov.StatusName("todo"))
	if err != nil {
		return Winner{}, false, fmt.Errorf("dispatch: reading Todo: %w", err)
	}
	toPlan, err := d.Board.TeamIssuesByState(ctx, d.TeamKey, d.Cov.StatusName("to_plan"))
	if err != nil {
		return Winner{}, false, fmt.Errorf("dispatch: reading To Plan: %w", err)
	}
	inPlanning, err := d.Board.TeamIssuesByState(ctx, d.TeamKey, d.Cov.StatusName("in_planning"))
	if err != nil {
		return Winner{}, false, fmt.Errorf("dispatch: reading In Planning: %w", err)
	}
	// A ticket In Planning carrying the re-plan label is one wand sweep has
	// already handed back for another cycle (verbs.ReturnToPlanning) —
	// resuming it needs no fresh blessing, unlike a re-review's trip
	// through Needs Input back to Todo, because the human's comments on
	// Plan Review already are the answer. One without the label is a live
	// plan run's own claim and is not a candidate for anything here.
	toPlan = append(toPlan, rePlanEligible(inPlanning)...)

	winner, ok, todoSkips, toPlanSkips := Select(todo, toPlan, laneFree)
	for _, s := range append(append([]queue.Skip{}, todoSkips...), toPlanSkips...) {
		fmt.Fprintf(d.Out, "dispatch: skipped %s: %s\n", s.Issue.Identifier, s.Reason)
	}
	if !laneFree {
		fmt.Fprintf(d.Out, "dispatch: %d/%d lanes in use; only a To Plan ticket may dispatch\n", used, d.Cov.Caps.Lanes)
	}
	return winner, ok, nil
}

// rePlanEligible filters In Planning issues down to the ones carrying
// verbs.RePlanLabel — a ticket wand sweep has handed back for another
// planning cycle, not one a live plan run currently holds.
func rePlanEligible(issues []linear.Issue) []linear.Issue {
	var out []linear.Issue
	for _, issue := range issues {
		for _, l := range issue.Labels {
			if strings.EqualFold(l, verbs.RePlanLabel) {
				out = append(out, issue)
				break
			}
		}
	}
	return out
}

// runWinner hands the winner to the orchestrator it belongs to. An error
// here means the claim raced and lost, or the run/plan journal would not
// open — the winner was chosen honestly and starting it was refused, which
// is [KindRefused] rather than a wand bug.
func (d Deps) runWinner(ctx context.Context, store *journal.Store, winner Winner) (Result, error) {
	switch winner.Verb {
	case VerbRun:
		out, err := run.Execute(ctx, run.Deps{
			Board:   d.Board,
			Cov:     d.Cov,
			Git:     d.Git,
			Hub:     d.Hub,
			Workers: d.Workers,
			Shell:   d.Shell,
			Repo:    d.Repo,
			Harness: d.Harness,
			Model:   d.Model,
			Effort:  d.Effort,
			Out:     d.Out,
		}, store, winner.Issue.Identifier)
		if err != nil {
			return Result{Kind: KindRefused, Winner: winner, Reason: err.Error()}, nil
		}
		return Result{Kind: Kind(out.Kind), Winner: winner, Reason: out.Reason, RunID: out.RunID}, nil

	case VerbPlan:
		out, err := plan.Execute(ctx, plan.Deps{
			Board:   d.Board,
			Cov:     d.Cov,
			Workers: d.Workers,
			Tree:    d.Tree,
			Repo:    d.Repo,
			Harness: d.Harness,
			Model:   d.Model,
			Effort:  d.Effort,
			Out:     d.Out,
		}, store, winner.Issue.Identifier)
		if err != nil {
			return Result{Kind: KindRefused, Winner: winner, Reason: err.Error()}, nil
		}
		return Result{Kind: Kind(out.Kind), Winner: winner, Reason: out.Reason, RunID: out.RunID}, nil

	default:
		return Result{}, fmt.Errorf("dispatch: winner has no verb; this is a wand bug")
	}
}

// unreachable reports whether err is a transport-level failure to reach
// Linear at all — DNS, connection refused, TLS, a timeout — as opposed to a
// reachable API answering with an error, a bad key, or a decode failure.
// net/http wraps every transport failure in a *url.Error; nothing else in
// this codebase's Linear calls produces one.
func unreachable(err error) bool {
	var uerr *url.Error
	return errors.As(err, &uerr)
}
