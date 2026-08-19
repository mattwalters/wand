package worker_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/worker"
)

// specFor returns a valid Spec rooted in temp dirs; tests then break the
// one field they are about.
func specFor(t *testing.T) worker.Spec {
	t.Helper()
	scratch := t.TempDir()
	return worker.Spec{
		Mode:        "test",
		Prompt:      "do the thing",
		Dir:         t.TempDir(),
		ScratchDir:  scratch,
		HandoffPath: filepath.Join(scratch, "handoff.json"),
		Timeout:     time.Minute,
	}
}

// shAdapter runs a shell script, standing in for a harness. The script sees
// the handoff path as $HANDOFF and the composed prompt on stdin.
type shAdapter struct {
	script string
	// seen captures what the runner handed the adapter, for wiring asserts.
	seenPrompt  string
	seenEnviron []string
}

func (a *shAdapter) Name() string { return "sh" }

func (a *shAdapter) Invocation(spec worker.Spec, prompt string, environ []string) (worker.Invocation, error) {
	a.seenPrompt = prompt
	a.seenEnviron = environ
	return worker.Invocation{
		Argv:  []string{"/bin/sh", "-c", a.script},
		Env:   append(append([]string{}, environ...), "HANDOFF="+spec.HandoffPath),
		Dir:   spec.Dir,
		Stdin: prompt,
	}, nil
}

func TestSpecValidation(t *testing.T) {
	breakField := map[string]func(*worker.Spec){
		"Mode":        func(s *worker.Spec) { s.Mode = " " },
		"Prompt":      func(s *worker.Spec) { s.Prompt = "" },
		"Dir":         func(s *worker.Spec) { s.Dir = "" },
		"ScratchDir":  func(s *worker.Spec) { s.ScratchDir = "" },
		"HandoffPath": func(s *worker.Spec) { s.HandoffPath = "" },
		"Timeout":     func(s *worker.Spec) { s.Timeout = 0 },
	}
	for name, brk := range breakField {
		t.Run(name, func(t *testing.T) {
			spec := specFor(t)
			brk(&spec)
			if _, err := worker.Run(context.Background(), &shAdapter{script: "true"}, spec); err == nil {
				t.Fatalf("Run accepted a Spec with no %s", name)
			}
		})
	}
}

func TestComposeStatesTheContract(t *testing.T) {
	spec := specFor(t)
	spec.Mode = "scope (read-only research)"
	spec.Rules = []string{"do not modify the working tree"}
	got := worker.Compose(spec)

	for _, want := range []string{
		spec.Mode,
		spec.ScratchDir,
		spec.HandoffPath,
		spec.Rules[0],
		spec.Prompt,
		"no Linear or GitHub credentials",
		"Do not probe your environment",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("contract does not state %q:\n%s", want, got)
		}
	}
	// The contract comes first; the task is appended, not interleaved.
	if !strings.HasSuffix(got, spec.Prompt) {
		t.Errorf("the task is not the tail of the prompt:\n%s", got)
	}
}

func TestStripCredentials(t *testing.T) {
	got := worker.StripCredentials([]string{
		"PATH=/usr/bin",
		"LINEAR_API_KEY=secret",
		"GITHUB_TOKEN=secret",
		"GH_TOKEN=secret",
		"GH_ENTERPRISE_TOKEN=secret",
		"HOME=/home/x",
	})
	want := []string{"PATH=/usr/bin", "HOME=/home/x"}
	if len(got) != len(want) {
		t.Fatalf("StripCredentials = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("StripCredentials = %v, want %v", got, want)
		}
	}
}

func TestRunStripsEnvironBeforeAdapter(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "planted-linear-credential")
	a := &shAdapter{script: `echo '{"ok":true}' > "$HANDOFF"`}
	if _, err := worker.Run(context.Background(), a, specFor(t)); err != nil {
		t.Fatal(err)
	}
	for _, kv := range a.seenEnviron {
		if strings.Contains(kv, "planted-linear-credential") {
			t.Fatalf("the adapter saw the orchestrator's credential: %s", kv)
		}
	}
}

func TestRunCollectsAndDeletesHandoff(t *testing.T) {
	spec := specFor(t)
	a := &shAdapter{script: `printf '{"answer": 42}' > "$HANDOFF"`}
	res, err := worker.Run(context.Background(), a, spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Handoff) != `{"answer": 42}` {
		t.Errorf("Handoff = %s", res.Handoff)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if _, err := os.Stat(spec.HandoffPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("handoff file survived its read: stat err = %v", err)
	}
	if !strings.Contains(a.seenPrompt, spec.Prompt) {
		t.Errorf("adapter did not receive the composed prompt")
	}
}

func TestRunRejectsStaleHandoff(t *testing.T) {
	// A handoff left by an earlier phase must not satisfy this one: the
	// runner clears it before spawning, and a worker that writes nothing
	// is an error, not a reader of leftovers.
	spec := specFor(t)
	if err := os.WriteFile(spec.HandoffPath, []byte(`{"stale": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := worker.Run(context.Background(), &shAdapter{script: "true"}, spec)
	if err == nil {
		t.Fatalf("Run consumed a stale handoff: %s", res.Handoff)
	}
	if res.Handoff != nil {
		t.Errorf("Handoff = %s, want nil", res.Handoff)
	}
}

func TestRunErrorsWithoutHandoff(t *testing.T) {
	res, err := worker.Run(context.Background(), &shAdapter{script: "exit 3"}, specFor(t))
	if err == nil {
		t.Fatal("Run reported success for a worker that handed nothing off")
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
	if !strings.Contains(err.Error(), "exited 3") {
		t.Errorf("error does not carry the exit code: %v", err)
	}
}

func TestRunRejectsInvalidHandoff(t *testing.T) {
	spec := specFor(t)
	_, err := worker.Run(context.Background(), &shAdapter{script: `printf 'not json' > "$HANDOFF"`}, spec)
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err = %v, want invalid-JSON error", err)
	}
	if _, statErr := os.Stat(spec.HandoffPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("invalid handoff was left on disk for a later phase to trip over")
	}
}

func TestRunTimeout(t *testing.T) {
	spec := specFor(t)
	spec.Timeout = 100 * time.Millisecond
	res, err := worker.Run(context.Background(), &shAdapter{script: "sleep 10"}, spec)
	if err == nil {
		t.Fatal("Run reported success for a timed-out worker")
	}
	if !res.TimedOut {
		t.Errorf("TimedOut = false, want true")
	}
}

func TestRunCapturesOutput(t *testing.T) {
	res, err := worker.Run(context.Background(),
		&shAdapter{script: `echo to-stdout; echo to-stderr >&2; printf '{}' > "$HANDOFF"`},
		specFor(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"to-stdout", "to-stderr"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("Output missing %q:\n%s", want, res.Output)
		}
	}
}

func TestRunSpawnFailure(t *testing.T) {
	a := &binAdapter{bin: "wand-test-no-such-binary-on-any-path"}
	if _, err := worker.Run(context.Background(), a, specFor(t)); err == nil {
		t.Fatal("Run reported success for a binary that does not exist")
	}
}

// binAdapter execs a bare binary with no arguments.
type binAdapter struct{ bin string }

func (a *binAdapter) Name() string { return "bin" }

func (a *binAdapter) Invocation(spec worker.Spec, prompt string, environ []string) (worker.Invocation, error) {
	return worker.Invocation{Argv: []string{a.bin}, Env: environ, Dir: spec.Dir, Stdin: prompt}, nil
}
