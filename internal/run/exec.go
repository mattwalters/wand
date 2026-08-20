package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
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
type ExecGit struct{}

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
func (ExecGit) DefaultBranch(ctx context.Context, repo string) (string, error) {
	out, err := git(ctx, repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		// The ref reads "origin/main"; the branch name is the part after
		// the remote.
		if _, name, ok := strings.Cut(out, "/"); ok {
			return name, nil
		}
		return out, nil
	}
	if ctx.Err() != nil {
		return "", err
	}
	for _, name := range []string{"main", "master"} {
		if _, verr := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+name); verr == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("origin/HEAD is unset and neither origin/main nor origin/master exists — run `git remote set-head origin --auto` in the repository: %w", err)
}

func (ExecGit) AddWorktree(ctx context.Context, repo, dir, branch, base string) error {
	// A branch left by an earlier run is resumed, not recreated: -b would
	// refuse, and a second branch for one ticket is a fork nobody asked for.
	if _, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		// An earlier run's preserved worktree still holds the branch, and
		// git refuses to check one branch out twice. A clean preserved tree
		// has nothing left to preserve — everything is committed on the
		// branch — so remove it and resume; `worktree remove` itself
		// refuses a dirty tree, and that refusal is surfaced whole: work
		// at risk stays a human's call, never collateral of a resume.
		git(ctx, repo, "worktree", "prune")
		if old, err := worktreeFor(ctx, repo, branch); err == nil && old != "" && old != dir {
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
	out, err := gh(ctx, dir, "pr", "list", "--head", branch, "--state", "open",
		"--json", "number,title,url", "--limit", "1")
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
