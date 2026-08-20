// Package tuitest is the test-facing layer over internal/screen.
//
// It exists so that tests read as assertions about what is on screen rather
// than as terminal plumbing, and so that the plumbing itself lives in one
// place. If the experimental Charm packages underneath churn — teatest and vt
// are both untagged — this package absorbs the change and the tests do not.
//
// Two levels are available:
//
//   - AssertScreen compares the rendered screen against a golden file. This is
//     the agent-readable layer: the golden is a picture of the UI.
//   - FinalModel runs the model through a real program and hands back its end
//     state plus a trace of every message the runtime processed, for asserting
//     on behaviour rather than pixels.
package tuitest

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aymanbagabas/go-udiff"
	"github.com/charmbracelet/colorprofile"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/mattwalters/wand/internal/screen"
)

// init registers -update unless something else already has.
//
// The transitively-imported x/exp/golden package registers a flag of the same
// name, and registering it twice panics at startup. Binding to whichever
// registration wins keeps a single -update flag driving every golden in the
// repo, and keeps working if that dependency ever goes away.
func init() {
	if flag.Lookup(updateFlag) == nil {
		flag.Bool(updateFlag, false, "update golden files")
	}
}

const updateFlag = "update"

// updateRequested reports whether the run was invoked with -update.
//
// Regenerating is a deliberate act: read the resulting diff and confirm the
// change was intended before committing it. Never run it just to turn a red
// test green — see CLAUDE.md.
func updateRequested() bool {
	f := flag.Lookup(updateFlag)
	if f == nil {
		return false
	}
	getter, ok := f.Value.(flag.Getter)
	if !ok {
		return false
	}
	on, _ := getter.Get().(bool)
	return on
}

// goldenDir is where screen goldens live, relative to the package under test.
const goldenDir = "testdata/screens"

// Render renders m after applying script, failing the test on error.
//
// script is a comma-separated key sequence such as "j,j,enter"; see
// screen.ParseScript.
func Render(tb testing.TB, m tea.Model, script string) screen.Result {
	tb.Helper()
	return RenderSize(tb, m, script, screen.DefaultWidth, screen.DefaultHeight)
}

// RenderSize is Render at an explicit terminal size, for the few screens
// (the wizard splash among them) whose appearance depends on room rather
// than being fixed at the default 80x24.
func RenderSize(tb testing.TB, m tea.Model, script string, width, height int) screen.Result {
	tb.Helper()

	msgs, err := screen.ParseScript(script)
	if err != nil {
		tb.Fatalf("bad key script %q: %v", script, err)
	}

	result, err := screen.Render(m, msgs, width, height)
	if err != nil {
		tb.Fatalf("rendering screen: %v", err)
	}
	return result
}

// AssertScreen renders m after applying script and compares the result against
// the golden file named by name.
//
// On mismatch the failure is a unified diff of the rendered screen, which is
// readable directly: it shows what changed on screen, not which bytes moved.
func AssertScreen(tb testing.TB, name string, m tea.Model, script string) {
	tb.Helper()
	AssertScreenSize(tb, name, m, script, screen.DefaultWidth, screen.DefaultHeight)
}

// AssertScreenSize is AssertScreen at an explicit terminal size.
func AssertScreenSize(tb testing.TB, name string, m tea.Model, script string, width, height int) {
	tb.Helper()

	got := RenderSize(tb, m, script, width, height).String()
	path := filepath.Join(goldenDir, name+".txt")

	if updateRequested() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			tb.Fatalf("creating golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			tb.Fatalf("writing golden %s: %v", path, err)
		}
		tb.Logf("updated golden %s", path)
		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			tb.Fatalf("golden %s does not exist; create it with:\n\tgo test ./... -update\n\nrendered screen was:\n%s", path, got)
		}
		tb.Fatalf("reading golden %s: %v", path, err)
	}

	want := strings.TrimRight(string(raw), "\n")
	if got == want {
		return
	}

	tb.Errorf("screen does not match %s\n\n%s\nIf this change is intended, regenerate with `make update-goldens`, then read the diff before committing.",
		path, udiff.Unified("golden", "rendered", want+"\n", got+"\n"))
}

// Trace records every message the Bubble Tea runtime processed, in order.
//
// Asserting on the message sequence gives a far more actionable failure than a
// screen diff: "the select key produced no command" points at a cause, where
// "row 7 differs" only points at a symptom.
type Trace struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (t *Trace) record(_ tea.Model, msg tea.Msg) tea.Msg {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.msgs = append(t.msgs, msg)
	return msg
}

// Messages returns the recorded messages in the order they were processed.
func (t *Trace) Messages() []tea.Msg {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]tea.Msg(nil), t.msgs...)
}

// Keys returns the string form of every key press that reached the runtime.
func (t *Trace) Keys() []string {
	var keys []string
	for _, msg := range t.Messages() {
		if k, ok := msg.(tea.KeyPressMsg); ok {
			keys = append(keys, k.String())
		}
	}
	return keys
}

// FinalModel runs m through a real Bubble Tea program, applies script, and
// returns the model as it was when the program exited, along with the trace of
// messages the runtime saw.
//
// Unlike AssertScreen this asserts on state rather than appearance, so use it
// for wiring: that a key reaches the right branch, that a command fires.
func FinalModel(tb testing.TB, m tea.Model, script string) (tea.Model, *Trace) {
	tb.Helper()

	msgs, err := screen.ParseScript(script)
	if err != nil {
		tb.Fatalf("bad key script %q: %v", script, err)
	}

	trace := &Trace{}
	tm := teatest.NewTestModel(tb, m,
		teatest.WithInitialTermSize(screen.DefaultWidth, screen.DefaultHeight),
		teatest.WithProgramOptions(
			tea.WithColorProfile(colorprofile.Ascii),
			tea.WithFilter(trace.record),
		),
	)

	for _, msg := range msgs {
		tm.Send(msg)
	}
	if err := tm.Quit(); err != nil {
		tb.Fatalf("quitting program: %v", err)
	}

	return tm.FinalModel(tb), trace
}
