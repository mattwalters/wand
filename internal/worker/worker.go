// Package worker is the seam between wand's orchestrators and the coding
// harnesses that do the work.
//
// An adapter is small by design: it turns a Spec into one headless
// invocation — argv, environment, a prompt delivered on stdin. Everything
// else is the runner's, shared across harnesses: the timeout, the prompt
// contract, the credential strip, and collecting the JSON handoff file the
// worker leaves behind. Keeping the harness-specific surface this narrow is
// what keeps harness-isms out of the core, and what makes runs comparable
// across harnesses — the same Spec, the same contract, the same handoff.
//
// Two rules here are structural rather than advisory:
//
//   - Workers never write Linear or GitHub. The orchestrator owns every
//     external write, because two writers on one ticket cannot be
//     reconciled afterwards. Run builds the child environment through
//     ChildEnviron before the adapter ever sees it — credentials
//     stripped, machine-wide ambient credentials (gh's config-dir token)
//     redirected away — and each adapter closes what is specific to its
//     own harness (inherited MCP servers, settings files). The guarantee
//     is proven per adapter by the isolation conformance suite
//     (internal/workertest), never assumed:
//     in the reference system it held only by accident, and a different
//     harness would have broken it silently.
//
//   - A worker never discovers what kind of run it is in by probing its
//     environment. Permission and sandbox layers can block exactly the
//     probes the docs suggest, and the failure is silent (the PW-174
//     lesson). So Run composes the contract — mode, rules, scratch
//     directory, handoff path — into the head of every prompt, and the
//     adapter receives the finished prompt rather than the pieces:
//     nothing about the run is left to inference, and no adapter can
//     forget to say so.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Spec is everything the orchestrator hands down for one worker run. Every
// field the worker needs to know about is stated here and ends up in the
// prompt; a Spec that leaves something to be discovered is invalid by
// design, which is why the fields below are required rather than defaulted.
type Spec struct {
	// Mode names the kind of run, stated to the worker verbatim — "scope
	// (read-only research)", "implement", "review". The worker acts on this
	// statement, never on what its environment appears to allow.
	Mode string

	// Rules are the mode-dependent rules, stated in the contract ahead of
	// the task. The always-true rules (handoff, scratch, no external
	// writes) are composed in by Run; these are the orchestrator's own.
	Rules []string

	// Prompt is the phase's task, appended after the contract.
	Prompt string

	// Dir is the working directory the harness runs in — the worktree.
	Dir string

	// ScratchDir is the per-run scratch directory, part of the handed-down
	// contract: the worker is told where it may write freely instead of
	// guessing at tmp dirs.
	ScratchDir string

	// HandoffPath is where the worker writes its result as a single JSON
	// object. The runner reads it and then deletes it, so a later phase
	// cannot consume an earlier phase's output. It must live inside
	// ScratchDir: the scratch directory is the only write grant an adapter
	// is required to give the worker.
	HandoffPath string

	// Timeout bounds the whole run. A worker with no deadline is a zombie
	// factory, so zero is invalid rather than "no timeout".
	Timeout time.Duration

	// Model and Effort are the per-phase selection, passed through to the
	// harness. Empty means the harness's default.
	Model  string
	Effort string

	// Out, if set, receives a periodic heartbeat line while the worker is
	// outstanding — "<Label>: Nm elapsed, Mm left" — so a slow phase and a
	// wedged one are distinguishable from outside. Nil means no heartbeat,
	// which is why every existing Spec literal keeps compiling unchanged.
	// This is narration only, never read back by Run or anything else.
	Out io.Writer

	// Label names the phase in the heartbeat line ("implement round 1").
	// Unused when Out is nil.
	Label string

	// HeartbeatInterval is how often the heartbeat writes. Zero means the
	// package default (heartbeatInterval).
	HeartbeatInterval time.Duration

	// OnHeartbeat, if set, is called alongside each heartbeat tick — the
	// same tick that writes to Out, so a caller can turn it into a lease
	// renewal (journal.Run.Heartbeat) without this package knowing what a
	// lease is. Runs on the heartbeat goroutine, so it must not block or
	// touch anything Run itself is using concurrently; errors are the
	// caller's own to report, through Out or otherwise. Nil means nothing
	// extra happens on a tick, which is why every existing Spec literal
	// keeps compiling unchanged.
	OnHeartbeat func()

	// Clock is the heartbeat's clock. Nil means time.Now — tests set it so
	// heartbeat timing is reproducible, matching journal.Store.Now.
	Clock func() time.Time
}

// Paths in a Spec must be absolute: the worker resolves a relative path
// against its own working directory (spec.Dir), while the runner's own
// MkdirAll/ReadFile/Remove resolve the same string against the
// orchestrator's — a correct worker would hand off into one directory and
// the runner would look in another.
func (s Spec) validate() error {
	switch {
	case strings.TrimSpace(s.Mode) == "":
		return errors.New("worker: Spec.Mode is required — the mode is stated, never inferred")
	case strings.TrimSpace(s.Prompt) == "":
		return errors.New("worker: Spec.Prompt is required")
	case s.Dir == "":
		return errors.New("worker: Spec.Dir is required")
	case !filepath.IsAbs(s.Dir):
		return errors.New("worker: Spec.Dir must be absolute — the worker and the orchestrator resolve relative paths against different directories")
	case s.ScratchDir == "":
		return errors.New("worker: Spec.ScratchDir is required — the scratch directory is part of the handed-down contract")
	case !filepath.IsAbs(s.ScratchDir):
		return errors.New("worker: Spec.ScratchDir must be absolute — the worker and the orchestrator resolve relative paths against different directories")
	case s.HandoffPath == "":
		return errors.New("worker: Spec.HandoffPath is required")
	case !filepath.IsAbs(s.HandoffPath):
		return errors.New("worker: Spec.HandoffPath must be absolute — the worker and the orchestrator resolve relative paths against different directories")
	case !within(s.ScratchDir, s.HandoffPath):
		return errors.New("worker: Spec.HandoffPath must be inside Spec.ScratchDir — the scratch directory is the only write grant an adapter is required to give the worker")
	case s.Timeout <= 0:
		return errors.New("worker: Spec.Timeout is required — a worker with no deadline is a zombie factory")
	}
	return nil
}

// within reports whether path lies strictly inside dir.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Invocation is one ready-to-exec headless run, an adapter's whole output.
type Invocation struct {
	// Argv is the command and its arguments; Argv[0] is resolved via PATH.
	Argv []string
	// Env is the complete child environment. The adapter builds it from
	// the environ Run hands it — already stripped and redirected by
	// ChildEnviron — plus its own harness-specific additions.
	Env []string
	// Dir is the working directory.
	Dir string
	// Stdin is the composed prompt, delivered on standard input — no
	// argv-length limits, nothing visible in `ps`.
	Stdin string
}

// Adapter turns a Spec into a harness invocation. Implementations must be
// pure — same inputs, same Invocation — so a test can hold every decision
// without spawning anything. prompt is the finished contract-plus-task from
// Compose, and environ is the finished child environment from ChildEnviron
// (credentials stripped, ambient-credential redirects applied); the
// adapter's job is only what is specific to its harness: the command
// template, the flag spelling of model and effort, and closing the
// harness's own credential leak paths.
type Adapter interface {
	Name() string
	Invocation(spec Spec, prompt string, environ []string) (Invocation, error)
}

// ConformanceAdapter optionally chooses the model selection for the live
// isolation probe. The probe itself is shared across harnesses; its model
// cannot be, because a selection valid for one CLI (for example Claude's
// "haiku") may be invalid for another. An adapter that does not implement
// this interface receives the suite's default selection.
type ConformanceAdapter interface {
	Adapter
	ConformanceSpec(Spec) Spec
}

// Result is what came back from one run. It is populated as fully as
// possible even when Run also returns an error, because the exit code and
// output tail are exactly what an orchestrator wants to journal about a
// failed run.
type Result struct {
	// Handoff is the worker's JSON handoff, a single JSON object, already
	// deleted from disk. Nil when the worker left none.
	Handoff json.RawMessage
	// ExitCode is the process's exit code; -1 if it never ran or was
	// killed by a signal (including the timeout).
	ExitCode int
	// TimedOut reports whether the run hit Spec.Timeout.
	TimedOut bool
	// Output is the tail of combined stdout and stderr, for diagnostics.
	// The handoff file is the worker's result; this is only what it said
	// on the way.
	Output string
	// Usage is the harness's own token accounting, parsed from Output by
	// an adapter that implements UsageAdapter. Nil when the harness does
	// not report usage, or a parse failed — absent, never estimated.
	Usage *Usage
}

// Usage is the token accounting a harness reported for one invocation.
// Fields are pointers so a reported zero is distinguishable from absent: a
// harness that cannot report tokens, or output a parser could not read,
// leaves the field nil rather than a faked value.
type Usage struct {
	InputTokens  *int64
	OutputTokens *int64
}

// UsageAdapter optionally parses a harness's own token accounting out of
// its captured combined stdout/stderr. Not every harness reports usage,
// and neither CLI's output shape is a versioned, guaranteed-stable API — a
// harness upgrade can silently change or drop it — so a parse miss must
// fail soft: an adapter that does not implement this interface, or whose
// ParseUsage finds nothing it recognizes, simply leaves Result.Usage nil.
type UsageAdapter interface {
	Adapter
	ParseUsage(output string) *Usage
}

// Compose renders the contract the orchestrator hands down, followed by the
// task. Every worker prompt starts with this: the mode, the rules, where to
// scratch, where to hand off — stated, so the worker never has to probe.
func Compose(spec Spec) string {
	var b strings.Builder
	b.WriteString("You are a cold worker in a wand run. This message states everything " +
		"you need to know about the run. Do not probe your environment to work out " +
		"what kind of run this is or what you are allowed to do — sandbox and " +
		"permission layers may block the probe silently, and the answer is already " +
		"stated here.\n\n")
	fmt.Fprintf(&b, "Mode: %s\n\n", spec.Mode)
	b.WriteString("Rules:\n")
	fmt.Fprintf(&b, "- Scratch directory: %s. Write temporary files there, not in the "+
		"working tree and not in system tmp dirs.\n", spec.ScratchDir)
	fmt.Fprintf(&b, "- Handoff: when you are done, write your result as a single JSON "+
		"object to %s. That file is your entire report — anything not in it is lost.\n",
		spec.HandoffPath)
	b.WriteString("- You have no Linear or GitHub credentials and you must not attempt " +
		"to write to either; the orchestrator owns every external write. If your task " +
		"seems to require one, report that in the handoff instead of attempting it.\n")
	for _, r := range spec.Rules {
		fmt.Fprintf(&b, "- %s\n", r)
	}
	b.WriteString("\n---\n\n")
	b.WriteString(spec.Prompt)
	return b.String()
}

// tailLimit bounds how much combined output a Result retains. The handoff
// file carries the worker's actual result; output is diagnostics only.
const tailLimit = 64 << 10

// Tail is an io.Writer keeping only the last Limit bytes, so a chatty or
// wedged child cannot grow an unbounded buffer. os/exec serializes writes
// when stdout and stderr share one non-file writer, so no lock is needed.
// Exported because every subprocess wand spawns — workers here, git and
// covenant commands in the orchestrator — needs the same bound, and one
// clipping rule (marker included) beats three drifting copies.
type Tail struct {
	// Limit is the byte bound; zero means the worker default.
	Limit   int
	buf     []byte
	clipped bool
}

func (t *Tail) limit() int {
	if t.Limit > 0 {
		return t.Limit
	}
	return tailLimit
}

func (t *Tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if n := t.limit(); len(t.buf) > n {
		t.buf = t.buf[len(t.buf)-n:]
		t.clipped = true
	}
	return len(p), nil
}

func (t *Tail) String() string {
	if t.clipped {
		return "[… earlier output clipped …]\n" + string(t.buf)
	}
	return string(t.buf)
}

// Clip is Tail for text already in hand: the last limit bytes of s, marked
// when anything was dropped.
func Clip(s string, limit int) string {
	t := &Tail{Limit: limit}
	t.Write([]byte(s))
	return t.String()
}

// Run spawns one worker through the adapter and collects its handoff.
//
// The error reports whether the run produced a usable handoff; the Result
// is populated either way. A worker may exit nonzero and still have handed
// off — the handoff is the report, the exit code is context — so only a
// missing or invalid handoff, a timeout, or a failure to spawn is an error.
func Run(ctx context.Context, a Adapter, spec Spec) (Result, error) {
	if err := spec.validate(); err != nil {
		return Result{ExitCode: -1}, err
	}

	environ := ChildEnviron(spec, os.Environ())
	inv, err := a.Invocation(spec, Compose(spec), environ)
	if err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("worker: %s: %w", a.Name(), err)
	}
	if len(inv.Argv) == 0 {
		return Result{ExitCode: -1}, fmt.Errorf("worker: %s produced an empty argv", a.Name())
	}
	// A nil Env would make os/exec fall back to inheriting this process's
	// full environment — exactly the leak the strip exists to prevent.
	if inv.Env == nil {
		inv.Env = environ
	}

	if err := os.MkdirAll(spec.ScratchDir, 0o755); err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("worker: creating scratch dir: %w", err)
	}
	// A handoff already sitting at the path is an earlier phase's output;
	// left in place, this phase could "succeed" without its worker writing
	// a thing.
	if err := os.Remove(spec.HandoffPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Result{ExitCode: -1}, fmt.Errorf("worker: clearing stale handoff: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, inv.Argv[0], inv.Argv[1:]...)
	cmd.Dir = inv.Dir
	cmd.Env = inv.Env
	cmd.Stdin = strings.NewReader(inv.Stdin)
	out := &Tail{}
	cmd.Stdout, cmd.Stderr = out, out
	// The deadline must actually end the run. Killing only the direct
	// child is not enough: a harness spawns helpers of its own, and any
	// survivor both keeps running in the worktree and holds the inherited
	// output pipe open — and Wait blocks until every write end closes. So
	// the kill targets the whole process group, and WaitDelay bounds the
	// wait for anything that escapes even that (a double-forked daemon).
	SetupProcessGroup(cmd)
	cmd.WaitDelay = WaitDelay

	stopHeartbeat := startHeartbeat(runCtx, spec)
	runErr := cmd.Run()
	stopHeartbeat()

	res := Result{ExitCode: -1, Output: out.String()}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ua, ok := a.(UsageAdapter); ok {
		res.Usage = ua.ParseUsage(res.Output)
	}
	// Attribute the deadline honestly: Spec.Timeout fired only if the run
	// actually failed while the caller's own context was still live. A
	// caller's earlier deadline or cancellation is the caller's act, and
	// reporting it as this run overrunning would journal a wrong diagnosis.
	res.TimedOut = runErr != nil && ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded)

	if runErr != nil && cmd.ProcessState == nil {
		// Never started: binary missing, unrunnable, context already dead.
		return res, fmt.Errorf("worker: spawning %s: %w", a.Name(), runErr)
	}

	// Collect (and delete) the handoff even after a timeout, so a partial
	// file cannot survive to satisfy a later phase.
	handoff, herr := collect(spec.HandoffPath)
	res.Handoff = handoff

	switch {
	case res.TimedOut:
		return res, fmt.Errorf("worker: %s timed out after %s", a.Name(), spec.Timeout)
	case runErr != nil && ctx.Err() != nil:
		return res, fmt.Errorf("worker: %s run canceled by the caller: %w", a.Name(), context.Cause(ctx))
	case herr != nil:
		return res, fmt.Errorf("worker: %s exited %d without a usable handoff: %w", a.Name(), res.ExitCode, herr)
	}
	return res, nil
}

// heartbeatInterval is the default period between heartbeat lines, used
// when Spec.HeartbeatInterval is zero.
const heartbeatInterval = 60 * time.Second

// startHeartbeat starts (unless spec.Out is nil) a goroutine that writes a
// periodic "<Label>: Nm elapsed, Mm left" line to spec.Out while ctx is
// live, and returns a function that stops the goroutine and blocks until it
// has exited. The caller must call the returned function before returning
// from Run, so nothing writes to spec.Out after Run has handed control back
// — a heartbeat racing the caller's own use of the same writer is exactly
// the bug this exists to avoid.
func startHeartbeat(ctx context.Context, spec Spec) func() {
	if spec.Out == nil && spec.OnHeartbeat == nil {
		return func() {}
	}
	interval := spec.HeartbeatInterval
	if interval <= 0 {
		interval = heartbeatInterval
	}
	clock := spec.Clock
	if clock == nil {
		clock = time.Now
	}
	start := clock()

	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if spec.Out != nil {
					fmt.Fprint(spec.Out, heartbeatLine(spec.Label, spec.Timeout, clock().Sub(start)))
				}
				if spec.OnHeartbeat != nil {
					spec.OnHeartbeat()
				}
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

// heartbeatLine renders one heartbeat line: elapsed since the worker
// started, and what remains of the timeout budget, clamped at zero so an
// elapsed run past its deadline (which Run then kills) never prints a
// negative remainder in the sliver before the kill lands.
func heartbeatLine(label string, timeout, elapsed time.Duration) string {
	remaining := timeout - elapsed
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("%s: %dm elapsed, %dm left\n", label, int64(elapsed/time.Minute), int64(remaining/time.Minute))
}

// WaitDelay bounds how long a Run waits, after the child exits or the kill
// lands, for the output pipe to close. It exists for the escape artists: a
// descendant that detached into its own process group before the kill still
// holds the pipe, and without a bound its survival would hold Run open
// forever. Exported alongside SetupProcessGroup: every subprocess wand
// spawns needs both, or a surviving grandchild holds it open forever.
const WaitDelay = 10 * time.Second

// collect reads the handoff and deletes it. Deletion is not tidiness: a
// handoff that outlives its read could be consumed again by the next phase,
// and a phase acting on a stale handoff is unrecoverable afterwards.
func collect(path string) (json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("no handoff file was written")
		}
		return nil, err
	}
	if err := os.Remove(path); err != nil {
		return nil, fmt.Errorf("deleting handoff after read: %w", err)
	}
	// The contract says a single JSON object, and json.Valid alone would
	// also accept `null`, a bare string or an array — any of which a later
	// phase would unmarshal into all-zero fields without an error.
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, errors.New("handoff is not a single JSON object")
	}
	return json.RawMessage(trimmed), nil
}

// AdapterFor resolves a harness name to its adapter. Empty means the
// default. The registry lives here, with the adapters, so every
// orchestrator resolves the same names to the same harnesses — a second
// copy of this switch is how one verb grows a harness another does not
// have.
func AdapterFor(name string) (Adapter, error) {
	switch name {
	case "", "claude-code":
		return ClaudeCode{}, nil
	case "codex":
		return Codex{}, nil
	}
	return nil, fmt.Errorf("worker: no adapter named %q (have claude-code, codex)", name)
}
