package tui

import "strings"

// wizardArt is the board header's decoration — a hat, a spark, a couple of
// stars. It is pure delight: nothing on the board depends on it, and it
// never grows the chrome the board actually needs to lay out its rows.
var wizardArt = []string{
	`      ✦`,
	`     /_\`,
	`    /   \`,
	`   / * * \`,
	`  /_______\`,
}

// splashMinHeight is how tall the terminal has to be before the art shows.
// Set comfortably above screen.DefaultHeight (24) so every golden captured
// at the default size — where the board is already close to full, see
// board-sample.txt — is untouched by this file.
const splashMinHeight = 32

// splashMinWidth is the narrowest terminal the art will still draw in.
// Below it the art is omitted outright rather than clipped into something
// unrecognizable.
const splashMinWidth = 20

// splashView renders the wizard art, or nothing if the terminal is too
// small to spare the room. It is a pure function of size, so it renders the
// same frame every time — nothing here needs to check theme.Static itself,
// there is simply nothing in it that varies between renders.
func (m Model) splashView() string {
	if m.height < splashMinHeight || m.width < splashMinWidth {
		return ""
	}
	lines := make([]string, len(wizardArt))
	for i, art := range wizardArt {
		lines[i] = m.theme.Splash.Render(truncate(pad(gutter)+art, m.width))
	}
	return strings.Join(lines, "\n") + "\n\n"
}
