package run

import (
	"bytes"
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

// outputLimit bounds what a shell command's captured output can grow to.
// The tail is what a fix-CI worker is prompted with and what a hand-back
// quotes; the interesting part of a build failure is at the end.
const outputLimit = 16 << 10

func clip(b []byte) string {
	if len(b) <= outputLimit {
		return string(b)
	}
	return "[… earlier output clipped …]\n" + string(b[len(b)-outputLimit:])
}

// ExecGit is Git over the git binary.
type ExecGit struct{}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, clip(out.Bytes()))
	}
	return strings.TrimSpace(out.String()), nil
}

// DefaultBranch resolves origin's HEAD; a repo with no origin HEAD ref
// (fresh clone quirks) falls back to "main".
func (ExecGit) DefaultBranch(ctx context.Context, repo string) (string, error) {
	out, err := git(ctx, repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "main", nil
	}
	// The ref reads "origin/main"; the branch name is the part after the
	// remote.
	if _, name, ok := strings.Cut(out, "/"); ok {
		return name, nil
	}
	return out, nil
}

func (ExecGit) AddWorktree(ctx context.Context, repo, dir, branch, base string) error {
	// A branch left by an earlier run is resumed, not recreated: -b would
	// refuse, and a second branch for one ticket is a fork nobody asked for.
	if _, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err := git(ctx, repo, "worktree", "add", dir, branch)
		return err
	}
	_, err := git(ctx, repo, "worktree", "add", "-b", branch, dir, base)
	return err
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
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh %s: %w\n%s", strings.Join(args, " "), err, clip(stderr.Bytes()))
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

// UnresolvedThreads reads the PR's review threads through the GraphQL API —
// `gh pr view` does not expose them. isOutdated is deliberately ignored:
// outdated is not answered.
func (ExecHub) UnresolvedThreads(ctx context.Context, dir string, number int) (int, error) {
	repo, err := gh(ctx, dir, "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	if err != nil {
		return 0, err
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return 0, fmt.Errorf("gh repo view returned %q, not owner/name", repo)
	}
	out, err := gh(ctx, dir, "api", "graphql",
		"-F", "owner="+owner, "-F", "name="+name, "-F", "number="+strconv.Itoa(number),
		"-f", `query=query($owner: String!, $name: String!, $number: Int!) {
			repository(owner: $owner, name: $name) {
				pullRequest(number: $number) {
					reviewThreads(first: 100) { nodes { isResolved } }
				}
			}
		}`)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
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
	unresolved := 0
	for _, n := range resp.Data.Repository.PullRequest.ReviewThreads.Nodes {
		if !n.IsResolved {
			unresolved++
		}
	}
	return unresolved, nil
}

// ExecShell runs covenant commands through sh -c, capturing a bounded
// combined-output tail.
type ExecShell struct{}

func (ExecShell) Run(ctx context.Context, dir, command string) (bool, string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err == nil {
		return true, clip(out.Bytes()), nil
	}
	var exitErr *exec.ExitError
	if ctx.Err() == nil && errors.As(err, &exitErr) {
		// The command ran and failed: that is a red verdict, not an error.
		return false, clip(out.Bytes()), nil
	}
	return false, clip(out.Bytes()), fmt.Errorf("running %q: %w", command, err)
}

// AdapterWorkers is Workers over worker.Run with a fixed adapter.
type AdapterWorkers struct{ Adapter worker.Adapter }

func (w AdapterWorkers) Run(ctx context.Context, spec worker.Spec) (worker.Result, error) {
	return worker.Run(ctx, w.Adapter, spec)
}
