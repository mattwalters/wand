// Package home answers one question: what is waiting on a human? — but not
// as one flat list. The five queues are not one job; they are three, and a
// person sitting down at wand has come to do exactly one of them: [Decide]
// what to start (Triage), [Review] what came back (Plan Review, Needs
// Input, In Review), or [Unblock] the machine (Stalled). A landing
// screen names the three and how much is waiting in each; picking one
// scopes the screen to that job alone. See [View] and [Board.SectionsFor].
//
// Five queues still, and nothing else besides them. Triage to judge, Plan
// Review to bless a plan, Needs Input to answer, and In Review to see what
// is moving — the ready-for-human label floats what a run deliberately
// flagged to the top, but every In Review issue is a row — and stalled runs
// no process is driving any more. Each is a queue
// that nothing drains on its own — the failure mode PLAN.md names "queues
// nothing drains" — and each is invisible until something puts it on
// screen. [Build] always returns all five; a [View] is a lens over that one
// board, not a second construction path.
//
// What is deliberately *not* here: Backlog. A Backlog ticket is not waiting
// on you; it is the pool, and browsing a pool is Linear's job, done better
// there. Home shows what has stopped, not what exists.
//
// Alongside the queues sits one strip that answers a different question —
// not "what is waiting on a human?" but "what is the machine doing right
// now?" (see [Active] and [Board.Running]). It is read-only, rendered from
// the journal and lease store alone, and it counts toward nothing: a run
// being actively driven is, by definition, not waiting on anyone.
//
// # Blessing
//
// Promotion to Todo and to To Plan is the transition [guard] refuses
// everywhere else, and this package is the one place in wand that performs
// it. That is not an exception to the rule, it is the rule's other half: the
// guard stops *agents* from granting authorization unattended, and blessing
// is what a human does when they grant it deliberately. See apply.go, where
// the absence of a guard call is the load-bearing detail.
//
// This file is the pure half — a [Snapshot] of what was read becomes a
// [Board] of sections and rows, and each row knows which [Disposition]s a
// human may pass on it. No I/O, no clock: read.go fetches, apply.go writes,
// and every decision in between is a function a test can hold.
//
// [guard]: https://pkg.go.dev/github.com/mattwalters/wand/internal/guard
package home

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/plan"
	"github.com/mattwalters/wand/internal/queue"
)

// ReadyForHumanLabel names the label a run sets deliberately at
// convergence — "a human specifically needs to look at this" — used here
// only to sort In Review's rows, the labeled ones first. Covenant topology
// like the other labels, not a parameter (see covenant.Default).
const ReadyForHumanLabel = "ready-for-human"

// Snapshot is everything home reads, already fetched. Pure input:
// [Build] turns one of these into a board without touching the network.
type Snapshot struct {
	// Team is the team key the board was read for, e.g. "WND".
	Team string
	// Triage and NeedsInput are two of the judgment queues, as read.
	Triage     []linear.Issue
	NeedsInput []linear.Issue
	// PlanReview is every issue carrying a finished plan, awaiting the human
	// judgment that either blesses it into Todo or sends it back.
	PlanReview []linear.Issue
	// InReview is every issue in the In Review status. [Build] floats the
	// ones also carrying the ready-for-human label to the top of the
	// section, so a status-true heading loses nothing of the label's signal.
	InReview []linear.Issue
	// Stalled are runs the journal says a person has to resolve.
	Stalled []StalledRun
	// Active are runs a live process is presently driving — WND-41's
	// answer to "what is the machine doing?" rather than "what is waiting
	// on a human?". See [Active] and [Board.Running].
	Active []Active
}

// Kind names one queue on the board.
type Kind string

const (
	KindTriage     Kind = "triage"
	KindPlanReview Kind = "plan_review"
	KindNeedsInput Kind = "needs_input"
	KindInReview   Kind = "in_review"
	KindStalled    Kind = "stalled"
)

// View is one of the three jobs a person comes to home to do. It groups
// Kinds; it does not change what [Build] reads or returns.
type View int

const (
	// ViewDecide is judging the pool: rank it, promote it. Sourced from
	// Triage today; a ranked Backlog slice is a later, one-line addition to
	// [kindView], not a reason to change this type.
	ViewDecide View = iota
	// ViewReview is blessing plans, answering questions and merging PRs:
	// judgment on work that already exists, not on what to start.
	ViewReview
	// ViewUnblock is repairing the tooling, not judging the work: clearing
	// parks and resolving runs no process is driving.
	ViewUnblock
)

// kindView is the fixed Kind→View table every view is built from. Adding a
// Kind to a view — a ranked Backlog slice into ViewDecide, say — is the one
// line this map needs; nothing else in this file changes shape.
var kindView = map[Kind]View{
	KindTriage:     ViewDecide,
	KindPlanReview: ViewReview,
	KindNeedsInput: ViewReview,
	KindInReview:   ViewReview,
	KindStalled:    ViewUnblock,
}

// ViewInfo names one view for the landing screen and the docs: the key that
// selects it, what it is called, and the job it exists to do.
type ViewInfo struct {
	View  View
	Key   string
	Title string
	Job   string
}

// Views lists the three, in the order the landing screen shows them and the
// order their keys count up in.
var Views = []ViewInfo{
	{View: ViewDecide, Key: "1", Title: "Decide", Job: "judge the pool, rank it, promote it"},
	{View: ViewReview, Key: "2", Title: "Review", Job: "bless plans, answer questions, merge PRs"},
	{View: ViewUnblock, Key: "3", Title: "Unblock", Job: "clear parks, resolve stuck lanes"},
}

// Row is one line a human can put a cursor on: an issue in one of the four
// issue queues, or a stalled run. Exactly one of Issue and Stalled is
// meaningful, and [Row.IsStalled] says which.
type Row struct {
	Kind    Kind
	Issue   linear.Issue
	Stalled StalledRun
}

// IsStalled reports whether the row is a stalled run rather than an issue.
func (r Row) IsStalled() bool { return r.Kind == KindStalled }

// Section is one queue with its rows.
type Section struct {
	Kind Kind
	// Title names the queue, and Verb says what it wants from you —
	// "to judge", "to answer". A count with no verb is a number; the
	// point of this screen is that every row is somebody's next move.
	Title string
	Verb  string
	// Empty is what to say when the queue has nothing in it. An empty
	// queue and an unread one are different answers, and the difference
	// is the whole reason this field is not "".
	Empty string
	Rows  []Row
}

// Board is the whole screen: the five sections in a fixed order, plus the
// Active-runs strip.
type Board struct {
	Team     string
	Sections []Section
	// Running is what a live process is doing right now. Deliberately not
	// a Section: nothing in it is waiting on a human, so it is excluded
	// from [Board.Waiting] and from the cursor's row order — see [Active].
	Running []Active
}

// Waiting counts every row across every section — the one number that
// answers the question the screen exists for.
func (b Board) Waiting() int {
	n := 0
	for _, s := range b.Sections {
		n += len(s.Rows)
	}
	return n
}

// Rows flattens the board into cursor order: every row of every section,
// sections in board order. The screen's cursor indexes into this.
func (b Board) Rows() []Row {
	var rows []Row
	for _, s := range b.Sections {
		rows = append(rows, s.Rows...)
	}
	return rows
}

// SectionsFor is one view's slice of the board: the sections whose Kind
// belongs to v, in board order. A lens over [Board.Sections], not a second
// construction path — [Build] still returns all five every time.
func (b Board) SectionsFor(v View) []Section {
	var out []Section
	for _, s := range b.Sections {
		if kindView[s.Kind] == v {
			out = append(out, s)
		}
	}
	return out
}

// RowsIn flattens one view's sections into cursor order, the same way
// [Board.Rows] does for the whole board. A screen scoped to a view indexes
// its cursor into this rather than into Rows.
func (b Board) RowsIn(v View) []Row {
	var rows []Row
	for _, s := range b.SectionsFor(v) {
		rows = append(rows, s.Rows...)
	}
	return rows
}

// WaitingIn counts rows within one view — the per-view answer to what
// [Board.Waiting] answers globally.
func (b Board) WaitingIn(v View) int {
	n := 0
	for _, s := range b.SectionsFor(v) {
		n += len(s.Rows)
	}
	return n
}

// Build turns a snapshot into a board. The sections are always all five,
// even when empty: a queue that vanishes when it drains teaches you to stop
// looking for it, and the day it refills you will not notice.
func Build(s Snapshot) Board {
	return Board{
		Team:    s.Team,
		Running: s.Active,
		Sections: []Section{
			{
				Kind:  KindTriage,
				Title: "Triage",
				Verb:  "to judge",
				Empty: "nothing filed since you last looked.",
				Rows:  issueRows(KindTriage, s.Triage),
			},
			{
				Kind:  KindPlanReview,
				Title: "Plan Review",
				Verb:  "to bless",
				Empty: "no plan is waiting on a blessing.",
				Rows:  issueRows(KindPlanReview, s.PlanReview),
			},
			{
				Kind:  KindNeedsInput,
				Title: "Needs Input",
				Verb:  "to answer",
				Empty: "no run is parked on a question.",
				Rows:  issueRows(KindNeedsInput, s.NeedsInput),
			},
			{
				Kind:  KindInReview,
				Title: "In Review",
				Verb:  "in review",
				Empty: "nothing is in review.",
				Rows:  inReviewRows(s.InReview),
			},
			{
				Kind:  KindStalled,
				Title: "Stalled",
				Verb:  "to resolve",
				Empty: "every run is either finished or being driven.",
				Rows:  stalledRows(s.Stalled),
			},
		},
	}
}

// PlanSection returns the plan region `wand plan` wrote onto issue's
// description — the marker-fenced text [plan.PlanMarkdown] rendered and
// nothing else — and whether the issue carries one. A Plan Review ticket
// always should; a malformed pair of markers is reported as an error rather
// than guessed at, the same refusal [linear.ReadSection] makes for every
// other reader of a fenced section.
func PlanSection(issue linear.Issue) (string, bool, error) {
	return linear.ReadSection(issue.Description, plan.PlanSectionID)
}

// issueRows orders a queue and wraps it as rows.
//
// Order is the queue package's, deliberately: the ranked order an agent
// starts work in is the order a human should judge it in, and two orders
// would mean the ticket you blessed first is not the one that runs first.
// Vetting is *not* applied — a human-only ticket is precisely one waiting on
// a human, and a blocked one is still yours to judge.
func issueRows(kind Kind, issues []linear.Issue) []Row {
	ordered := make([]linear.Issue, len(issues))
	copy(ordered, issues)
	sort.SliceStable(ordered, func(i, j int) bool {
		return queue.Less(ordered[i], ordered[j])
	})
	rows := make([]Row, 0, len(ordered))
	for _, issue := range ordered {
		rows = append(rows, Row{Kind: kind, Issue: issue})
	}
	return rows
}

// inReviewRows orders In Review status-driven, label-sorted: issues a run
// deliberately flagged with ready-for-human first, the rest after — each
// group internally in the same queue order [issueRows] gives every other
// section. A status-true heading loses nothing of the label's signal this
// way; it just stops being the only thing the section can show.
func inReviewRows(issues []linear.Issue) []Row {
	var flagged, rest []linear.Issue
	for _, issue := range issues {
		if hasLabel(issue, ReadyForHumanLabel) {
			flagged = append(flagged, issue)
		} else {
			rest = append(rest, issue)
		}
	}
	rows := issueRows(KindInReview, flagged)
	return append(rows, issueRows(KindInReview, rest)...)
}

// hasLabel reports whether issue carries label.
func hasLabel(issue linear.Issue, label string) bool {
	for _, l := range issue.Labels {
		if l == label {
			return true
		}
	}
	return false
}

// Field is the extra input a disposition needs before it can be applied.
// The fields exist because three of the dispositions are meaningless
// without one: an unranked Backlog ticket, a Duplicate naming nothing, and
// a Cancel with no reason are each a decision nobody can audit later.
type Field int

const (
	FieldNone Field = iota
	// FieldPriority is Linear's 1-4; 0 is not offered here, because
	// "no priority" is its own disposition rather than a value you land on
	// by not choosing.
	FieldPriority
	// FieldIdentifier is the canonical issue a duplicate points at.
	FieldIdentifier
	// FieldReason is free text, posted as a comment before the status moves.
	FieldReason
)

// Disposition is one judgment a human may pass on a row.
type Disposition struct {
	// Key is the keystroke that starts it.
	Key string
	// Name is what it is called on screen.
	Name string
	// Status is the covenant status key it moves the issue to.
	Status string
	// Field is what must be supplied first.
	Field Field
	// Gravity is what the confirmation says. Every one of these
	// transitions is either an authorization or a closure, and the
	// sentence is where the weight of it lives — the point of making
	// blessing a moment is that the moment says what it is doing.
	Gravity string
	// Bless marks the two promotions the guard forbids agents. The screen
	// renders them differently, and that difference is the branding.
	Bless bool
	// Stalled marks a disposition that acts on the stalled run a row names
	// rather than on an issue's status — ClearParked is the only one.
	// [Apply] and [Intent.Ready] branch on it, and the screen never asks it
	// for a status name: a stalled run carries no covenant status to move to.
	Stalled bool
}

// The dispositions. Seven, and no more: five statuses plus the split between
// a ranked and an unranked Backlog (the difference between "worth doing" and
// "worth keeping"), plus RejectPlan — a second, reasoned way into Backlog
// that only a Plan Review row offers, because sending a finished plan back
// is a verdict on it and a raw ticket's Backlog move is not.
var (
	BlessTodo = Disposition{
		Key: "t", Name: "Bless → Todo", Status: "todo", Bless: true,
		Gravity: "Todo is the gate between written down and a bot may act on this " +
			"unattended. Blessing it lets an agent claim the ticket, branch, write " +
			"code and open a pull request without asking you again.",
	}
	BlessToPlan = Disposition{
		Key: "s", Name: "Bless → To Plan", Status: "to_plan", Bless: true,
		Gravity: "To Plan blesses research rather than building: a ticket sitting here " +
			"is one a dispatcher may spend a scout on unattended. The scout claims it " +
			"into In Planning, then ends at Needs Input, back on this screen, with the " +
			"decision still yours.",
	}
	ToBacklog = Disposition{
		Key: "b", Name: "Backlog, ranked", Status: "backlog", Field: FieldPriority,
		Gravity: "Backlog is the pool. A priority says you have judged this worth doing " +
			"and roughly when — it is what decides the order the queue hands work out in.",
	}
	ToBacklogUnranked = Disposition{
		Key: "u", Name: "Backlog, unranked", Status: "backlog",
		Gravity: "Worth keeping, not worth ranking. It sits in the pool at No priority, " +
			"which sorts behind everything ranked, and waits for someone to disagree.",
	}
	AsDuplicate = Disposition{
		Key: "d", Name: "Duplicate", Status: "duplicate", Field: FieldIdentifier,
		Gravity: "The link is written before the status moves, so a Duplicate ticket " +
			"always names the one it duplicates. A duplicate that points at nothing " +
			"is a dead end someone re-reads a year from now.",
	}
	Cancel = Disposition{
		Key: "x", Name: "Canceled, with reason", Status: "canceled", Field: FieldReason,
		Gravity: "Closing is final in practice: nobody re-reads a Canceled ticket. The " +
			"reason is posted as a comment before the status moves, because a close " +
			"nobody can explain is one nobody can undo either.",
	}
	// RejectPlan sends a Plan Review ticket back to the pool. Demoting a plan is
	// as much a judgment as blessing one, and the reason exists for the same
	// reason Cancel's does: the next plan run over this ticket reads it
	// instead of guessing why the last one did not land.
	RejectPlan = Disposition{
		Key: "b", Name: "Backlog, with reason", Status: "backlog", Field: FieldReason,
		Gravity: "Backlog is the pool. The reason is posted as a comment before the status " +
			"moves, because a rejected plan with no reason on it leaves the next plan run " +
			"over this ticket guessing at what was wrong.",
	}
	// ClearParked removes the parked label from the ticket a parked run
	// names — the act ReportPark's own comment instructs and, until this,
	// nothing in wand could perform. It is offered only on a StallParked row:
	// a stalled run is not a ticket for the other six dispositions, but a park's
	// own comment names exactly one next move, and this is the door to it.
	ClearParked = Disposition{
		Key: "c", Name: "Clear parked label", Stalled: true,
		Gravity: "Clearing the label authorizes wand dispatch to pick this ticket up again " +
			"on a later pass. It does not explain the park away — the comment already on " +
			"the ticket is what a later reader has to go on.",
	}
)

// judgments are the six Triage and Needs Input rows offer, in the order they
// are offered.
var judgments = []Disposition{BlessTodo, BlessToPlan, ToBacklog, ToBacklogUnranked, AsDuplicate, Cancel}

// planReviewJudgments are what a Plan Review row offers: bless the plan into
// Todo, or reject it back to Backlog with the reasoning that made the call.
// Judging a plan is narrower than judging a raw ticket — there is no
// research left to authorize, and no title vague enough yet to be someone
// else's ticket — so it gets a shorter list rather than the full six.
var planReviewJudgments = []Disposition{BlessTodo, RejectPlan}

// stalledJudgments is what a parked row offers: the one act a park's own
// comment names.
var stalledJudgments = []Disposition{ClearParked}

// Dispositions returns what a human may do to this row.
//
// Triage and Needs Input get all six. Plan Review gets the narrower pair:
// bless the plan, or reject it. In Review gets none, and that is the
// honest answer rather than a gap: the act a flagged row is asking for is a
// review or a merge, which happens on the pull request — and the covenant's
// on-merge automation closes the ticket seconds later. A status write from
// here would be racing that automation to say the same thing. An unflagged
// row is further still from a judgment of this screen's: it is mid-review,
// with nobody yet asking for anything.
//
// Stalled rows get none, with one exception: a parked run offers
// ClearParked. A stalled run is otherwise not a ticket, and resolving one
// means going to the machine — but a park is a hand-back with a stated next
// move, and clearing its label is that move, not a judgment about the run.
func Dispositions(r Row) []Disposition {
	switch r.Kind {
	case KindTriage, KindNeedsInput:
		return judgments
	case KindPlanReview:
		return planReviewJudgments
	case KindStalled:
		if r.Stalled.Kind == StallParked {
			return stalledJudgments
		}
		return nil
	default:
		return nil
	}
}

// DispositionByKey finds the disposition a keystroke starts on this row, if
// the row offers one.
func DispositionByKey(r Row, key string) (Disposition, bool) {
	for _, d := range Dispositions(r) {
		if d.Key == key {
			return d, true
		}
	}
	return Disposition{}, false
}

// Intent is a disposition aimed at an issue, with whatever its field needs.
// It exists as a value so the screen can hold a half-made decision — the
// confirmation step is the point — and so [Apply] takes one argument that
// carries the whole act.
type Intent struct {
	Issue linear.Issue
	// Stalled is what a stalled-run disposition — ClearParked — acts on.
	// Meaningful only when Disp.Stalled is set; an issue disposition leaves
	// it zero.
	Stalled  StalledRun
	Disp     Disposition
	Priority int    // when Disp.Field is FieldPriority
	Text     string // the identifier or the reason
	// Done records which of [Apply]'s writes have already landed. Two of
	// the dispositions write twice, and the first write is not undoable: a
	// posted cancellation reason and a duplicate relation both stay put
	// when the status move that was meant to follow them fails. The screen
	// is built to let a person retry that failure, so the retry has to know
	// what it must not do again.
	Done Progress
}

// Progress is how far one intent got. Carried on the intent rather than
// held by the screen because it is a fact about the ticket, not about the
// session looking at it — [Apply] is what learns it, and [Apply] is what
// has to honor it on the way back through.
type Progress struct {
	// PreWritten is set once the comment or the relation is on the ticket.
	PreWritten bool
}

// Ready reports whether the intent may be applied, and when it may not, the
// sentence saying what is missing. Called by the screen to decide whether
// the confirm key does anything, and again by [Apply], because a screen is
// not a validator.
func (in Intent) Ready() (bool, string) {
	if in.Disp.Stalled {
		if strings.TrimSpace(in.Stalled.Ticket) == "" {
			return false, "no stalled run selected"
		}
		return true, ""
	}
	switch in.Disp.Field {
	case FieldPriority:
		if in.Priority < 1 || in.Priority > 4 {
			return false, "pick a priority: 1 Urgent, 2 High, 3 Medium, 4 Low"
		}
	case FieldIdentifier:
		id := strings.TrimSpace(in.Text)
		if id == "" {
			return false, "name the issue this duplicates, e.g. " + exampleIdentifier(in.Issue.Identifier)
		}
		if strings.EqualFold(id, in.Issue.Identifier) {
			return false, fmt.Sprintf("%s cannot duplicate itself", in.Issue.Identifier)
		}
		if !looksLikeIdentifier(id) {
			return false, fmt.Sprintf("%q is not an issue identifier; wand expects the TEAM-123 form", id)
		}
	case FieldReason:
		if strings.TrimSpace(in.Text) == "" {
			return false, "say why — the reason is the only thing a reader gets a year from now"
		}
	}
	return true, ""
}

// looksLikeIdentifier accepts the TEAM-123 shape and nothing else. Checked
// here rather than left to the API because the failure Linear returns for a
// malformed identifier is indistinguishable from the one it returns for an
// issue that does not exist, and those are different mistakes.
func looksLikeIdentifier(s string) bool {
	team, _, ok := splitIdentifier(s)
	return ok && team != ""
}

func splitIdentifier(id string) (team string, number string, ok bool) {
	i := strings.LastIndex(id, "-")
	if i <= 0 || i == len(id)-1 {
		return "", "", false
	}
	number = id[i+1:]
	for _, r := range number {
		if r < '0' || r > '9' {
			return "", "", false
		}
	}
	return id[:i], number, true
}

// exampleIdentifier borrows the team prefix off the issue being judged, so
// the hint reads in the reader's own board rather than in a stranger's.
func exampleIdentifier(id string) string {
	if team, _, ok := splitIdentifier(id); ok {
		return team + "-1"
	}
	return "WND-1"
}
