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

// TestDoctorExitContract pins the precondition half of doctor's could-not-check
// exit code on the compiled binary: 2, never fang's generic 1, because a
// caller scripting doctor must be able to tell "drift" from "the check did
// not run". These preconditions (no LINEAR_API_KEY, no team key) fail before
// doctor.Run is ever reached, which is what only this tier can exercise: the
// os.Exit path in the cobra layer. The clean and drift codes, and the
// repo-local shim check merged into them, all need a live or faked Linear
// team to exercise and are covered in-process in internal/doctor instead.
//
// Like the guard test above, this is a plain exec test — no pty, no network.
func TestDoctorExitContract(t *testing.T) {
	bin := buildWand(t)

	run := func(t *testing.T, env []string, args ...string) (code int, stderr string) {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"doctor"}, args...)...)
		// An empty cwd so a wand.toml in this repo cannot leak in.
		cmd.Dir = t.TempDir()
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
			t.Fatalf("running wand doctor: %v", err)
			return 0, ""
		}
	}

	// The ambient environment minus any real Linear credential.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "LINEAR_API_KEY=") {
			env = append(env, kv)
		}
	}

	t.Run("no API key exits 2", func(t *testing.T) {
		code, stderr := run(t, env, "--team-key", "WND")
		if code != 2 {
			t.Fatalf("exit code = %d, want 2; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "LINEAR_API_KEY") {
			t.Errorf("stderr does not explain itself:\n%s", stderr)
		}
	})

	t.Run("no team key exits 2", func(t *testing.T) {
		code, stderr := run(t, append(env, "LINEAR_API_KEY=lin_api_placeholder"))
		if code != 2 {
			t.Fatalf("exit code = %d, want 2; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "--team-key") {
			t.Errorf("stderr does not explain itself:\n%s", stderr)
		}
	})
}
