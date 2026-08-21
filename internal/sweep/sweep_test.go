package sweep

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/run"
	"github.com/mattwalters/wand/internal/verbs"
)

// fakeBoard is a minimal, mutable stand-in for sweep.Board: enough to drive
// verbs.Handback (IssueByIdentifier, TeamStates, CreateComment, UpdateIssue)
// and the two label/state reads sweep does directly.
type fakeBoard struct {
	issues   map[string]*linear.Issue // by identifier
	states   []linear.WorkflowState
	label    map[string][]linear.Issue // by label name
	byState  map[string][]linear.Issue // by state name
	comments map[string][]string       // by identifier
}

func newFakeBoard() *fakeBoard {
	return &fakeBoard{
		issues:   map[string]*linear.Issue{},
		label:    map[string][]linear.Issue{},
		byState:  map[string][]linear.Issue{},
		comments: map[string][]string{},
		states: []linear.WorkflowState{
			{ID: "st-needs-input", Name: "Needs Input", Type: "unstarted"},
			{ID: "st-in-review", Name: "In Review", Type: "started"},
			{ID: "st-in-progress", Name: "In Progress", Type: "started"},
			{ID: "st-in-planning", Name: "In Planning", Type: "started"},
			{ID: "st-plan-review", Name: "Plan Review", Type: "unstarted"},
		},
	}
}

func (b *fakeBoard) addIssue(issue linear.Issue) {
	cp := issue
	b.issues[issue.Identifier] = &cp
}

func (b *fakeBoard) IssueByIdentifier(_ context.Context, id string) (linear.Issue, error) {
	issue, ok := b.issues[id]
	if !ok {
		return linear.Issue{}, errors.New("no such issue")
	}
	return *issue, nil
}
func (b *fakeBoard) TeamStates(context.Context, string) ([]linear.WorkflowState, error) {
	return b.states, nil
}
func (b *fakeBoard) Viewer(context.Context) (linear.User, error) { return linear.User{ID: "u1"}, nil }
func (b *fakeBoard) CreateComment(_ context.Context, issueID, body string) error {
	for id, issue := range b.issues {
		if issue.ID == issueID || id == issueID {
			b.comments[id] = append(b.comments[id], body)
			return nil
		}
	}
	return errors.New("no such issue")
}
func (b *fakeBoard) UpdateIssue(_ context.Context, issueID string, u linear.IssueUpdate) error {
	for id, issue := range b.issues {
		if issue.ID == issueID || id == issueID {
			for _, s := range b.states {
				if s.ID == u.StateID {
					issue.State = linear.IssueState{Name: s.Name, Type: s.Type}
				}
			}
			return nil
		}
	}
	return errors.New("no such issue")
}
func (b *fakeBoard) TeamByKey(context.Context, string) (linear.Team, error) {
	return linear.Team{}, errors.New("not implemented in this fake")
}
func (b *fakeBoard) LabelByName(context.Context, string) (linear.Label, bool, error) {
	return linear.Label{}, false, errors.New("not implemented in this fake")
}
func (b *fakeBoard) CreateIssue(context.Context, linear.IssueCreate) (linear.Issue, error) {
	return linear.Issue{}, errors.New("not implemented in this fake")
}
func (b *fakeBoard) SearchIssues(context.Context, string, string) ([]linear.Issue, error) {
	return nil, errors.New("not implemented in this fake")
}
func (b *fakeBoard) TeamIssuesByLabel(_ context.Context, _, label string) ([]linear.Issue, error) {
	return b.label[label], nil
}
func (b *fakeBoard) TeamIssuesByState(_ context.Context, _, stateName string) ([]linear.Issue, error) {
	return b.byState[stateName], nil
}

type fakeHub struct {
	prs        map[string]run.PR // by branch
	unresolved map[int]int       // by PR number
}

func (h *fakeHub) PRForBranch(_ context.Context, _, branch string) (run.PR, bool, error) {
	pr, ok := h.prs[branch]
	return pr, ok, nil
}
func (h *fakeHub) UnresolvedThreads(_ context.Context, _ string, number int) (int, error) {
	return h.unresolved[number], nil
}

func baseSweepDeps(t *testing.T, board Board, hub Hub, store *journal.Store) Deps {
	t.Helper()
	return Deps{
		Board:   board,
		Hub:     hub,
		Runs:    store,
		Cov:     covenant.Default(),
		TeamKey: "WND",
		Repo:    t.TempDir(),
	}
}

func newSweepStore(t *testing.T) *journal.Store {
	t.Helper()
	return journal.New(t.TempDir())
}

func TestExecuteNothingToActOn(t *testing.T) {
	store := newSweepStore(t)
	d := baseSweepDeps(t, newFakeBoard(), &fakeHub{}, store)

	res, err := Execute(context.Background(), d, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Acted != ActedNothing {
		t.Errorf("Acted = %s, want %s", res.Acted, ActedNothing)
	}
}

func TestExecuteReReviewHandsBackAndMovesStatus(t *testing.T) {
	store := newSweepStore(t)
	board := newFakeBoard()
	board.addIssue(linear.Issue{ID: "id-1", Identifier: "WND-1", State: linear.IssueState{Name: "In Review", Type: "started"}, Labels: []string{"re-review"}})
	board.label[ReReviewLabel] = []linear.Issue{{ID: "id-1", Identifier: "WND-1", State: linear.IssueState{Type: "started"}, CreatedAt: time.Now()}}

	d := baseSweepDeps(t, board, &fakeHub{}, store)
	res, err := Execute(context.Background(), d, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Acted != ActedHandback || res.Candidate.Kind != KindReReview {
		t.Fatalf("res = %+v, want a re-review hand-back", res)
	}
	if got := board.issues["WND-1"].State.Name; got != "Needs Input" {
		t.Errorf("issue state = %q, want Needs Input", got)
	}
	if len(board.comments["WND-1"]) != 1 {
		t.Errorf("comments = %v, want exactly one", board.comments["WND-1"])
	}
}

func TestExecuteSkipsAReReviewCandidateWhoseLabelWasRemoved(t *testing.T) {
	store := newSweepStore(t)
	board := newFakeBoard()
	// The read found it labeled; by the time act() re-reads it, the human
	// removed the label — resolved in the interim, nothing to do for it.
	board.addIssue(linear.Issue{ID: "id-1", Identifier: "WND-1", State: linear.IssueState{Name: "In Review", Type: "started"}})
	board.label[ReReviewLabel] = []linear.Issue{{ID: "id-1", Identifier: "WND-1", CreatedAt: time.Now()}}

	d := baseSweepDeps(t, board, &fakeHub{}, store)
	res, err := Execute(context.Background(), d, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Acted != ActedNothing {
		t.Fatalf("Acted = %s, want %s: the label was gone by the time sweep acted", res.Acted, ActedNothing)
	}
}

// The planning-side twin of TestExecuteReReviewHandsBackAndMovesStatus: a
// re-plan label hands the ticket back the same way, aimed at In Planning
// instead of Needs Input.
func TestExecuteRePlanHandsBackAndMovesStatus(t *testing.T) {
	store := newSweepStore(t)
	board := newFakeBoard()
	board.addIssue(linear.Issue{ID: "id-1", Identifier: "WND-1", State: linear.IssueState{Name: "Plan Review", Type: "unstarted"}, Labels: []string{verbs.RePlanLabel}})
	board.label[verbs.RePlanLabel] = []linear.Issue{{ID: "id-1", Identifier: "WND-1", State: linear.IssueState{Type: "unstarted"}, CreatedAt: time.Now()}}

	d := baseSweepDeps(t, board, &fakeHub{}, store)
	res, err := Execute(context.Background(), d, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Acted != ActedHandback || res.Candidate.Kind != KindRePlan {
		t.Fatalf("res = %+v, want a re-plan hand-back", res)
	}
	if got := board.issues["WND-1"].State.Name; got != "In Planning" {
		t.Errorf("issue state = %q, want In Planning", got)
	}
	if len(board.comments["WND-1"]) != 1 {
		t.Errorf("comments = %v, want exactly one", board.comments["WND-1"])
	}
}

func TestExecuteSkipsARePlanCandidateWhoseLabelWasRemoved(t *testing.T) {
	store := newSweepStore(t)
	board := newFakeBoard()
	// The read found it labeled; by the time act() re-reads it, the human
	// removed the label — resolved in the interim, nothing to do for it.
	board.addIssue(linear.Issue{ID: "id-1", Identifier: "WND-1", State: linear.IssueState{Name: "Plan Review", Type: "unstarted"}})
	board.label[verbs.RePlanLabel] = []linear.Issue{{ID: "id-1", Identifier: "WND-1", CreatedAt: time.Now()}}

	d := baseSweepDeps(t, board, &fakeHub{}, store)
	res, err := Execute(context.Background(), d, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Acted != ActedNothing {
		t.Fatalf("Acted = %s, want %s: the label was gone by the time sweep acted", res.Acted, ActedNothing)
	}
}

func TestExecutePrefersDeadLeaseOverReReview(t *testing.T) {
	store := newSweepStore(t)
	board := newFakeBoard()
	board.addIssue(linear.Issue{ID: "id-1", Identifier: "WND-1", State: linear.IssueState{Name: "In Progress", Type: "started"}})
	board.addIssue(linear.Issue{ID: "id-2", Identifier: "WND-2", State: linear.IssueState{Name: "In Review", Type: "started"}, Labels: []string{"re-review"}})
	board.label[ReReviewLabel] = []linear.Issue{{ID: "id-2", Identifier: "WND-2", CreatedAt: time.Now()}}

	repo := t.TempDir()
	fabricateDeadRun(t, store, "run-1", journal.Meta{Ticket: "WND-1", Verb: "run", Repo: repo})

	d := baseSweepDeps(t, board, &fakeHub{}, store)
	d.Repo = repo
	res, err := Execute(context.Background(), d, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Acted != ActedReaped || res.Candidate.Ticket != "WND-1" {
		t.Fatalf("res = %+v, want the dead lease reaped first", res)
	}
	if got := board.issues["WND-1"].State.Name; got != "Needs Input" {
		t.Errorf("WND-1 state = %q, want Needs Input", got)
	}
	if got := board.issues["WND-2"].State.Name; got != "In Review" {
		t.Errorf("WND-2 state = %q, want untouched (In Review): only one action per pass", got)
	}
}

// fabricateDeadRun writes a run directory by hand, the way a crashed
// process would leave one: one run.started record and a lease, but no
// terminal record and no held lock. journal.Store's own liveness check
// (lease.go) does not trust the recorded pid at all — it is provable only
// by whether the run's lock file is currently flock'd — so a lock file
// this test never creates or holds reads as journal.Dead, honestly, the
// same way a real crash would leave one behind.
func fabricateDeadRun(t *testing.T, store *journal.Store, id string, m journal.Meta) {
	t.Helper()
	dir := store.Dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	// The lease's Host must match this machine's own hostname: liveness()
	// answers Unknown, never Dead, for a lease naming a host it cannot
	// examine — see internal/journal/lease.go.
	rec := journal.Record{Seq: 1, At: time.Now(), Kind: journal.KindStarted, Run: &m, Host: host, PID: 999999}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "journal.jsonl"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	lease := journal.Lease{RunID: id, Ticket: m.Ticket, Host: host, PID: 999999, Taken: time.Now(), Renewed: time.Now()}
	raw, err = json.Marshal(lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lease.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteUnresolvedThreads(t *testing.T) {
	store := newSweepStore(t)
	board := newFakeBoard()
	board.addIssue(linear.Issue{ID: "id-1", Identifier: "WND-1", State: linear.IssueState{Name: "In Review", Type: "started"}, BranchName: "wand/wnd-1"})
	board.label[run.ReadyForHumanLabel] = []linear.Issue{{ID: "id-1", Identifier: "WND-1", BranchName: "wand/wnd-1", State: linear.IssueState{Type: "started"}, CreatedAt: time.Now()}}

	hub := &fakeHub{
		prs:        map[string]run.PR{"wand/wnd-1": {Number: 7, URL: "https://github.com/x/y/pull/7", State: run.PRStateOpen}},
		unresolved: map[int]int{7: 2},
	}
	d := baseSweepDeps(t, board, hub, store)
	res, err := Execute(context.Background(), d, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Acted != ActedHandback || res.Candidate.Kind != KindUnresolvedThreads {
		t.Fatalf("res = %+v, want an unresolved-threads hand-back", res)
	}
	if got := board.issues["WND-1"].State.Name; got != "Needs Input" {
		t.Errorf("issue state = %q, want Needs Input", got)
	}
}

func TestExecuteReportsZombiesWithoutActing(t *testing.T) {
	store := newSweepStore(t)
	board := newFakeBoard()
	board.byState["In Progress"] = []linear.Issue{{ID: "id-1", Identifier: "WND-1", Title: "no run behind it", State: linear.IssueState{Type: "started"}}}

	d := baseSweepDeps(t, board, &fakeHub{}, store)
	res, err := Execute(context.Background(), d, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Acted != ActedNothing {
		t.Errorf("Acted = %s, want %s: a zombie report is never acted on", res.Acted, ActedNothing)
	}
	if len(res.Zombies) != 1 || res.Zombies[0].Ticket != "WND-1" {
		t.Fatalf("Zombies = %+v, want WND-1 reported", res.Zombies)
	}
}

// WND-70. PRForBranch now returns a branch's most recent PR whatever state
// it is in, so the open-only requirement lives at the call sites that have
// it — and this is one. A thread left unresolved on a PR that has since
// merged is not a person still waiting: merging is the answer, and handing
// the ticket back over it would reopen a question already settled by the
// strongest means available.
func TestAMergedPRsUnresolvedThreadsAreNotACandidate(t *testing.T) {
	store := newSweepStore(t)
	board := newFakeBoard()
	board.addIssue(linear.Issue{ID: "id-1", Identifier: "WND-1", State: linear.IssueState{Name: "In Review", Type: "started"}, BranchName: "wand/wnd-1"})
	board.label[run.ReadyForHumanLabel] = []linear.Issue{{ID: "id-1", Identifier: "WND-1", BranchName: "wand/wnd-1", State: linear.IssueState{Type: "started"}, CreatedAt: time.Now()}}

	hub := &fakeHub{
		prs:        map[string]run.PR{"wand/wnd-1": {Number: 7, URL: "https://github.com/x/y/pull/7", State: run.PRStateMerged}},
		unresolved: map[int]int{7: 2},
	}
	d := baseSweepDeps(t, board, hub, store)
	res, err := Execute(context.Background(), d, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Acted != ActedNothing {
		t.Fatalf("res = %+v, want nothing done over a merged PR's threads", res)
	}
	if got := board.issues["WND-1"].State.Name; got != "In Review" {
		t.Errorf("issue state = %q, want it left alone", got)
	}
}
