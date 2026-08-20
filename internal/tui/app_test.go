package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mattwalters/wand/internal/cockpit"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/screen"
	"github.com/mattwalters/wand/internal/tuitest"
)

// fakeBackend records what the screen asked for. Every write the cockpit
// makes is a status transition the guard forbids agents, so a test that
// could not see exactly which one was attempted would not be testing much.
type fakeBackend struct {
	applied []cockpit.Intent
	err     error
	// preWritten makes Apply fail the way a partial judgment really does:
	// the comment or the relation landed, the status move did not. It is
	// what cockpit.Apply reports back through the returned intent.
	preWritten bool

	reads   int
	snap    cockpit.Snapshot
	readErr error
}

func (f *fakeBackend) Apply(_ context.Context, in cockpit.Intent) (cockpit.Intent, error) {
	f.applied = append(f.applied, in)
	if f.preWritten {
		in.Done.PreWritten = true
	}
	return in, f.err
}

func (f *fakeBackend) Read(context.Context) (cockpit.Snapshot, error) {
	f.reads++
	if f.readErr != nil {
		return cockpit.Snapshot{}, f.readErr
	}
	return f.snap, nil
}

// board builds a model over the sample board with a writer behind it.
func board(t *testing.T, back Backend) Model {
	t.Helper()
	return New(Config{
		Snapshot: cockpit.Sample(),
		Backend:  back,
		Width:    screen.DefaultWidth,
		Height:   screen.DefaultHeight,
	})
}

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

// run drives a command to its message and feeds that back in, the way the
// runtime would. Commands here are the only place I/O happens, so this is
// what lets a Tier 0 test cover a whole write round trip.
func run(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	next, _ := m.Update(cmd())
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want tui.Model", next)
	}
	return updated
}

// --- Tier 0: pure Update transitions -------------------------------------

func TestCursorMoves(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		want    int
		wantRow string // identifier or lane ticket under the cursor
	}{
		{name: "starts on the first row", script: "", want: 0, wantRow: "WND-42"},
		{name: "j moves down", script: "j", want: 1, wantRow: "WND-41"},
		{name: "arrow moves down", script: "down", want: 1, wantRow: "WND-41"},
		{name: "k moves back up", script: "j,k", want: 0, wantRow: "WND-42"},
		{name: "crosses a section boundary", script: "j,j", want: 2, wantRow: "WND-44"},
		{name: "reaches the lanes", script: "j,j,j,j,j", want: 5, wantRow: "WND-36"},
		{name: "stops at the last row", script: "j,j,j,j,j,j,j,j", want: 6, wantRow: "WND-33"},
		{name: "stops at the first row", script: "k,k,k", want: 0, wantRow: "WND-42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := apply(t, board(t, &fakeBackend{}), tt.script)
			if m.cursor != tt.want {
				t.Errorf("cursor = %d, want %d", m.cursor, tt.want)
			}
			row, ok := m.current()
			if !ok {
				t.Fatal("no row under the cursor")
			}
			got := row.Issue.Identifier
			if row.IsLane() {
				got = row.Lane.Ticket
			}
			if got != tt.wantRow {
				t.Errorf("row under cursor = %q, want %q", got, tt.wantRow)
			}
		})
	}
}

func TestDispositionKeysOpenTheConfirmation(t *testing.T) {
	tests := []struct {
		key        string
		wantStatus string
		wantBless  bool
	}{
		{key: "t", wantStatus: "todo", wantBless: true},
		{key: "s", wantStatus: "scoping", wantBless: true},
		{key: "b", wantStatus: "backlog"},
		{key: "u", wantStatus: "backlog"},
		{key: "d", wantStatus: "duplicate"},
		{key: "x", wantStatus: "canceled"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			m, _ := apply(t, board(t, &fakeBackend{}), tt.key)
			if m.state != stateConfirm {
				t.Fatalf("state = %v, want stateConfirm", m.state)
			}
			if m.pending.Disp.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", m.pending.Disp.Status, tt.wantStatus)
			}
			if m.pending.Disp.Bless != tt.wantBless {
				t.Errorf("bless = %v, want %v", m.pending.Disp.Bless, tt.wantBless)
			}
			if m.pending.Issue.Identifier != "WND-42" {
				t.Errorf("subject = %q, want WND-42", m.pending.Issue.Identifier)
			}
		})
	}
}

// Lanes and ready-for-human rows are read-only by design: the act each is
// asking for happens somewhere other than a status field.
func TestRowsWithoutDispositions(t *testing.T) {
	for _, script := range []string{"j,j,j,j", "j,j,j,j,j"} {
		t.Run(script, func(t *testing.T) {
			m, _ := apply(t, board(t, &fakeBackend{}), script+",t")
			if m.state != stateBoard {
				t.Errorf("state = %v, want stateBoard: this row offers no judgments", m.state)
			}
		})
	}
}

func TestBlessWritesAndDropsTheRow(t *testing.T) {
	back := &fakeBackend{}
	m, cmd := apply(t, board(t, back), "t,enter")
	if !m.busy {
		t.Error("model is not busy after confirming; the write should be in flight")
	}
	m = run(t, m, cmd)

	if len(back.applied) != 1 {
		t.Fatalf("applied %d intents, want 1", len(back.applied))
	}
	got := back.applied[0]
	if got.Issue.Identifier != "WND-42" || got.Disp.Status != "todo" {
		t.Errorf("applied %s → %s, want WND-42 → todo", got.Issue.Identifier, got.Disp.Status)
	}
	if m.state != stateBoard {
		t.Errorf("state = %v, want stateBoard", m.state)
	}
	// The row is gone: it is not in Triage any more, and a board still
	// offering it invites a second judgment on a ticket already judged.
	for _, row := range m.rows() {
		if row.Issue.Identifier == "WND-42" {
			t.Error("WND-42 is still on the board after being blessed")
		}
	}
	if !m.flashOK || m.flash == "" {
		t.Errorf("flash = %q (ok=%v), want a success message", m.flash, m.flashOK)
	}
}

// A refused write must not throw away what the user typed: the commonest
// refusal is a mistyped identifier, and the fix is one keystroke away only
// if the screen is still holding the text.
func TestRefusedWriteStaysOnTheConfirmation(t *testing.T) {
	back := &fakeBackend{err: errors.New("linear: no issue \"WND-999\"")}
	m, cmd := apply(t, board(t, back), "d,W,N,D,-,9,9,9,enter")
	m = run(t, m, cmd)

	if m.state != stateConfirm {
		t.Fatalf("state = %v, want stateConfirm", m.state)
	}
	if m.busy {
		t.Error("model is still busy after the write returned")
	}
	if m.failure == "" {
		t.Error("no failure recorded; the user has no idea what happened")
	}
	if got := m.input.Value(); got != "WND-999" {
		t.Errorf("input = %q, want the typed text kept", got)
	}
}

// Nothing may be written until the disposition's field is filled in.
func TestConfirmRefusesAnIncompleteIntent(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{name: "cancel with no reason", script: "x,enter"},
		{name: "duplicate with no target", script: "d,enter"},
		{name: "duplicate of itself", script: "d,W,N,D,-,4,2,enter"},
		{name: "backlog with no priority", script: "j,b,enter"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			back := &fakeBackend{}
			m, _ := apply(t, board(t, back), tt.script)
			if len(back.applied) != 0 {
				t.Errorf("applied %d intents, want 0", len(back.applied))
			}
			if m.state != stateConfirm {
				t.Errorf("state = %v, want to stay on the confirmation", m.state)
			}
		})
	}
}

func TestPriorityKeysPickARank(t *testing.T) {
	back := &fakeBackend{}
	// WND-41 is unranked, so the confirmation starts with nothing chosen.
	m, _ := apply(t, board(t, back), "j,b")
	if m.pending.Priority != 0 {
		t.Errorf("seeded priority = %d, want 0 for an unranked ticket", m.pending.Priority)
	}
	m, cmd := apply(t, m, "2,enter")
	m = run(t, m, cmd)

	if len(back.applied) != 1 {
		t.Fatalf("applied %d intents, want 1", len(back.applied))
	}
	if got := back.applied[0].Priority; got != 2 {
		t.Errorf("priority = %d, want 2", got)
	}
}

// A ticket that already carries a priority seeds the picker with it: that is
// a judgment somebody already made, and retyping it is not the decision the
// screen is asking for.
func TestRankedTicketSeedsItsOwnPriority(t *testing.T) {
	m, _ := apply(t, board(t, &fakeBackend{}), "b")
	if m.pending.Priority != 4 {
		t.Errorf("seeded priority = %d, want 4 (WND-42 is Low)", m.pending.Priority)
	}
}

func TestEscapeReturnsWhereItCameFrom(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   state
	}{
		{name: "from the board", script: "t,esc", want: stateBoard},
		{name: "from the detail screen", script: "enter,t,esc", want: stateDetail},
		{name: "detail back to the board", script: "enter,esc", want: stateBoard},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := apply(t, board(t, &fakeBackend{}), tt.script)
			if m.state != tt.want {
				t.Errorf("state = %v, want %v", m.state, tt.want)
			}
		})
	}
}

// The read-only model is the one an agent can reach, through
// `wand ui --dump-screen`. It must walk every screen and write nothing.
func TestReadOnlyModelWalksButNeverWrites(t *testing.T) {
	m := New(Config{Snapshot: cockpit.Sample(), Width: 80, Height: 24})

	got, cmd := apply(t, m, "t,enter")
	if got.state != stateConfirm {
		t.Errorf("state = %v, want stateConfirm: --dump-screen must be able to show the blessing screen", got.state)
	}
	if cmd != nil {
		t.Error("the confirm key produced a command; a read-only model must never write")
	}
	if _, refreshCmd := apply(t, m, "r"); refreshCmd != nil {
		t.Error("the refresh key produced a command; a read-only model has nothing to refresh from")
	}
}

func TestRefreshReplacesTheBoard(t *testing.T) {
	back := &fakeBackend{snap: cockpit.Snapshot{
		Team:   "WND",
		Triage: []linear.Issue{{Identifier: "WND-99", Title: "filed since you looked"}},
	}}
	m, cmd := apply(t, board(t, back), "r")
	m = run(t, m, cmd)

	if back.reads != 1 {
		t.Errorf("reads = %d, want 1", back.reads)
	}
	rows := m.rows()
	if len(rows) != 1 || rows[0].Issue.Identifier != "WND-99" {
		t.Errorf("board after refresh = %v, want the one re-read row", rows)
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0: a shorter board must not leave it past the end", m.cursor)
	}
}

func TestQuitKeys(t *testing.T) {
	for _, script := range []string{"q", "ctrl+c", "enter,q"} {
		t.Run(script, func(t *testing.T) {
			_, cmd := apply(t, board(t, &fakeBackend{}), script)
			if cmd == nil {
				t.Fatalf("%q produced no command, want quit", script)
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Errorf("%q produced %T, want tea.QuitMsg", script, cmd())
			}
		})
	}
}

// "q" is a letter. Someone half-way through typing a cancellation reason
// must not lose it to a quit.
func TestQuitDoesNotFireWhileTyping(t *testing.T) {
	m, cmd := apply(t, board(t, &fakeBackend{}), "x,q")
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("q quit the program while a reason was being typed")
		}
	}
	if got := m.input.Value(); got != "q" {
		t.Errorf("input = %q, want %q", got, "q")
	}
	// ctrl+c still works from everywhere, because an escape hatch that
	// depends on which field has focus is not an escape hatch.
	if _, force := apply(t, m, "ctrl+c"); force == nil {
		t.Error("ctrl+c produced no command while typing, want quit")
	}
}

func TestWindowResizeIsRecorded(t *testing.T) {
	next, _ := board(t, &fakeBackend{}).Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	got := next.(Model)
	if got.width != 100 || got.height != 40 {
		t.Errorf("model size = %dx%d, want 100x40", got.width, got.height)
	}
}

// --- Tier 1: wiring through the real runtime -----------------------------

func TestProgramWiring(t *testing.T) {
	final, trace := tuitest.FinalModel(t, board(t, &fakeBackend{}), "j,enter")

	m, ok := final.(Model)
	if !ok {
		t.Fatalf("final model is %T, want tui.Model", final)
	}
	if m.state != stateDetail {
		t.Errorf("state = %v, want stateDetail", m.state)
	}
	if m.detail.Issue.Identifier != "WND-41" {
		t.Errorf("detail = %q, want WND-41", m.detail.Issue.Identifier)
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
		{golden: "board", script: ""},
		{golden: "board-lane-selected", script: "j,j,j,j,j"},
		{golden: "detail", script: "j,enter"},
		{golden: "detail-lane", script: "j,j,j,j,j,enter"},
		{golden: "detail-scoped", script: "j,j,enter"},
		{golden: "bless-todo", script: "t"},
		{golden: "bless-todo-unranked", script: "j,t"},
		{golden: "bless-scoping", script: "s"},
		{golden: "backlog-ranked", script: "b"},
		{golden: "backlog-unranked", script: "u"},
		{golden: "duplicate", script: "d,W,N,D,-,4,1"},
		{golden: "cancel", script: "x,o,b,s,o,l,e,t,e"},
		{golden: "bless-scoped-todo", script: "j,j,t"},
		{golden: "reject-plan", script: "j,j,b,w,r,o,n,g"},
	}

	for _, tt := range tests {
		t.Run(tt.golden, func(t *testing.T) {
			tuitest.AssertScreen(t, tt.golden, board(t, &fakeBackend{}), tt.script)
		})
	}
}

// The plan is the one thing on the detail screen whose wrapping and
// line-clamping depend on the terminal it is shown in — every other field
// there is a short, fixed-width line. A narrower or shorter terminal must
// still produce a readable, deterministic screen rather than a garbled one.
func TestScopedPlanAtOtherSizes(t *testing.T) {
	tests := []struct {
		golden        string
		width, height int
	}{
		{golden: "detail-scoped-narrow", width: 48, height: screen.DefaultHeight},
		{golden: "detail-scoped-short", width: screen.DefaultWidth, height: 14},
	}

	for _, tt := range tests {
		t.Run(tt.golden, func(t *testing.T) {
			m := New(Config{
				Snapshot: cockpit.Sample(),
				Backend:  &fakeBackend{},
				Width:    tt.width,
				Height:   tt.height,
			})
			tuitest.AssertScreenSized(t, tt.golden, m, "j,j,enter", tt.width, tt.height)
		})
	}
}

// The empty board is the answer the cockpit exists to be able to give, and
// it has to say so rather than showing five blank headings.
func TestEmptyBoard(t *testing.T) {
	m := New(Config{
		Snapshot: cockpit.Snapshot{Team: "WND"},
		Backend:  &fakeBackend{},
		Width:    screen.DefaultWidth,
		Height:   screen.DefaultHeight,
	})
	tuitest.AssertScreen(t, "board-empty", m, "")
}

// The read-only board is what `wand ui --dump-screen` and `wand ui --sample`
// render, notice and all.
func TestSampleBoardScreens(t *testing.T) {
	sample := func() Model {
		return New(Config{
			Snapshot: cockpit.Sample(),
			Width:    screen.DefaultWidth,
			Height:   screen.DefaultHeight,
			Notice:   "sample board — reads no Linear team, and writes none",
		})
	}
	tuitest.AssertScreen(t, "board-sample", sample(), "")
	tuitest.AssertScreen(t, "bless-read-only", sample(), "t")
}

// --- refreshing from the detail screen -----------------------------------

// The detail screen holds a copy of the row it was opened on, and refresh
// works from there. Leaving the copy in place would make the one key a
// person presses to get current data the key that hides how stale it is —
// and the disposition keys act on this row, so the stale copy is not merely
// displayed, it is judged.
func TestRefreshOnDetailResyncsTheOpenedRow(t *testing.T) {
	back := &fakeBackend{}
	m, _ := apply(t, board(t, back), "enter") // open WND-42
	if m.state != stateDetail {
		t.Fatalf("state = %v, want the detail screen", m.state)
	}
	if got := m.detail.Issue.Title; got == "" {
		t.Fatal("the detail row has no title to change")
	}

	// The board comes back with the same ticket, retitled.
	const retitled = "a title the opened copy has never seen"
	next := cockpit.Sample()
	next.Triage = retitle(t, next.Triage, m.detail.Issue.Identifier, retitled)
	back.snap = next

	m, cmd := apply(t, m, "r")
	m = run(t, m, cmd)

	if m.state != stateDetail {
		t.Fatalf("state = %v, want to still be on the detail screen", m.state)
	}
	if got := m.detail.Issue.Title; got != retitled {
		t.Errorf("detail title = %q, want the re-read %q; the screen is showing a pre-refresh copy",
			got, retitled)
	}
}

// retitle copies a queue with one issue's title replaced, found by
// identifier: the sample's slice order is not the board's display order, and
// a test that indexed into it would be asserting about a different ticket.
func retitle(t *testing.T, issues []linear.Issue, identifier, title string) []linear.Issue {
	t.Helper()
	out := append([]linear.Issue(nil), issues...)
	for i := range out {
		if out[i].Identifier == identifier {
			out[i].Title = title
			return out
		}
	}
	t.Fatalf("no issue %s in the queue", identifier)
	return nil
}

// without copies a queue with one issue removed, found by identifier.
func without(t *testing.T, issues []linear.Issue, identifier string) []linear.Issue {
	t.Helper()
	var out []linear.Issue
	for _, issue := range issues {
		if issue.Identifier != identifier {
			out = append(out, issue)
		}
	}
	if len(out) == len(issues) {
		t.Fatalf("no issue %s in the queue", identifier)
	}
	return out
}

// When the opened row is gone from the re-read board there is nothing left
// to show, and nothing left to judge. Closing without a word would read as
// the key having quit the screen.
func TestRefreshOnDetailLeavesWhenTheRowIsGone(t *testing.T) {
	back := &fakeBackend{}
	m, _ := apply(t, board(t, back), "enter")
	gone := m.detail.Issue.Identifier

	next := cockpit.Sample()
	next.Triage = without(t, next.Triage, gone) // somebody else judged it
	back.snap = next

	m, cmd := apply(t, m, "r")
	m = run(t, m, cmd)

	if m.state != stateBoard {
		t.Errorf("state = %v, want to be back on the board", m.state)
	}
	if !strings.Contains(m.flash, gone) {
		t.Errorf("flash = %q, want it to name %s", m.flash, gone)
	}
	if m.flashOK {
		t.Error("flashOK is true; a row vanishing under you is not good news")
	}
}

// A failed refresh reports itself through the flash, and the detail screen
// has to render it. Without that, refreshing from there is byte-identical
// before and after — a silent failure that leaves a person reading stale
// rows believing they just re-read them.
func TestRefreshFailureIsVisibleOnTheDetailScreen(t *testing.T) {
	back := &fakeBackend{readErr: errors.New("linear is unreachable")}
	m, _ := apply(t, board(t, back), "enter")
	before := m.View().Content

	m, cmd := apply(t, m, "r")
	m = run(t, m, cmd)

	if m.state != stateDetail {
		t.Fatalf("state = %v, want to still be on the detail screen", m.state)
	}
	after := m.View().Content
	if after == before {
		t.Fatal("the detail screen is unchanged after a failed refresh; the failure is invisible")
	}
	if !strings.Contains(after, "linear is unreachable") {
		t.Errorf("the detail screen does not name the failure; screen was:\n%s", after)
	}
}

// --- retrying a partly-applied judgment ----------------------------------

// The screen keeps a failed judgment on the confirmation so it can be
// retried. When the failure came after the pre-write landed, the retry has
// to carry that fact back to cockpit.Apply, or it posts the same reason a
// second time.
func TestRetryCarriesTheProgressOfAFailedJudgment(t *testing.T) {
	back := &fakeBackend{err: errors.New("linear: refused the issue update"), preWritten: true}
	m, _ := apply(t, board(t, back), "x") // cancel, with reason
	m, _ = apply(t, m, "n,o,p,e")         // a reason to post
	m, cmd := apply(t, m, "enter")
	m = run(t, m, cmd)

	if m.state != stateConfirm {
		t.Fatalf("state = %v, want to stay on the confirmation after a failure", m.state)
	}
	if !m.pending.Done.PreWritten {
		t.Fatal("the pending intent lost the progress the failed attempt made; " +
			"the retry would post the reason twice")
	}
	if m.pending.Text != "nope" {
		t.Errorf("pending text = %q, want the reason that was actually posted", m.pending.Text)
	}

	// The retry hands the progress back to the backend.
	back.err = nil
	m, cmd = apply(t, m, "enter")
	m = run(t, m, cmd)
	if len(back.applied) != 2 {
		t.Fatalf("backend saw %d applies, want 2", len(back.applied))
	}
	if !back.applied[1].Done.PreWritten {
		t.Error("the retry did not tell Apply the reason was already posted")
	}
}

// Once the pre-write has landed the field is spent: the text it held is on
// the ticket, and typing into it would look like an edit that cannot reach
// anywhere.
func TestASpentFieldStopsTakingKeystrokes(t *testing.T) {
	back := &fakeBackend{err: errors.New("refused"), preWritten: true}
	m, _ := apply(t, board(t, back), "x")
	m, _ = apply(t, m, "h,i")
	m, cmd := apply(t, m, "enter")
	m = run(t, m, cmd)

	if m.typing() {
		t.Error("the screen still routes keystrokes to a field whose text is already on the ticket")
	}
	m, _ = apply(t, m, "z,z,z")
	if m.intent().Text != "hi" {
		t.Errorf("text = %q, want the posted reason to be unchanged by later keystrokes", m.intent().Text)
	}
	if !strings.Contains(m.View().Content, "already posted") {
		t.Errorf("the confirmation does not say the reason already landed; screen was:\n%s", m.View().Content)
	}
}
