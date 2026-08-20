//go:build e2e

// Package e2e drives the compiled wand binary through a real pseudo-terminal.
//
// This is the only tier that exercises what an in-process test cannot: that
// the binary detects a TTY, enters and leaves the alternate screen, responds
// to a keystroke arriving as bytes over a pty, and exits cleanly. How a screen
// *looks* is covered far more cheaply by the golden-screen tests in
// internal/tui, so this stays deliberately at one smoke test.
//
// Run with: make test-e2e
package e2e

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
)

const (
	termWidth  = 80
	termHeight = 24
	timeout    = 30 * time.Second
)

// buildWand compiles the binary under test and returns its path.
func buildWand(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "wand")
	cmd := exec.Command("go", "build", "-o", bin, "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building wand: %v\n%s", err, out)
	}
	return bin
}

// liveScreen is a virtual terminal fed by a pty in the background.
//
// The mutex guards the screen buffer only. Replies deliberately sit outside
// it: reading them touches the emulator's reply pipe rather than the buffer,
// and blocking on that pipe while holding the lock would wedge everything.
type liveScreen struct {
	mu  sync.Mutex
	emu *vt.Emulator
}

func newLiveScreen() *liveScreen {
	return &liveScreen{emu: vt.NewEmulator(termWidth, termHeight)}
}

func (s *liveScreen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.Write(p)
}

func (s *liveScreen) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.String()
}

// Close releases the emulator, unblocking anything reading its replies.
func (s *liveScreen) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.Close()
}

// replies is the stream of responses the emulator generates when the
// application queries the terminal's capabilities.
//
// These must be drained. The emulator writes them into an unbuffered pipe, so
// leaving them unread blocks its Write, which stops the screen updating and
// backs up until the application itself blocks writing to the pty. Copying
// them back to the application is also simply what a real terminal does.
func (s *liveScreen) replies() io.Reader { return s.emu }

// waitForText polls the screen until it contains want and returns what was on
// screen at that moment. Polling is unavoidable here: unlike the in-process
// harness, a real process renders whenever it likes.
func (s *liveScreen) waitForText(t *testing.T, want string) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := s.String(); strings.Contains(got, want) {
			return got
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %q on screen; last screen was:\n%s", want, s.String())
	return ""
}

// waitExit waits for cmd, failing the test if it outlives the timeout.
//
// xpty.WaitProcess handles the Windows ConPty case, but on every other
// platform it is a bare cmd.Wait that ignores its context, so the timeout has
// to be enforced out here.
func waitExit(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- xpty.WaitProcess(context.Background(), cmd) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wand exited with an error, want a clean exit: %v", err)
		}
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("wand did not exit after receiving the quit key")
	}
}

// waitExitError waits for cmd, failing the test if it outlives the timeout
// or if it exits cleanly — the caller expects a failure.
func waitExitError(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- xpty.WaitProcess(context.Background(), cmd) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("wand exited cleanly, want an error (no team key)")
		}
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("wand did not exit")
	}
}

// Bare `wand` over a real pty must take the same path `wand ui` does: it is
// a TTY, so it must attempt the cockpit rather than falling back to help.
// It cannot render a full board without live Linear, so what this proves is
// narrower but just as load-bearing: given a real terminal, root's RunE
// reaches runCockpit's team-key resolution rather than either hanging or
// silently printing help — the two ways an inverted TTY check would fail
// silently. Run in an empty directory with no wand.toml, so the failure is
// hermetic: no network, no team, no API key required.
func TestBareWandOverAPTYEntersTheCockpitPath(t *testing.T) {
	bin := buildWand(t)

	pty, err := xpty.NewPty(termWidth, termHeight)
	if err != nil {
		t.Fatalf("creating pty: %v", err)
	}
	defer pty.Close() //nolint:errcheck // best effort on teardown

	cmd := exec.Command(bin)
	cmd.Dir = t.TempDir()
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	if err := pty.Start(cmd); err != nil {
		t.Fatalf("starting wand: %v", err)
	}

	screen := newLiveScreen()
	defer screen.Close() //nolint:errcheck // best effort on teardown

	go func() { _, _ = io.Copy(pty, screen.replies()) }()

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(screen, pty)
	}()

	got := screen.waitForText(t, "team key")
	if !strings.Contains(got, "pass --team-key, or add [team] key to wand.toml") {
		t.Errorf("unexpected refusal text; screen was:\n%s", got)
	}

	waitExitError(t, cmd)

	_ = pty.Close()

	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Error("pty reader did not finish after the pty was closed")
	}
}

func TestBinaryRendersAndQuitsCleanly(t *testing.T) {
	bin := buildWand(t)

	pty, err := xpty.NewPty(termWidth, termHeight)
	if err != nil {
		t.Fatalf("creating pty: %v", err)
	}
	defer pty.Close() //nolint:errcheck // best effort on teardown

	// --sample so the smoke test needs no API key and no team: what it is
	// proving is that the binary drives a real terminal, not that Linear
	// answered.
	cmd := exec.Command(bin, "ui", "--sample")
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	if err := pty.Start(cmd); err != nil {
		t.Fatalf("starting wand: %v", err)
	}

	screen := newLiveScreen()
	defer screen.Close() //nolint:errcheck // best effort on teardown

	// Terminal to application: answer capability queries.
	go func() { _, _ = io.Copy(pty, screen.replies()) }()

	// Application to terminal: paint the screen.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		// Runs until the child exits and the pty reports EOF (or EIO, which is
		// how some platforms report the same thing). Neither is a failure.
		_, _ = io.Copy(screen, pty)
	}()

	// The cockpit should render all four queues without any input.
	got := screen.waitForText(t, "Needs Input")
	for _, want := range []string{"wand cockpit", "Triage", "Needs Input", "Ready for human", "Lanes"} {
		if !strings.Contains(got, want) {
			t.Errorf("cockpit is missing %q; screen was:\n%s", want, got)
		}
	}

	// Quit via a keystroke arriving as bytes over the pty, as a real user's would.
	if _, err := pty.Write([]byte("q")); err != nil {
		t.Fatalf("sending quit key: %v", err)
	}

	waitExit(t, cmd)

	// Closing our end releases the reader, which is otherwise parked on a pty
	// that stays open as long as we hold it. The deferred Close is retained
	// for the paths that fail before reaching here.
	_ = pty.Close()

	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Error("pty reader did not finish after the pty was closed")
	}
}
