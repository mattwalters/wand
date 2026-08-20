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

	runs := t.TempDir()
	g.RunsRoot = runs

	first := filepath.Join(runs, "run-1", "tree")
	if err := g.AddWorktree(ctx, repo, first, "wand/wnd-9", "trunk"); err != nil {
		t.Fatalf("first AddWorktree: %v", err)
	}

	second := filepath.Join(runs, "run-2", "tree")
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

	runs := t.TempDir()
	g.RunsRoot = runs

	first := filepath.Join(runs, "run-1", "tree")
	if err := g.AddWorktree(ctx, repo, first, "wand/wnd-9", "trunk"); err != nil {
		t.Fatalf("first AddWorktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(first, "half-done.go"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := g.AddWorktree(ctx, repo, filepath.Join(runs, "run-2", "tree"), "wand/wnd-9", "trunk")
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

// DiffStat reads git's own --shortstat against base, empty when the
// branch has not diverged from it yet and populated once a commit lands —
// the two states a phase can actually be in when the loop asks for one.
func TestDiffStat(t *testing.T) {
	repo := testRepo(t)
	mustGit(t, repo, "checkout", "-q", "-b", "feature")

	stat, err := ExecGit{}.DiffStat(context.Background(), repo, "trunk")
	if err != nil {
		t.Fatalf("DiffStat before any change: %v", err)
	}
	if stat != "" {
		t.Errorf("stat = %q, want empty before any commit diverges from base", stat)
	}

	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("a line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "new.txt")
	mustGit(t, repo, "commit", "-q", "-m", "add a file")

	stat, err = ExecGit{}.DiffStat(context.Background(), repo, "trunk")
	if err != nil {
		t.Fatalf("DiffStat after a commit: %v", err)
	}
	if !strings.Contains(stat, "1 file changed") || !strings.Contains(stat, "insertion") {
		t.Errorf("stat = %q, want a shortstat naming the changed file", stat)
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
	want := Base{Name: "master", Ref: "origin/master"}
	got, err := ExecGit{}.DefaultBranch(context.Background(), repo)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != want {
		t.Errorf("DefaultBranch = %+v, want the branch that exists (%+v)", got, want)
	}

	// origin/HEAD set: it wins over the fallback order.
	mustGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")
	got, err = ExecGit{}.DefaultBranch(context.Background(), repo)
	if err != nil || got != want {
		t.Errorf("DefaultBranch with origin/HEAD = %+v (%v), want %+v", got, err, want)
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

// The base a worktree starts from must be a ref that resolves on its own.
// With only origin/main on hand, `git worktree add -b <ticket> <dir> main`
// does not fail: git DWIMs the bare name into a new local branch called
// "main" and drops the -b outright, so the run commits onto main, counts
// zero commits ahead of it, and parks blaming the worker for writing
// nothing. Resolving the base to origin/main is what makes -b hold.
func TestAddWorktreeStartsTheTicketBranchFromARemoteOnlyBase(t *testing.T) {
	ctx := context.Background()
	g := ExecGit{RunsRoot: t.TempDir()}

	// A clone whose default branch was never checked out locally: origin
	// has main, the working copy is on a feature branch, refs/heads/main
	// does not exist. A CI checkout and `git clone --branch` both land here.
	origin := filepath.Join(t.TempDir(), "origin.git")
	mustGit(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", origin)
	seed := testRepo(t)
	mustGit(t, seed, "branch", "-M", "main")
	mustGit(t, seed, "remote", "add", "origin", origin)
	mustGit(t, seed, "push", "-q", "-u", "origin", "main")

	repo := filepath.Join(t.TempDir(), "clone")
	mustGit(t, t.TempDir(), "clone", "-q", origin, repo)
	mustGit(t, repo, "checkout", "-q", "-b", "feature")
	mustGit(t, repo, "branch", "-D", "main")

	base, err := ExecGit{}.DefaultBranch(ctx, repo)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if base.Name != "main" || base.Ref != "origin/main" {
		t.Fatalf("DefaultBranch = %+v, want {main origin/main}", base)
	}

	tree := filepath.Join(g.RunsRoot, "run-1", "tree")
	if err := g.AddWorktree(ctx, repo, tree, "wand/wnd-10", base.Ref); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if got := mustGit(t, tree, "branch", "--show-current"); got != "wand/wnd-10" {
		t.Errorf("the worktree is on branch %q, want wand/wnd-10 — the -b was DWIMed away", got)
	}
}

// Resume reclaims what wand left behind; it does not clear a human out of
// the way to do it. `git worktree remove` refuses uncommitted *tracked*
// work and nothing else, so a clean worktree a developer made goes quietly
// — ignored files (.env, build caches) and all.
func TestAddWorktreeRefusesToRemoveAWorktreeItDidNotCreate(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()
	g := ExecGit{RunsRoot: t.TempDir()}

	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", ".gitignore")
	mustGit(t, repo, "commit", "-q", "-m", "ignore .env")

	// A developer's own worktree on the ticket branch: clean by git's
	// reckoning, and holding a file `worktree remove` would delete without
	// a word.
	mine := filepath.Join(t.TempDir(), "my-checkout")
	mustGit(t, repo, "worktree", "add", "-q", "-b", "wand/wnd-9", mine, "trunk")
	env := filepath.Join(mine, ".env")
	if err := os.WriteFile(env, []byte("SECRET=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := mustGit(t, mine, "status", "--porcelain"); out != "" {
		t.Fatalf("the developer's worktree is not clean, so the refusal proves nothing: %q", out)
	}

	err := g.AddWorktree(ctx, repo, filepath.Join(g.RunsRoot, "run-1", "tree"), "wand/wnd-9", "trunk")
	if err == nil {
		t.Fatal("resume removed a worktree wand did not create")
	}
	if !strings.Contains(err.Error(), mine) {
		t.Errorf("the refusal does not name the worktree in the way: %v", err)
	}
	if _, serr := os.Stat(env); serr != nil {
		t.Errorf("the developer's ignored file did not survive the refusal: %v", serr)
	}
}
