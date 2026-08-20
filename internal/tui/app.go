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
	"time"

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

// EngageResult is one engage-mode poll's outcome, translated from
// dispatch's own TickResult into what the header needs to render it. A
// sibling type rather than a re-export of dispatch.TickResult, so this
// package's dependency on the process-manager mechanics stays confined to
// the shape it actually renders.
type EngageResult struct {
	// Dispatched reports whether a winner was spawned this poll.
	Dispatched bool
	// Ticket and Verb name the winner. Set only when Dispatched.
	Ticket, Verb string
	// LanesUsed and LanesCap are lane occupancy as of this poll.
	LanesUsed, LanesCap int
}

// Engager is the cockpit's process-manager extension to Backend: the
// mechanics `wand dispatch --watch` already implements, driven from inside
// the cockpit instead of a standalone process. Nil means engage mode is
// unavailable — the toggle refuses and says why, the same shape readOnly
// already uses for Backend.
//
// AcquireLock and ReleaseLock hold the same per-repo dispatch lock
// dispatch.Acquire arbitrates: engaging is what starts holding it, so two
// engaged cockpits — or an engaged cockpit and a standalone `wand dispatch`
// — never race the same Todo read. Tick drives exactly one non-blocking
// poll of dispatch's own gc/read/select/spawn-if-winner logic.
type Engager interface {
	AcquireLock() error
	ReleaseLock() error
	Tick(ctx context.Context) (EngageResult, error)
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
	// Engager makes engage mode available. Nil means the toggle refuses
	// and says why — the same shape a nil Backend gives read-only.
	Engager Engager
	// Interval is how long engage mode waits between polls. Zero means one
	// minute, `wand dispatch --watch`'s own default.
	Interval time.Duration
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

	// engager drives engage mode's lock and polling. Nil means the toggle
	// refuses.
	engager  Engager
	interval time.Duration
	// engaged is whether engage mode currently holds the dispatch lock.
	engaged bool
	// engaging is true while a lock acquire or release is in flight, so a
	// key repeated before the first lands cannot start a second one.
	engaging bool
	// quitting is true once a quit key has fired while engaged: the lock
	// release it triggered has to land before the program actually quits,
	// or a killed terminal would not be the only way to leave the lock
	// held past this process's own exit.
	quitting bool
	// clockTicking is whether the 1s countdown redraw loop is already
	// running, so a poll result does not start a second one alongside it.
	clockTicking bool
	// tickResult is the last engage-mode poll's outcome.
	tickResult EngageResult
	// nextPollAt is when the next poll fires, carried in from the tea.Cmd
	// that observed the clock — Update and View never read it themselves.
	nextPollAt time.Time
	// now is the last clock reading a redraw tick carried in, for rendering
	// the countdown to nextPollAt without View reading the clock itself.
	now time.Time
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

	interval := cfg.Interval
	if interval <= 0 {
		interval = time.Minute
	}

	m := Model{
		snap:     cfg.Snapshot,
		board:    cockpit.Build(cfg.Snapshot),
		backend:  cfg.Backend,
		cov:      cov,
		keys:     defaultKeyMap(),
		theme:    theme.New(),
		notice:   cfg.Notice,
		width:    cfg.Width,
		height:   cfg.Height,
		input:    in,
		engager:  cfg.Engager,
		interval: interval,
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

// lockMsg carries the result of one acquire or release of the dispatch
// lock. acquiring distinguishes the two: both produce the same shape, but
// only one of them turns engaged on.
type lockMsg struct {
	acquiring bool
	err       error
}

// engageTickMsg carries one engage-mode poll's outcome, along with the
// clock reading the tea.Cmd took when it fired — Update stores it, never
// reads the clock itself, so nextPollAt is derived here and nowhere else.
type engageTickMsg struct {
	at  time.Time
	res EngageResult
	err error
}

// clockTickMsg is the low-frequency redraw tick that lets the header's
// countdown move without a real poll happening: the countdown is derived
// from nextPollAt and this message's own clock reading, never from View
// reading the clock itself.
type clockTickMsg struct {
	now time.Time
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

	case lockMsg:
		m.engaging = false
		if msg.acquiring {
			if msg.err != nil {
				m.flash, m.flashOK = "engage: "+msg.err.Error(), false
				return m, nil
			}
			m.engaged = true
			return m, engageTickNowCmd(m.engager)
		}
		if msg.err != nil {
			m.flash, m.flashOK = "engage: releasing the dispatch lock: "+msg.err.Error(), false
		}
		if m.quitting {
			return m, tea.Quit
		}
		return m, nil

	case engageTickMsg:
		if !m.engaged {
			// Disengaged while this poll was in flight: nothing left to
			// show it on, and rescheduling would resurrect a countdown
			// nobody asked for any more.
			return m, nil
		}
		if msg.err != nil {
			m.flash, m.flashOK = "engage: "+msg.err.Error(), false
		} else {
			m.tickResult = msg.res
			if msg.res.Dispatched {
				m.flash, m.flashOK = fmt.Sprintf("engaged: dispatched %s (%s)", msg.res.Ticket, msg.res.Verb), true
			}
		}
		m.nextPollAt = msg.at.Add(m.interval)
		next := engageTickAfterCmd(m.engager, m.interval)
		if m.clockTicking {
			return m, next
		}
		m.clockTicking = true
		return m, tea.Batch(next, clockTickCmd())

	case clockTickMsg:
		m.now = msg.now
		if !m.engaged {
			m.clockTicking = false
			return m, nil
		}
		return m, clockTickCmd()

	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m Model) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.ForceQuit) {
		return m.quit()
	}
	if m.state == stateConfirm {
		// Quit works here too, but only when nothing has focus: on a
		// text field "q" is a letter, which is why ForceQuit above is
		// the escape hatch that never depends on focus.
		if !m.typing() && key.Matches(msg, m.keys.Quit) {
			return m.quit()
		}
		return m.confirmKey(msg)
	}
	if key.Matches(msg, m.keys.Quit) {
		return m.quit()
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

	case key.Matches(msg, m.keys.Engage):
		return m.toggleEngage()
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

// --- engage mode -----------------------------------------------------------

// engageUnavailable is what the toggle says when there is no Engager —
// the same "name the mode, not the key" shape readOnlyRefusal already
// uses for a nil Backend.
const engageUnavailable = "engage mode is unavailable: wand ui was not given what wand dispatch needs to run a winner"

// toggleEngage starts or stops engage mode. Starting acquires the repo's
// dispatch lock through a tea.Cmd and, once held, fires an immediate poll —
// the same "check right away, then wait" shape Watch's own loop opens
// with. Stopping releases the lock through a tea.Cmd and clears the polling
// state optimistically, so the header stops claiming a countdown that no
// longer means anything even before the release lands.
func (m Model) toggleEngage() (Model, tea.Cmd) {
	if m.engager == nil {
		m.flash, m.flashOK = engageUnavailable, false
		return m, nil
	}
	if m.engaging {
		return m, nil
	}
	if m.engaged {
		m.engaging = true
		m.engaged = false
		m.tickResult = EngageResult{}
		m.nextPollAt = time.Time{}
		return m, releaseLockCmd(m.engager)
	}
	m.engaging, m.flash = true, ""
	return m, acquireLockCmd(m.engager)
}

// quit is what every quit key calls: a plain tea.Quit normally, or — while
// engaged — the lock release first, landing as an ordinary lockMsg that the
// quitting flag turns into the actual tea.Quit once it lands. That keeps
// the release on the same message-driven path toggle-off already uses,
// rather than a second, opaque way to sequence two Cmds — a killed
// terminal is still the only way engage mode ever leaves the lock held past
// its own exit, and that case needs no code here: lock.go's own
// dead-holder reclaim is what covers it.
func (m Model) quit() (Model, tea.Cmd) {
	if m.engaged {
		m.quitting = true
		return m, releaseLockCmd(m.engager)
	}
	return m, tea.Quit
}

func acquireLockCmd(e Engager) tea.Cmd {
	return func() tea.Msg {
		return lockMsg{acquiring: true, err: e.AcquireLock()}
	}
}

func releaseLockCmd(e Engager) tea.Cmd {
	return func() tea.Msg {
		return lockMsg{acquiring: false, err: e.ReleaseLock()}
	}
}

// engageTickNowCmd fires one poll immediately — used the moment the lock is
// acquired, mirroring Watch's own "check right away" first pass.
func engageTickNowCmd(e Engager) tea.Cmd {
	return func() tea.Msg {
		res, err := e.Tick(context.Background())
		return engageTickMsg{at: time.Now(), res: res, err: err}
	}
}

// engageTickAfterCmd waits interval, then fires one poll — the steady-state
// shape once the first poll has landed.
func engageTickAfterCmd(e Engager, interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg {
		res, err := e.Tick(context.Background())
		return engageTickMsg{at: time.Now(), res: res, err: err}
	})
}

// clockTickCmd is the low-frequency redraw loop that moves the countdown
// between polls, without either Update or View reading the clock
// themselves — the tea.Cmd is the one place time.Now() is read.
func clockTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return clockTickMsg{now: t}
	})
}
