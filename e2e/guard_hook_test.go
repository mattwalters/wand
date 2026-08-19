//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestGuardHookContract drives the compiled binary the way the harness does:
// JSON on stdin, verdict as an exit code. The exit code IS the hook's whole
// contract — 2 blocks, anything else allows — and the os.Exit path in the
// cobra layer is unreachable from an in-process test, so this is the one
// place it runs for real.
//
// This is a plain exec test, not a pty test; the "one smoke test" rule for
// this tier governs the pty/TUI smoke test above, and this file adds no
// terminal machinery.
func TestGuardHookContract(t *testing.T) {
	bin := buildWand(t)

	run := func(t *testing.T, stdin string) (code int, stderr string) {
		t.Helper()
		cmd := exec.Command(bin, "guard")
		cmd.Stdin = strings.NewReader(stdin)
		var errBuf bytes.Buffer
		cmd.Stderr = &errBuf
		err := cmd.Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
			return 0, errBuf.String()
		case errors.As(err, &exit):
			return exit.ExitCode(), errBuf.String()
		default:
			t.Fatalf("running wand guard: %v", err)
			return 0, ""
		}
	}

	const saveIssue = "mcp__79e4e202-24ab-40dc-b0d9-91f6f3b6201f__save_issue"

	t.Run("blocks with exit 2", func(t *testing.T) {
		code, stderr := run(t, `{"tool_name":"`+saveIssue+`","tool_input":{"id":"WND-1","state":"Todo"}}`)
		if code != 2 {
			t.Fatalf("exit code = %d, want 2; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "wand guard") {
			t.Errorf("stderr does not explain itself:\n%s", stderr)
		}
	})

	t.Run("allows with exit 0", func(t *testing.T) {
		code, stderr := run(t, `{"tool_name":"`+saveIssue+`","tool_input":{"id":"WND-1","state":"In Review"}}`)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr:\n%s", code, stderr)
		}
	})

	t.Run("fails open on garbage", func(t *testing.T) {
		if code, _ := run(t, "not json"); code != 0 {
			t.Fatalf("exit code = %d, want 0 — a broken guard must never wedge a session", code)
		}
	})
}
