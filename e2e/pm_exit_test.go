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

// TestPMExitContract pins the never-started half of pm's exit-code
// contract on the compiled binary, the same way scope_exit_test.go pins
// scope's: 1, and only 1, for everything that fails before a run exists. A
// scheduler reads 0/2/3 as outcomes of a propose that happened, so a
// missing API key must not land in that range and be counted as one.
func TestPMExitContract(t *testing.T) {
	bin := buildWand(t)

	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "LINEAR_API_KEY=") {
			env = append(env, kv)
		}
	}

	run := func(t *testing.T, stdin string, args ...string) (code int, stderr string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = t.TempDir()
		cmd.Env = env
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
			t.Fatalf("running %v: %v", args, err)
			return 0, ""
		}
	}

	t.Run("pm with no API key exits 1", func(t *testing.T) {
		code, stderr := run(t, "Build a signup flow.", "pm", "--team-key", "WND")
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "LINEAR_API_KEY") {
			t.Errorf("stderr does not explain itself:\n%s", stderr)
		}
	})

	t.Run("pm bless with no API key exits 1", func(t *testing.T) {
		code, stderr := run(t, "", "pm", "bless", "--team-key", "WND", "does-not-exist.json")
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "LINEAR_API_KEY") {
			t.Errorf("stderr does not explain itself:\n%s", stderr)
		}
	})

	t.Run("pm with no brief and no stdin exits 1", func(t *testing.T) {
		code, stderr := run(t, "", "pm", "--team-key", "WND", "no-such-brief.md")
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "no-such-brief.md") {
			t.Errorf("stderr does not say what was missing:\n%s", stderr)
		}
	})

	t.Run("pm bless against a missing proposal file exits 1", func(t *testing.T) {
		fakeEnv := append(append([]string{}, env...), "LINEAR_API_KEY=fake-key-not-used")
		// No cmd.Dir override: stays inside this repo's checkout, so
		// repoRoot resolves and Bless gets far enough to read the (missing)
		// file — which it does before any Linear call, so this needs no
		// network.
		cmd := exec.Command(bin, "pm", "bless", "--team-key", "WND", "does-not-exist.json")
		cmd.Env = fakeEnv
		var errBuf bytes.Buffer
		cmd.Stderr = &errBuf
		err := cmd.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("running pm bless: %v", err)
		}
		if exit.ExitCode() != 1 {
			t.Fatalf("exit code = %d, want 1; stderr:\n%s", exit.ExitCode(), errBuf.String())
		}
		if !strings.Contains(errBuf.String(), "does-not-exist.json") {
			t.Errorf("stderr does not name the missing file:\n%s", errBuf.String())
		}
	})
}
