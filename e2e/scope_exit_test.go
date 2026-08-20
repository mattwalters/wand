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

// TestScopeExitContract pins the never-started half of scope's exit-code
// contract on the compiled binary: 1, and only 1, for everything that fails
// before a run exists. A scheduler reads 0/2/3 as outcomes of a scope that
// happened — scoped, handed back, parked — so a missing API key or a
// terminal-less interview must not land in that range and be counted as a
// scope's verdict. Those are the codes only this tier can reach, because
// the command exits itself rather than returning an error to fang.
//
// The outcome codes need workers and a live board, and are covered
// in-process against fakes in internal/scope. Like the guard and doctor
// tests, this is a plain exec test — no pty, no network.
func TestScopeExitContract(t *testing.T) {
	bin := buildWand(t)

	// The ambient environment minus any real Linear credential, so a
	// developer's own key cannot turn this into a live call.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "LINEAR_API_KEY=") {
			env = append(env, kv)
		}
	}

	run := func(t *testing.T, stdin string, args ...string) (code int, stderr string) {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"scope"}, args...)...)
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
			t.Fatalf("running wand scope: %v", err)
			return 0, ""
		}
	}

	t.Run("no API key exits 1", func(t *testing.T) {
		code, stderr := run(t, "", "WND-9")
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "LINEAR_API_KEY") {
			t.Errorf("stderr does not explain itself:\n%s", stderr)
		}
	})

	// An unattended caller that passes --interactive gets a refusal it can
	// see in its logs. The alternative is a run blocked forever on a read
	// nobody is there to answer, which no exit code describes.
	t.Run("--interactive without a terminal exits 1 rather than hanging", func(t *testing.T) {
		code, stderr := run(t, "", "--interactive", "WND-9")
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "terminal") {
			t.Errorf("stderr does not say what is missing:\n%s", stderr)
		}
	})
}
