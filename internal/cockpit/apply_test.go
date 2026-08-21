package cockpit

import (
	"context"
	"errors"
	"fmt"
	"github.com/mattwalters/wand/internal/queue"
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
)

// fakeLinear records every call in order. The orderings in Apply are the
// point of that file — the reason comment lands before the close, the
// duplicate link before the status — so the test has to be able to see the
// sequence, not just the effects.
type fakeLinear struct {
	calls  []string
	states []linear.WorkflowState
	byID   map[string]linear.Issue

	failOn string // the first call whose name contains this fails
}

func newFake() *fakeLinear {
	return &fakeLinear{
		states: []linear.WorkflowState{
			{ID: "st-todo", Name: "Todo", Type: "unstarted"},
			{ID: "st-to-plan", Name: "To Plan", Type: "unstarted"},
			{ID: "st-backlog", Name: "Backlog", Type: "backlog"},
			{ID: "st-duplicate", Name: "Duplicate", Type: "duplicate"},
			{ID: "st-canceled", Name: "Canceled", Type: "canceled"},
		},
		byID: map[string]linear.Issue{
			"WND-3": {ID: "uuid-WND-3", Identifier: "WND-3", TeamID: "team"},
		},
	}
}

func (f *fakeLinear) record(call string) error {
	f.calls = append(f.calls, call)
	if f.failOn != "" && strings.Contains(call, f.failOn) {
		return errors.New("linear: refused " + call)
	}
	return nil
}

func (f *fakeLinear) TeamStates(context.Context, string) ([]linear.WorkflowState, error) {
	return f.states, f.record("TeamStates")
}

func (f *fakeLinear) IssueByIdentifier(_ context.Context, id string) (linear.Issue, error) {
	if err := f.record("IssueByIdentifier " + id); err != nil {
		return linear.Issue{}, err
	}
	issue, ok := f.byID[id]
	if !ok {
		return linear.Issue{}, fmt.Errorf("linear: no issue %q", id)
	}
	return issue, nil
}

func (f *fakeLinear) CreateComment(_ context.Context, issueID, body string) error {
	return f.record("CreateComment " + issueID + " " + body)
}

func (f *fakeLinear) CreateRelation(_ context.Context, issueID, relatedID, relType string) error {
	return f.record("CreateRelation " + issueID + " " + relType + " " + relatedID)
}

func (f *fakeLinear) UpdateIssue(_ context.Context, issueID string, u linear.IssueUpdate) error {
	call := "UpdateIssue " + issueID + " state=" + u.StateID
	if u.Priority != nil {
		call += fmt.Sprintf(" priority=%d", *u.Priority)
	}
	return f.record(call)
}

func (f *fakeLinear) TeamIssuesByState(context.Context, string, string) ([]linear.Issue, error) {
	return nil, f.record("TeamIssuesByState")
}

func (f *fakeLinear) TeamIssuesByStateType(context.Context, string, string) ([]linear.Issue, error) {
	return nil, f.record("TeamIssuesByStateType")
}

func (f *fakeLinear) TeamIssuesByLabel(context.Context, string, string) ([]linear.Issue, error) {
	return nil, f.record("TeamIssuesByLabel")
}

func (f *fakeLinear) LabelByName(_ context.Context, name string) (linear.Label, bool, error) {
	if err := f.record("LabelByName " + name); err != nil {
		return linear.Label{}, false, err
	}
	if name != "parked" {
		return linear.Label{}, false, nil
	}
	return linear.Label{ID: "label-parked", Name: "parked"}, true, nil
}

func (f *fakeLinear) RemoveLabel(_ context.Context, issueID, labelID string) error {
	return f.record("RemoveLabel " + issueID + " " + labelID)
}

func subject() linear.Issue {
	return linear.Issue{
		ID: "uuid-WND-9", Identifier: "WND-9", TeamID: "team",
		State: linear.IssueState{Name: "Triage", Type: "triage"},
	}
}

// The cockpit performs, on purpose, four transitions the guard refuses
// everywhere else. If any of these stops working, the human's half of the
// protocol has no surface at all.
func TestApplyPerformsTheTransitionsTheGuardForbids(t *testing.T) {
	tests := []struct {
		name string
		in   Intent
		want string
	}{
		{name: "bless to Todo", in: Intent{Issue: subject(), Disp: BlessTodo},
			want: "UpdateIssue uuid-WND-9 state=st-todo"},
		{name: "bless to To Plan", in: Intent{Issue: subject(), Disp: BlessToPlan},
			want: "UpdateIssue uuid-WND-9 state=st-to-plan"},
		{name: "backlog, ranked", in: Intent{Issue: subject(), Disp: ToBacklog, Priority: 2},
			want: "UpdateIssue uuid-WND-9 state=st-backlog priority=2"},
		{name: "backlog, unranked", in: Intent{Issue: subject(), Disp: ToBacklogUnranked},
			want: "UpdateIssue uuid-WND-9 state=st-backlog priority=0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := newFake()
			if _, err := Apply(context.Background(), cl, covenant.Default(), tt.in); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if last := cl.calls[len(cl.calls)-1]; last != tt.want {
				t.Errorf("last call = %q, want %q", last, tt.want)
			}
		})
	}
}

// An unranked Backlog move sends an explicit 0. Leaving the field alone
// would carry whatever priority the ticket had into the pool, which is the
// opposite of the judgment the disposition records.
func TestUnrankedBacklogClearsAnExistingPriority(t *testing.T) {
	issue := subject()
	issue.Priority = 1

	cl := newFake()
	if _, err := Apply(context.Background(), cl, covenant.Default(),
		Intent{Issue: issue, Disp: ToBacklogUnranked}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if last := cl.calls[len(cl.calls)-1]; !strings.HasSuffix(last, "priority=0") {
		t.Errorf("last call = %q, want an explicit priority=0", last)
	}
}

// The reason lands before the close. A crash between the two leaves an open
// ticket carrying its own argument for closure, which a person can finish;
// the reverse leaves a closed ticket nobody can explain.
func TestCancelCommentsBeforeItCloses(t *testing.T) {
	cl := newFake()
	_, err := Apply(context.Background(), cl, covenant.Default(),
		Intent{Issue: subject(), Disp: Cancel, Text: "  superseded by WND-3  "})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	comment, update := indexOf(cl.calls, "CreateComment"), indexOf(cl.calls, "UpdateIssue")
	if comment < 0 || update < 0 || comment > update {
		t.Fatalf("calls = %v, want the comment before the status move", cl.calls)
	}
	if got := cl.calls[comment]; !strings.HasSuffix(got, "superseded by WND-3") {
		t.Errorf("comment = %q, want the trimmed reason", got)
	}
}

// A refused comment must leave the status alone: the whole point of writing
// it first is that the failure mode is a ticket still open.
func TestCancelDoesNotCloseWhenTheCommentFails(t *testing.T) {
	cl := newFake()
	cl.failOn = "CreateComment"
	_, err := Apply(context.Background(), cl, covenant.Default(),
		Intent{Issue: subject(), Disp: Cancel, Text: "obsolete"})
	if err == nil {
		t.Fatal("Apply succeeded, want the comment failure to stop it")
	}
	if i := indexOf(cl.calls, "UpdateIssue"); i >= 0 {
		t.Errorf("calls = %v, want no status write after the comment failed", cl.calls)
	}
}

// Same shape one disposition over: a Duplicate status that names nothing is
// a dead end.
func TestDuplicateLinksBeforeItCloses(t *testing.T) {
	cl := newFake()
	_, err := Apply(context.Background(), cl, covenant.Default(),
		Intent{Issue: subject(), Disp: AsDuplicate, Text: "WND-3"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	relation, update := indexOf(cl.calls, "CreateRelation"), indexOf(cl.calls, "UpdateIssue")
	if relation < 0 || update < 0 || relation > update {
		t.Fatalf("calls = %v, want the relation before the status move", cl.calls)
	}
	want := "CreateRelation uuid-WND-9 duplicate uuid-WND-3"
	if got := cl.calls[relation]; got != want {
		t.Errorf("relation = %q, want %q — the duplicate is the subject, not the canonical", got, want)
	}
}

// Same shape as Cancel, one disposition over and a different destination: a
// rejected plan with no reason on it leaves the next plan run guessing.
func TestRejectPlanCommentsBeforeItMovesToBacklog(t *testing.T) {
	cl := newFake()
	_, err := Apply(context.Background(), cl, covenant.Default(),
		Intent{Issue: subject(), Disp: RejectPlan, Text: "  the estimate assumed a library that does not exist  "})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	comment, update := indexOf(cl.calls, "CreateComment"), indexOf(cl.calls, "UpdateIssue")
	if comment < 0 || update < 0 || comment > update {
		t.Fatalf("calls = %v, want the comment before the status move", cl.calls)
	}
	if got := cl.calls[comment]; !strings.HasSuffix(got, "the estimate assumed a library that does not exist") {
		t.Errorf("comment = %q, want the trimmed reason", got)
	}
	if last := cl.calls[len(cl.calls)-1]; last != "UpdateIssue uuid-WND-9 state=st-backlog" {
		t.Errorf("last call = %q, want the plain backlog move with no priority touched", last)
	}
}

func TestDuplicateRefusesAnUnknownIssue(t *testing.T) {
	cl := newFake()
	_, err := Apply(context.Background(), cl, covenant.Default(),
		Intent{Issue: subject(), Disp: AsDuplicate, Text: "WND-404"})
	if err == nil {
		t.Fatal("Apply succeeded, want a refusal for an issue that does not exist")
	}
	if i := indexOf(cl.calls, "UpdateIssue"); i >= 0 {
		t.Errorf("calls = %v, want nothing written", cl.calls)
	}
}

// Every refusal that can be reached without writing must be reached without
// writing: a drifted board stops the act with nothing left behind.
func TestApplyChecksBeforeItWrites(t *testing.T) {
	tests := []struct {
		name string
		in   Intent
		prep func(*fakeLinear)
	}{
		{name: "an incomplete intent", in: Intent{Issue: subject(), Disp: Cancel}},
		{
			name: "a board missing the status",
			in:   Intent{Issue: subject(), Disp: BlessTodo},
			prep: func(f *fakeLinear) { f.states = f.states[1:] },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := newFake()
			if tt.prep != nil {
				tt.prep(cl)
			}
			if _, err := Apply(context.Background(), cl, covenant.Default(), tt.in); err == nil {
				t.Fatal("Apply succeeded, want a refusal")
			}
			for _, call := range cl.calls {
				if strings.HasPrefix(call, "UpdateIssue") || strings.HasPrefix(call, "CreateComment") {
					t.Errorf("wrote %q before refusing; calls = %v", call, cl.calls)
				}
			}
		})
	}
}

// A covenant that renames the blessed column must be honoured: the cockpit
// refers to statuses by what they mean, not by what a stock board calls them.
func TestApplyFollowsACovenantRename(t *testing.T) {
	cov := covenant.Default()
	for i := range cov.Statuses {
		if cov.Statuses[i].Key == "todo" {
			cov.Statuses[i].Name = "Ready"
		}
	}

	cl := newFake()
	cl.states = append(cl.states, linear.WorkflowState{ID: "st-ready", Name: "Ready", Type: "unstarted"})

	if _, err := Apply(context.Background(), cl, cov, Intent{Issue: subject(), Disp: BlessTodo}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if last := cl.calls[len(cl.calls)-1]; !strings.HasSuffix(last, "state=st-ready") {
		t.Errorf("last call = %q, want the renamed status", last)
	}
}

func indexOf(calls []string, prefix string) int {
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

// The retry is the dangerous path, not the safe one. Both of these
// dispositions write twice, the first write is not undoable, and the screen
// is built to let a person press enter again after a failure. Running Apply
// from the top a second time would post the same cancellation reason twice,
// and would re-issue a relation Linear already holds — which it refuses,
// leaving the duplicate permanently uncompletable through the one screen
// that can complete it.
func TestApplyRetryDoesNotRepeatThePreWrite(t *testing.T) {
	tests := []struct {
		name string
		in   Intent
		// pre is the call the first attempt must make and the retry must
		// not; move is the status write both attempts make.
		pre  string
		move string
	}{
		{
			name: "cancel does not post the reason twice",
			in:   Intent{Issue: subject(), Disp: Cancel, Text: "superseded by WND-3"},
			pre:  "CreateComment uuid-WND-9 superseded by WND-3",
			move: "UpdateIssue uuid-WND-9 state=st-canceled",
		},
		{
			name: "duplicate does not re-issue the relation",
			in:   Intent{Issue: subject(), Disp: AsDuplicate, Text: "WND-3"},
			pre:  "CreateRelation uuid-WND-9 duplicate uuid-WND-3",
			move: "UpdateIssue uuid-WND-9 state=st-duplicate",
		},
		{
			name: "a rejected plan does not post its reason twice",
			in:   Intent{Issue: subject(), Disp: RejectPlan, Text: "wrong approach"},
			pre:  "CreateComment uuid-WND-9 wrong approach",
			move: "UpdateIssue uuid-WND-9 state=st-backlog",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// First attempt: the pre-write lands, the status move fails.
			cl := newFake()
			cl.failOn = "UpdateIssue"
			out, err := Apply(context.Background(), cl, covenant.Default(), tt.in)
			if err == nil {
				t.Fatal("Apply succeeded; the fake was told to refuse the status move")
			}
			if !contains(cl.calls, tt.pre) {
				t.Fatalf("the first attempt did not make the pre-write %q; calls were %v", tt.pre, cl.calls)
			}
			if !out.Done.PreWritten {
				t.Fatal("Apply reported no progress after the pre-write landed; " +
					"the retry has no way to know what it must not do again")
			}

			// The retry, from the returned intent, as the screen does it.
			cl = newFake()
			if _, err := Apply(context.Background(), cl, covenant.Default(), out); err != nil {
				t.Fatalf("the retry failed: %v", err)
			}
			if contains(cl.calls, tt.pre) {
				t.Errorf("the retry repeated the pre-write %q; calls were %v", tt.pre, cl.calls)
			}
			if !contains(cl.calls, tt.move) {
				t.Errorf("the retry did not make the status move %q; calls were %v", tt.move, cl.calls)
			}
		})
	}
}

// A pre-write that never happened must not be skipped. The progress flag
// says "already on the ticket", and the failure it guards against is the
// mirror of the one above: a cancellation that closes the ticket without
// ever recording why.
func TestApplyStillPreWritesWhenNothingHasLanded(t *testing.T) {
	cl := newFake()
	in := Intent{Issue: subject(), Disp: Cancel, Text: "no longer wanted"}
	out, err := Apply(context.Background(), cl, covenant.Default(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !contains(cl.calls, "CreateComment uuid-WND-9 no longer wanted") {
		t.Errorf("the reason was not posted; calls were %v", cl.calls)
	}
	if !out.Done.PreWritten {
		t.Error("a landed pre-write was not recorded on the returned intent")
	}
}

// ClearParked is the write ReportPark's own comment instructs and, before
// this, nothing in wand could perform. It looks the ticket up by identifier
// — a lane carries no UUID — resolves the label, then removes it.
func TestApplyClearsTheParkedLabel(t *testing.T) {
	cl := newFake()
	// The ticket has to actually carry the label, or this passes for the
	// wrong reason: clearing is a no-op on an issue that does not.
	issue := cl.byID["WND-3"]
	issue.Labels = append(issue.Labels, queue.ParkedLabel)
	cl.byID["WND-3"] = issue

	_, err := Apply(context.Background(), cl, covenant.Default(),
		Intent{Lane: Lane{Ticket: "WND-3"}, Disp: ClearParked})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := "RemoveLabel uuid-WND-3 label-parked"
	if last := cl.calls[len(cl.calls)-1]; last != want {
		t.Errorf("last call = %q, want %q", last, want)
	}
}

// WND-85. Clearing a park that is already cleared is success, not failure.
// Linear refuses to remove a label an issue does not carry, and until the
// lane derived from the label a person who cleared a park saw it come
// straight back — so the second click landed on a hard error for having
// done the right thing the first time.
func TestApplyClearingAnAlreadyClearedParkIsANoOp(t *testing.T) {
	cl := newFake() // WND-3 carries no parked label

	_, err := Apply(context.Background(), cl, covenant.Default(),
		Intent{Lane: Lane{Ticket: "WND-3"}, Disp: ClearParked})
	if err != nil {
		t.Fatalf("Apply: %v, want silence on an already-cleared park", err)
	}
	for _, c := range cl.calls {
		if strings.HasPrefix(c, "RemoveLabel") {
			t.Errorf("calls = %v, want no write when there is nothing to remove", cl.calls)
		}
	}
}

// A lane row's identity is its ticket. Nothing selected must refuse before
// touching the network, the same as every other disposition's missing field.
func TestApplyRefusesToClearWithNoLaneSelected(t *testing.T) {
	cl := newFake()
	if _, err := Apply(context.Background(), cl, covenant.Default(), Intent{Disp: ClearParked}); err == nil {
		t.Fatal("Apply succeeded, want a refusal for no lane selected")
	}
	if len(cl.calls) != 0 {
		t.Errorf("calls = %v, want nothing written", cl.calls)
	}
}

func TestApplyClearParkRefusesAnUnknownTicket(t *testing.T) {
	cl := newFake()
	_, err := Apply(context.Background(), cl, covenant.Default(),
		Intent{Lane: Lane{Ticket: "WND-404"}, Disp: ClearParked})
	if err == nil {
		t.Fatal("Apply succeeded, want a refusal for a ticket that does not exist")
	}
	if i := indexOf(cl.calls, "RemoveLabel"); i >= 0 {
		t.Errorf("calls = %v, want nothing written", cl.calls)
	}
}

func contains(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}
