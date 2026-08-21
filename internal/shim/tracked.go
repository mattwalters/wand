package shim

import (
	"context"
	"os/exec"
)

// TrackStatus is git's answer to "is this path committed", with a third
// state for when the question could not be put to git at all.
type TrackStatus int

const (
	// StatusUnknown covers both "dir is not a git repository" and "git is
	// not on PATH" — a caller must treat this as an unanswered question,
	// not as evidence the file needs committing.
	StatusUnknown TrackStatus = iota
	StatusTracked
	StatusUntracked
)

// Tracked reports whether path — relative to dir — is tracked by git in the
// repository rooted at dir. It shells out rather than reading .git directly,
// the same choice internal/plan/exec.go makes for working-tree status,
// because git's index format is not this package's to parse.
func Tracked(ctx context.Context, dir, path string) TrackStatus {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "--error-unmatch", "--", path)
	if err := cmd.Run(); err == nil {
		return StatusTracked
	} else if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		// Exit 1 is git ls-files's documented answer for "no such path in
		// the index" — as opposed to exit 128 for "not a git repository at
		// all" or a failure to even start the process, both of which leave
		// the question unanswered rather than answered "no".
		return StatusUntracked
	}
	return StatusUnknown
}
