package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mattwalters/wand/internal/cockpit"
	"github.com/mattwalters/wand/internal/linear"
)

// gutter is the left margin every line shares, cursor marker included.
const gutter = 2

// fieldLabelWidth is the column the confirm screen's field labels are padded
// to, so the priority row and the text rows start their values in the same
// place.
const fieldLabelWidth = 13

// View implements tea.Model. It is a pure function of model state.
func (m Model) View() tea.View {
	var body string
	switch m.state {
	case stateDetail:
		body = m.detailView()
	case stateConfirm:
		body = m.confirmView()
	default:
		body = m.boardView()
	}

	v := tea.NewView(body)
	v.AltScreen = true
	return v
}

// --- the board -----------------------------------------------------------

// line is one rendered row of the screen, plus which board row it belongs
// to. The index is what lets the scroll window keep the cursor visible
// without the view having to re-derive the layout.
type line struct {
	text string
	row  int // index into board.Rows(), or -1 for chrome
}

func (m Model) boardView() string {
	lines := m.boardLines()
	head := m.headerView()
	foot := m.footerView()

	// Everything that is not the scrolling middle: the header and footer as
	// rendered, plus the blank line on either side of the rows. Measured
	// rather than assumed, because both grow — the header by a notice, the
	// footer by a flash or a judgment line — and a screen that guessed
	// would push its own help line off the bottom.
	chrome := countLines(head) + countLines(foot) + 2
	window := max(m.height-chrome, 1)

	visible, above, below := scroll(lines, m.cursor, window)
	if above > 0 || below > 0 {
		// The "n above, n below" line costs a row of its own, and only
		// once it is known to be needed.
		visible, above, below = scroll(lines, m.cursor, max(window-1, 1))
	}

	var b strings.Builder
	b.WriteString(head)
	b.WriteString("\n\n")
	for _, l := range visible {
		b.WriteString(l.text)
		b.WriteString("\n")
	}
	if above > 0 || below > 0 {
		b.WriteString(m.theme.Muted.Render(fmt.Sprintf("%s… %d above, %d below", pad(gutter), above, below)))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(foot)
	return b.String()
}

func countLines(s string) int { return strings.Count(s, "\n") + 1 }

// scroll picks the window of lines to show, keeping the cursor's row in it.
//
// Pure, and deliberately not a viewport component: the thing that must stay
// visible is a *row*, which may be several lines below its section header,
// and a component that scrolls by line has no way to know that.
func scroll(lines []line, cursor, height int) (visible []line, above, below int) {
	if len(lines) <= height {
		return lines, 0, 0
	}
	// Find the cursor's line, and the header above it that gives it context.
	target := 0
	for i, l := range lines {
		if l.row == cursor {
			target = i
			break
		}
	}
	start := max(target-height/2, 0)
	start = min(start, len(lines)-height)
	return lines[start : start+height], start, len(lines) - start - height
}

func (m Model) headerView() string {
	left := m.theme.Title.Render("wand cockpit")
	if m.board.Team != "" {
		left += m.theme.Muted.Render("· " + m.board.Team)
	}

	waiting := m.board.Waiting()
	var right string
	switch waiting {
	case 0:
		right = m.theme.Good.Render("nothing waiting on you")
	case 1:
		right = m.theme.Body.Render("1 waiting on you")
	default:
		right = m.theme.Body.Render(fmt.Sprintf("%d waiting on you", waiting))
	}

	head := spread(left, right, m.width)
	if line := m.engageLine(); line != "" {
		head += "\n" + line
	}
	if m.notice != "" {
		head += "\n" + m.theme.Muted.Render(pad(gutter)+truncate(m.notice, m.width-gutter))
	}
	return head
}

// engageLine is the header's second line while engage mode is on: idle
// with a countdown to the next poll, or the ticket the last poll spawned.
// Empty while disengaged, so a cockpit nobody has turned into a scheduler
// still reads exactly as it did before this mode existed.
func (m Model) engageLine() string {
	if !m.engaged {
		return ""
	}
	status := "idle"
	style := m.theme.Muted
	if m.tickResult.Dispatched {
		status = fmt.Sprintf("dispatched %s (%s)", m.tickResult.Ticket, m.tickResult.Verb)
		style = m.theme.Good
	}
	if !m.nextPollAt.IsZero() && !m.now.IsZero() {
		remaining := int(m.nextPollAt.Sub(m.now).Seconds())
		if remaining < 0 {
			remaining = 0
		}
		status += fmt.Sprintf(" · next poll in %ds", remaining)
	}
	return style.Render(pad(gutter) + "engaged · " + status)
}

func (m Model) boardLines() []line {
	var lines []line
	idx := 0
	cols := m.columns()

	if len(m.board.Running) > 0 {
		lines = append(lines, m.runningLines(cols)...)
		lines = append(lines, line{row: -1})
	}

	for i, section := range m.board.Sections {
		if i > 0 {
			lines = append(lines, line{row: -1})
		}
		lines = append(lines, line{text: m.sectionHeading(section), row: -1})
		if len(section.Rows) == 0 {
			lines = append(lines, line{
				text: m.theme.Muted.Render(pad(gutter+2) + section.Empty),
				row:  -1,
			})
			continue
		}
		for _, row := range section.Rows {
			lines = append(lines, line{text: m.rowView(row, idx == m.cursor, cols), row: idx})
			idx++
		}
	}
	return lines
}

func (m Model) sectionHeading(s cockpit.Section) string {
	head := m.theme.Heading.Render(pad(gutter) + s.Title)
	if n := len(s.Rows); n > 0 {
		head += m.theme.Muted.Render(fmt.Sprintf("  %d %s", n, s.Verb))
	}
	return head
}

// columns is the width of every fixed-width column on the board. Each one
// is measured across every row that shares it, so the board reads as a
// table rather than as five sections that happen to be stacked.
//
// The ordering is the point: identifier, then the one short fact about the
// row, then the title. Everything fixed-width sits on the left where the
// eye can run down it, and the one variable-length field is last, free to
// run to the edge. A short column pushed to the right margin lines up with
// itself and with nothing else, which leaves the reader tracking a title
// across a gap to find out what it says.
type columns struct {
	// id spans the five queues and the Running strip both: the identifier
	// is the same kind of thing everywhere on screen, so it gets one width.
	id int
	// tag spans the five queues only — priority, status, lane kind.
	tag int
	// phase is the Running strip's own tag width. It is separate because a
	// phase label ("implement (round 2)") is twice the width of a priority,
	// and folding it into tag would open a gap on every queue row to hold a
	// column those rows never fill.
	phase int
}

// render lays one row out across the columns: fixed fields padded, the
// variable one last and unpadded.
func (c columns) render(id, tag, text string, tagWidth int) string {
	return fmt.Sprintf("%-*s  %-*s  %s", c.id, id, tagWidth, tag, text)
}

func (m Model) columns() columns {
	var c columns
	for _, row := range m.rows() {
		c.id = max(c.id, len(rowIdentifier(row)))
		c.tag = max(c.tag, len([]rune(m.rowTag(row))))
	}
	for _, a := range m.board.Running {
		c.id = max(c.id, len(a.Ticket))
		c.phase = max(c.phase, len(a.PhaseLabel()))
	}
	return c
}

// rowIdentifier is the row's left column: the ticket, whether the row is a
// ticket or a lane standing in front of one.
func rowIdentifier(row cockpit.Row) string {
	if row.IsLane() {
		return row.Lane.Ticket
	}
	return row.Issue.Identifier
}

// --- the Active-runs strip ------------------------------------------------

// runningLines renders the strip of runs a live process is presently
// driving — the answer to "what is the machine doing right now?", as
// opposed to the four queues below it, which answer "what is waiting on a
// human?". None of these lines carry a cursor row: an active run is not a
// ticket a person disposits, and resolving one, if it ever needs resolving,
// means going to the machine — the same read-only reasoning [noJudgment]
// already gives a lane.
func (m Model) runningLines(cols columns) []line {
	heading := m.theme.Heading.Render(pad(gutter) + "Running")
	heading += m.theme.Muted.Render(fmt.Sprintf("  %d in flight", len(m.board.Running)))
	lines := []line{{text: heading, row: -1}}
	for _, a := range m.board.Running {
		lines = append(lines, line{text: m.runningRowView(a, cols), row: -1})
	}
	return lines
}

// runningRowView is one line of the Running strip: ticket, phase, harness,
// how long it has been going, and how long since its lease last renewed. A
// run whose own liveness judgment is not [journal.Alive] gets the sentence
// sweep's dead-lease logic exists to confirm, never a second verdict this
// package computed from the heartbeat's age itself — and that sentence is
// kept first among the free-text fields, so a narrow terminal's truncation
// clips harness or heartbeat before it ever clips the one thing that says a
// row may not mean what it looks like.
func (m Model) runningRowView(a cockpit.Active, cols columns) string {
	var fields []string
	if a.Stale() {
		fields = append(fields, "possibly dead, sweep will confirm")
	}
	fields = append(fields, a.HarnessLabel(), "up "+ago(m.now, a.Started), "hb "+ago(m.now, a.Heartbeat)+" ago")
	body := cols.render(a.Ticket, a.PhaseLabel(), strings.Join(fields, " · "), cols.phase)
	rendered := truncate(pad(gutter)+body, m.width)
	if a.Stale() {
		return m.theme.Warn.Render(rendered)
	}
	return m.theme.Body.Render(rendered)
}

// ago renders how long ago at was, as of now — "8s", "12m", "3h4m" — or an
// em dash when either clock reading is unset, which happens only for a
// board built with no [tui.Config.Now] and nothing running to time.
func ago(now, at time.Time) string {
	if now.IsZero() || at.IsZero() {
		return "—"
	}
	d := now.Sub(at)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func (m Model) rowView(row cockpit.Row, selected bool, cols columns) string {
	marker := "  "
	if selected {
		marker = "› "
	}

	text := row.Issue.Title
	if row.IsLane() {
		text = row.Lane.Reason
	}
	body := cols.render(rowIdentifier(row), m.rowTag(row), text, cols.tag)

	line := pad(gutter-len(marker)) + marker + truncate(body, max(m.width-gutter, 8))
	if selected {
		return m.theme.Selected.Render(line)
	}
	if row.IsLane() && row.Lane.Kind != cockpit.LaneParked {
		return m.theme.Warn.Render(line)
	}
	return m.theme.Body.Render(line)
}

// rowTag is the middle column: the one short fact about the row that its
// title does not say. For work you are ranking that is the rank; for work
// already moving it is where it has got to; for a lane it is why the lane
// is there.
//
// An unranked ticket gets a dash rather than a blank. Blank reads as "this
// column does not apply here", and in Triage the absence of a rank is
// precisely the thing being judged.
func (m Model) rowTag(row cockpit.Row) string {
	if row.IsLane() {
		return string(row.Lane.Kind)
	}
	if row.Kind == cockpit.KindReadyForHuman {
		return row.Issue.State.Name
	}
	if row.Issue.Priority != 0 {
		return linear.PriorityName(row.Issue.Priority)
	}
	return "—"
}

func (m Model) footerView() string {
	if m.flash != "" {
		style := m.theme.Good
		if !m.flashOK {
			style = m.theme.Bad
		}
		return m.wrap(style, m.flash) + "\n" + m.navHelp()
	}
	if help := m.dispositionHelp(); help != "" {
		return help + "\n" + m.navHelp()
	}
	return m.navHelp()
}

// flashLine is the board's flash, rendered wherever it has to be seen. The
// detail screen needs it as much as the board does: refresh works from
// there, and a failed refresh that printed nothing would leave a person
// reading stale rows believing they had just re-read them.
func (m Model) flashLine() string {
	if m.flash == "" {
		return ""
	}
	style := m.theme.Good
	if !m.flashOK {
		style = m.theme.Bad
	}
	return m.wrap(style, m.flash) + "\n"
}

func (m Model) navHelp() string {
	items := []string{"↑/k ↓/j move", "enter open"}
	if !m.readOnly() {
		items = append(items, "r refresh")
	}
	if m.engager != nil {
		label := "e engage"
		if m.engaged {
			label = "e disengage"
		}
		items = append(items, label)
	}
	items = append(items, "q quit")
	return m.theme.Muted.Render(pad(gutter) + strings.Join(items, " • "))
}

// dispositionHelp is the second help line: what you may do to the row under
// the cursor. When the answer is nothing, it says why — a key that silently
// does nothing reads as a broken key.
func (m Model) dispositionHelp() string {
	row, ok := m.subject()
	if !ok {
		return ""
	}
	disps := cockpit.Dispositions(row)
	if len(disps) == 0 {
		return m.theme.Muted.Render(pad(gutter) + noJudgment(row.Kind))
	}
	parts := make([]string, 0, len(disps))
	for _, d := range disps {
		parts = append(parts, d.Key+" "+shortName(d, m.statusName(d.Status)))
	}
	// Truncated rather than wrapped: a covenant may rename every status to
	// something long, and a help line that grows to two lines pushes the
	// board up by one row every time the cursor crosses a section.
	help := "judge  " + strings.Join(parts, "  ")
	return m.theme.Muted.Render(pad(gutter) + truncate(help, m.width-gutter))
}

// noJudgment says why a row offers no dispositions. Both sentences name the
// place the act actually happens, because "read-only" on its own tells a
// user what they cannot do and not what they should.
func noJudgment(k cockpit.Kind) string {
	switch k {
	case cockpit.KindReadyForHuman:
		return "read-only: this one is asking for a review, which happens on the pull request"
	case cockpit.KindLanes:
		return "read-only: a lane is not a ticket — resolving one means going to the machine"
	default:
		return "read-only"
	}
}

// shortName is the disposition's name for a help line, with the covenant's
// own status name in it.
// The ✦ is the same mark the blessing screen wears, and it is the only
// place it appears on the board — one glyph for one act.
func shortName(d cockpit.Disposition, status string) string {
	if d.Lane {
		return "clear parked"
	}
	switch d.Field {
	case cockpit.FieldPriority:
		return status
	case cockpit.FieldIdentifier:
		return "duplicate"
	case cockpit.FieldReason:
		// FieldReason now backs two dispositions with different
		// destinations — Cancel and RejectPlan — so the label has to name
		// which one this is rather than assuming the older of the two.
		if d.Status == "canceled" {
			return "cancel"
		}
		return status
	}
	if d.Bless {
		return "✦" + status
	}
	return "unranked"
}

// --- one issue -----------------------------------------------------------

func (m Model) detailView() string {
	row := m.detail
	var b strings.Builder

	if row.IsLane() {
		b.WriteString(m.theme.Title.Render(string(row.Lane.Kind) + " lane"))
		b.WriteString("\n\n")
		b.WriteString(m.field("run", row.Lane.RunID))
		b.WriteString(m.field("ticket", row.Lane.Ticket))
		b.WriteString(m.field("verb", row.Lane.Verb))
		b.WriteString(m.field("repo", row.Lane.Repo))
		b.WriteString("\n")
		b.WriteString(m.wrap(m.theme.Body, row.Lane.Reason))
		b.WriteString("\n\n")
		b.WriteString(m.indentWrap(m.theme.Muted, noJudgment(cockpit.KindLanes), gutter))
		b.WriteString("\n")
		b.WriteString(m.flashLine())
		b.WriteString(m.theme.Muted.Render(pad(gutter) + "esc back • q quit"))
		return b.String()
	}

	issue := row.Issue
	b.WriteString(m.theme.Title.Render(issue.Identifier))
	b.WriteString(m.theme.Body.Render(" " + truncate(issue.Title, m.width-len(issue.Identifier)-4)))
	b.WriteString("\n\n")
	b.WriteString(m.field("status", issue.State.Name))
	b.WriteString(m.field("priority", linear.PriorityName(issue.Priority)))
	if issue.Assignee != "" {
		b.WriteString(m.field("assignee", issue.Assignee))
	}
	if len(issue.Labels) > 0 {
		b.WriteString(m.field("labels", strings.Join(issue.Labels, ", ")))
	}
	if len(issue.BlockedBy) > 0 {
		parts := make([]string, len(issue.BlockedBy))
		for i, blocker := range issue.BlockedBy {
			parts[i] = fmt.Sprintf("%s (%s)", blocker.Identifier, blocker.State.Name)
		}
		b.WriteString(m.field("blocked", "by "+strings.Join(parts, ", ")))
	}
	if issue.URL != "" {
		b.WriteString(m.field("url", issue.URL))
	}
	b.WriteString("\n")

	desc := m.body(row)
	// The body is clipped rather than scrolled: this screen exists to
	// remind you what the ticket is before you judge it, and a ticket that
	// needs more than a screenful to decide on is one to read in Linear,
	// where the comments are too.
	b.WriteString(m.clampLines(m.wrap(m.theme.Body, desc), m.descriptionRoom()))
	b.WriteString("\n\n")

	if help := m.dispositionHelp(); help != "" {
		b.WriteString(help)
		b.WriteString("\n")
	}
	b.WriteString(m.flashLine())
	b.WriteString(m.theme.Muted.Render(pad(gutter) + "esc back • q quit"))
	return b.String()
}

// body is what the detail screen shows under the header block: the plan
// `wand plan` wrote, for a Plan Review row, or the ticket's own description
// for every other kind. A Plan Review row shows only the fenced region —
// the thing this screen exists to put a human's judgment on — not the
// description around it, which is the ticket as it stood before the plan
// run that just ended.
func (m Model) body(row cockpit.Row) string {
	if row.Kind == cockpit.KindPlanReview {
		text, ok, err := cockpit.PlanSection(row.Issue)
		switch {
		case err != nil:
			return "the plan section could not be read: " + err.Error()
		case !ok:
			return "(no plan section — this ticket has not been through `wand plan`)"
		default:
			return text
		}
	}
	desc := strings.TrimSpace(row.Issue.Description)
	if desc == "" {
		return "(no description)"
	}
	return desc
}

// descriptionRoom is how many lines of description fit under the header
// block. Derived rather than guessed so a short terminal clips the body
// instead of pushing the help line off the bottom.
func (m Model) descriptionRoom() int {
	issue := m.detail.Issue
	fields := 2 // status, priority
	if issue.Assignee != "" {
		fields++
	}
	if len(issue.Labels) > 0 {
		fields++
	}
	if len(issue.BlockedBy) > 0 {
		fields++
	}
	if issue.URL != "" {
		fields++
	}
	// title(1) blank(1) fields blank(1) … blank(1) judge(1) help(1)
	room := m.height - fields - 6
	if m.flash != "" {
		room -= countLines(m.flashLine())
	}
	return max(room, 1)
}

func (m Model) field(name, value string) string {
	label := m.theme.Muted.Render(fmt.Sprintf("%s%-10s", pad(gutter), name))
	return label + m.theme.Body.Render(truncate(value, m.width-gutter-10)) + "\n"
}

// --- the confirmation ----------------------------------------------------

func (m Model) confirmView() string {
	in := m.intent()
	d := in.Disp
	var b strings.Builder

	title, style := d.Name, m.theme.Heading
	if d.Bless {
		title, style = "✦ Blessing", m.theme.Bless
	}
	b.WriteString(style.Render(pad(gutter) + title))
	b.WriteString("\n\n")

	subject, transition := in.Issue.Identifier+"  "+in.Issue.Title,
		in.Issue.State.Name+" → "+m.statusName(d.Status)
	if d.Lane {
		subject = in.Lane.Ticket + "  " + in.Lane.Reason
		transition = string(in.Lane.Kind) + " → cleared"
	}
	b.WriteString(m.theme.Body.Render(pad(gutter+2) + truncate(subject, m.width-gutter-2)))
	b.WriteString("\n")
	b.WriteString(m.theme.Muted.Render(pad(gutter+2) + transition))
	b.WriteString("\n\n")

	b.WriteString(m.indentWrap(m.theme.Body, d.Gravity, gutter+2))
	b.WriteString("\n")

	if warning := m.blessWarning(in); warning != "" {
		b.WriteString("\n")
		b.WriteString(m.indentWrap(m.theme.Warn, warning, gutter+2))
		b.WriteString("\n")
	}

	if field := m.fieldView(in); field != "" {
		b.WriteString("\n")
		b.WriteString(field)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if m.failure != "" {
		b.WriteString(m.indentWrap(m.theme.Bad, m.failure, gutter+2))
		b.WriteString("\n")
		if note := preWriteNote(in); note != "" {
			b.WriteString(m.indentWrap(m.theme.Warn, note, gutter+2))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(m.confirmHelp(in))
	return b.String()
}

// blessWarning is the one thing worth saying at the moment of blessing that
// the disposition itself cannot know: an unranked ticket promoted to Todo
// sorts behind everything ranked, so the blessing works and the work still
// never starts.
func (m Model) blessWarning(in cockpit.Intent) string {
	if !in.Disp.Bless || in.Issue.Priority != 0 {
		return ""
	}
	return fmt.Sprintf("%s has no priority, and No priority sorts last in the queue. "+
		"Blessing it authorizes the work without scheduling it.", in.Issue.Identifier)
}

func (m Model) fieldView(in cockpit.Intent) string {
	switch in.Disp.Field {
	case cockpit.FieldPriority:
		var parts []string
		for p := 1; p <= 4; p++ {
			label := fmt.Sprintf("%d %s", p, linear.PriorityName(p))
			if in.Priority == p {
				parts = append(parts, m.theme.Selected.Render("["+label+"]"))
				continue
			}
			parts = append(parts, m.theme.Muted.Render(" "+label+" "))
		}
		return m.theme.Muted.Render(fmt.Sprintf("%s%-*s", pad(gutter+2), fieldLabelWidth, "priority")) +
			strings.Join(parts, " ")
	case cockpit.FieldIdentifier, cockpit.FieldReason:
		label := "duplicate of"
		if in.Disp.Field == cockpit.FieldReason {
			label = "reason"
		}
		value := m.input.View()
		if in.Done.PreWritten {
			// Spent: what this field held is already on the ticket, and a
			// cursor blinking in it would invite an edit that cannot
			// reach anywhere.
			value = m.theme.Body.Render(truncate(in.Text, max(m.width-gutter-2-fieldLabelWidth, 10)))
		}
		return m.theme.Muted.Render(fmt.Sprintf("%s%-*s", pad(gutter+2), fieldLabelWidth, label)) + value
	}
	return ""
}

// preWriteNote says what a failed judgment already left on the ticket.
//
// Without it the honest reading of a failure is "nothing happened", which
// for these two dispositions is wrong in exactly the direction that gets a
// cancellation reason posted twice. The sentence is also what makes the
// frozen field make sense rather than look broken.
func preWriteNote(in cockpit.Intent) string {
	if !in.Done.PreWritten {
		return ""
	}
	switch in.Disp.Field {
	case cockpit.FieldIdentifier:
		return "The duplicate link is already recorded, and is not written again: enter retries the status move alone."
	case cockpit.FieldReason:
		return "The reason is already posted on the ticket, and is not posted again: enter retries the status move alone."
	}
	return ""
}

func (m Model) confirmHelp(in cockpit.Intent) string {
	if m.busy {
		return m.theme.Muted.Render(pad(gutter) + "writing to Linear…")
	}
	if m.readOnly() {
		return m.indentWrap(m.theme.Muted, readOnlyRefusal, gutter) + "\n" +
			m.theme.Muted.Render(pad(gutter)+"esc back")
	}
	if ok, why := in.Ready(); !ok {
		return m.theme.Muted.Render(pad(gutter)+why) + "\n" +
			m.theme.Muted.Render(pad(gutter)+"esc back")
	}
	var confirm string
	switch {
	case in.Disp.Lane:
		confirm = fmt.Sprintf("enter clear parked → %s", in.Lane.Ticket)
	case in.Disp.Bless:
		confirm = fmt.Sprintf("enter bless %s → %s", in.Issue.Identifier, m.statusName(in.Disp.Status))
	default:
		confirm = fmt.Sprintf("enter move %s → %s", in.Issue.Identifier, m.statusName(in.Disp.Status))
	}
	return m.theme.Muted.Render(pad(gutter) + confirm + " • esc back")
}

// --- text helpers --------------------------------------------------------

func pad(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// truncate clips to width runes, ending in an ellipsis when it had to cut.
// Rune-based rather than byte-based: an identifier is ASCII but a ticket
// title is whatever a person typed.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

// spread puts left and right on one line, right-aligned to width. When they
// do not both fit, the right side is dropped rather than wrapped: the count
// is context, and context that pushes the title off screen is not context.
func spread(left, right string, width int) string {
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	if lw+rw+2 > width {
		return left
	}
	return left + pad(width-lw-rw) + right
}

func (m Model) wrap(style lipgloss.Style, s string) string {
	return m.indentWrap(style, s, gutter)
}

// indentWrap wraps text to the screen and indents every line of it. lipgloss
// does the wrapping; the indent is applied afterwards so the style's own
// width is the text width and not the text width plus the margin.
func (m Model) indentWrap(style lipgloss.Style, s string, indent int) string {
	wrapped := style.Width(max(m.width-indent, 20)).Render(s)
	lines := strings.Split(wrapped, "\n")
	for i, l := range lines {
		lines[i] = pad(indent) + l
	}
	return strings.Join(lines, "\n")
}

// clampLines cuts a block to at most n lines, saying so when it cut.
func (m Model) clampLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	kept := append([]string(nil), lines[:max(n-1, 0)]...)
	kept = append(kept, m.theme.Muted.Render(fmt.Sprintf("%s… %d more lines; read it in Linear",
		pad(gutter), len(lines)-n+1)))
	return strings.Join(kept, "\n")
}
