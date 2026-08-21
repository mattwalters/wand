package tui

import (
	"charm.land/bubbles/v2/key"

	"github.com/mattwalters/wand/internal/home"
)

// keyMap collects every binding the app responds to. Keeping them in one
// struct means the help line and the Update switch cannot drift apart.
//
// The disposition keys are deliberately *not* here: they live on
// home.Disposition, next to the sentence each one means, so that adding a
// judgment is one edit rather than three that have to agree.
type keyMap struct {
	Up      key.Binding
	Down    key.Binding
	Open    key.Binding
	Confirm key.Binding
	Back    key.Binding
	Refresh key.Binding
	Engage  key.Binding
	Quit    key.Binding
	// ForceQuit works from everywhere, typing included. Quit does not:
	// "q" is a letter someone may be part-way through typing into a
	// cancellation reason, and losing that text to a quit is the kind of
	// small betrayal that makes people stop using a screen.
	ForceQuit key.Binding
	// View selects one of the three views by number, from home or from
	// another view. Plain digits are the only keys free of every
	// disposition (t/s/b/u/d/x), engage (e), refresh (r), quit (q/ctrl+c)
	// and move (j/k) — see TestDispositionKeysAvoidNavigation and its
	// tui-side analogue.
	View [3]key.Binding
}

func defaultKeyMap() keyMap {
	return keyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Open: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "open"),
		),
		// Confirm is enter and only enter, on every screen that asks for
		// one. A y/n prompt cannot coexist with a text field — "y" is a
		// letter someone is trying to type into the reason — and one
		// confirm key everywhere beats two that depend on the field.
		Confirm: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "confirm"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "refresh"),
		),
		Engage: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "engage"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q"),
			key.WithHelp("q", "quit"),
		),
		ForceQuit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
		View: viewKeys(),
	}
}

// viewKeys builds the three view bindings from home.Views, so the digit
// each one answers to has one source rather than a copy kept in sync here.
func viewKeys() [3]key.Binding {
	var out [3]key.Binding
	for i, vi := range home.Views {
		out[i] = key.NewBinding(
			key.WithKeys(vi.Key),
			key.WithHelp(vi.Key, vi.Title),
		)
	}
	return out
}
