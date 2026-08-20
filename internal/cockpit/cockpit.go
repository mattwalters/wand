// Package cockpit answers one question: what is waiting on a human?
//
// Five queues, and nothing else. Triage to judge, Scoped to bless a plan,
// Needs Input to answer, ready-for-human work to look at, and lanes no
// process is driving any more. Each is a queue that nothing drains on its
// own — the failure mode PLAN.md names "queues nothing drains" — and each is
// invisible until something puts it on one screen.
//
// What is deliberately *not* here: Backlog. A Backlog ticket is not waiting
// on you; it is the pool, and browsing a pool is Linear's job, done better
// there. The cockpit shows what has stopped, not what exists.
//
// Alongside the four queues sits one strip that answers a different
// question — not "what is waiting on a human?" but "what is the machine
// doing right now?" (see [Active] and [Board.Running]). It is read-only,
// rendered from the journal and lease store alone, and it counts toward
// nothing: a run being actively driven is, by definition, not waiting on
// anyone.
//
// # Blessing
//
// Promotion to Todo and to Scoping is the transition [guard] refuses
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
package cockpit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/queue"
	"github.com/mattwalters/wand/internal/scope"
)

// ReadyForHumanLabel marks work a person has to look at — a PR to review, a
// merge to press. Covenant topology like the other two labels, not a
// parameter (see covenant.Default).
const ReadyForHumanLabel = "ready-for-human"

// Snapshot is everything the cockpit reads, already fetched. Pure input:
// [Build] turns one of these into a board without touching the network.
type Snapshot struct {
	// Team is the team key the board was read for, e.g. "WND".
	Team string
	// Triage and NeedsInput are two of the judgment queues, as read.
	Triage     []linear.Issue
	NeedsInput []linear.Issue
	// Scoped is every issue carrying a finished plan, awaiting the human
	// judgment that either blesses it into Todo or sends it back.
	Scoped []linear.Issue
	// ReadyForHuman is every open issue carrying the ready-for-human label.
	ReadyForHuman []linear.Issue
	// Lanes are runs the journal says a person has to resolve.
	Lanes []Lane
	// Active are runs a live process is presently driving — WND-41's
	// answer to "what is the machine doing?" rather than "what is waiting
	// on a human?". See [Active] and [Board.Running].
	Active []Active
}

// Kind names one queue on the board.
type Kind string

const (
	KindTriage        Kind = "triage"
	KindScoped        Kind = "scoped"
	KindNeedsInput    Kind = "needs_input"
	KindReadyForHuman Kind = "ready_for_human"
	KindLanes         Kind = "lanes"
)

// Row is one line a human can put a cursor on: an issue in one of the three
// issue queues, or a lane. Exactly one of Issue and Lane is meaningful, and
// [Row.IsLane] says which.
type Row struct {
	Kind  Kind
	Issue linear.Issue
	Lane  Lane
}

// IsLane reports whether the row is a lane rather than an issue.
func (r Row) IsLane() bool { return r.Kind == KindLanes }

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
				Kind:  KindScoped,
				Title: "Scoped",
				Verb:  "to bless",
				Empty: "no plan is waiting on a blessing.",
				Rows:  issueRows(KindScoped, s.Scoped),
			},
			{
				Kind:  KindNeedsInput,
				Title: "Needs Input",
				Verb:  "to answer",
				Empty: "no run is parked on a question.",
				Rows:  issueRows(KindNeedsInput, s.NeedsInput),
			},
			{
				Kind:  KindReadyForHuman,
				Title: "Ready for human",
				Verb:  "to look at",
				Empty: "nothing is labeled " + ReadyForHumanLabel + ".",
				Rows:  issueRows(KindReadyForHuman, openIssues(s.ReadyForHuman)),
			},
			{
				Kind:  KindLanes,
				Title: "Lanes",
				Verb:  "to resolve",
				Empty: "every run is either finished or being driven.",
				Rows:  laneRows(s.Lanes),
			},
		},
	}
}

// openIssues drops closed work. The ready-for-human label outlives the merge
// that answered it — Linear does not strip labels on close — so a board that
// showed every labeled issue would fill with work already done.
func openIssues(issues []linear.Issue) []linear.Issue {
	var open []linear.Issue
	for _, issue := range issues {
		switch issue.State.Type {
		case "completed", "canceled":
		default:
			open = append(open, issue)
		}
	}
	return open
}

// PlanSection returns the plan region scope wrote onto issue's description —
// the marker-fenced text [scope.PlanMarkdown] rendered and nothing else —
// and whether the issue carries one. A Scoped ticket always should; a
// malformed pair of markers is reported as an error rather than guessed at,
// the same refusal [linear.ReadSection] makes for every other reader of a
// fenced section.
func PlanSection(issue linear.Issue) (string, bool, error) {
	return linear.ReadSection(issue.Description, scope.PlanSectionID)
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
}

// The dispositions. Seven, and no more: five statuses plus the split between
// a ranked and an unranked Backlog (the difference between "worth doing" and
// "worth keeping"), plus RejectPlan — a second, reasoned way into Backlog
// that only a Scoped row offers, because sending a finished plan back is a
// verdict on it and a raw ticket's Backlog move is not.
var (
	BlessTodo = Disposition{
		Key: "t", Name: "Bless → Todo", Status: "todo", Bless: true,
		Gravity: "Todo is the gate between written down and a bot may act on this " +
			"unattended. Blessing it lets an agent claim the ticket, branch, write " +
			"code and open a pull request without asking you again.",
	}
	BlessScoping = Disposition{
		Key: "s", Name: "Bless → Scoping", Status: "scoping", Bless: true,
		Gravity: "Scoping blesses research rather than building: a ticket sitting here " +
			"is one a dispatcher may spend a scout on unattended. The scout ends at " +
			"Needs Input, back on this screen, with the decision still yours.",
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
	// RejectPlan sends a Scoped ticket back to the pool. Demoting a plan is
	// as much a judgment as blessing one, and the reason exists for the same
	// reason Cancel's does: the next scope of this ticket reads it instead of
	// guessing why the last one did not land.
	RejectPlan = Disposition{
		Key: "b", Name: "Backlog, with reason", Status: "backlog", Field: FieldReason,
		Gravity: "Backlog is the pool. The reason is posted as a comment before the status " +
			"moves, because a rejected plan with no reason on it leaves the next scope of " +
			"this ticket guessing at what was wrong.",
	}
)

// judgments are the six Triage and Needs Input rows offer, in the order they
// are offered.
var judgments = []Disposition{BlessTodo, BlessScoping, ToBacklog, ToBacklogUnranked, AsDuplicate, Cancel}

// scopedJudgments are what a Scoped row offers: bless the plan into Todo, or
// reject it back to Backlog with the reasoning that made the call. Judging a
// plan is narrower than judging a raw ticket — there is no research left to
// authorize, and no title vague enough yet to be someone else's ticket — so
// it gets a shorter list rather than the full six.
var scopedJudgments = []Disposition{BlessTodo, RejectPlan}

// Dispositions returns what a human may do to this row.
//
// Triage and Needs Input get all six. Scoped gets the narrower pair: bless
// the plan, or reject it. Ready-for-human gets none, and that is the honest
// answer rather than a gap: the act that row is asking for is a review or a
// merge, which happens on the pull request — and the covenant's on-merge
// automation closes the ticket seconds later. A status write from here would
// be racing that automation to say the same thing.
//
// Lanes get none because a lane is not a ticket. Resolving one means going
// to the machine.
func Dispositions(r Row) []Disposition {
	switch r.Kind {
	case KindTriage, KindNeedsInput:
		return judgments
	case KindScoped:
		return scopedJudgments
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
	Issue    linear.Issue
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
