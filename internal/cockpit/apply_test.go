package cockpit

import (
	"context"
	"errors"
	"fmt"
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
			{ID: "st-scoping", Name: "Scoping", Type: "unstarted"},
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
		{name: "bless to Scoping", in: Intent{Issue: subject(), Disp: BlessScoping},
			want: "UpdateIssue uuid-WND-9 state=st-scoping"},
		{name: "backlog, ranked", in: Intent{Issue: subject(), Disp: ToBacklog, Priority: 2},
			want: "UpdateIssue uuid-WND-9 state=st-backlog priority=2"},
		{name: "backlog, unranked", in: Intent{Issue: subject(), Disp: ToBacklogUnranked},
			want: "UpdateIssue uuid-WND-9 state=st-backlog priority=0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := newFake()
			if err := Apply(context.Background(), cl, covenant.Default(), tt.in); err != nil {
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
	if err := Apply(context.Background(), cl, covenant.Default(),
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
	err := Apply(context.Background(), cl, covenant.Default(),
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
	err := Apply(context.Background(), cl, covenant.Default(),
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
	err := Apply(context.Background(), cl, covenant.Default(),
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

func TestDuplicateRefusesAnUnknownIssue(t *testing.T) {
	cl := newFake()
	err := Apply(context.Background(), cl, covenant.Default(),
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
			if err := Apply(context.Background(), cl, covenant.Default(), tt.in); err == nil {
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

	if err := Apply(context.Background(), cl, cov, Intent{Issue: subject(), Disp: BlessTodo}); err != nil {
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
