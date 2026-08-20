package worker_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
		// Relative paths resolve against the worker's cwd on one side of
		// the seam and the orchestrator's on the other.
		"RelativeDir":        func(s *worker.Spec) { s.Dir = "some/worktree" },
		"RelativeScratchDir": func(s *worker.Spec) { s.ScratchDir = "scratch" },
		"RelativeHandoff":    func(s *worker.Spec) { s.HandoffPath = "handoff.json" },
		// The scratch directory is the only write grant an adapter is
		// required to give, so the handoff must live inside it.
		"HandoffOutsideScratch": func(s *worker.Spec) { s.HandoffPath = filepath.Join(s.Dir, "handoff.json") },
	}
	for name, brk := range breakField {
		t.Run(name, func(t *testing.T) {
			spec := specFor(t)
			brk(&spec)
			if _, err := worker.Run(context.Background(), &shAdapter{script: "true"}, spec); err == nil {
				t.Fatalf("Run accepted a Spec with a broken %s", name)
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
		"GITHUB_ENTERPRISE_TOKEN=secret",
		// The strip is case-insensitive: Windows resolves env var names
		// without regard to case, so a case variant is the same leak.
		"Gh_Token=secret",
		"github_token=secret",
		// The orchestrator's ssh-agent is a GitHub write credential in
		// all but name.
		"SSH_AUTH_SOCK=/tmp/agent.sock",
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
	// `null` is valid JSON that unmarshals into all-zero fields without an
	// error — the contract demands a single JSON object, not any value.
	for name, script := range map[string]string{
		"NotJSON":    `printf 'not json' > "$HANDOFF"`,
		"JSONNull":   `printf 'null' > "$HANDOFF"`,
		"BareString": `printf '"done"' > "$HANDOFF"`,
	} {
		t.Run(name, func(t *testing.T) {
			spec := specFor(t)
			_, err := worker.Run(context.Background(), &shAdapter{script: script}, spec)
			if err == nil || !strings.Contains(err.Error(), "not a single JSON object") {
				t.Fatalf("err = %v, want a not-a-JSON-object error", err)
			}
			if _, statErr := os.Stat(spec.HandoffPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("invalid handoff was left on disk for a later phase to trip over")
			}
		})
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

func TestRunTimeoutKillsProcessGroup(t *testing.T) {
	// A worker's own children inherit the output pipe, and Wait blocks
	// until every write end closes. If the deadline kill reaped only the
	// direct child, the surviving background sleep would hold Run open for
	// its full 10s instead of the 100ms deadline. The 5s bound is a wide
	// margin over the deadline but well under the survivor's lifetime.
	spec := specFor(t)
	spec.Timeout = 100 * time.Millisecond
	start := time.Now()
	res, err := worker.Run(context.Background(),
		&shAdapter{script: `(sleep 10; echo escaped) & sleep 10`}, spec)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Run reported success for a timed-out worker")
	}
	if !res.TimedOut {
		t.Errorf("TimedOut = false, want true")
	}
	if elapsed > 5*time.Second {
		t.Errorf("Run returned after %s; the worker's process group was not killed at the deadline", elapsed)
	}
}

func TestRunDoesNotBlameSpecTimeoutForCallerDeadline(t *testing.T) {
	// The caller's own context expiring is the caller's act; reporting it
	// as Spec.Timeout firing would journal a wrong diagnosis.
	spec := specFor(t)
	spec.Timeout = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res, err := worker.Run(ctx, &shAdapter{script: "sleep 10"}, spec)
	if err == nil {
		t.Fatal("Run reported success for a run its caller cut short")
	}
	if res.TimedOut {
		t.Errorf("TimedOut = true, but the caller's deadline fired, not Spec.Timeout")
	}
	if !strings.Contains(err.Error(), "canceled by the caller") {
		t.Errorf("error does not attribute the caller's deadline: %v", err)
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

// counterClock returns a deterministic, ever-advancing clock: each call
// advances by step, starting at base. Real time never enters the heartbeat
// arithmetic, so the test does not depend on wall-clock scheduling for its
// expected values — only for how many ticks land, which the tests treat as
// "at least a few," never "exactly N."
func counterClock(base time.Time, step time.Duration) func() time.Time {
	n := 0
	return func() time.Time {
		t := base.Add(time.Duration(n) * step)
		n++
		return t
	}
}

var heartbeatLineRE = regexp.MustCompile(`^(.+): (\d+)m elapsed, (\d+)m left$`)

func TestRunHeartbeatWritesToOut(t *testing.T) {
	spec := specFor(t)
	spec.Timeout = 30 * time.Minute
	spec.HeartbeatInterval = 2 * time.Millisecond
	spec.Label = "implement round 1"
	var buf bytes.Buffer
	spec.Out = &buf
	spec.Clock = counterClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 6*time.Minute)

	// Long enough, at a 2ms interval, to all but guarantee several ticks
	// land, without depending on exactly how many.
	if _, err := worker.Run(context.Background(), &shAdapter{script: `sleep 0.05; printf '{}' > "$HANDOFF"`}, spec); err != nil {
		t.Fatal(err)
	}

	lines := splitLines(buf.String())
	if len(lines) < 2 {
		t.Fatalf("got %d heartbeat lines, want at least 2:\n%s", len(lines), buf.String())
	}
	prevElapsed := 0
	for i, line := range lines {
		m := heartbeatLineRE.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("line %d = %q, does not match the heartbeat format", i, line)
		}
		if m[1] != spec.Label {
			t.Errorf("line %d label = %q, want %q", i, m[1], spec.Label)
		}
		var elapsed, remaining int
		fmt.Sscanf(m[2], "%d", &elapsed)
		fmt.Sscanf(m[3], "%d", &remaining)
		if elapsed <= prevElapsed {
			t.Errorf("line %d elapsed = %d, want > previous %d (clock must advance each tick)", i, elapsed, prevElapsed)
		}
		prevElapsed = elapsed
		wantRemaining := 30 - elapsed
		if wantRemaining < 0 {
			wantRemaining = 0
		}
		if remaining != wantRemaining {
			t.Errorf("line %d = %q, remaining = %d, want %d (timeout 30m - elapsed, clamped at 0)", i, line, remaining, wantRemaining)
		}
	}

	// The first two ticks land on the clean numbers the counter clock
	// guarantees, regardless of exactly how many ticks eventually fire.
	if lines[0] != "implement round 1: 6m elapsed, 24m left" {
		t.Errorf("first line = %q, want %q", lines[0], "implement round 1: 6m elapsed, 24m left")
	}
	if lines[1] != "implement round 1: 12m elapsed, 18m left" {
		t.Errorf("second line = %q, want %q", lines[1], "implement round 1: 12m elapsed, 18m left")
	}

	// Nothing must write to spec.Out after Run has handed control back to
	// its caller — the goroutine must already be joined.
	after := buf.String()
	time.Sleep(20 * time.Millisecond)
	if buf.String() != after {
		t.Errorf("heartbeat wrote after Run returned: before sleep %q, after %q", after, buf.String())
	}
}

func TestRunHeartbeatNilOutIsANoop(t *testing.T) {
	// Every existing Spec literal in the codebase leaves Out unset; nil must
	// stay a safe no-op rather than a panic or a required field.
	spec := specFor(t)
	spec.HeartbeatInterval = time.Millisecond
	res, err := worker.Run(context.Background(), &shAdapter{script: `sleep 0.02; printf '{}' > "$HANDOFF"`}, spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Handoff == nil {
		t.Fatal("Handoff = nil")
	}
}

func TestRunTimeoutHeartbeatStopsCleanly(t *testing.T) {
	// Run under `go test -race`: a heartbeat that keeps ticking (and
	// writing) after the timeout kills the child, instead of stopping when
	// Run returns, would race the read of buf below.
	spec := specFor(t)
	spec.Timeout = 50 * time.Millisecond
	spec.HeartbeatInterval = 2 * time.Millisecond
	var buf bytes.Buffer
	spec.Out = &buf
	spec.Label = "revise round 1"
	spec.Clock = counterClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Minute)

	res, err := worker.Run(context.Background(), &shAdapter{script: "sleep 10"}, spec)
	if err == nil {
		t.Fatal("Run reported success for a timed-out worker")
	}
	if !res.TimedOut {
		t.Errorf("TimedOut = false, want true")
	}

	after := buf.String()
	if after == "" {
		t.Fatal("no heartbeat lines were written before the timeout")
	}
	time.Sleep(20 * time.Millisecond)
	if buf.String() != after {
		t.Errorf("heartbeat wrote after Run returned from a timeout: before sleep %q, after %q", after, buf.String())
	}
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
