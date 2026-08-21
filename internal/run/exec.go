package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mattwalters/wand/internal/worker"
)

// The production implementations of Git, Hub, Shell and Workers: thin exec
// wrappers around git, gh and sh. Thin on purpose — decisions live in the
// loop, and these translate them into processes.
//
// Every subprocess here gets the same three guards the worker runner has:
// a bounded output tail (a wedged child cannot grow an unbounded buffer),
// a process-group kill, and a WaitDelay — because a verify command's leaked
// grandchild holding the output pipe would otherwise keep Run blocked past
// every deadline, with the ticket stuck In Progress and no terminal record.

// outputLimit bounds what a subprocess's captured output can grow to.
// The tail is what a fix-CI worker is prompted with and what a hand-back
// quotes; the interesting part of a build failure is at the end.
const outputLimit = 16 << 10

// ExecGit is Git over the git binary.
type ExecGit struct {
	// RunsRoot is the run journal's store directory. Resume may remove a
	// worktree holding the ticket branch only from inside it: a worktree
	// anywhere else belongs to a human, and `git worktree remove` takes a
	// clean tree's ignored files (.env, node_modules, build caches) with
	// it without the refusal a dirty tree would earn.
	RunsRoot string
}

// owns reports whether a worktree lives inside the run store — the only
// place resume may remove one from. Both sides are resolved through
// symlinks first: git reports a real path, while a state directory under
// /var or /tmp on macOS is reached through one.
func (g ExecGit) owns(dir string) bool {
	if g.RunsRoot == "" {
		return false
	}
	root, err := filepath.EvalSymlinks(g.RunsRoot)
	if err != nil {
		return false
	}
	path, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// Stdout is parsed and stderr is not: git writes warnings and advice to
	// stderr even on exit 0, and a warning mixed into `status --porcelain`
	// or `rev-list --count` output would read as a dirty tree or an
	// unparseable count.
	stdout := &worker.Tail{Limit: outputLimit}
	stderr := &worker.Tail{Limit: outputLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	worker.SetupProcessGroup(cmd)
	cmd.WaitDelay = worker.WaitDelay
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err,
			strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// DefaultBranch resolves origin's HEAD. When that ref is unset — a repo
// added with `git remote add`, a refspec-limited fetch — it falls back to
// the conventional names, but only to one that actually exists as a remote
// branch: guessing a base is how a PR opens against the wrong branch.
//
// What it resolves is always a *remote* branch, so it returns both forms
// (see [Base]) rather than the bare name. Handing the bare name to a local
// command is not a failure git reports cleanly: with origin/main on hand
// and no local main, `worktree add -b <ticket> <dir> main` DWIMs the name
// into a new local branch called "main" and drops the -b outright. The run
// then commits onto main, counts zero commits ahead of it, and parks
// blaming the worker for writing nothing.
func (ExecGit) DefaultBranch(ctx context.Context, repo string) (Base, error) {
	out, err := git(ctx, repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		// The ref reads "origin/main": the whole string is the
		// remote-tracking ref, and the part after the remote is the branch
		// name a PR is opened against.
		if _, name, ok := strings.Cut(out, "/"); ok {
			return Base{Name: name, Ref: out}, nil
		}
		err = fmt.Errorf("origin/HEAD reads %q, not origin/<branch>", out)
	}
	if ctx.Err() != nil {
		return Base{}, err
	}
	for _, name := range []string{"main", "master"} {
		if _, verr := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+name); verr == nil {
			return Base{Name: name, Ref: "origin/" + name}, nil
		}
	}
	return Base{}, fmt.Errorf("origin/HEAD is unset and neither origin/main nor origin/master exists — run `git remote set-head origin --auto` in the repository: %w", err)
}

func (g ExecGit) AddWorktree(ctx context.Context, repo, dir, branch, base string) error {
	// A branch left by an earlier run is resumed, not recreated: -b would
	// refuse, and a second branch for one ticket is a fork nobody asked for.
	if _, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		// An earlier run's preserved worktree still holds the branch, and
		// git refuses to check one branch out twice. A clean preserved tree
		// has nothing left to preserve — everything is committed on the
		// branch — so remove it and resume; `worktree remove` itself
		// refuses a dirty tree, and that refusal is surfaced whole: work
		// at risk stays a human's call, never collateral of a resume.
		//
		// Only a tree inside the run store, though. `worktree remove`
		// refuses uncommitted *tracked* work and nothing else: a clean
		// worktree a human made goes quietly, ignored files and all. A
		// resume may reclaim what wand left behind; it may not clear a
		// human out of the way to do it.
		git(ctx, repo, "worktree", "prune")
		if old, err := worktreeFor(ctx, repo, branch); err == nil && old != "" && old != dir {
			if !g.owns(old) {
				return fmt.Errorf("branch %s is checked out at %s, which wand did not create — removing it would take its ignored files (.env, build caches) with it, so that is your call: check the branch out elsewhere, or `git worktree remove %s` yourself", branch, old, old)
			}
			if _, rerr := git(ctx, repo, "worktree", "remove", old); rerr != nil {
				return fmt.Errorf("branch %s is held by an earlier run's worktree at %s, which could not be removed (it may hold uncommitted work — inspect it, then `git worktree remove --force %s`): %w", branch, old, old, rerr)
			}
		}
		_, err := git(ctx, repo, "worktree", "add", dir, branch)
		return err
	}
	_, err := git(ctx, repo, "worktree", "add", "-b", branch, dir, base)
	return err
}

// worktreeFor finds the worktree that has branch checked out, if any.
func worktreeFor(ctx context.Context, repo, branch string) (string, error) {
	out, err := git(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	var path string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case line == "branch refs/heads/"+branch:
			return path, nil
		}
	}
	return "", nil
}

func (ExecGit) RemoveWorktree(ctx context.Context, repo, dir string) error {
	_, err := git(ctx, repo, "worktree", "remove", dir)
	return err
}

func (ExecGit) Dirty(ctx context.Context, dir string) (bool, error) {
	out, err := git(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

func (ExecGit) CommitsAhead(ctx context.Context, dir, base string) (int, error) {
	out, err := git(ctx, dir, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count returned %q", out)
	}
	return n, nil
}

func (ExecGit) Push(ctx context.Context, dir, branch string) error {
	_, err := git(ctx, dir, "push", "--set-upstream", "origin", branch)
	return err
}

// DiffStat summarizes the lines changed between base and HEAD in git's own
// --shortstat form ("2 files changed, 34 insertions(+), 5 deletions(-)") —
// human-readable, the same shape a PR page shows, so a journal reader does
// not need a second format to recognize it. Empty when there is no diff yet
// (a phase that ran before the first commit landed), never an error for
// that case.
func (ExecGit) DiffStat(ctx context.Context, dir, base string) (string, error) {
	return git(ctx, dir, "diff", "--shortstat", base+"...HEAD")
}

// ExecHub is Hub over the gh CLI, which carries the operator's GitHub
// credentials — the ones every worker environment strips.
type ExecHub struct{}

func gh(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	stdout := &worker.Tail{Limit: outputLimit}
	stderr := &worker.Tail{Limit: outputLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	worker.SetupProcessGroup(cmd)
	cmd.WaitDelay = worker.WaitDelay
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh %s: %w\n%s", strings.Join(args, " "), err,
			strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (ExecHub) PRForBranch(ctx context.Context, dir, branch string) (PR, bool, error) {
	// pr list returns an empty array for "none", where pr view returns an
	// error a caller would have to parse apart from real failures.
	//
	// --state all, deliberately: a merged PR is not an absent one, and
	// filtering it out here is what made a converged run look like a run
	// whose PR vanished. Newest first is gh's default ordering, so --limit 1
	// is the branch's most recent PR.
	out, err := gh(ctx, dir, "pr", "list", "--head", branch, "--state", "all",
		"--json", "number,title,url,state", "--limit", "1")
	if err != nil {
		return PR{}, false, err
	}
	var prs []PR
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return PR{}, false, fmt.Errorf("gh pr list returned unparseable JSON: %w", err)
	}
	if len(prs) == 0 {
		return PR{}, false, nil
	}
	return prs[0], true, nil
}

func (ExecHub) OpenPR(ctx context.Context, dir, base, branch, title, body string) (string, error) {
	return gh(ctx, dir, "pr", "create", "--base", base, "--head", branch,
		"--title", title, "--body", body)
}

func (ExecHub) RetitlePR(ctx context.Context, dir string, number int, title string) error {
	_, err := gh(ctx, dir, "pr", "edit", strconv.Itoa(number), "--title", title)
	return err
}

// threadsQuery pages through the PR's review threads. isOutdated is
// deliberately not queried: outdated is not answered.
const threadsQuery = `query($owner: String!, $name: String!, $number: Int!, $cursor: String) {
	repository(owner: $owner, name: $name) {
		pullRequest(number: $number) {
			reviewThreads(first: 100, after: $cursor) {
				pageInfo { hasNextPage endCursor }
				nodes { isResolved }
			}
		}
	}
}`

// UnresolvedThreads reads the PR's review threads through the GraphQL API —
// `gh pr view` does not expose them — and pages through all of them: an
// unresolved human thread past the first page still blocks convergence.
func (ExecHub) UnresolvedThreads(ctx context.Context, dir string, number int) (int, error) {
	repo, err := gh(ctx, dir, "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	if err != nil {
		return 0, err
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return 0, fmt.Errorf("gh repo view returned %q, not owner/name", repo)
	}
	unresolved, cursor := 0, ""
	for {
		args := []string{"api", "graphql",
			"-F", "owner=" + owner, "-F", "name=" + name, "-F", "number=" + strconv.Itoa(number),
			"-f", "query=" + threadsQuery}
		if cursor != "" {
			args = append(args, "-F", "cursor="+cursor)
		}
		out, err := gh(ctx, dir, args...)
		if err != nil {
			return 0, err
		}
		var resp struct {
			Data struct {
				Repository struct {
					PullRequest struct {
						ReviewThreads struct {
							PageInfo struct {
								HasNextPage bool   `json:"hasNextPage"`
								EndCursor   string `json:"endCursor"`
							} `json:"pageInfo"`
							Nodes []struct {
								IsResolved bool `json:"isResolved"`
							} `json:"nodes"`
						} `json:"reviewThreads"`
					} `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			return 0, fmt.Errorf("gh api graphql returned unparseable JSON: %w", err)
		}
		threads := resp.Data.Repository.PullRequest.ReviewThreads
		for _, n := range threads.Nodes {
			if !n.IsResolved {
				unresolved++
			}
		}
		if !threads.PageInfo.HasNextPage {
			return unresolved, nil
		}
		cursor = threads.PageInfo.EndCursor
	}
}

// ExecShell runs covenant commands through sh -c, capturing a bounded
// combined-output tail.
type ExecShell struct{}

func (ExecShell) Run(ctx context.Context, dir, command string) (bool, string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	out := &worker.Tail{Limit: outputLimit}
	cmd.Stdout, cmd.Stderr = out, out
	worker.SetupProcessGroup(cmd)
	cmd.WaitDelay = worker.WaitDelay
	err := cmd.Run()
	if err == nil {
		return true, out.String(), nil
	}
	var exitErr *exec.ExitError
	if ctx.Err() == nil && errors.As(err, &exitErr) {
		// The command ran and failed: that is a red verdict, not an error.
		return false, out.String(), nil
	}
	return false, out.String(), fmt.Errorf("running %q: %w", command, err)
}

// AdapterWorkers is Workers over worker.Run with a fixed adapter.
type AdapterWorkers struct{ Adapter worker.Adapter }

func (w AdapterWorkers) Run(ctx context.Context, spec worker.Spec) (worker.Result, error) {
	return worker.Run(ctx, w.Adapter, spec)
}
