// Package screen renders a Bubble Tea model to plain text, exactly as a
// terminal would display it.
//
// This is the core of wand's verification story, and it deliberately has no
// dependency on the testing package so that both the test harness
// (internal/tuitest) and the shipped binary (`wand ui --dump-screen`) can use
// it. Those two paths therefore produce byte-identical output: what an agent
// sees when it runs the command by hand is what the golden files contain.
//
// How it works: the model is driven by a real tea.Program whose output is
// captured rather than written to a terminal. Those raw ANSI bytes are then
// replayed into a virtual terminal emulator, which resolves them into a grid
// of cells. Reading that grid back gives the rendered screen.
//
// Golden-filing the raw ANSI stream instead would be a mistake: it contains
// every intermediate frame from the incremental renderer plus color escapes,
// so it is brittle across color profiles and unreadable in a diff.
package screen

import (
	"bytes"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/vt"

	"github.com/mattwalters/wand/internal/theme"
)

// Default terminal dimensions for rendering. Fixed on purpose: a screen
// rendered at the ambient terminal size would produce goldens that differ
// between machines.
const (
	DefaultWidth  = 80
	DefaultHeight = 24
)

// exitAltScreen is the escape sequence a program emits when it leaves the
// alternate screen buffer, which Bubble Tea does automatically on quit. The
// emulator honours it and switches back to the (empty) primary buffer, so the
// stream is truncated here before replay — otherwise every snapshot of a
// full-screen app would come back blank.
const exitAltScreen = "\x1b[?1049l"

// setLineFeedNewLine enables LNM, which makes a bare line feed also return the
// cursor to column 0.
//
// Bubble Tea emits "\n" between rows, not "\r\n". On a real terminal the tty
// driver's ONLCR flag expands it in the kernel on the way out. Captured output
// never passes through a tty, so the emulator is put into the equivalent
// terminal mode instead. Without this every row starts where the previous one
// ended and the screen renders as a diagonal cascade.
const setLineFeedNewLine = "\x1b[20h"

// Result is a rendered screen.
type Result struct {
	// Rows is the screen content, one string per terminal row, with trailing
	// whitespace and trailing blank rows removed.
	Rows []string

	// Raw is the untruncated ANSI stream the program produced. Kept for
	// debugging; assertions should use Rows.
	Raw []byte
}

// String returns the rendered screen as it would appear in a terminal.
func (r Result) String() string {
	return strings.Join(r.Rows, "\n")
}

// Render runs m, delivers each message in script to it in order, and returns
// the resulting screen.
//
// Rendering is synchronous and deterministic: tea.Program.Send blocks until
// the runtime accepts each message, and messages are processed in order, so
// the returned screen always reflects the whole script. Nothing here depends
// on wall-clock timing.
//
// Render is not safe for concurrent use — it toggles theme.Static for the
// duration of the call to freeze animation.
func Render(m tea.Model, script []tea.Msg, width, height int) (Result, error) {
	if width <= 0 {
		width = DefaultWidth
	}
	if height <= 0 {
		height = DefaultHeight
	}

	defer func(prev bool) { theme.Static = prev }(theme.Static)
	theme.Static = true

	out := &syncBuffer{}
	p := tea.NewProgram(m,
		// Input is disabled; the script drives the model through Send.
		tea.WithInput(nil),
		tea.WithOutput(out),
		// Ascii strips color, so goldens do not depend on the color profile
		// of whatever terminal or CI runner happens to be rendering them.
		tea.WithColorProfile(colorprofile.Ascii),
		tea.WithWindowSize(width, height),
		tea.WithoutSignalHandler(),
	)

	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()

	// Send blocks until the runtime receives the message, and Quit travels the
	// same channel, so the script is fully applied before shutdown begins.
	for _, msg := range script {
		p.Send(msg)
	}
	p.Quit()

	if err := <-done; err != nil {
		return Result{Raw: out.Bytes()}, err
	}

	raw := out.Bytes()
	emu := vt.NewEmulator(width, height)
	if _, err := emu.WriteString(setLineFeedNewLine); err != nil {
		return Result{Raw: raw}, err
	}
	if _, err := emu.Write(lastFrame(raw)); err != nil {
		return Result{Raw: raw}, err
	}

	return Result{Rows: normalize(emu.String()), Raw: raw}, nil
}

// lastFrame trims the shutdown sequence so the emulator is left holding the
// final frame the user actually saw, rather than the empty primary buffer the
// program restores on its way out.
func lastFrame(raw []byte) []byte {
	if i := bytes.LastIndex(raw, []byte(exitAltScreen)); i >= 0 {
		return raw[:i]
	}
	return raw
}

// normalize makes a screen safe to store in a golden file: trailing spaces
// carry no visible information but would show up in every diff, and a
// full-height screen is mostly empty padding at the bottom.
func normalize(s string) []string {
	rows := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, row := range rows {
		rows[i] = strings.TrimRight(row, " \t")
	}
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

// syncBuffer is an io.Writer safe for the renderer goroutine to write to while
// the calling goroutine is driving the script.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}
