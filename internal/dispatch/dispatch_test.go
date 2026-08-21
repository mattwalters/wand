package dispatch

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/plan"
	"github.com/mattwalters/wand/internal/run"
	"github.com/mattwalters/wand/internal/worker"
)

// fakeBoard implements dispatch.Board. Only TeamIssuesByState is exercised
// by the tests here — every path that would reach the rest is refused
// before it gets that far (lock/nothing-to-do/unreachable), or is already
// covered end to end in internal/run and internal/plan.
type fakeBoard struct {
	todo, toPlan []linear.Issue
	err          error
}

func (f *fakeBoard) TeamIssuesByState(_ context.Context, _, stateName string) ([]linear.Issue, error) {
	if f.err != nil {
		return nil, f.err
	}
	if stateName == "Todo" {
		return f.todo, nil
	}
	return f.toPlan, nil
}

func (f *fakeBoard) IssueByIdentifier(context.Context, string) (linear.Issue, error) {
	return linear.Issue{}, errors.New("not implemented in this fake")
}
func (f *fakeBoard) TeamStates(context.Context, string) ([]linear.WorkflowState, error) {
	return nil, errors.New("not implemented in this fake")
}
func (f *fakeBoard) Viewer(context.Context) (linear.User, error) {
	return linear.User{}, errors.New("not implemented in this fake")
}
func (f *fakeBoard) CreateComment(context.Context, string, string) error {
	return errors.New("not implemented in this fake")
}
func (f *fakeBoard) UpdateIssue(context.Context, string, linear.IssueUpdate) error {
	return errors.New("not implemented in this fake")
}
func (f *fakeBoard) TeamByKey(context.Context, string) (linear.Team, error) {
	return linear.Team{}, errors.New("not implemented in this fake")
}
func (f *fakeBoard) LabelByName(context.Context, string) (linear.Label, bool, error) {
	return linear.Label{}, false, errors.New("not implemented in this fake")
}
func (f *fakeBoard) CreateIssue(context.Context, linear.IssueCreate) (linear.Issue, error) {
	return linear.Issue{}, errors.New("not implemented in this fake")
}
func (f *fakeBoard) SearchIssues(context.Context, string, string) ([]linear.Issue, error) {
	return nil, errors.New("not implemented in this fake")
}
func (f *fakeBoard) IssueComments(context.Context, string) ([]linear.Comment, error) {
	return nil, errors.New("not implemented in this fake")
}
func (f *fakeBoard) AddLabel(context.Context, string, string) error {
	return errors.New("not implemented in this fake")
}
func (f *fakeBoard) UpsertSection(context.Context, string, string, string, string) (string, bool, error) {
	return "", false, errors.New("not implemented in this fake")
}

// unimplementedGit, unimplementedHub, unimplementedShell, unimplementedTree
// and unimplementedWorkers fill Deps for tests that never reach a winner's
// loop, so validate() is satisfied without any real orchestration surface.
type unimplementedGit struct{ run.Git }
type unimplementedHub struct{ run.Hub }
type unimplementedShell struct{ run.Shell }
type unimplementedTree struct{ plan.Tree }
type unimplementedWorkers struct{}

func (unimplementedWorkers) Run(context.Context, worker.Spec) (worker.Result, error) {
	return worker.Result{}, errors.New("not implemented in this fake")
}

type fakeRuns struct {
	reports map[string]journal.Report
}

func (f *fakeRuns) List() ([]string, error) {
	ids := make([]string, 0, len(f.reports))
	for id := range f.reports {
		ids = append(ids, id)
	}
	return ids, nil
}

func (f *fakeRuns) Inspect(id string) (journal.Report, error) {
	r, ok := f.reports[id]
	if !ok {
		return journal.Report{}, errors.New("no such run")
	}
	return r, nil
}

func baseDeps(t *testing.T, board Board, runs Runs) Deps {
	t.Helper()
	return Deps{
		Board:   board,
		Cov:     covenant.Default(),
		Runs:    runs,
		Git:     unimplementedGit{},
		Hub:     unimplementedHub{},
		Shell:   unimplementedShell{},
		Tree:    unimplementedTree{},
		Workers: unimplementedWorkers{},
		TeamKey: "WND",
		Repo:    t.TempDir(),
	}
}

func newStore(t *testing.T) *journal.Store {
	t.Helper()
	return journal.New(t.TempDir())
}

func TestExecuteNothingToDo(t *testing.T) {
	store := newStore(t)
	d := baseDeps(t, &fakeBoard{}, &fakeRuns{})

	res, err := Execute(context.Background(), d, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != KindNothingToDo {
		t.Errorf("Kind = %s, want %s", res.Kind, KindNothingToDo)
	}
	if res.ExitCode() != ExitNothingToDo {
		t.Errorf("ExitCode = %d, want %d", res.ExitCode(), ExitNothingToDo)
	}
}

func TestExecuteLocked(t *testing.T) {
	store := newStore(t)
	d := baseDeps(t, &fakeBoard{}, &fakeRuns{})

	lock, err := Acquire(store.Root, d.Repo)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	res, err := Execute(context.Background(), d, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != KindLocked {
		t.Errorf("Kind = %s, want %s", res.Kind, KindLocked)
	}
	if res.ExitCode() != ExitLocked {
		t.Errorf("ExitCode = %d, want %d", res.ExitCode(), ExitLocked)
	}
}

func TestExecuteUnreachable(t *testing.T) {
	store := newStore(t)
	board := &fakeBoard{err: &url.Error{Op: "Post", URL: "https://api.linear.app/graphql", Err: errors.New("connection refused")}}
	d := baseDeps(t, board, &fakeRuns{})

	res, err := Execute(context.Background(), d, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != KindUnreachable {
		t.Errorf("Kind = %s, want %s", res.Kind, KindUnreachable)
	}
	if res.ExitCode() != ExitUnreachable {
		t.Errorf("ExitCode = %d, want %d", res.ExitCode(), ExitUnreachable)
	}
}

func TestExecuteOtherLinearErrorIsAWandError(t *testing.T) {
	store := newStore(t)
	board := &fakeBoard{err: errors.New("linear: bad credentials")}
	d := baseDeps(t, board, &fakeRuns{})

	_, err := Execute(context.Background(), d, store)
	if err == nil {
		t.Fatal("expected an error for a non-transport Linear failure")
	}
}

func TestExecuteRefusesInvalidDeps(t *testing.T) {
	store := newStore(t)
	d := baseDeps(t, &fakeBoard{}, &fakeRuns{})
	d.Board = nil

	if _, err := Execute(context.Background(), d, store); err == nil {
		t.Fatal("expected a validation error")
	}
}

func TestExecuteWinnerClaimRefusedIsResultNotError(t *testing.T) {
	store := newStore(t)
	// A Todo issue that vets clean at read time but is not actually in
	// Todo by the time run.Execute re-reads it: fakeBoard.IssueByIdentifier
	// always errors, which is what a claim raced-and-lost looks like from
	// here — refused, not a wand bug.
	board := &fakeBoard{todo: []linear.Issue{{Identifier: "WND-1", State: linear.IssueState{Name: "Todo"}}}}
	d := baseDeps(t, board, &fakeRuns{})

	res, err := Execute(context.Background(), d, store)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != KindRefused {
		t.Errorf("Kind = %s, want %s", res.Kind, KindRefused)
	}
	if res.ExitCode() != ExitRefused {
		t.Errorf("ExitCode = %d, want %d", res.ExitCode(), ExitRefused)
	}
}
