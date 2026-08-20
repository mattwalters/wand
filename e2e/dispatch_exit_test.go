//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestDispatchExitContract pins the parts of dispatch's exit-code contract
// reachable without a live board: 1 for a missing --team-key or API key,
// both refusals that must happen before dispatch ever takes its lock or
// contacts Linear. The outcome codes (0/2/4/5) and the locked code (3) need
// a live board or a held lock and are covered in-process against fakes in
// internal/dispatch.
//
// Plain exec — no pty, no network.
func TestDispatchExitContract(t *testing.T) {
	bin := buildWand(t)

	execDispatch := func(t *testing.T, dir string, env []string, args ...string) (code int, stderr string) {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"dispatch"}, args...)...)
		cmd.Dir = dir
		cmd.Env = env
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
			t.Fatalf("running wand dispatch: %v", err)
			return 0, ""
		}
	}

	var baseEnv []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "LINEAR_API_KEY=") {
			baseEnv = append(baseEnv, kv)
		}
	}

	t.Run("no team key exits 1", func(t *testing.T) {
		env := append(baseEnv, "LINEAR_API_KEY=lin_api_placeholder")
		code, stderr := execDispatch(t, t.TempDir(), env)
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "--team-key") {
			t.Errorf("stderr does not name the missing flag:\n%s", stderr)
		}
	})

	t.Run("no API key exits 1", func(t *testing.T) {
		code, stderr := execDispatch(t, t.TempDir(), baseEnv, "--team-key", "WND")
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "LINEAR_API_KEY") {
			t.Errorf("stderr does not explain itself:\n%s", stderr)
		}
	})

	t.Run("unknown harness exits 1", func(t *testing.T) {
		env := append(baseEnv, "LINEAR_API_KEY=lin_api_placeholder")
		code, stderr := execDispatch(t, t.TempDir(), env, "--team-key", "WND", "--harness", "abacus")
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "abacus") {
			t.Errorf("stderr does not name the harness:\n%s", stderr)
		}
	})
}
