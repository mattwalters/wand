// Package tui holds wand's Bubble Tea models — the cockpit, and nothing
// else. One screen, one question: what is waiting on a human?
//
// Everything here obeys the determinism rules in CLAUDE.md: Update is a pure
// function of (model, msg), View is a pure function of model state, and
// neither reads the clock, the filesystem, or the network. Reads and writes
// happen in tea.Cmds over a [Backend], which is an interface so tests hold a
// fake. That is what lets the golden-screen harness produce byte-stable
// output.
//
// # Read-only is a state, not an accident
//
// A model built with a nil Backend cannot write, and says so on screen. It
// still walks every screen — the refusal sits on the key that writes, not on
// the keys that navigate, so the blessing screen stays inspectable. That is
// the mode `wand ui --dump-screen` and `wand ui --sample` both run in, and it
// is the reason an agent cannot bless a ticket by scripting the very command
// it uses to look at the interface. See cockpit.Apply for the other half of
// that argument.
package tui

import (
	"context"
	"fmt"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/mattwalters/wand/internal/cockpit"
	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/theme"
)

// Backend is the cockpit's I/O. Nil means read-only: the screen navigates
// as usual, and the key that would write does nothing and says why.
type Backend interface {
	// Read fetches the board again.
	Read(ctx context.Context) (cockpit.Snapshot, error)
	// Apply performs one judgment, and returns the intent with whatever
	// landed marked on it — see cockpit.Progress. A failed judgment is
	// retried from the returned value, never from the original.
	Apply(ctx context.Context, in cockpit.Intent) (cockpit.Intent, error)
}

// Config is what New needs. Snapshot is passed in already read rather than
// fetched on Init, so that a failure to reach Linear is an ordinary command
// error printed at a shell — not an error message trapped inside an
// alternate screen the user then has to quit out of.
type Config struct {
	Snapshot cockpit.Snapshot
	Backend  Backend
	// Covenant names the statuses. The screen says "→ Todo" only because
	// the covenant calls it that; a repo whose blessed column is called
	// Ready should read "→ Ready" here, or the moment is describing
	// somebody else's board. Zero value means covenant.Default.
	Covenant covenant.Covenant
	Width    int
	Height   int
	// Notice is a banner shown under the header. Used to say that a board
	// is the built-in sample rather than the user's team.
	Notice string
}

// state is which screen the app is showing.
type state int

const (
	stateBoard state = iota
	stateDetail
	stateConfirm
)

// Model is the root model.
type Model struct {
	state  state
	snap   cockpit.Snapshot
	board  cockpit.Board
	cursor int

	backend Backend
	cov     covenant.Covenant
	keys    keyMap
	theme   theme.Theme
	notice  string
	width   int
	height  int

	// detail is the row the detail screen is showing.
	detail cockpit.Row
	// pending is the half-made judgment the confirm screen is holding.
	pending cockpit.Intent
	// from is the screen the confirmation was started from, so esc goes
	// back where the user came from rather than somewhere plausible.
	from  state
	input textinput.Model

	// busy is set while a command is in flight. Keys that would start a
	// second write are ignored until it lands.
	busy bool
	// failure is the last refused write, shown on the confirm screen so
	// the user can fix the input and try again rather than losing it.
	failure string
	// flash is what just happened, shown on the board.
	flash   string
	flashOK bool
}

// New returns a cockpit model over an already-read snapshot.
func New(cfg Config) Model {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 500

	cov := cfg.Covenant
	if len(cov.Statuses) == 0 {
		cov = covenant.Default()
	}

	m := Model{
		snap:    cfg.Snapshot,
		board:   cockpit.Build(cfg.Snapshot),
		backend: cfg.Backend,
		cov:     cov,
		keys:    defaultKeyMap(),
		theme:   theme.New(),
		notice:  cfg.Notice,
		width:   cfg.Width,
		height:  cfg.Height,
		input:   in,
	}
	if m.width <= 0 {
		m.width = 80
	}
	if m.height <= 0 {
		m.height = 24
	}
	return m
}

// readOnly reports whether this model may write. Read-only is the honest
// name for it: the screen still reads, refreshes and navigates.
func (m Model) readOnly() bool { return m.backend == nil }

// rows is the flat cursor order across every section.
func (m Model) rows() []cockpit.Row { return m.board.Rows() }

// current returns the row under the cursor, if there is one.
func (m Model) current() (cockpit.Row, bool) {
	rows := m.rows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return cockpit.Row{}, false
	}
	return rows[m.cursor], true
}

// subject is the row the disposition keys act on: the highlighted row on
// the board, the opened row on the detail screen.
func (m Model) subject() (cockpit.Row, bool) {
	if m.state == stateDetail {
		return m.detail, true
	}
	return m.current()
}

// typing reports whether keystrokes belong to the text input rather than to
// the app. Only the confirm screen's free-text fields type.
func (m Model) typing() bool {
	return m.state == stateConfirm && !m.pending.Done.PreWritten &&
		m.pending.Disp.Field != cockpit.FieldPriority &&
		m.pending.Disp.Field != cockpit.FieldNone
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

// appliedMsg carries the result of one judgment: the intent as cockpit.Apply
// left it, so a failure that happened after a pre-write lands comes back
// carrying that fact rather than losing it.
type appliedMsg struct {
	intent cockpit.Intent
	err    error
}

// refreshedMsg carries a re-read board.
type refreshedMsg struct {
	snap cockpit.Snapshot
	err  error
}

// Update implements tea.Model. It is pure: no I/O, no clock, no randomness.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case appliedMsg:
		m.busy = false
		if msg.err != nil {
			// Stay on the confirmation. The commonest refusal is a
			// mistyped identifier, and a screen that threw the text away
			// on the way to reporting that would be worse than useless.
			//
			// The returned intent replaces the pending one, so a retry
			// carries the progress the failed attempt made. Without this
			// the retry would post the reason a second time — see
			// cockpit.Apply's Retrying section.
			m.pending = msg.intent
			m.failure = msg.err.Error()
			if m.pending.Done.PreWritten {
				// The field is spent: the text it held is already on the
				// ticket, and editing it now would change nothing while
				// looking like it changed something.
				m.input.Blur()
			}
			return m, nil
		}
		m.snap = withoutIssue(m.snap, msg.intent.Issue.ID, msg.intent.Issue.Identifier)
		m.board = cockpit.Build(m.snap)
		m.clampCursor()
		m.state = stateBoard
		m.failure = ""
		m.flash, m.flashOK = m.applied(msg.intent), true
		return m, nil

	case refreshedMsg:
		m.busy = false
		if msg.err != nil {
			m.flash, m.flashOK = "refresh failed: "+msg.err.Error(), false
			return m, nil
		}
		m.snap = msg.snap
		m.board = cockpit.Build(m.snap)
		m.clampCursor()
		m.flash, m.flashOK = "", false
		m.resyncDetail()
		return m, nil

	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m Model) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.ForceQuit) {
		return m, tea.Quit
	}
	if m.state == stateConfirm {
		// Quit works here too, but only when nothing has focus: on a
		// text field "q" is a letter, which is why ForceQuit above is
		// the escape hatch that never depends on focus.
		if !m.typing() && key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		return m.confirmKey(msg)
	}
	if key.Matches(msg, m.keys.Quit) {
		return m, tea.Quit
	}

	switch {
	case key.Matches(msg, m.keys.Back):
		if m.state == stateDetail {
			m.state = stateBoard
		}
		return m, nil

	case key.Matches(msg, m.keys.Up):
		if m.state == stateBoard {
			m.moveCursor(-1)
		}
		return m, nil

	case key.Matches(msg, m.keys.Down):
		if m.state == stateBoard {
			m.moveCursor(1)
		}
		return m, nil

	case key.Matches(msg, m.keys.Open):
		if m.state != stateBoard {
			return m, nil
		}
		if row, ok := m.current(); ok {
			m.detail, m.state = row, stateDetail
		}
		return m, nil

	case key.Matches(msg, m.keys.Refresh):
		if m.readOnly() || m.busy {
			return m, nil
		}
		m.busy, m.flash = true, ""
		return m, refreshCmd(m.backend)
	}

	// Anything left may be a disposition.
	row, ok := m.subject()
	if !ok {
		return m, nil
	}
	disp, ok := cockpit.DispositionByKey(row, msg.String())
	if !ok || m.busy {
		return m, nil
	}
	// A read-only model still walks to the confirmation. The refusal
	// belongs on the key that writes, not on the key that navigates:
	// `--dump-screen` exists so an agent can *look* at every screen, and a
	// blessing screen it could not reach would be the one screen in wand
	// nobody could inspect — which is exactly the one worth inspecting.
	return m.begin(row, disp), nil
}

// readOnlyRefusal is what the confirm key does when there is no writer. It
// names the mode rather than the key, because someone who pressed it has not
// made a mistake — they are looking at a picture of a screen.
const readOnlyRefusal = "read-only: this is the sample board. Every key here walks the screen; none of them write."

// begin moves to the confirmation for one disposition.
func (m Model) begin(row cockpit.Row, disp cockpit.Disposition) Model {
	m.pending = cockpit.Intent{Issue: row.Issue, Disp: disp}
	m.from = m.state
	m.state = stateConfirm
	m.failure = ""
	m.flash = ""

	if disp.Field == cockpit.FieldPriority {
		// Seed with the ticket's own priority when it has one: that is a
		// judgment someone already made, and re-typing it is not the
		// decision this screen is asking for. An unranked ticket seeds
		// nothing, so the choice has to be made.
		if p := row.Issue.Priority; p >= 1 && p <= 4 {
			m.pending.Priority = p
		}
	}
	m.input.SetValue("")
	m.input.Focus()
	m.input.Placeholder = placeholderFor(disp)
	m.input.SetWidth(max(m.width-fieldLabelWidth-gutter-2, 10))
	return m
}

func placeholderFor(d cockpit.Disposition) string {
	switch d.Field {
	case cockpit.FieldIdentifier:
		return "the issue this duplicates, e.g. WND-3"
	case cockpit.FieldReason:
		return "why this is being closed"
	default:
		return ""
	}
}

func (m Model) confirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		if m.busy {
			return m, nil
		}
		m.state, m.failure = m.from, ""
		m.input.Blur()
		return m, nil

	case key.Matches(msg, m.keys.Confirm):
		if m.busy || m.readOnly() {
			return m, nil
		}
		in := m.intent()
		if ok, _ := in.Ready(); !ok {
			return m, nil // the screen already says what is missing
		}
		m.busy, m.failure = true, ""
		return m, applyCmd(m.backend, in)
	}

	if m.pending.Disp.Field == cockpit.FieldPriority {
		if p := priorityKey(msg.String()); p != 0 {
			m.pending.Priority = p
		}
		return m, nil
	}
	if !m.typing() || m.busy {
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// priorityKey maps 1-4 to Linear's priority numbering. Zero means the key
// was not a priority — 0 is "No priority", which is its own disposition and
// not something you land on by pressing a digit.
func priorityKey(s string) int {
	switch s {
	case "1", "2", "3", "4":
		return int(s[0] - '0')
	}
	return 0
}

// intent is the pending judgment with the live text field folded in.
func (m Model) intent() cockpit.Intent {
	in := m.pending
	if m.typing() {
		in.Text = m.input.Value()
	}
	return in
}

func (m *Model) moveCursor(delta int) {
	n := len(m.rows())
	if n == 0 {
		m.cursor = 0
		return
	}
	m.cursor = min(max(m.cursor+delta, 0), n-1)
}

func (m *Model) clampCursor() {
	n := len(m.rows())
	if n == 0 {
		m.cursor = 0
		return
	}
	m.cursor = min(max(m.cursor, 0), n-1)
}

// resyncDetail re-resolves the opened row against a board that has just
// been re-read.
//
// A refresh is reachable from the detail screen, and the row it is showing
// is a copy taken when the row was opened. Leaving that copy in place would
// make the one key a person pressed to get current data the key that hides
// how stale it is — and the disposition keys act on this row, so the stale
// copy is not merely displayed, it is judged. When the row is gone from the
// board entirely, the detail screen has nothing left to show and says so on
// the way back rather than closing without explanation.
func (m *Model) resyncDetail() {
	if m.state != stateDetail {
		return
	}
	want := rowKey(m.detail)
	for _, row := range m.rows() {
		if rowKey(row) == want {
			m.detail = row
			return
		}
	}
	m.state = stateBoard
	m.flash, m.flashOK = rowGone(m.detail)+" is no longer waiting on you.", false
}

// rowKey is a row's identity across a re-read: the issue it names, or the
// run behind a lane. Not the cursor index, which is a position and belongs
// to the board rather than to the row.
func rowKey(r cockpit.Row) string {
	if r.IsLane() {
		return "lane:" + r.Lane.RunID
	}
	if r.Issue.ID != "" {
		return "issue:" + r.Issue.ID
	}
	return "issue:" + r.Issue.Identifier
}

// rowGone names a row for the sentence saying it left the board.
func rowGone(r cockpit.Row) string {
	if r.IsLane() {
		if r.Lane.Ticket != "" {
			return "the " + string(r.Lane.Kind) + " lane on " + r.Lane.Ticket
		}
		return "run " + r.Lane.RunID
	}
	return r.Issue.Identifier
}

// applied is the sentence the board flashes after a judgment lands. It
// names the destination, because the row vanishing is not by itself an
// answer to what happened to it.
func (m Model) applied(in cockpit.Intent) string {
	verb := "moved"
	if in.Disp.Bless {
		verb = "blessed"
	}
	sentence := fmt.Sprintf("%s %s → %s.", verb, in.Issue.Identifier, m.statusName(in.Disp.Status))
	if in.Disp.Field == cockpit.FieldPriority {
		sentence += " " + linear.PriorityName(in.Priority) + "."
	}
	return sentence
}

// statusName is what this board calls a covenant status. A covenant that
// somehow lacks the key falls back to the key itself rather than printing
// an empty arrow — a screen that says "→" and nothing else is a screen
// nobody can act on.
func (m Model) statusName(key string) string {
	if name := m.cov.StatusName(key); name != "" {
		return name
	}
	return key
}

// dropIssue removes one issue from a queue, matched on whichever identity
// is present. Both, because the sample board has no UUIDs and a live board
// has both — and a match on the empty string would clear the queue.
func dropIssue(issues []linear.Issue, id, identifier string) []linear.Issue {
	var kept []linear.Issue
	for _, issue := range issues {
		switch {
		case id != "" && issue.ID == id:
		case identifier != "" && issue.Identifier == identifier:
		default:
			kept = append(kept, issue)
		}
	}
	return kept
}

// withoutIssue drops one issue from every queue it might be in.
//
// The row is removed locally rather than by re-reading: the write returned
// nil, so the issue is not in that queue any more, and a board that kept
// showing it until a refresh would invite a second judgment on a ticket
// already judged.
func withoutIssue(s cockpit.Snapshot, id, identifier string) cockpit.Snapshot {
	s.Triage = dropIssue(s.Triage, id, identifier)
	s.NeedsInput = dropIssue(s.NeedsInput, id, identifier)
	s.ReadyForHuman = dropIssue(s.ReadyForHuman, id, identifier)
	return s
}

func applyCmd(b Backend, in cockpit.Intent) tea.Cmd {
	return func() tea.Msg {
		out, err := b.Apply(context.Background(), in)
		return appliedMsg{intent: out, err: err}
	}
}

func refreshCmd(b Backend) tea.Cmd {
	return func() tea.Msg {
		snap, err := b.Read(context.Background())
		return refreshedMsg{snap: snap, err: err}
	}
}
