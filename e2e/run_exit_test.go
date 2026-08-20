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

// TestRunExitContract pins the never-started half of run's exit-code
// contract on the compiled binary: 1, and nothing written anywhere. The
// three terminal codes (0 converged, 2 handed back, 3 parked) need a live
// board and workers and are covered in-process in internal/run; what only
// this tier can reach is the os.Exit path in the cobra layer and the
// promise that every refusal happens before the claim.
//
// Plain exec — no pty, no network.
func TestRunExitContract(t *testing.T) {
	bin := buildWand(t)

	execRun := func(t *testing.T, dir string, env []string, args ...string) (code int, stderr string) {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"run"}, args...)...)
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
			t.Fatalf("running wand run: %v", err)
			return 0, ""
		}
	}

	// The ambient environment minus any real Linear credential.
	var baseEnv []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "LINEAR_API_KEY=") {
			baseEnv = append(baseEnv, kv)
		}
	}

	t.Run("no API key exits 1", func(t *testing.T) {
		code, stderr := execRun(t, t.TempDir(), baseEnv, "WND-1")
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "LINEAR_API_KEY") {
			t.Errorf("stderr does not explain itself:\n%s", stderr)
		}
	})

	t.Run("unknown harness exits 1", func(t *testing.T) {
		env := append(baseEnv, "LINEAR_API_KEY=lin_api_placeholder")
		code, stderr := execRun(t, t.TempDir(), env, "WND-1", "--harness", "abacus")
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "abacus") {
			t.Errorf("stderr does not name the harness:\n%s", stderr)
		}
	})

	t.Run("missing verify command exits 1 before any claim", func(t *testing.T) {
		// A real (empty) git repo with no wand.toml: the stock covenant has
		// no verify command, and run must refuse on that before touching
		// Linear — the placeholder key would fail any call it made, and the
		// error must be about configuration, not authentication.
		repo := t.TempDir()
		if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		env := append(baseEnv,
			"LINEAR_API_KEY=lin_api_placeholder",
			"WAND_STATE_DIR="+t.TempDir())
		code, stderr := execRun(t, repo, env, "WND-1")
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "commands.verify") {
			t.Errorf("stderr does not name the missing configuration:\n%s", stderr)
		}
	})
}
