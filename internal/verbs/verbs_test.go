package verbs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/queue"
)

// fake is a Linear that records the order of its writes. The ordering rules
// are what this package exists to encode, so the calls slice is the assertion
// surface: a regression that swaps comment and status still passes every
// "did it happen" check and fails only here.
type fake struct {
	issue  linear.Issue
	states []linear.WorkflowState
	viewer linear.User
	team   linear.Team
	labels []linear.Label
	search []linear.Issue

	commentErr error
	labelErr   error
	addErr     error
	removeErr  error

	calls    []string
	comments []string
	added    []string
	removed  []string
	updates  []linear.IssueUpdate
	creates  []linear.IssueCreate
}

func (f *fake) IssueByIdentifier(ctx context.Context, identifier string) (linear.Issue, error) {
	f.calls = append(f.calls, "read")
	return f.issue, nil
}

func (f *fake) TeamStates(ctx context.Context, teamID string) ([]linear.WorkflowState, error) {
	return f.states, nil
}

func (f *fake) Viewer(ctx context.Context) (linear.User, error) {
	return f.viewer, nil
}

func (f *fake) CreateComment(ctx context.Context, issueID, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.commentErr != nil {
		return f.commentErr
	}
	f.calls = append(f.calls, "comment")
	f.comments = append(f.comments, body)
	return nil
}

func (f *fake) UpdateIssue(ctx context.Context, issueID string, u linear.IssueUpdate) error {
	f.calls = append(f.calls, "update")
	f.updates = append(f.updates, u)
	return nil
}

func (f *fake) TeamByKey(ctx context.Context, key string) (linear.Team, error) {
	return f.team, nil
}

func (f *fake) AddLabel(ctx context.Context, issueID, labelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.addErr != nil {
		return f.addErr
	}
	f.calls = append(f.calls, "label")
	f.added = append(f.added, labelID)
	return nil
}

func (f *fake) RemoveLabel(ctx context.Context, issueID, labelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.removeErr != nil {
		return f.removeErr
	}
	f.calls = append(f.calls, "unlabel")
	f.removed = append(f.removed, labelID)
	return nil
}

func (f *fake) LabelByName(ctx context.Context, name string) (linear.Label, bool, error) {
	if f.labelErr != nil {
		return linear.Label{}, false, f.labelErr
	}
	for _, l := range f.labels {
		if strings.EqualFold(l.Name, name) {
			return l, true, nil
		}
	}
	return linear.Label{}, false, nil
}

func (f *fake) CreateIssue(ctx context.Context, in linear.IssueCreate) (linear.Issue, error) {
	f.calls = append(f.calls, "create")
	f.creates = append(f.creates, in)
	return linear.Issue{ID: "new", Identifier: "WND-99", Title: in.Title}, nil
}

func (f *fake) SearchIssues(ctx context.Context, teamKey, term string) ([]linear.Issue, error) {
	f.calls = append(f.calls, "search")
	return f.search, nil
}

func (f *fake) writes() []string {
	var w []string
	for _, c := range f.calls {
		if c != "read" && c != "search" {
			w = append(w, c)
		}
	}
	return w
}

// wndStates is a board satisfying the stock covenant, ids distinct from names
// so a test that passes a name where an id belongs fails loudly.
var wndStates = []linear.WorkflowState{
	{ID: "st-triage", Name: "Triage", Type: "triage"},
	{ID: "st-backlog", Name: "Backlog", Type: "backlog"},
	{ID: "st-todo", Name: "Todo", Type: "unstarted"},
	{ID: "st-to-plan", Name: "To Plan", Type: "unstarted"},
	{ID: "st-in-planning", Name: "In Planning", Type: "started"},
	{ID: "st-needs-input", Name: "Needs Input", Type: "unstarted"},
	{ID: "st-in-progress", Name: "In Progress", Type: "started"},
}

func todoIssue() linear.Issue {
	return linear.Issue{
		ID:         "uuid-6",
		Identifier: "WND-6",
		Title:      "Lifecycle verbs",
		TeamID:     "team-1",
		BranchName: "matt/wnd-6-lifecycle-verbs",
		State:      linear.IssueState{Name: "Todo", Type: "unstarted"},
	}
}

func toPlanIssue() linear.Issue {
	return linear.Issue{
		ID:         "uuid-6",
		Identifier: "WND-6",
		Title:      "Lifecycle verbs",
		TeamID:     "team-1",
		State:      linear.IssueState{Name: "To Plan", Type: "unstarted"},
	}
}

// noVet allows every issue; the cases that need a refusal build their own.
func noVet(linear.Issue) string { return "" }

func TestClaimWritesStateAndAssigneeTogether(t *testing.T) {
	f := &fake{issue: todoIssue(), states: wndStates, viewer: linear.User{ID: "u1", Name: "Matt"}}

	claimed, err := Claim(context.Background(), f, covenant.Default(), "WND-6")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got := f.writes(); len(got) != 1 || got[0] != "update" {
		t.Fatalf("writes = %v, want exactly one update: claim is a single write, no comment", got)
	}
	u := f.updates[0]
	// One write carrying both: status without assignee reads as taken but
	// owned by nobody.
	if u.StateID != "st-in-progress" || u.AssigneeID != "u1" {
		t.Errorf("update = %+v, want In Progress + viewer in the same write", u)
	}
	if claimed.Assignee != "Matt" {
		t.Errorf("assignee = %q, want the viewer's name", claimed.Assignee)
	}
	if claimed.Issue.BranchName != "matt/wnd-6-lifecycle-verbs" {
		t.Errorf("claim result must carry the branch name; the session branches from it")
	}
}

func TestClaimRefusesOutsideTodo(t *testing.T) {
	issue := todoIssue()
	issue.State = linear.IssueState{Name: "In Progress", Type: "started"}
	f := &fake{issue: issue, states: wndStates, viewer: linear.User{ID: "u1"}}

	_, err := Claim(context.Background(), f, covenant.Default(), "WND-6")
	if err == nil || !strings.Contains(err.Error(), "In Progress") {
		t.Fatalf("err = %v, want a refusal naming the actual status", err)
	}
	if w := f.writes(); len(w) != 0 {
		t.Errorf("writes = %v, want none on refusal", w)
	}
}

func TestClaimRefusesVettedIssues(t *testing.T) {
	cases := map[string]func(*linear.Issue){
		"human-only": func(i *linear.Issue) { i.Labels = []string{"human-only"} },
		// Claim refuses for exactly the reasons the queue skips, so a
		// ticket a previous run parked cannot be taken by hand either
		// until someone clears the label.
		"parked": func(i *linear.Issue) { i.Labels = []string{queue.ParkedLabel} },
		"blocked": func(i *linear.Issue) {
			i.BlockedBy = []linear.Blocker{{
				Identifier: "WND-4",
				// Started very much included: "it's already In Progress" is
				// precisely the race blocked-by exists to prevent.
				State: linear.IssueState{Name: "In Progress", Type: "started"},
			}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			issue := todoIssue()
			mutate(&issue)
			f := &fake{issue: issue, states: wndStates, viewer: linear.User{ID: "u1"}}
			_, err := Claim(context.Background(), f, covenant.Default(), "WND-6")
			if err == nil {
				t.Fatal("claim of a vetted issue must refuse")
			}
			if w := f.writes(); len(w) != 0 {
				t.Errorf("writes = %v, want none on refusal", w)
			}
		})
	}
}

func TestClaimRespectsCovenantRenames(t *testing.T) {
	cov := covenant.Default()
	for i, s := range cov.Statuses {
		if s.Key == "todo" {
			cov.Statuses[i].Name = "Ready"
		}
	}
	issue := todoIssue()
	issue.State = linear.IssueState{Name: "Ready", Type: "unstarted"}
	f := &fake{
		issue:  issue,
		states: append([]linear.WorkflowState{{ID: "st-ready", Name: "Ready", Type: "unstarted"}}, wndStates...),
		viewer: linear.User{ID: "u1", Name: "Matt"},
	}
	if _, err := Claim(context.Background(), f, cov, "WND-6"); err != nil {
		t.Fatalf("claim from a renamed blessed column: %v", err)
	}
}

func TestClaimForPlanningWritesStateAloneNoAssignee(t *testing.T) {
	f := &fake{issue: toPlanIssue(), states: wndStates}

	issue, err := ClaimForPlanning(context.Background(), f, covenant.Default(), "WND-6", noVet)
	if err != nil {
		t.Fatalf("ClaimForPlanning: %v", err)
	}
	if got := f.writes(); len(got) != 1 || got[0] != "update" {
		t.Fatalf("writes = %v, want exactly one update: the claim is a single write, no comment", got)
	}
	u := f.updates[0]
	if u.StateID != "st-in-planning" {
		t.Errorf("update = %+v, want In Planning", u)
	}
	if u.AssigneeID != "" {
		t.Errorf("update assigned %q; a plan run is read-only research nobody owns the way a build owns a branch", u.AssigneeID)
	}
	if issue.Identifier != "WND-6" {
		t.Errorf("claim result = %+v, want the issue as read", issue)
	}
}

func TestClaimForPlanningRefusesOutsideToPlan(t *testing.T) {
	issue := toPlanIssue()
	issue.State = linear.IssueState{Name: "In Planning", Type: "started"}
	f := &fake{issue: issue, states: wndStates}

	_, err := ClaimForPlanning(context.Background(), f, covenant.Default(), "WND-6", noVet)
	if err == nil || !strings.Contains(err.Error(), "not yours to plan") {
		t.Fatalf("err = %v, want a refusal naming that it is not yours to plan", err)
	}
	if w := f.writes(); len(w) != 0 {
		t.Errorf("writes = %v, want none on refusal", w)
	}
}

func TestClaimForPlanningRefusesWhatVetRejects(t *testing.T) {
	f := &fake{issue: toPlanIssue(), states: wndStates}
	rejectAll := func(linear.Issue) string { return "labeled human-only" }

	_, err := ClaimForPlanning(context.Background(), f, covenant.Default(), "WND-6", rejectAll)
	if err == nil || !strings.Contains(err.Error(), "human-only") {
		t.Fatalf("err = %v, want the vet's reason", err)
	}
	if w := f.writes(); len(w) != 0 {
		t.Errorf("writes = %v, want none on refusal", w)
	}
}

func inPlanningIssue() linear.Issue {
	return linear.Issue{
		ID:         "uuid-6",
		Identifier: "WND-6",
		Title:      "Lifecycle verbs",
		TeamID:     "team-1",
		State:      linear.IssueState{Name: "In Planning", Type: "started"},
		Labels:     []string{RePlanLabel},
	}
}

func TestReturnToPlanningCommentsBeforeStatus(t *testing.T) {
	f := &fake{issue: toPlanIssue(), states: wndStates}

	_, err := ReturnToPlanning(context.Background(), f, covenant.Default(), "WND-6",
		"labeled re-plan: a human asked for another cycle")
	if err != nil {
		t.Fatalf("ReturnToPlanning: %v", err)
	}
	want := []string{"comment", "update"}
	got := f.writes()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("writes = %v, want %v: the comment lands before In Planning", got, want)
	}
	if f.updates[0].StateID != "st-in-planning" {
		t.Errorf("state = %q, want In Planning's id", f.updates[0].StateID)
	}
}

func TestReturnToPlanningNeedsAComment(t *testing.T) {
	f := &fake{issue: toPlanIssue(), states: wndStates}

	_, err := ReturnToPlanning(context.Background(), f, covenant.Default(), "WND-6", "  ")
	if err == nil {
		t.Fatal("ReturnToPlanning with a blank comment must refuse")
	}
	if w := f.writes(); len(w) != 0 {
		t.Errorf("writes = %v, want none on refusal", w)
	}
}

func TestClaimForReplanningRemovesTheLabel(t *testing.T) {
	f := &fake{issue: inPlanningIssue(), states: wndStates, labels: []linear.Label{{ID: "l-re-plan", Name: RePlanLabel}}}

	issue, err := ClaimForReplanning(context.Background(), f, covenant.Default(), "WND-6", noVet)
	if err != nil {
		t.Fatalf("ClaimForReplanning: %v", err)
	}
	if got := f.writes(); len(got) != 1 || got[0] != "unlabel" {
		t.Fatalf("writes = %v, want exactly one label removal: there is no status to flip, the ticket is already In Planning", got)
	}
	if len(f.removed) != 1 || f.removed[0] != "l-re-plan" {
		t.Errorf("removed = %v, want the re-plan label's id", f.removed)
	}
	if issue.Identifier != "WND-6" {
		t.Errorf("claim result = %+v, want the issue as read", issue)
	}
}

func TestClaimForReplanningRefusesOutsideInPlanning(t *testing.T) {
	issue := inPlanningIssue()
	issue.State = linear.IssueState{Name: "Plan Review", Type: "unstarted"}
	f := &fake{issue: issue, states: wndStates}

	_, err := ClaimForReplanning(context.Background(), f, covenant.Default(), "WND-6", noVet)
	if err == nil || !strings.Contains(err.Error(), "In Planning") {
		t.Fatalf("err = %v, want a refusal naming In Planning", err)
	}
	if w := f.writes(); len(w) != 0 {
		t.Errorf("writes = %v, want none on refusal", w)
	}
}

// A ticket In Planning with no re-plan label is exactly what a live plan
// run's claim leaves behind — taking it as a re-plan too would let two
// plan runs write over the same ticket at once, the invariant
// ClaimForPlanning's own status flip protects on the fresh-plan side.
func TestClaimForReplanningRefusesWithoutTheLabel(t *testing.T) {
	issue := inPlanningIssue()
	issue.Labels = nil
	f := &fake{issue: issue, states: wndStates}

	_, err := ClaimForReplanning(context.Background(), f, covenant.Default(), "WND-6", noVet)
	if err == nil || !strings.Contains(err.Error(), "re-plan") {
		t.Fatalf("err = %v, want a refusal naming the missing label", err)
	}
	if w := f.writes(); len(w) != 0 {
		t.Errorf("writes = %v, want none on refusal", w)
	}
}

func TestClaimForReplanningRefusesWhatVetRejects(t *testing.T) {
	f := &fake{issue: inPlanningIssue(), states: wndStates, labels: []linear.Label{{ID: "l-re-plan", Name: RePlanLabel}}}
	rejectAll := func(linear.Issue) string { return "labeled human-only" }

	_, err := ClaimForReplanning(context.Background(), f, covenant.Default(), "WND-6", rejectAll)
	if err == nil || !strings.Contains(err.Error(), "human-only") {
		t.Fatalf("err = %v, want the vet's reason", err)
	}
	if w := f.writes(); len(w) != 0 {
		t.Errorf("writes = %v, want none on refusal", w)
	}
}

func TestHandbackCommentsBeforeStatus(t *testing.T) {
	f := &fake{issue: todoIssue(), states: wndStates}

	_, err := Handback(context.Background(), f, covenant.Default(), "WND-6",
		"Which auth flow? Options: A, B. I would pick A.")
	if err != nil {
		t.Fatalf("handback: %v", err)
	}
	want := []string{"comment", "update"}
	got := f.writes()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("writes = %v, want %v: the question lands before Needs Input", got, want)
	}
	if f.updates[0].StateID != "st-needs-input" {
		t.Errorf("state = %q, want Needs Input's id", f.updates[0].StateID)
	}
}

func TestHandbackFailedCommentStopsTheStatusMove(t *testing.T) {
	f := &fake{issue: todoIssue(), states: wndStates, commentErr: errors.New("api down")}

	_, err := Handback(context.Background(), f, covenant.Default(), "WND-6", "a question")
	if err == nil {
		t.Fatal("want the comment failure surfaced")
	}
	// The failure mode this ordering exists for: a Needs Input ticket with
	// no question on it parks forever.
	if len(f.updates) != 0 {
		t.Errorf("status was moved after the comment failed: %+v", f.updates)
	}
}

// The guard judges only the destination, so the verbs must gate the source:
// a Done or Canceled ticket refuses, or an abandon/handback with stale
// context silently undoes a human's close.
func TestHandbackAndAbandonRefuseClosedTickets(t *testing.T) {
	for name, state := range map[string]linear.IssueState{
		"done":     {Name: "Done", Type: "completed"},
		"canceled": {Name: "Canceled", Type: "canceled"},
	} {
		t.Run(name, func(t *testing.T) {
			issue := todoIssue()
			issue.State = state
			f := &fake{issue: issue, states: wndStates}
			if _, err := Handback(context.Background(), f, covenant.Default(), "WND-6", "a question"); err == nil {
				t.Error("handback of a closed ticket must refuse")
			}
			if _, err := Abandon(context.Background(), f, covenant.Default(), "WND-6", "evidence", nil); err == nil {
				t.Error("abandon of a closed ticket must refuse")
			}
			if w := f.writes(); len(w) != 0 {
				t.Errorf("writes = %v, want none: reopening a close is a human's call", w)
			}
		})
	}
}

// A drifted board (or a guard refusal) must stop the verb before the
// comment: a failure after it would leave a comment promising a correction
// that never happened, and a repaired re-run would post the question twice.
func TestHandbackAndAbandonResolveTheStateBeforeCommenting(t *testing.T) {
	f := &fake{issue: todoIssue()} // no states: every ResolveState fails as drift
	if _, err := Handback(context.Background(), f, covenant.Default(), "WND-6", "a question"); err == nil {
		t.Fatal("want the drift surfaced")
	}
	if _, err := Abandon(context.Background(), f, covenant.Default(), "WND-6", "evidence", nil); err == nil {
		t.Fatal("want the drift surfaced")
	}
	if len(f.comments) != 0 {
		t.Errorf("comments = %q, want none: the refusal must come before any write", f.comments)
	}
}

func TestHandbackRefusesAnEmptyQuestion(t *testing.T) {
	f := &fake{issue: todoIssue(), states: wndStates}
	if _, err := Handback(context.Background(), f, covenant.Default(), "WND-6", "  \n "); err == nil {
		t.Fatal("a hand-back with no question must refuse")
	}
	if w := f.writes(); len(w) != 0 {
		t.Errorf("writes = %v, want none", w)
	}
}

func TestAbandonEvidenceFirstThenOneWrite(t *testing.T) {
	issue := todoIssue()
	issue.Description = "Fix the flaky login test; it fails on every third run."
	f := &fake{issue: issue, states: wndStates}

	_, err := Abandon(context.Background(), f, covenant.Default(), "WND-6",
		"Ran the test 200 times on main: zero failures. The flake was fixed by #42.",
		&Correction{
			Old: "it fails on every third run",
			New: "it no longer fails (see the abandon comment)",
		})
	if err != nil {
		t.Fatalf("abandon: %v", err)
	}

	want := []string{"comment", "update"}
	got := f.writes()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("writes = %v, want %v: evidence lands before the demotion", got, want)
	}

	// Description, status and assignee travel in ONE write: a Backlog ticket
	// still asserting the disproven premise would be re-blessed on the
	// strength of it.
	u := f.updates[0]
	if u.StateID != "st-backlog" {
		t.Errorf("state = %q, want Backlog's id", u.StateID)
	}
	if !u.Unassign {
		t.Error("abandon must unassign: the ticket returns to the pool owned by nobody")
	}
	if u.Description == nil || !strings.Contains(*u.Description, "no longer fails") {
		t.Errorf("description not corrected in the same write: %+v", u)
	}

	// The comment preserves the old wording: Linear's description history is
	// where corrections go to be forgotten.
	comment := f.comments[0]
	if !strings.Contains(comment, "> it fails on every third run") {
		t.Errorf("comment does not quote the old wording:\n%s", comment)
	}
	if !strings.Contains(comment, "zero failures") {
		t.Errorf("comment lost the evidence:\n%s", comment)
	}
}

func TestAbandonRefusesAnAnchorThatDoesNotPin(t *testing.T) {
	issue := todoIssue()
	issue.Description = "the test fails. the test fails."
	f := &fake{issue: issue, states: wndStates}

	for name, corr := range map[string]Correction{
		"absent":    {Old: "wording nobody wrote", New: "x"},
		"ambiguous": {Old: "the test fails.", New: "x"},
	} {
		t.Run(name, func(t *testing.T) {
			f.calls, f.comments, f.updates = nil, nil, nil
			c := corr
			_, err := Abandon(context.Background(), f, covenant.Default(), "WND-6", "evidence", &c)
			if err == nil {
				t.Fatal("want a refusal")
			}
			if w := f.writes(); len(w) != 0 {
				t.Errorf("writes = %v, want none: the refusal must come before any write", w)
			}
		})
	}
}

func TestAbandonWithoutCorrectionLeavesTheDescriptionAlone(t *testing.T) {
	f := &fake{issue: todoIssue(), states: wndStates}
	_, err := Abandon(context.Background(), f, covenant.Default(), "WND-6", "the premise predates the redesign", nil)
	if err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if u := f.updates[0]; u.Description != nil {
		t.Errorf("no correction was asked for, but the description was written: %q", *u.Description)
	}
}

func TestAbandonRefusesEmptyEvidence(t *testing.T) {
	f := &fake{issue: todoIssue(), states: wndStates}
	if _, err := Abandon(context.Background(), f, covenant.Default(), "WND-6", "", nil); err == nil {
		t.Fatal("an abandon with no evidence must refuse")
	}
	if w := f.writes(); len(w) != 0 {
		t.Errorf("writes = %v, want none", w)
	}
}

func TestFileRefusesWhenTheSearchFindsCandidates(t *testing.T) {
	f := &fake{
		team:   linear.Team{ID: "team-1", Key: "WND"},
		states: wndStates,
		labels: []linear.Label{{ID: "lb-agent", Name: "agent-filed"}},
		search: []linear.Issue{{Identifier: "WND-2", Title: "Flaky login test"}},
	}
	res, err := File(context.Background(), f, covenant.Default(), FileRequest{
		TeamKey: "WND", Title: "Login test is flaky",
	})
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	if res.Created != nil {
		t.Fatal("filed despite candidates: search-first is the rule")
	}
	if len(res.Duplicates) != 1 || res.Duplicates[0].Identifier != "WND-2" {
		t.Errorf("duplicates = %+v, want the search hit surfaced", res.Duplicates)
	}
	if len(f.creates) != 0 {
		t.Errorf("createIssue was called: %+v", f.creates)
	}
}

func TestFileForceFilesPastCandidates(t *testing.T) {
	f := &fake{
		team:   linear.Team{ID: "team-1", Key: "WND"},
		states: wndStates,
		labels: []linear.Label{{ID: "lb-agent", Name: "agent-filed"}},
		search: []linear.Issue{{Identifier: "WND-2"}},
	}
	res, err := File(context.Background(), f, covenant.Default(), FileRequest{
		TeamKey: "WND", Title: "Login test is flaky", Description: "seen twice in CI", Force: true,
	})
	if err != nil {
		t.Fatalf("file --force: %v", err)
	}
	if res.Created == nil {
		t.Fatal("force did not file")
	}
	in := f.creates[0]
	if in.StateID != "st-triage" {
		t.Errorf("state = %q, want Triage's id: an agent files into the inbox, never past it", in.StateID)
	}
	if len(in.LabelIDs) != 1 || in.LabelIDs[0] != "lb-agent" {
		t.Errorf("labels = %v, want the agent-filed label", in.LabelIDs)
	}
	// No priority and no assignee fields exist on IssueCreate at all —
	// ranking and owning are part of blessing. This asserts the search still
	// ran: force skips the refusal, not the looking.
	if got := f.calls[0]; got != "search" {
		t.Errorf("first call = %q, want search even under force", got)
	}
}

func TestFileMissingLabelNamesTheRepair(t *testing.T) {
	f := &fake{
		team:   linear.Team{ID: "team-1", Key: "WND"},
		states: wndStates,
	}
	_, err := File(context.Background(), f, covenant.Default(), FileRequest{TeamKey: "WND", Title: "x", Force: true})
	if err == nil || !strings.Contains(err.Error(), "wand init") {
		t.Fatalf("err = %v, want the missing label to name wand init as the repair", err)
	}
}

func TestResolveStateRoutesThroughTheGuard(t *testing.T) {
	// No verb writes a forbidden status, so force the seam directly: a
	// covenant whose needs_input display name collides with a closing
	// status must refuse, or a rename could smuggle a close past the guard.
	cov := covenant.Default()
	for i, s := range cov.Statuses {
		if s.Key == "needs_input" {
			cov.Statuses[i].Name = "Done"
		}
	}
	f := &fake{issue: todoIssue(), states: wndStates}
	_, err := Handback(context.Background(), f, cov, "WND-6", "a question")
	if err == nil || !strings.Contains(err.Error(), "human") {
		t.Fatalf("err = %v, want the guard's refusal", err)
	}
	if len(f.updates) != 0 {
		t.Errorf("status was written past the guard: %+v", f.updates)
	}
}

// parkedLabels is a board that already carries the covenant's parked label.
func parkedLabels() []linear.Label {
	return []linear.Label{{ID: "lbl-parked", Name: queue.ParkedLabel}}
}

// The ordering rule, stated the way Handback's is: the explanation lands
// before the mark. A ticket marked parked with nothing saying why is the
// same shape as a Needs Input ticket that asks no question.
func TestReportParkCommentsBeforeLabeling(t *testing.T) {
	f := &fake{labels: parkedLabels()}
	var out strings.Builder

	ReportPark(context.Background(), f, &out, "issue-1", "the implement worker timed out after 30m0s")

	if got := strings.Join(f.calls, ","); got != "comment,label" {
		t.Fatalf("calls = %q, want %q", got, "comment,label")
	}
	if len(f.added) != 1 || f.added[0] != "lbl-parked" {
		t.Errorf("added = %v, want the parked label's id", f.added)
	}
	if body := f.comments[0]; !strings.Contains(body, "timed out after 30m0s") {
		t.Errorf("the report does not quote the reason:\n%s", body)
	}
	if body := f.comments[0]; !strings.Contains(body, queue.ParkedLabel) {
		t.Errorf("the report does not tell the reader how to clear it:\n%s", body)
	}
}

// The property that makes this callable from a failure path. Half of run's
// park sites are themselves Linear failures; a report that could fail the
// caller would turn "Linear is down" into a park that parks on its own
// report, recursing exactly when Linear is already broken.
func TestReportParkSurvivesAFailingBoard(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    *fake
		want string
	}{
		{"the comment fails", &fake{labels: parkedLabels(), commentErr: errors.New("linear is down")}, "saying so on the ticket failed"},
		{"the label cannot be resolved", &fake{labelErr: errors.New("linear is down")}, "resolving the \"parked\" label failed"},
		{"the workspace has no parked label", &fake{}, "run `wand init`"},
		{"the label will not attach", &fake{labels: parkedLabels(), addErr: errors.New("linear is down")}, "labeling it \"parked\" failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			ReportPark(context.Background(), tc.f, &out, "issue-1", "a reason")
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("the operator is not told what went wrong:\ngot  %q\nwant it to contain %q", out.String(), tc.want)
			}
		})
	}
}

// The most common park is an interrupt, which arrives as a canceled
// context — the very context every Linear call would ride. Reporting on it
// would fail every time, so the writes run on a context that outlives the
// cancellation. Remove context.WithoutCancel and this is what fails.
func TestReportParkOutlivesACanceledContext(t *testing.T) {
	f := &fake{labels: parkedLabels()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out strings.Builder
	ReportPark(ctx, f, &out, "issue-1", "interrupted by terminated")

	if got := strings.Join(f.calls, ","); got != "comment,label" {
		t.Fatalf("an interrupted run could not report its park: calls = %q, out = %q", got, out.String())
	}
}

// Nothing to report to is not an error. wand pm runs against a brief
// rather than a ticket, and a park there is journal-only by design.
func TestReportParkWithNoTicketWritesNothing(t *testing.T) {
	f := &fake{labels: parkedLabels()}
	ReportPark(context.Background(), f, nil, "", "a reason")
	if len(f.calls) != 0 {
		t.Errorf("wrote to a ticket that does not exist: %v", f.calls)
	}
}
