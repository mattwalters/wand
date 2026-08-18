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
	}
}
