package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mattwalters/wand/internal/screen"
	"github.com/mattwalters/wand/internal/tuitest"
)

// apply feeds a key script through Update and returns the resulting model.
// This is the Tier 0 path: no runtime, no terminal, just the pure transition
// function.
func apply(t *testing.T, m Model, script string) (Model, tea.Cmd) {
	t.Helper()

	msgs, err := screen.ParseScript(script)
	if err != nil {
		t.Fatalf("bad key script %q: %v", script, err)
	}

	var cmd tea.Cmd
	for _, msg := range msgs {
		var next tea.Model
		next, cmd = m.Update(msg)
		updated, ok := next.(Model)
		if !ok {
			t.Fatalf("Update returned %T, want tui.Model", next)
		}
		m = updated
	}
	return m, cmd
}

// --- Tier 0: pure Update transitions -------------------------------------

func TestUpdateNavigation(t *testing.T) {
	tests := []struct {
		name         string
		script       string
		wantState    state
		wantIndex    int
		wantSelected string
	}{
		{name: "starts on the first item", script: "", wantState: stateMenu, wantIndex: 0},
		{name: "j moves down", script: "j", wantState: stateMenu, wantIndex: 1},
		{name: "arrow key moves down", script: "down", wantState: stateMenu, wantIndex: 1},
		{name: "k moves back up", script: "j,k", wantState: stateMenu, wantIndex: 0},
		{name: "down stops at the last item", script: "j,j,j,j", wantState: stateMenu, wantIndex: 2},
		{name: "enter opens the first command", script: "enter", wantState: stateDetail, wantIndex: 0, wantSelected: "init"},
		{name: "enter opens the selected command", script: "j,enter", wantState: stateDetail, wantIndex: 1, wantSelected: "covenant"},
		{name: "esc returns to the menu", script: "j,enter,esc", wantState: stateMenu, wantIndex: 1, wantSelected: "covenant"},
		{name: "navigation is inert on the detail screen", script: "enter,j,j", wantState: stateDetail, wantIndex: 0, wantSelected: "init"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := apply(t, New(screen.DefaultWidth, screen.DefaultHeight), tt.script)

			if got.state != tt.wantState {
				t.Errorf("state = %v, want %v", got.state, tt.wantState)
			}
			if got.list.Index() != tt.wantIndex {
				t.Errorf("list index = %d, want %d", got.list.Index(), tt.wantIndex)
			}
			if got.selected.Name != tt.wantSelected {
				t.Errorf("selected = %q, want %q", got.selected.Name, tt.wantSelected)
			}
		})
	}
}

func TestQuitKeys(t *testing.T) {
	for _, script := range []string{"q", "ctrl+c", "enter,q"} {
		t.Run(script, func(t *testing.T) {
			_, cmd := apply(t, New(screen.DefaultWidth, screen.DefaultHeight), script)
			if cmd == nil {
				t.Fatalf("%q produced no command, want quit", script)
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Errorf("%q produced %T, want tea.QuitMsg", script, cmd())
			}
		})
	}
}

func TestWindowResizePropagatesToList(t *testing.T) {
	m := New(screen.DefaultWidth, screen.DefaultHeight)

	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	got := next.(Model)

	if got.width != 100 || got.height != 40 {
		t.Errorf("model size = %dx%d, want 100x40", got.width, got.height)
	}
	if w := got.list.Width(); w != 100 {
		t.Errorf("list width = %d, want 100", w)
	}
}

// --- Tier 1: wiring through the real runtime -----------------------------

func TestProgramWiring(t *testing.T) {
	final, trace := tuitest.FinalModel(t, New(screen.DefaultWidth, screen.DefaultHeight), "j,enter")

	m, ok := final.(Model)
	if !ok {
		t.Fatalf("final model is %T, want tui.Model", final)
	}
	if m.state != stateDetail {
		t.Errorf("state = %v, want stateDetail", m.state)
	}
	if m.selected.Name != "covenant" {
		t.Errorf("selected = %q, want %q", m.selected.Name, "covenant")
	}

	// The runtime should have seen exactly the keys we sent, in order. This is
	// what distinguishes a wiring failure from a rendering one.
	want := []string{"j", "enter"}
	got := trace.Keys()
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("key %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// --- Tier 2: rendered screens --------------------------------------------

func TestScreens(t *testing.T) {
	tests := []struct {
		golden string
		script string
	}{
		{golden: "menu", script: ""},
		{golden: "menu-second-item", script: "j"},
		{golden: "detail", script: "j,enter"},
	}

	for _, tt := range tests {
		t.Run(tt.golden, func(t *testing.T) {
			tuitest.AssertScreen(t, tt.golden, New(screen.DefaultWidth, screen.DefaultHeight), tt.script)
		})
	}
}
