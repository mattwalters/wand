package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/shim"
)

// initTestRepo makes dir (already t.TempDir()) a real, committed git repo,
// the state installShim assumes when it shells out to check tracked status.
func initTestRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "trunk"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "test"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func gitAddCommit(t *testing.T, dir, path string) {
	t.Helper()
	for _, args := range [][]string{
		{"add", path},
		{"commit", "-q", "-m", "commit " + path},
	} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// commitMeSubstring is the note installOneShim attaches to a line reporting
// a shim that needs to be committed. Its exact wording is an implementation
// detail; only its presence or absence is this test's business.
const commitMeSubstring = "commit this file"

func TestInstallShimSaysCommitMeOnFreshWrite(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir)
	t.Chdir(dir)

	var out bytes.Buffer
	if err := installShim(&out, false); err != nil {
		t.Fatalf("installShim: %v", err)
	}

	for _, path := range []string{settingsPath, codexHooksPath} {
		if !strings.Contains(out.String(), path) {
			t.Fatalf("output does not mention %s:\n%s", path, out.String())
		}
	}
	if strings.Count(out.String(), commitMeSubstring) != 2 {
		t.Errorf("want the commit-me note once per freshly written shim (2 total), got:\n%s", out.String())
	}
}

func TestInstallShimDryRunOmitsCommitMe(t *testing.T) {
	// The dry-run contract is "print the plan and write nothing" — no file
	// exists yet, so there is nothing to tell anyone to commit. A prior
	// draft of this change appended the note to the dry-run line too; this
	// case exists so that regression can't come back silently.
	dir := t.TempDir()
	initTestRepo(t, dir)
	t.Chdir(dir)

	var out bytes.Buffer
	if err := installShim(&out, true); err != nil {
		t.Fatalf("installShim: %v", err)
	}

	if !strings.Contains(out.String(), "would install") {
		t.Fatalf("output does not describe the dry-run plan:\n%s", out.String())
	}
	if strings.Contains(out.String(), commitMeSubstring) {
		t.Errorf("dry-run output carries the commit-me note, but wrote no file:\n%s", out.String())
	}
	for _, path := range []string{settingsPath, codexHooksPath} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("dry-run wrote %s to disk", path)
		}
	}
}

func TestInstallShimAlreadyInstalledAndCommittedIsSilent(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir)
	t.Chdir(dir)

	var first bytes.Buffer
	if err := installShim(&first, false); err != nil {
		t.Fatalf("installShim (first run): %v", err)
	}
	gitAddCommit(t, dir, settingsPath)
	gitAddCommit(t, dir, codexHooksPath)

	var out bytes.Buffer
	if err := installShim(&out, false); err != nil {
		t.Fatalf("installShim (second run): %v", err)
	}
	if !strings.Contains(out.String(), "already installed") {
		t.Fatalf("output does not say the shim was already installed:\n%s", out.String())
	}
	if strings.Contains(out.String(), commitMeSubstring) {
		t.Errorf("a committed, matching shim should not carry the commit-me note:\n%s", out.String())
	}
}

func TestInstallShimAlreadyInstalledButUntrackedSaysCommitMe(t *testing.T) {
	// This is WND-86's own shape: the file was generated and matches, but
	// nobody ever ran `git add`, so the guard it installs protects only
	// this one checkout.
	dir := t.TempDir()
	initTestRepo(t, dir)
	t.Chdir(dir)

	var first bytes.Buffer
	if err := installShim(&first, false); err != nil {
		t.Fatalf("installShim (first run): %v", err)
	}
	// Deliberately not committed.

	var out bytes.Buffer
	if err := installShim(&out, false); err != nil {
		t.Fatalf("installShim (second run): %v", err)
	}
	if !strings.Contains(out.String(), "already installed") {
		t.Fatalf("output does not say the shim was already installed:\n%s", out.String())
	}
	if strings.Count(out.String(), commitMeSubstring) != 2 {
		t.Errorf("want the commit-me note for both untracked shims, got:\n%s", out.String())
	}
}

// A direct check of shim.Tracked's role in installOneShim: an environment
// where git cannot answer the question at all must not be read as "needs
// committing" — a false positive would be noisier, and more likely to be
// tuned out, than saying nothing.
func TestInstallShimNotARepoOmitsCommitMe(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var first bytes.Buffer
	if err := installShim(&first, false); err != nil {
		t.Fatalf("installShim (first run): %v", err)
	}
	if got := shim.Tracked(t.Context(), ".", settingsPath); got != shim.StatusUnknown {
		t.Fatalf("test precondition: want git to be unable to answer outside a repo, got %v", got)
	}

	var out bytes.Buffer
	if err := installShim(&out, false); err != nil {
		t.Fatalf("installShim (second run): %v", err)
	}
	if strings.Contains(out.String(), commitMeSubstring) {
		t.Errorf("commit-me note printed where tracked status could not be determined:\n%s", out.String())
	}
}
