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

// Git is the loop's git surface, on the orchestrator's own credentials.
// Workers commit; everything that leaves the machine goes through here.
type Git interface {
	// DefaultBranch names the branch PRs target.
	DefaultBranch(ctx context.Context, repo string) (string, error)
	// AddWorktree creates the run's worktree at dir on branch, branching
	// from base when the branch does not exist yet.
	AddWorktree(ctx context.Context, repo, dir, branch, base string) error
	// RemoveWorktree removes a clean, fully-pushed worktree.
	RemoveWorktree(ctx context.Context, repo, dir string) error
	// Dirty reports uncommitted changes or untracked files in dir.
	Dirty(ctx context.Context, dir string) (bool, error)
	// CommitsAhead counts commits on HEAD not on base.
	CommitsAhead(ctx context.Context, dir, base string) (int, error)
	// Push pushes the branch to origin, setting upstream.
	Push(ctx context.Context, dir, branch string) error
}

// PR is one open pull request, as much of it as the loop reads.
type PR struct {
	Number int
	Title  string
	URL    string
}

// Hub is the loop's GitHub surface — the single writer's, workers have none.
type Hub interface {
	// PRForBranch finds the open PR whose head is branch, if any.
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
