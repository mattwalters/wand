package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The exec adapters are the one layer whose correctness lives in git's real
// behavior — resume semantics, warning streams, ref fallbacks — so these
// tests hold them against a real (local, network-free) git, the way the
// journal's crash tests hold the lock against a real kernel.

func testRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	mustGit(t, repo, "init", "-q", "-b", "trunk")
	mustGit(t, repo, "config", "user.email", "test@example.invalid")
	mustGit(t, repo, "config", "user.name", "test")
	mustGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	return repo
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A hand-back preserves its worktree, and that worktree holds the ticket
// branch — which git refuses to check out twice. Resume must clear a clean
// preserved tree (everything it held is committed on the branch) instead of
// parking every re-entry until a human deletes it by hand.
func TestAddWorktreeResumesPastAPreservedCleanWorktree(t *testing.T) {
	repo := testRepo(t)
	g := ExecGit{}
	ctx := context.Background()

	first := filepath.Join(t.TempDir(), "tree")
	if err := g.AddWorktree(ctx, repo, first, "wand/wnd-9", "trunk"); err != nil {
		t.Fatalf("first AddWorktree: %v", err)
	}

	second := filepath.Join(t.TempDir(), "tree")
	if err := g.AddWorktree(ctx, repo, second, "wand/wnd-9", "trunk"); err != nil {
		t.Fatalf("resume AddWorktree: %v", err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("the clean preserved worktree at %s was not removed", first)
	}
	if _, err := os.Stat(second); err != nil {
		t.Errorf("the resumed worktree does not exist: %v", err)
	}
}

// A dirty preserved worktree is work at risk: resume must refuse with the
// old tree's path in the error, never silently discard uncommitted changes.
func TestAddWorktreeRefusesToDropUncommittedWork(t *testing.T) {
	repo := testRepo(t)
	g := ExecGit{}
	ctx := context.Background()

	first := filepath.Join(t.TempDir(), "tree")
	if err := g.AddWorktree(ctx, repo, first, "wand/wnd-9", "trunk"); err != nil {
		t.Fatalf("first AddWorktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(first, "half-done.go"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := g.AddWorktree(ctx, repo, filepath.Join(t.TempDir(), "tree"), "wand/wnd-9", "trunk")
	if err == nil {
		t.Fatal("resume dropped a dirty worktree without a word")
	}
	if !strings.Contains(err.Error(), first) {
		t.Errorf("the refusal does not name the worktree holding the work: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(first, "half-done.go")); serr != nil {
		t.Errorf("the uncommitted work did not survive the refusal: %v", serr)
	}
}

// git writes warnings to stderr while exiting 0 — an ambiguous refname is
// the classic — and a warning parsed as output turns a count unparseable
// and a clean tree "dirty". Stdout alone is the data (the review's repro).
func TestCommitsAheadSurvivesAmbiguousRefWarning(t *testing.T) {
	repo := testRepo(t)
	mustGit(t, repo, "tag", "trunk") // now "trunk" names a branch and a tag

	n, err := ExecGit{}.CommitsAhead(context.Background(), repo, "trunk")
	if err != nil {
		t.Fatalf("CommitsAhead over an ambiguous ref warning: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

// DefaultBranch must not guess: with origin/HEAD unset it may fall back
// only to a conventional branch that exists on the remote, and must surface
// an error — not "main" — when none does. A wrong guess here opens the PR
// against the wrong base.
func TestDefaultBranchFallsBackOnlyToARealRemoteBranch(t *testing.T) {
	repo := testRepo(t)
	sha := mustGit(t, repo, "rev-parse", "HEAD")

	// origin/HEAD unset, only origin/master exists (a remote-add repo).
	mustGit(t, repo, "update-ref", "refs/remotes/origin/master", sha)
	got, err := ExecGit{}.DefaultBranch(context.Background(), repo)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "master" {
		t.Errorf("DefaultBranch = %q, want the branch that exists (master)", got)
	}

	// origin/HEAD set: it wins over the fallback order.
	mustGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")
	got, err = ExecGit{}.DefaultBranch(context.Background(), repo)
	if err != nil || got != "master" {
		t.Errorf("DefaultBranch with origin/HEAD = %q (%v), want master", got, err)
	}
}

func TestDefaultBranchSurfacesAnUnresolvableRemote(t *testing.T) {
	repo := testRepo(t) // no remote refs at all
	_, err := ExecGit{}.DefaultBranch(context.Background(), repo)
	if err == nil {
		t.Fatal(`DefaultBranch guessed a base for a repo with no remote branches`)
	}
	if !strings.Contains(err.Error(), "set-head") {
		t.Errorf("the error does not tell the operator the fix: %v", err)
	}
}
