package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mattwalters/wand/internal/home"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/screen"
	"github.com/mattwalters/wand/internal/tuitest"
)

// fakeBackend records what the screen asked for. Every write home
// makes is a status transition the guard forbids agents, so a test that
// could not see exactly which one was attempted would not be testing much.
type fakeBackend struct {
	applied []home.Intent
	err     error
	// preWritten makes Apply fail the way a partial judgment really does:
	// the comment or the relation landed, the status move did not. It is
	// what home.Apply reports back through the returned intent.
	preWritten bool

	reads   int
	snap    home.Snapshot
	readErr error
}

func (f *fakeBackend) Apply(_ context.Context, in home.Intent) (home.Intent, error) {
	f.applied = append(f.applied, in)
	if f.preWritten {
		in.Done.PreWritten = true
	}
	return in, f.err
}

func (f *fakeBackend) Read(context.Context) (home.Snapshot, error) {
	f.reads++
	if f.readErr != nil {
		return home.Snapshot{}, f.readErr
	}
	return f.snap, nil
}

// board builds a model over the sample board with a writer behind it.
func board(t *testing.T, back Backend) Model {
	t.Helper()
	return New(Config{
		Snapshot: home.Sample(),
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

// TestCursorMoves covers the Decide view: two Triage rows, and nothing to
// cross into. Review's and Unblock's own cursor motion — including the
// section-boundary crossing this table used to test by walking off Decide
// entirely — get their own tests below, because that crossing is no longer
// reachable by one script now that each view scopes its own cursor order.
func TestCursorMoves(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		want    int
		wantRow string
	}{
		{name: "starts on the first row", script: "1", want: 0, wantRow: "WND-42"},
		{name: "j moves down", script: "1,j", want: 1, wantRow: "WND-41"},
		{name: "arrow moves down", script: "1,down", want: 1, wantRow: "WND-41"},
		{name: "k moves back up", script: "1,j,k", want: 0, wantRow: "WND-42"},
		{name: "stops at the last row", script: "1,j,j,j", want: 1, wantRow: "WND-41"},
		{name: "stops at the first row", script: "1,k,k,k", want: 0, wantRow: "WND-42"},
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
			if row.IsStalled() {
				got = row.Stalled.Ticket
			}
			if got != tt.wantRow {
				t.Errorf("row under cursor = %q, want %q", got, tt.wantRow)
			}
		})
	}
}

// The Review view's three sections (Plan Review, Needs Input, Ready for
// human) are exactly one row each, so moving down crosses a section
// boundary on every step — the replacement for TestCursorMoves' old
// "crosses a section boundary" case, now that the boundary it crossed sits
// inside one view rather than between two.
func TestCursorMovesInReviewView(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		want    int
		wantRow string
	}{
		{name: "starts on Plan Review", script: "2", want: 0, wantRow: "WND-44"},
		{name: "crosses into Needs Input", script: "2,j", want: 1, wantRow: "WND-38"},
		{name: "crosses into Ready for human", script: "2,j,j", want: 2, wantRow: "WND-35"},
		{name: "stops at the last row", script: "2,j,j,j", want: 2, wantRow: "WND-35"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := apply(t, board(t, &fakeBackend{}), tt.script)
			if m.cursor != tt.want {
				t.Errorf("cursor = %d, want %d", m.cursor, tt.want)
			}
			row, ok := m.current()
			if !ok || row.Issue.Identifier != tt.wantRow {
				t.Errorf("row under cursor = %+v, want %q", row, tt.wantRow)
			}
		})
	}
}

// The Unblock view is Stalled alone — the replacement for TestCursorMoves'
// old "reaches the stalled" case, now that reaching it means selecting this
// view rather than walking off the end of a flat board.
func TestCursorMovesInUnblockView(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		want    int
		wantRow string
	}{
		{name: "starts on the stuck run", script: "3", want: 0, wantRow: "WND-36"},
		{name: "reaches the parked run", script: "3,j", want: 1, wantRow: "WND-33"},
		{name: "stops at the last row", script: "3,j,j", want: 1, wantRow: "WND-33"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := apply(t, board(t, &fakeBackend{}), tt.script)
			if m.cursor != tt.want {
				t.Errorf("cursor = %d, want %d", m.cursor, tt.want)
			}
			row, ok := m.current()
			if !ok || row.Stalled.Ticket != tt.wantRow {
				t.Errorf("row under cursor = %+v, want %q", row, tt.wantRow)
			}
		})
	}
}

// Running rows are chrome, not cursor stops: an active run is not a ticket
// a person disposits, so it must not shift the cursor order the four
// queues already have, nor be reachable by it.
func TestRunningRowsAreNotInTheCursorOrder(t *testing.T) {
	plain := board(t, &fakeBackend{}).selectView(home.ViewDecide)

	withRunning := home.Sample()
	withRunning.Active = []home.Active{{RunID: "r", Ticket: "WND-99", Phase: "implement", Verb: "run"}}
	m := New(Config{
		Snapshot: withRunning,
		Backend:  &fakeBackend{},
		Width:    screen.DefaultWidth,
		Height:   screen.DefaultHeight,
	}).selectView(home.ViewDecide)

	if got, want := len(m.rows()), len(plain.rows()); got != want {
		t.Errorf("rows = %d, want %d: the Active run must not appear in the cursor order", got, want)
	}
	row, ok := m.current()
	if !ok || row.Issue.Identifier != "WND-42" {
		t.Errorf("row under cursor = %+v, want the board's first Triage row, unmoved by Running", row)
	}
}

// ago is the one clock arithmetic the Active-runs strip does, and it is
// pure: no clock read of its own, and unset readings degrade to a dash
// rather than a nonsense duration.
func TestAgo(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		now, at time.Time
		want    string
	}{
		{"a zero now reads as unset", time.Time{}, base, "—"},
		{"a zero at reads as unset", base, time.Time{}, "—"},
		{"seconds", base.Add(8 * time.Second), base, "8s"},
		{"minutes", base.Add(12 * time.Minute), base, "12m"},
		{"hours and minutes", base.Add(3*time.Hour + 4*time.Minute), base, "3h4m"},
		{"a heartbeat newer than now clamps to zero", base, base.Add(time.Minute), "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ago(tt.now, tt.at); got != tt.want {
				t.Errorf("ago() = %q, want %q", got, tt.want)
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
		{key: "s", wantStatus: "to_plan", wantBless: true},
		{key: "b", wantStatus: "backlog"},
		{key: "u", wantStatus: "backlog"},
		{key: "d", wantStatus: "duplicate"},
		{key: "x", wantStatus: "canceled"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			m, _ := apply(t, board(t, &fakeBackend{}), "1,"+tt.key)
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

// Ready-for-human rows are read-only by design: the act that row is asking
// for happens on the pull request, not on a status field.
func TestReadyForHumanRowsOfferNoDisposition(t *testing.T) {
	// Review view: Plan Review, Needs Input, then Ready for human (WND-35).
	m, _ := apply(t, board(t, &fakeBackend{}), "2,j,j,t")
	if m.state != stateBoard {
		t.Errorf("state = %v, want stateBoard: this row offers no judgments", m.state)
	}
}

// A non-parked stalled row is read-only: resolving it means going to the
// machine, not writing a status.
func TestNonParkedStalledRowsOfferNoDisposition(t *testing.T) {
	// Unblock view's first row is WND-36, a stuck (non-parked) run.
	m, _ := apply(t, board(t, &fakeBackend{}), "3,t")
	if m.state != stateBoard {
		t.Errorf("state = %v, want stateBoard: this row offers no judgments", m.state)
	}
}

func TestBlessWritesAndDropsTheRow(t *testing.T) {
	back := &fakeBackend{}
	m, cmd := apply(t, board(t, back), "1,t,enter")
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

// The key ReportPark's own comment names — clear the parked label — must
// exist somewhere in wand, and this is it: WND-33 is the sample board's
// parked stalled run, the disposition writes through the same read-only-free path
// every other judgment does, and the stalled run leaves the board the same way a
// judged issue does.
func TestClearParkedWritesAndDropsTheRow(t *testing.T) {
	back := &fakeBackend{}
	// Unblock view: WND-36 (stuck), then WND-33 (parked).
	m, _ := apply(t, board(t, back), "3,j")
	row, ok := m.current()
	if !ok || !row.IsStalled() || row.Stalled.Ticket != "WND-33" {
		t.Fatalf("row under cursor = %+v, want the WND-33 parked run", row)
	}

	m, cmd := apply(t, m, "c,enter")
	if !m.busy {
		t.Error("model is not busy after confirming; the write should be in flight")
	}
	m = run(t, m, cmd)

	if len(back.applied) != 1 {
		t.Fatalf("applied %d intents, want 1", len(back.applied))
	}
	got := back.applied[0]
	if got.Stalled.Ticket != "WND-33" || !got.Disp.Stalled {
		t.Errorf("applied %+v, want a stalled-run disposition on WND-33", got)
	}
	if m.state != stateBoard {
		t.Errorf("state = %v, want stateBoard", m.state)
	}
	for _, row := range m.rows() {
		if row.IsStalled() && row.Stalled.Ticket == "WND-33" {
			t.Error("the WND-33 stalled run is still on the board after its park was cleared")
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
	m, cmd := apply(t, board(t, back), "1,d,W,N,D,-,9,9,9,enter")
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
		{name: "cancel with no reason", script: "1,x,enter"},
		{name: "duplicate with no target", script: "1,d,enter"},
		{name: "duplicate of itself", script: "1,d,W,N,D,-,4,2,enter"},
		{name: "backlog with no priority", script: "1,j,b,enter"},
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
	m, _ := apply(t, board(t, back), "1,j,b")
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
	m, _ := apply(t, board(t, &fakeBackend{}), "1,b")
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
		{name: "from the board", script: "1,t,esc", want: stateBoard},
		{name: "from the detail screen", script: "1,enter,t,esc", want: stateDetail},
		{name: "detail back to the board", script: "1,enter,esc", want: stateBoard},
		{name: "board back to home", script: "1,esc", want: stateHome},
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

// Every view is reachable from home, and every view is escapable back to
// it — the state machine's two basic promises, checked directly rather than
// inferred from the individual screens above.
func TestEveryViewIsReachableAndEscapable(t *testing.T) {
	for _, vi := range home.Views {
		t.Run(vi.Title, func(t *testing.T) {
			m, _ := apply(t, board(t, &fakeBackend{}), vi.Key)
			if m.state != stateBoard {
				t.Fatalf("state = %v, want stateBoard after pressing %q", m.state, vi.Key)
			}
			if m.view != vi.View {
				t.Fatalf("view = %v, want %v", m.view, vi.View)
			}
			back, _ := apply(t, m, "esc")
			if back.state != stateHome {
				t.Errorf("state after esc = %v, want stateHome", back.state)
			}
		})
	}
}

// A view key pressed while already inside a view jumps straight to the
// other view, rather than being swallowed or requiring a detour through
// home first.
func TestViewKeysSwitchDirectlyBetweenViews(t *testing.T) {
	m, _ := apply(t, board(t, &fakeBackend{}), "1,3")
	if m.state != stateBoard || m.view != home.ViewUnblock {
		t.Errorf("state/view = %v/%v, want stateBoard/ViewUnblock", m.state, m.view)
	}
}

// The read-only model is the one an agent can reach, through
// `wand ui --dump-screen`. It must walk every screen and write nothing.
func TestReadOnlyModelWalksButNeverWrites(t *testing.T) {
	m := New(Config{Snapshot: home.Sample(), Width: 80, Height: 24})

	got, cmd := apply(t, m, "1,t,enter")
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
	back := &fakeBackend{snap: home.Snapshot{
		Team:   "WND",
		Triage: []linear.Issue{{Identifier: "WND-99", Title: "filed since you looked"}},
	}}
	m, cmd := apply(t, board(t, back), "1,r")
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
	for _, script := range []string{"q", "ctrl+c", "1,enter,q"} {
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
	m, cmd := apply(t, board(t, &fakeBackend{}), "1,x,q")
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

// "1", "2" and "3" are view keys everywhere else, but on the confirm
// screen's free-text fields they are letters someone may be typing into a
// reason — the same guard TestQuitDoesNotFireWhileTyping proves for "q".
func TestViewKeysDoNotFireWhileTyping(t *testing.T) {
	m, _ := apply(t, board(t, &fakeBackend{}), "1,x,1,2,3")
	if m.state != stateConfirm {
		t.Fatalf("state = %v, want stateConfirm: a view key must not navigate away mid-type", m.state)
	}
	if got := m.input.Value(); got != "123" {
		t.Errorf("input = %q, want %q", got, "123")
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
	final, trace := tuitest.FinalModel(t, board(t, &fakeBackend{}), "1,j,enter")

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
	want := []string{"1", "j", "enter"}
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
		// The landing screen, and each of the three views at rest.
		{golden: "home", script: ""},
		{golden: "decide", script: "1"},
		{golden: "review", script: "2"},
		{golden: "unblock", script: "3"},

		{golden: "unblock-stuck-selected", script: "3"},
		{golden: "unblock-parked-selected", script: "3,j"},
		{golden: "clear-parked", script: "3,j,c"},

		{golden: "detail", script: "1,j,enter"},
		{golden: "detail-stalled", script: "3,enter"},
		{golden: "detail-plan-review", script: "2,enter"},

		{golden: "bless-todo", script: "1,t"},
		{golden: "bless-todo-unranked", script: "1,j,t"},
		{golden: "bless-to-plan", script: "1,s"},
		{golden: "backlog-ranked", script: "1,b"},
		{golden: "backlog-unranked", script: "1,u"},
		{golden: "duplicate", script: "1,d,W,N,D,-,4,1"},
		{golden: "cancel", script: "1,x,o,b,s,o,l,e,t,e"},
		{golden: "bless-plan-review-todo", script: "2,t"},
		{golden: "reject-plan", script: "2,b,w,r,o,n,g"},
	}

	for _, tt := range tests {
		t.Run(tt.golden, func(t *testing.T) {
			tuitest.AssertScreen(t, tt.golden, board(t, &fakeBackend{}), tt.script)
		})
	}
}

// Two of the three views are empty on a board where only one job has
// anything to do — the common case this whole ticket exists to serve, and
// each has to say what would appear there rather than rendering nothing.
func TestEmptyViews(t *testing.T) {
	tests := []struct {
		golden string
		snap   func() home.Snapshot
		script string
	}{
		{golden: "decide-empty", snap: func() home.Snapshot {
			s := home.Sample()
			s.Triage = nil
			return s
		}, script: "1"},
		{golden: "review-empty", snap: func() home.Snapshot {
			s := home.Sample()
			s.PlanReview, s.NeedsInput, s.ReadyForHuman = nil, nil, nil
			return s
		}, script: "2"},
		{golden: "unblock-empty", snap: func() home.Snapshot {
			s := home.Sample()
			s.Stalled = nil
			return s
		}, script: "3"},
	}

	for _, tt := range tests {
		t.Run(tt.golden, func(t *testing.T) {
			m := New(Config{
				Snapshot: tt.snap(),
				Backend:  &fakeBackend{},
				Width:    screen.DefaultWidth,
				Height:   screen.DefaultHeight,
			})
			tuitest.AssertScreen(t, tt.golden, m, tt.script)
		})
	}
}

// The plan is the one thing on the detail screen whose wrapping and
// line-clamping depend on the terminal it is shown in — every other field
// there is a short, fixed-width line. A narrower or shorter terminal must
// still produce a readable, deterministic screen rather than a garbled one.
func TestPlanReviewDetailAtOtherSizes(t *testing.T) {
	tests := []struct {
		golden        string
		width, height int
	}{
		{golden: "detail-plan-review-narrow", width: 48, height: screen.DefaultHeight},
		{golden: "detail-plan-review-short", width: screen.DefaultWidth, height: 14},
	}

	for _, tt := range tests {
		t.Run(tt.golden, func(t *testing.T) {
			m := New(Config{
				Snapshot: home.Sample(),
				Backend:  &fakeBackend{},
				Width:    tt.width,
				Height:   tt.height,
			})
			tuitest.AssertScreenSized(t, tt.golden, m, "2,enter", tt.width, tt.height)
		})
	}
}

// The empty landing screen is the answer home exists to be able to give,
// and it has to say so rather than showing three blank counts.
func TestEmptyBoard(t *testing.T) {
	m := New(Config{
		Snapshot: home.Snapshot{Team: "WND"},
		Backend:  &fakeBackend{},
		Width:    screen.DefaultWidth,
		Height:   screen.DefaultHeight,
	})
	tuitest.AssertScreen(t, "home-empty", m, "")
}

// The read-only home is what `wand ui --dump-screen` and `wand ui --sample`
// render, notice and all.
func TestSampleBoardScreens(t *testing.T) {
	sample := func() Model {
		return New(Config{
			Snapshot: home.Sample(),
			Width:    screen.DefaultWidth,
			Height:   screen.DefaultHeight,
			Notice:   "sample board — reads no Linear team, and writes none",
		})
	}
	tuitest.AssertScreen(t, "home-sample", sample(), "")
	tuitest.AssertScreen(t, "bless-read-only", sample(), "1,t")
}

// The Running strip is what WND-41 adds: what a live process is doing right
// now, outside every view's cursor order and rendered on home alone. One
// alive run and one whose own liveness judgment already says its holder is
// gone — the "possibly dead, sweep will confirm" note is that judgment
// surfacing, never a second one this screen invents from the heartbeat's
// age.
func TestRunningStripScreen(t *testing.T) {
	now := time.Date(2026, 3, 5, 9, 30, 0, 0, time.UTC)
	snap := home.Sample()
	snap.Active = []home.Active{
		{
			RunID: "run-1", Ticket: "WND-12", Verb: "run", Harness: "claude-code",
			Phase: "implement", Round: 1,
			Started: now.Add(-18 * time.Minute), Heartbeat: now.Add(-8 * time.Second),
			Live: journal.Alive,
		},
		{
			RunID: "run-2", Ticket: "WND-7", Verb: "plan", Harness: "",
			Phase: "scout", Round: 0,
			Started: now.Add(-42 * time.Minute), Heartbeat: now.Add(-6 * time.Minute),
			Live: journal.Dead,
		},
	}
	m := New(Config{
		Snapshot: snap,
		Backend:  &fakeBackend{},
		Width:    screen.DefaultWidth,
		Height:   screen.DefaultHeight,
		Now:      now,
	})
	tuitest.AssertScreen(t, "home-running", m, "")
}

// --- refreshing from the detail screen -----------------------------------

// The detail screen holds a copy of the row it was opened on, and refresh
// works from there. Leaving the copy in place would make the one key a
// person presses to get current data the key that hides how stale it is —
// and the disposition keys act on this row, so the stale copy is not merely
// displayed, it is judged.
func TestRefreshOnDetailResyncsTheOpenedRow(t *testing.T) {
	back := &fakeBackend{}
	m, _ := apply(t, board(t, back), "1,enter") // open WND-42
	if m.state != stateDetail {
		t.Fatalf("state = %v, want the detail screen", m.state)
	}
	if got := m.detail.Issue.Title; got == "" {
		t.Fatal("the detail row has no title to change")
	}

	// The board comes back with the same ticket, retitled.
	const retitled = "a title the opened copy has never seen"
	next := home.Sample()
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
	m, _ := apply(t, board(t, back), "1,enter")
	gone := m.detail.Issue.Identifier

	next := home.Sample()
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
	m, _ := apply(t, board(t, back), "1,enter")
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
// to carry that fact back to home.Apply, or it posts the same reason a
// second time.
func TestRetryCarriesTheProgressOfAFailedJudgment(t *testing.T) {
	back := &fakeBackend{err: errors.New("linear: refused the issue update"), preWritten: true}
	m, _ := apply(t, board(t, back), "1,x") // cancel, with reason
	m, _ = apply(t, m, "n,o,p,e")           // a reason to post
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
	m, _ := apply(t, board(t, back), "1,x")
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
