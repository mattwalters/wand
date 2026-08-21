package shim

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func testRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	mustGit(t, repo, "init", "-q", "-b", "trunk")
	mustGit(t, repo, "config", "user.email", "test@example.invalid")
	mustGit(t, repo, "config", "user.name", "test")
	mustGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	return repo
}

func TestTracked(t *testing.T) {
	repo := testRepo(t)

	if err := os.WriteFile(filepath.Join(repo, "tracked.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "tracked.json")
	mustGit(t, repo, "commit", "-q", "-m", "add tracked.json")

	if err := os.WriteFile(filepath.Join(repo, "untracked.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := Tracked(context.Background(), repo, "tracked.json"); got != StatusTracked {
		t.Errorf("tracked.json: got %v, want StatusTracked", got)
	}
	if got := Tracked(context.Background(), repo, "untracked.json"); got != StatusUntracked {
		t.Errorf("untracked.json: got %v, want StatusUntracked", got)
	}
	// A path that does not exist on disk at all is also "not in the
	// index" — untracked, same as a path that exists but was never added.
	if got := Tracked(context.Background(), repo, "missing.json"); got != StatusUntracked {
		t.Errorf("missing.json: got %v, want StatusUntracked", got)
	}
}

func TestTrackedNotARepoIsUnknown(t *testing.T) {
	dir := t.TempDir()
	if got := Tracked(context.Background(), dir, "settings.json"); got != StatusUnknown {
		t.Errorf("got %v, want StatusUnknown for a directory that is not a git repository", got)
	}
}

func TestTrackedGitNotOnPathIsUnknown(t *testing.T) {
	repo := testRepo(t)
	t.Setenv("PATH", "")
	if got := Tracked(context.Background(), repo, "settings.json"); got != StatusUnknown {
		t.Errorf("got %v, want StatusUnknown when git cannot be found", got)
	}
}

// StatusUnknown must never print as if it were an untracked or tracked
// answer by accident of the enum's zero value being meaningful elsewhere.
func TestStatusUnknownIsZeroValue(t *testing.T) {
	var s TrackStatus
	if s != StatusUnknown {
		t.Errorf("zero value = %v, want StatusUnknown", s)
	}
}
