// Package theme holds every style wand renders with. Styles live here and
// nowhere else so that a visual change is a one-file diff, and so that the
// test harness can reason about what varies between renders.
package theme

import "charm.land/lipgloss/v2"

// Static disables anything that varies between otherwise-identical renders:
// animations, spinners, elapsed times. The test harness and the headless
// screen dump both set it, which is what makes golden screens byte-stable.
//
// See the determinism rules in CLAUDE.md.
var Static bool

// Theme is the full set of styles used by the TUI.
type Theme struct {
	Title    lipgloss.Style
	Heading  lipgloss.Style
	Body     lipgloss.Style
	Muted    lipgloss.Style
	Selected lipgloss.Style

	// Bless is the one style reserved for a single act: promoting work to
	// Todo or Scoping. Nothing else on the board wears it, which is what
	// makes the blessing screen read as a different kind of moment rather
	// than another confirmation dialog.
	Bless lipgloss.Style
	// Warn marks a state that needs a person — a stuck lane, an unranked
	// ticket about to be blessed.
	Warn lipgloss.Style
	// Bad marks a failure the user has to read: a refused write.
	Bad lipgloss.Style
	// Good marks a completed act.
	Good lipgloss.Style
	// Key styles a keystroke inside a help or prompt line.
	Key lipgloss.Style
	// Splash styles the wizard art on the board's header, when there is
	// room for it. It shares Title's color rather than inventing a new
	// one: the art and "wand cockpit" are the same kind of chrome.
	Splash lipgloss.Style
}

// New returns the default theme.
func New() Theme {
	return Theme{
		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("13")).
			Padding(0, 1),
		Heading: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("12")),
		Body: lipgloss.NewStyle(),
		Muted: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")),
		Selected: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("13")),
		Bless: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("13")),
		Warn: lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")),
		Bad: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("1")),
		Good: lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")),
		Key: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("12")),
		Splash: lipgloss.NewStyle().
			Foreground(lipgloss.Color("13")),
	}
}
