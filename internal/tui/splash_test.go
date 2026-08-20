package tui

import (
	"testing"

	"github.com/mattwalters/wand/internal/cockpit"
	"github.com/mattwalters/wand/internal/tuitest"
)

// The splash only shows on a terminal tall enough to spare the room; the
// default 80x24 goldens (board.txt, board-sample.txt, ...) pin that it stays
// out of the way there. This proves the other half: that a generously sized
// terminal gets the art, and a too-small one degrades cleanly rather than
// corrupting the layout.
//
// Each case builds its own model sized to match the render, rather than
// sharing one: the initial WindowSizeMsg a tea.Program sends is delivered
// asynchronously, and with no script to force a sync point, a model whose
// Config size disagreed with the render size could be captured mid-resize.
func TestSplashScreens(t *testing.T) {
	splashModel := func(width, height int) Model {
		return New(Config{
			Snapshot: cockpit.Sample(),
			Backend:  &fakeBackend{},
			Width:    width,
			Height:   height,
		})
	}
	tuitest.AssertScreenSize(t, "board-splash", splashModel(80, 32), "", 80, 32)
	tuitest.AssertScreenSize(t, "board-splash-narrow", splashModel(15, 32), "", 15, 32)
	tuitest.AssertScreenSize(t, "board-splash-short", splashModel(80, 10), "", 80, 10)
}
