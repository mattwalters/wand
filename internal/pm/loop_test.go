package pm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/pm"
	"github.com/mattwalters/wand/internal/worker"
)

// readOnlyBoard implements pm.Board and nothing else — the compiler is what
// proves the propose path cannot write to Linear, but this fake also
// records every call so a test can assert exactly what was read.
type readOnlyBoard struct {
	projects []linear.Project
	triage   []linear.Issue
	backlog  []linear.Issue
	searches []linear.Issue

	calls []string

	failProjects bool
	failTriage   bool
}

func (b *readOnlyBoard) Projects(ctx context.Context, teamKey string) ([]linear.Project, error) {
	b.calls = append(b.calls, "projects")
	if b.failProjects {
		return nil, fmt.Errorf("linear is down")
	}
	return b.projects, nil
}

func (b *readOnlyBoard) TeamIssuesByState(ctx context.Context, teamKey, stateName string) ([]linear.Issue, error) {
	b.calls = append(b.calls, "issues="+stateName)
	if b.failTriage && stateName == "Triage" {
		return nil, fmt.Errorf("linear is down")
	}
	switch stateName {
	case "Triage":
		return b.triage, nil
	case "Backlog":
		return b.backlog, nil
	}
	return nil, nil
}

func (b *readOnlyBoard) SearchIssues(ctx context.Context, teamKey, term string) ([]linear.Issue, error) {
	b.calls = append(b.calls, "search="+term)
	return b.searches, nil
}

type workers struct {
	results []workerResult
	modes   []string
	prompts []string
}

type workerResult struct {
	handoff any
	err     error
}

func (w *workers) Run(ctx context.Context, spec worker.Spec) (worker.Result, error) {
	w.modes = append(w.modes, spec.Mode)
	w.prompts = append(w.prompts, spec.Prompt)
	if len(w.results) == 0 {
		return worker.Result{}, fmt.Errorf("the loop spawned more workers than the test scripted")
	}
	next := w.results[0]
	w.results = w.results[1:]
	if next.err != nil {
		return worker.Result{ExitCode: 1}, next.err
	}
	var raw json.RawMessage
	switch h := next.handoff.(type) {
	case string:
		raw = json.RawMessage(h)
	default:
		b, err := json.Marshal(h)
		if err != nil {
			return worker.Result{}, err
		}
		raw = b
	}
	return worker.Result{Handoff: raw}, nil
}

type harness struct {
	deps  pm.Deps
	board *readOnlyBoard
	work  *workers
	store *journal.Store
	out   *strings.Builder
}

func newHarness(t *testing.T, results ...workerResult) *harness {
	t.Helper()
	b := &readOnlyBoard{}
	w := &workers{results: results}
	store := journal.New(t.TempDir())
	out := &strings.Builder{}
	return &harness{
		board: b, work: w, store: store, out: out,
		deps: pm.Deps{
			Board:   b,
			Cov:     covenant.Default(),
			Workers: w,
			TeamKey: "WND",
			Repo:    t.TempDir(),
			Harness: "claude-code",
			Out:     out,
		},
	}
}

func (h *harness) run(t *testing.T, brief string) (pm.Outcome, error) {
	t.Helper()
	return pm.Execute(context.Background(), h.deps, h.store, brief)
}

func TestProposeWritesOnlyAFileNeverLinear(t *testing.T) {
	h := newHarness(t, workerResult{handoff: goodDraft()})

	out, err := h.run(t, "Build a signup flow with email validation.")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}
	if out.ExitCode() != pm.ExitProposed {
		t.Errorf("exit code = %d, want %d", out.ExitCode(), pm.ExitProposed)
	}
	if out.ProposalPath == "" {
		t.Fatal("no proposal path returned")
	}

	// pm.Board carries no write method, so there is nothing to assert did
	// not happen beyond what did: reads only, and both statuses covered.
	want := map[string]bool{"projects": true, "issues=Triage": true, "issues=Backlog": true}
	for _, c := range h.board.calls {
		if strings.HasPrefix(c, "search=") {
			continue
		}
		if !want[c] {
			t.Errorf("unexpected board call %q", c)
		}
		delete(want, c)
	}
	if len(want) != 0 {
		t.Errorf("missing board calls: %v", want)
	}
}

func TestProposeRunsACollisionSearchPerProposedTitle(t *testing.T) {
	h := newHarness(t, workerResult{handoff: goodDraft()})
	h.board.searches = []linear.Issue{{Identifier: "WND-1", Title: "Add signup form validation"}}

	out, err := h.run(t, "Build a signup flow.")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var searched int
	for _, c := range h.board.calls {
		if strings.HasPrefix(c, "search=") {
			searched++
		}
	}
	if searched != 2 {
		t.Fatalf("ran %d collision searches, want 2 (one per proposed ticket)", searched)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s)", out.Kind, out.Reason)
	}
}

func TestAnInvalidHandoffParksWithNoProposalWritten(t *testing.T) {
	bad := goodDraft()
	delete(bad, "tickets")
	h := newHarness(t, workerResult{handoff: bad})

	out, err := h.run(t, "Build a signup flow.")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s (%s), want parked", out.Kind, out.Reason)
	}
	if out.ProposalPath != "" {
		t.Errorf("a proposal path was returned for a parked run: %q", out.ProposalPath)
	}
}

func TestAWrongPremiseHandsBackWithNoProposal(t *testing.T) {
	h := newHarness(t, workerResult{handoff: map[string]any{
		"premise": "wrong",
		"reason":  "WND-9 already covers this brief.",
	}})

	out, err := h.run(t, "Build a signup flow.")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.HandedBack {
		t.Fatalf("outcome = %s (%s), want handed back", out.Kind, out.Reason)
	}
	if out.ExitCode() != pm.ExitHandedBack {
		t.Errorf("exit code = %d, want %d", out.ExitCode(), pm.ExitHandedBack)
	}
	if !strings.Contains(h.out.String(), "WND-9 already covers this brief") {
		t.Errorf("the scout's reason was not surfaced:\n%s", h.out.String())
	}
}

func TestABoardReadFailureParks(t *testing.T) {
	h := newHarness(t, workerResult{handoff: goodDraft()})
	h.board.failProjects = true

	out, err := h.run(t, "Build a signup flow.")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s (%s), want parked", out.Kind, out.Reason)
	}
	if len(h.work.modes) != 0 {
		t.Errorf("a scout was spawned despite the board read failing: %v", h.work.modes)
	}
}

func TestAWorkerFailureParks(t *testing.T) {
	h := newHarness(t, workerResult{err: fmt.Errorf("claude: exited 1 without a usable handoff")})

	out, err := h.run(t, "Build a signup flow.")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s, want parked", out.Kind)
	}
}

func TestExecuteRefusesAnEmptyBrief(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(t, "   "); err == nil {
		t.Fatal("expected an error for an empty brief")
	}
	if ids, _ := h.store.List(); len(ids) != 0 {
		t.Errorf("a refused propose left a run behind: %v", ids)
	}
}

func TestTheBoardSummaryReachesTheScout(t *testing.T) {
	h := newHarness(t, workerResult{handoff: goodDraft()})
	h.board.projects = []linear.Project{{Name: "Existing Project", Description: "already here"}}
	h.board.triage = []linear.Issue{{Identifier: "WND-5", Title: "An existing triage ticket"}}

	if _, err := h.run(t, "Build a signup flow."); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(h.work.prompts) != 1 {
		t.Fatalf("spawned %d workers, want 1", len(h.work.prompts))
	}
	if !strings.Contains(h.work.prompts[0], "Existing Project") || !strings.Contains(h.work.prompts[0], "An existing triage ticket") {
		t.Error("the scout was not shown the board summary")
	}
}
