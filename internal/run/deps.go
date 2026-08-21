package run

import (
	"context"
	"fmt"
	"io"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/verbs"
	"github.com/mattwalters/wand/internal/worker"
)

// ReadyForHumanLabel marks a converged run's ticket: In Review, with a PR a
// human should read. Covenant topology, not a parameter (see
// covenant.Default), so the name is a constant here.
const ReadyForHumanLabel = "ready-for-human"

// Board is the slice of the Linear client the orchestrator writes through.
// It extends the verbs' slice because the loop reuses the verbs themselves —
// claim and handback carry ordering rules this package must not re-derive.
type Board interface {
	verbs.Linear
	IssueComments(ctx context.Context, issueID string) ([]linear.Comment, error)
	AddLabel(ctx context.Context, issueID, labelID string) error
}

// Base is the branch a run's work targets, in the two forms the run needs.
// They are not interchangeable, and conflating them is how a run branches
// from the wrong commit: the branch name is what GitHub is asked to merge
// into, while every local git command has to name the remote-tracking ref.
type Base struct {
	// Name is the branch on the remote — "main". It is what
	// `gh pr create --base` takes, and nothing else.
	Name string
	// Ref is the remote-tracking ref — "origin/main". Every local command
	// resolves this instead of Name: the branch may not exist locally at
	// all (a clone that never checked it out), and where it does exist it
	// is only as fresh as the last pull.
	Ref string
}

// Git is the loop's git surface, on the orchestrator's own credentials.
// Workers commit; everything that leaves the machine goes through here.
type Git interface {
	// DefaultBranch resolves the branch PRs target, in both forms.
	DefaultBranch(ctx context.Context, repo string) (Base, error)
	// AddWorktree creates the run's worktree at dir on branch, branching
	// from base when the branch does not exist yet. base is a commit-ish
	// that must resolve on its own: a bare branch name that exists only on
	// the remote is not one (see Base).
	AddWorktree(ctx context.Context, repo, dir, branch, base string) error
	// RemoveWorktree removes a clean, fully-pushed worktree.
	RemoveWorktree(ctx context.Context, repo, dir string) error
	// Dirty reports uncommitted changes or untracked files in dir.
	Dirty(ctx context.Context, dir string) (bool, error)
	// CommitsAhead counts commits on HEAD not on base.
	CommitsAhead(ctx context.Context, dir, base string) (int, error)
	// Push pushes the branch to origin, setting upstream.
	Push(ctx context.Context, dir, branch string) error
	// DiffStat summarizes lines changed between base and HEAD, in git's own
	// --shortstat form. Journaled at the end of every phase, alongside
	// harness, model, tokens and wall-clock.
	DiffStat(ctx context.Context, dir, base string) (string, error)
}

// PR is one open pull request, as much of it as the loop reads.
type PR struct {
	Number int
	Title  string
	URL    string
	// State is GitHub's own: [PRStateOpen], [PRStateMerged] or
	// [PRStateClosed]. Carried rather than filtered out at the query,
	// because "open" is a requirement two of this type's three readers
	// have and the third does not — see [Hub.PRForBranch].
	State string
}

// The states a [PR] can be in, as GitHub reports them.
const (
	PRStateOpen   = "OPEN"
	PRStateMerged = "MERGED"
	PRStateClosed = "CLOSED"
)

// Hub is the loop's GitHub surface — the single writer's, workers have none.
type Hub interface {
	// PRForBranch finds the most recent PR whose head is branch, if any,
	// whatever state it is in — the caller decides what states it accepts.
	//
	// It used to filter to open PRs in the query, which made a merged PR
	// indistinguishable from no PR at all, and convergence read that as
	// "the PR is gone" and parked runs that had in fact succeeded. The
	// filter is a caller's requirement, so it belongs at the call site
	// where it can be seen, not buried in a flag where it cannot.
	PRForBranch(ctx context.Context, dir, branch string) (PR, bool, error)
	// OpenPR opens a PR and returns its URL.
	OpenPR(ctx context.Context, dir, base, branch, title, body string) (string, error)
	// RetitlePR repairs a PR's title.
	RetitlePR(ctx context.Context, dir string, number int, title string) error
	// UnresolvedThreads counts the PR's unresolved review threads, and an
	// outdated thread counts as unresolved: a partial revision can outdate
	// the code a human commented on without answering the human (the
	// PW-177 lesson).
	UnresolvedThreads(ctx context.Context, dir string, number int) (int, error)
}

// Workers spawns one cold worker and collects its handoff; the production
// implementation is worker.Run behind an adapter.
type Workers interface {
	Run(ctx context.Context, spec worker.Spec) (worker.Result, error)
}

// Shell runs one covenant command (verify, provision) in a directory. ok is
// the command's own verdict (exit zero); err means it could not be run or
// was cut short, which is a different fact than a red build.
type Shell interface {
	Run(ctx context.Context, dir, command string) (ok bool, output string, err error)
}

// Deps is everything the loop acts through. All I/O sits behind the
// interfaces above so the loop's decisions can be tested with fakes.
type Deps struct {
	Board   Board
	Cov     covenant.Covenant
	Git     Git
	Hub     Hub
	Workers Workers
	Shell   Shell

	// Repo is the absolute path of the repository the run acts on.
	Repo string
	// Harness names the worker adapter, for the journal and the record.
	Harness string
	// Model and Effort pass through to every worker; empty means the
	// harness's default.
	Model  string
	Effort string

	// Out receives progress narration for a human running this
	// interactively. Never nil after validate.
	Out io.Writer
}

func (d Deps) validate() error {
	switch {
	case d.Board == nil, d.Git == nil, d.Hub == nil, d.Workers == nil, d.Shell == nil:
		return fmt.Errorf("run: Deps is missing an interface; this is a wand bug")
	case d.Repo == "":
		return fmt.Errorf("run: Deps.Repo is required")
	case d.Cov.Commands.Verify == "":
		return fmt.Errorf("run: the covenant configures no verify command; wand run cannot tell green from red without one — set commands.verify in wand.toml")
	case d.Cov.Caps.ReviewRounds < 1 || d.Cov.Caps.CIAttempts < 1 || d.Cov.Caps.WorkerTimeout <= 0:
		return fmt.Errorf("run: the covenant's caps are unset; this is a wand bug — covenant.Default and the file loader both guarantee them")
	}
	return nil
}

// AdapterFor resolves a harness name to its worker adapter. Empty means the
// default, Claude Code.
func AdapterFor(name string) (worker.Adapter, error) {
	switch name {
	case "", "claude-code":
		return worker.ClaudeCode{}, nil
	case "codex":
		return worker.Codex{}, nil
	}
	return nil, fmt.Errorf("run: no worker adapter named %q (have claude-code, codex)", name)
}
