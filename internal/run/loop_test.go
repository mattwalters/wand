package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/verbs"
	"github.com/mattwalters/wand/internal/worker"
)

// The loop's decisions, held against fakes: which phases run, which Linear
// writes happen and in what order, and which of the three terminal states
// the run ends in. The journal is real (a temp store) because its
// exactly-one-terminal-record guarantee is part of what is being asserted.

// --- fakes ----------------------------------------------------------------

// boardCall is one write the fake board saw, in order.
type boardCall struct {
	kind string // "update", "comment", "label"
	body string // comment body
	upd  linear.IssueUpdate
}

type fakeBoard struct {
	issue  linear.Issue
	states []linear.WorkflowState
	labels map[string]linear.Label

	// humanEdit, when set, is appended to the description on every fetch
	// after the first — a human editing the ticket while the worker runs.
	humanEdit string
	fetches   int

	calls []boardCall
}

func (b *fakeBoard) IssueByIdentifier(context.Context, string) (linear.Issue, error) {
	b.fetches++
	issue := b.issue
	if b.humanEdit != "" && b.fetches > 1 {
		issue.Description += b.humanEdit
	}
	return issue, nil
}
func (b *fakeBoard) TeamStates(context.Context, string) ([]linear.WorkflowState, error) {
	return b.states, nil
}
func (b *fakeBoard) Viewer(context.Context) (linear.User, error) {
	return linear.User{ID: "u1", Name: "orchestrator"}, nil
}
func (b *fakeBoard) CreateComment(_ context.Context, _ string, body string) error {
	b.calls = append(b.calls, boardCall{kind: "comment", body: body})
	return nil
}
func (b *fakeBoard) UpdateIssue(_ context.Context, _ string, u linear.IssueUpdate) error {
	b.calls = append(b.calls, boardCall{kind: "update", upd: u})
	if u.Description != nil {
		b.issue.Description = *u.Description
	}
	return nil
}
func (b *fakeBoard) LabelByName(_ context.Context, name string) (linear.Label, bool, error) {
	l, ok := b.labels[name]
	return l, ok, nil
}
func (b *fakeBoard) AddLabel(_ context.Context, _, labelID string) error {
	b.calls = append(b.calls, boardCall{kind: "label", body: labelID})
	return nil
}
func (b *fakeBoard) IssueComments(context.Context, string) ([]linear.Comment, error) {
	return nil, nil
}
func (b *fakeBoard) TeamByKey(context.Context, string) (linear.Team, error) {
	panic("not used by run")
}
func (b *fakeBoard) CreateIssue(context.Context, linear.IssueCreate) (linear.Issue, error) {
	panic("not used by run")
}
func (b *fakeBoard) SearchIssues(context.Context, string, string) ([]linear.Issue, error) {
	panic("not used by run")
}

// statusWrites returns the state ids written, in order.
func (b *fakeBoard) statusWrites() []string {
	var ids []string
	for _, c := range b.calls {
		if c.kind == "update" && c.upd.StateID != "" {
			ids = append(ids, c.upd.StateID)
		}
	}
	return ids
}

// lastComment returns the last comment body, or "".
func (b *fakeBoard) lastComment() string {
	for i := len(b.calls) - 1; i >= 0; i-- {
		if b.calls[i].kind == "comment" {
			return b.calls[i].body
		}
	}
	return ""
}

type fakeGit struct {
	dirty   []bool // popped per Dirty call; empty means clean
	ahead   int
	pushes  int
	removed bool

	// worktreeBase and aheadBase record the commit-ish the loop actually
	// handed git: the run must branch from and count against the
	// remote-tracking ref, never the bare branch name.
	worktreeBase string
	aheadBase    string

	// diffStat is what DiffStat returns every call; diffStatCalls counts
	// how many times the loop asked for one.
	diffStat      string
	diffStatCalls int
}

func (g *fakeGit) DefaultBranch(context.Context, string) (Base, error) {
	return Base{Name: "main", Ref: "origin/main"}, nil
}
func (g *fakeGit) AddWorktree(_ context.Context, _, _, _, base string) error {
	g.worktreeBase = base
	return nil
}
func (g *fakeGit) RemoveWorktree(context.Context, string, string) error {
	g.removed = true
	return nil
}
func (g *fakeGit) Dirty(context.Context, string) (bool, error) {
	if len(g.dirty) == 0 {
		return false, nil
	}
	d := g.dirty[0]
	g.dirty = g.dirty[1:]
	return d, nil
}
func (g *fakeGit) CommitsAhead(_ context.Context, _, base string) (int, error) {
	g.aheadBase = base
	return g.ahead, nil
}
func (g *fakeGit) Push(context.Context, string, string) error {
	g.pushes++
	return nil
}
func (g *fakeGit) DiffStat(context.Context, string, string) (string, error) {
	g.diffStatCalls++
	return g.diffStat, nil
}

type fakeHub struct {
	pr          *PR
	openedBase  string
	openedTitle string
	openedBody  string
	unresolved  int

	// lookups counts PRForBranch calls, so a test can make the PR change
	// under the run the way a human merging mid-run does. The loop looks
	// the PR up once to open or repair it and again to converge; a race
	// that only exists between those two calls cannot be reproduced by a
	// hub that answers the same thing every time.
	lookups        int
	mergeAtLookup  int // >0: from this lookup on, the PR reads merged
	vanishAtLookup int // >0: from this lookup on, there is no PR at all
}

func (h *fakeHub) PRForBranch(context.Context, string, string) (PR, bool, error) {
	h.lookups++
	if h.vanishAtLookup > 0 && h.lookups >= h.vanishAtLookup {
		return PR{}, false, nil
	}
	if h.pr == nil {
		return PR{}, false, nil
	}
	got := *h.pr
	if h.mergeAtLookup > 0 && h.lookups >= h.mergeAtLookup {
		got.State = PRStateMerged
	}
	return got, true, nil
}
func (h *fakeHub) OpenPR(_ context.Context, _, base, _, title, body string) (string, error) {
	h.openedBase, h.openedTitle, h.openedBody = base, title, body
	h.pr = &PR{Number: 1, Title: title, URL: "https://example.test/pr/1", State: PRStateOpen}
	return h.pr.URL, nil
}
func (h *fakeHub) RetitlePR(_ context.Context, _ string, _ int, title string) error {
	h.pr.Title = title
	return nil
}
func (h *fakeHub) UnresolvedThreads(context.Context, string, int) (int, error) {
	return h.unresolved, nil
}

// workerStep is one scripted worker: the handoff it leaves, or the error
// worker.Run would surface (no handoff, timeout, spawn failure).
type workerStep struct {
	handoff string
	err     error
	usage   *worker.Usage
}

type fakeWorkers struct {
	t     *testing.T
	steps []workerStep
	specs []worker.Spec
}

func (w *fakeWorkers) Run(_ context.Context, spec worker.Spec) (worker.Result, error) {
	w.specs = append(w.specs, spec)
	if len(w.steps) == 0 {
		w.t.Fatalf("unscripted worker spawn: mode %q", spec.Mode)
	}
	step := w.steps[0]
	w.steps = w.steps[1:]
	if step.err != nil {
		return worker.Result{ExitCode: 1, Output: "worker noise"}, step.err
	}
	return worker.Result{Handoff: json.RawMessage(step.handoff), ExitCode: 0, Usage: step.usage}, nil
}

// modes lists the phases that actually spawned, in order.
func (w *fakeWorkers) modes() []string {
	var m []string
	for _, s := range w.specs {
		m = append(m, s.Mode)
	}
	return m
}

// shellStep is one scripted verify/provision result.
type shellStep struct {
	ok     bool
	output string
}

type fakeShell struct {
	t     *testing.T
	steps []shellStep
}

func (s *fakeShell) Run(_ context.Context, _, command string) (bool, string, error) {
	if len(s.steps) == 0 {
		s.t.Fatalf("unscripted shell run: %q", command)
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return step.ok, step.output, nil
}

// --- fixture --------------------------------------------------------------

const (
	stateInProgress = "state-in-progress"
	stateNeedsInput = "state-needs-input"
	stateInReview   = "state-in-review"
)

func todoIssue() linear.Issue {
	return linear.Issue{
		ID:          "i1",
		Identifier:  "WND-1",
		Title:       "a ticket",
		Description: "the plan assumes the cache is warm",
		URL:         "https://linear.test/WND-1",
		BranchName:  "matt/wnd-1-a-ticket",
		TeamID:      "t1",
		Priority:    2,
		State:       linear.IssueState{Name: "Todo", Type: "unstarted"},
	}
}

type fixture struct {
	board   *fakeBoard
	git     *fakeGit
	hub     *fakeHub
	workers *fakeWorkers
	shell   *fakeShell
	store   *journal.Store
	deps    Deps
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	cov := covenant.Default()
	cov.Commands.Verify = "make check"
	cov.Caps = covenant.Caps{ReviewRounds: 3, CIAttempts: 3, WorkerTimeout: time.Minute}

	f := &fixture{
		board: &fakeBoard{
			issue: todoIssue(),
			states: []linear.WorkflowState{
				{ID: stateInProgress, Name: "In Progress", Type: "started"},
				{ID: stateNeedsInput, Name: "Needs Input", Type: "unstarted"},
				{ID: stateInReview, Name: "In Review", Type: "started"},
			},
			labels: map[string]linear.Label{
				ReadyForHumanLabel: {ID: "label-rfh", Name: ReadyForHumanLabel},
				verbs.ParkedLabel:  {ID: "label-parked", Name: verbs.ParkedLabel},
			},
		},
		git:     &fakeGit{ahead: 1},
		hub:     &fakeHub{},
		workers: &fakeWorkers{t: t},
		shell:   &fakeShell{t: t},
		store: &journal.Store{
			Root: filepath.Join(t.TempDir(), "state"),
			Now:  func() time.Time { return time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC) },
			Host: "testhost",
		},
	}
	f.deps = Deps{
		Board:   f.board,
		Cov:     cov,
		Git:     f.git,
		Hub:     f.hub,
		Workers: f.workers,
		Shell:   f.shell,
		Repo:    t.TempDir(),
		Harness: "claude-code",
		Out:     io.Discard,
	}
	return f
}

func (f *fixture) execute(t *testing.T) Outcome {
	t.Helper()
	out, err := Execute(context.Background(), f.deps, f.store, "WND-1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return out
}

// journalOutcome replays the run's journal and returns its terminal state.
func (f *fixture) journalOutcome(t *testing.T, id string) journal.State {
	t.Helper()
	state, err := f.store.State(id)
	if err != nil {
		t.Fatalf("replaying the journal: %v", err)
	}
	return state
}

const (
	doneHandoff    = `{"status": "done", "summary": "implemented it", "title": "add the loop"}`
	approveHandoff = `{"verdict": "approve", "summary": "read the diff, ran the suite; the change does what the ticket asks"}`
)

func findingsHandoff(findings ...string) string {
	var fs []string
	for _, f := range findings {
		fs = append(fs, f)
	}
	return fmt.Sprintf(`{"verdict": "revise", "findings": [%s]}`, strings.Join(fs, ","))
}

// --- the tests ------------------------------------------------------------

func TestConvergesOnApproval(t *testing.T) {
	f := newFixture(t)
	f.workers.steps = []workerStep{{handoff: doneHandoff}, {handoff: approveHandoff}}
	f.shell.steps = []shellStep{{ok: true}} // verify green first try

	out := f.execute(t)

	if out.Kind != journal.Converged || out.ExitCode() != ExitConverged {
		t.Fatalf("outcome %+v, want converged/exit 0", out)
	}
	if got := f.workers.modes(); !equal(got, []string{"implement", "review"}) {
		t.Errorf("phases %v", got)
	}
	// The PR was opened by the orchestrator, titled with the identifier.
	if f.hub.openedTitle != "[WND-1] add the loop" {
		t.Errorf("PR title %q", f.hub.openedTitle)
	}
	if !strings.Contains(f.hub.openedBody, "[WND-1 — a ticket](https://linear.test/WND-1)") {
		t.Errorf("PR body lacks the glossed reference:\n%s", f.hub.openedBody)
	}
	if f.git.pushes == 0 {
		t.Error("nothing was pushed")
	}
	if !f.git.removed {
		t.Error("the clean, pushed worktree was not removed")
	}

	// Terminal writes in order: comment, then label, then In Review — the
	// status is the last thing that moves.
	var kinds []string
	for _, c := range f.board.calls {
		kinds = append(kinds, c.kind)
	}
	want := []string{"update", "comment", "label", "update"} // claim, then the terminal three
	if !equal(kinds, want) {
		t.Errorf("board calls %v, want %v", kinds, want)
	}
	if got := f.board.statusWrites(); !equal(got, []string{stateInProgress, stateInReview}) {
		t.Errorf("status writes %v", got)
	}
	if c := f.board.lastComment(); !strings.Contains(c, "ready for a human") || !strings.Contains(c, out.PRURL) {
		t.Errorf("converged comment:\n%s", c)
	}

	state := f.journalOutcome(t, out.RunID)
	if state.Outcome != journal.Converged {
		t.Errorf("journal outcome %q", state.Outcome)
	}
}

func TestBlockedWorkerHandsBackVerbatim(t *testing.T) {
	f := newFixture(t)
	reason := "the ticket assumes an API that does not exist;\nI need a human to pick the alternative"
	f.workers.steps = []workerStep{{handoff: fmt.Sprintf(
		`{"status": "blocked", "summary": "stopped early", "reason": %q}`, reason)}}

	out := f.execute(t)

	if out.Kind != journal.HandedBack || out.ExitCode() != ExitHandedBack {
		t.Fatalf("outcome %+v, want handed back/exit 2", out)
	}
	// The worker's own account, quoted verbatim (PW-190) — both lines.
	c := f.board.lastComment()
	if !strings.Contains(c, "> the ticket assumes an API that does not exist;") ||
		!strings.Contains(c, "> I need a human to pick the alternative") {
		t.Errorf("hand-back does not quote the worker verbatim:\n%s", c)
	}
	// Comment before status: the Needs Input write comes after the comment.
	kinds := []string{}
	for _, call := range f.board.calls {
		kinds = append(kinds, call.kind)
	}
	if !equal(kinds, []string{"update", "comment", "update"}) {
		t.Errorf("board calls %v", kinds)
	}
	if got := f.board.statusWrites(); !equal(got, []string{stateInProgress, stateNeedsInput}) {
		t.Errorf("status writes %v", got)
	}
	if f.journalOutcome(t, out.RunID).Outcome != journal.HandedBack {
		t.Error("journal does not say handed back")
	}
}

func TestUnparseableReviewerParks(t *testing.T) {
	f := newFixture(t)
	f.workers.steps = []workerStep{
		{handoff: doneHandoff},
		{handoff: `{"verdict": "sounds fine to me"}`}, // not a verdict
	}
	f.shell.steps = []shellStep{{ok: true}}

	out := f.execute(t)

	// Parked, not converged: a crashed reviewer must never read as a pass.
	if out.Kind != journal.Parked || out.ExitCode() != ExitParked {
		t.Fatalf("outcome %+v, want parked/exit 3", out)
	}
	if !strings.Contains(out.Reason, "reviewer") {
		t.Errorf("park reason %q does not name the reviewer", out.Reason)
	}
	// No Needs Input, no In Review: the ticket was not touched after claim.
	if got := f.board.statusWrites(); !equal(got, []string{stateInProgress}) {
		t.Errorf("status writes %v, want the claim only", got)
	}
	if f.journalOutcome(t, out.RunID).Outcome != journal.Parked {
		t.Error("journal does not say parked")
	}
}

func TestReviseRoundThenApproval(t *testing.T) {
	f := newFixture(t)
	finding := `{"summary": "off-by-one in the cap", "failure_scenario": "cap=1 allows two rounds", "location": "loop.go:10"}`
	f.workers.steps = []workerStep{
		{handoff: doneHandoff},
		{handoff: findingsHandoff(finding)},
		{handoff: `{"status": "done", "summary": "fixed the cap"}`},
		{handoff: approveHandoff},
	}
	f.shell.steps = []shellStep{{ok: true}, {ok: true}} // verify after implement, after revise

	out := f.execute(t)

	if out.Kind != journal.Converged {
		t.Fatalf("outcome %+v", out)
	}
	if got := f.workers.modes(); !equal(got, []string{"implement", "review", "revise", "review"}) {
		t.Errorf("phases %v", got)
	}
	// The reviser was prompted with the concrete finding.
	revise := f.workers.specs[2]
	if !strings.Contains(revise.Prompt, "off-by-one in the cap") ||
		!strings.Contains(revise.Prompt, "cap=1 allows two rounds") {
		t.Errorf("revise prompt lacks the finding:\n%s", revise.Prompt)
	}
	if f.git.pushes < 2 {
		t.Errorf("pushes %d, want the revise round pushed too", f.git.pushes)
	}
}

func TestReviewCapExhaustionHandsBackTheFindings(t *testing.T) {
	f := newFixture(t)
	f.deps.Cov.Caps.ReviewRounds = 1
	concrete := `{"summary": "drops the last record", "failure_scenario": "a run with one record replays empty"}`
	vague := `{"summary": "feels convoluted", "failure_scenario": ""}`
	f.workers.steps = []workerStep{
		{handoff: doneHandoff},
		{handoff: findingsHandoff(concrete, vague)},
	}
	f.shell.steps = []shellStep{{ok: true}}

	out := f.execute(t)

	// Exhaustion is a hand-back that says so — the PW-176 lesson — with
	// the final round's real findings quoted, and the vague one dropped
	// in code before posting.
	if out.Kind != journal.HandedBack {
		t.Fatalf("outcome %+v, want handed back", out)
	}
	c := f.board.lastComment()
	if !strings.Contains(c, "cap of 1 ran out") {
		t.Errorf("comment does not name the exhausted cap:\n%s", c)
	}
	if !strings.Contains(c, "drops the last record") ||
		!strings.Contains(c, "a run with one record replays empty") {
		t.Errorf("comment does not quote the standing finding:\n%s", c)
	}
	if strings.Contains(c, "feels convoluted") {
		t.Errorf("a finding without a failure scenario was posted:\n%s", c)
	}
	if got := f.workers.modes(); !equal(got, []string{"implement", "review"}) {
		t.Errorf("phases %v — no revise round exists past the cap", got)
	}
}

func TestVagueFindingsBurnARoundWithoutRevising(t *testing.T) {
	f := newFixture(t)
	f.deps.Cov.Caps.ReviewRounds = 2
	vague := `{"summary": "feels off", "failure_scenario": " "}`
	f.workers.steps = []workerStep{
		{handoff: doneHandoff},
		{handoff: findingsHandoff(vague)},
		{handoff: approveHandoff},
	}
	f.shell.steps = []shellStep{{ok: true}}

	out := f.execute(t)

	if out.Kind != journal.Converged {
		t.Fatalf("outcome %+v", out)
	}
	// No reviser was spawned for findings that were all dropped; the next
	// cold reviewer got the same tree.
	if got := f.workers.modes(); !equal(got, []string{"implement", "review", "review"}) {
		t.Errorf("phases %v", got)
	}
}

func TestCICapExhaustionHandsBack(t *testing.T) {
	f := newFixture(t)
	f.deps.Cov.Caps.CIAttempts = 1
	f.workers.steps = []workerStep{
		{handoff: doneHandoff},
		{handoff: `{"status": "done", "summary": "tried a fix"}`}, // fix-ci
	}
	f.shell.steps = []shellStep{
		{ok: false, output: "FAIL: TestX"},
		{ok: false, output: "FAIL: TestX still"},
	}

	out := f.execute(t)

	if out.Kind != journal.HandedBack {
		t.Fatalf("outcome %+v, want handed back", out)
	}
	c := f.board.lastComment()
	if !strings.Contains(c, "make check") || !strings.Contains(c, "FAIL: TestX still") {
		t.Errorf("CI hand-back lacks the command or the last failure:\n%s", c)
	}
	if !strings.Contains(c, "Handing back rather than converging on exhaustion") {
		t.Errorf("CI hand-back does not name exhaustion:\n%s", c)
	}
	if got := f.workers.modes(); !equal(got, []string{"implement", "fix-ci"}) {
		t.Errorf("phases %v", got)
	}
	// The PR never opened: verify never went green.
	if f.hub.pr != nil {
		t.Error("a PR was opened for a branch that never went green")
	}
}

func TestDirtyTreeParks(t *testing.T) {
	f := newFixture(t)
	f.workers.steps = []workerStep{{handoff: doneHandoff}}
	f.git.dirty = []bool{true}

	out := f.execute(t)

	if out.Kind != journal.Parked {
		t.Fatalf("outcome %+v, want parked", out)
	}
	if !strings.Contains(out.Reason, "dirty") {
		t.Errorf("park reason %q", out.Reason)
	}
	if f.git.removed {
		t.Error("a dirty worktree was removed; it must be preserved")
	}
}

func TestDoneWithNoCommitsParks(t *testing.T) {
	f := newFixture(t)
	f.workers.steps = []workerStep{{handoff: doneHandoff}}
	f.git.ahead = 0

	out := f.execute(t)

	if out.Kind != journal.Parked || !strings.Contains(out.Reason, "no commits") {
		t.Fatalf("outcome %+v, want parked over the empty branch", out)
	}
}

func TestWorkerFailureParks(t *testing.T) {
	f := newFixture(t)
	f.workers.steps = []workerStep{{err: errors.New("claude-code timed out after 1m0s")}}

	out := f.execute(t)

	if out.Kind != journal.Parked || !strings.Contains(out.Reason, "timed out") {
		t.Fatalf("outcome %+v, want parked quoting the failure", out)
	}
}

func TestRefusedClaimMeansNoRun(t *testing.T) {
	f := newFixture(t)
	f.board.issue.State = linear.IssueState{Name: "Backlog", Type: "backlog"}

	_, err := Execute(context.Background(), f.deps, f.store, "WND-1")
	if err == nil {
		t.Fatal("Execute succeeded against an unblessed ticket")
	}
	// Losing the claim race costs nothing — not even a run directory.
	ids, lerr := f.store.List()
	if lerr != nil {
		t.Fatalf("List: %v", lerr)
	}
	if len(ids) != 0 {
		t.Errorf("a refused claim left run directories: %v", ids)
	}
}

func TestDescriptionCorrectionsReachTheTicket(t *testing.T) {
	f := newFixture(t)
	f.workers.steps = []workerStep{
		{handoff: `{"status": "done", "summary": "implemented it",
			"description_corrections": [{"old": "the cache is warm", "new": "the cache starts cold"}],
			"plan_deviations": ["used the journal's clock, not a new one"]}`},
		{handoff: approveHandoff},
	}
	f.shell.steps = []shellStep{{ok: true}}

	out := f.execute(t)
	if out.Kind != journal.Converged {
		t.Fatalf("outcome %+v", out)
	}

	// The correction landed: comment quoting the old wording first, then
	// the description write, then the run went on.
	var sawQuote bool
	for i, c := range f.board.calls {
		if c.kind == "comment" && strings.Contains(c.body, "> the cache is warm") {
			sawQuote = true
			// The very next write applies the correction.
			if i+1 >= len(f.board.calls) || f.board.calls[i+1].upd.Description == nil {
				t.Error("the quote comment was not followed by the description write")
			}
		}
	}
	if !sawQuote {
		t.Error("no comment quoted the corrected wording")
	}
	if !strings.Contains(f.board.issue.Description, "the cache starts cold") {
		t.Errorf("description was not corrected: %q", f.board.issue.Description)
	}
	// The deviation reached the PR body and the converged comment.
	if !strings.Contains(f.hub.openedBody, "used the journal's clock") {
		t.Errorf("PR body lacks the deviation:\n%s", f.hub.openedBody)
	}
	if !strings.Contains(f.board.lastComment(), "used the journal's clock") {
		t.Errorf("converged comment lacks the deviation:\n%s", f.board.lastComment())
	}
}

// A deviation reported by a revise worker arrives after the PR body was
// composed, so on a hand-back the PR body cannot carry it and the converged
// comment never runs. Nothing downstream would ever say it — which is the
// transcript death the rule exists against (the PW-191 lesson) — so every
// hand-back carries the run's deviations, and the journal keeps them too
// for the endings that write nothing else.
func TestDeviationsReachAHandBack(t *testing.T) {
	f := newFixture(t)
	f.deps.Cov.Caps.ReviewRounds = 2
	f.workers.steps = []workerStep{
		{handoff: doneHandoff},
		{handoff: `{"verdict": "revise", "findings": [{"summary": "off by one",
			"failure_scenario": "n=0 underflows the slice index"}]}`},
		{handoff: `{"status": "done", "summary": "fixed the index",
			"plan_deviations": ["dropped the retry the ticket asked for: the call is already idempotent"]}`},
		{handoff: `{"verdict": "revise", "findings": [{"summary": "still off by one",
			"failure_scenario": "n=0 still underflows"}]}`},
	}
	f.shell.steps = []shellStep{{ok: true}, {ok: true}}

	out := f.execute(t)
	if out.Kind != journal.HandedBack {
		t.Fatalf("outcome %+v, want handed back at the review cap", out)
	}
	const deviation = "dropped the retry the ticket asked for"
	if c := f.board.lastComment(); !strings.Contains(c, deviation) {
		t.Errorf("the hand-back comment lost the revise worker's deviation:\n%s", c)
	}
	// The PR body was composed before the revise phase existed, so it
	// cannot be what carries this — the hand-back has to.
	if strings.Contains(f.hub.openedBody, deviation) {
		t.Error("the PR body somehow carried a deviation reported after it was written")
	}
	// And the journal has it, so a park — which writes nothing else —
	// would keep it too.
	raw, err := os.ReadFile(filepath.Join(f.store.Dir(out.RunID), "journal.jsonl"))
	if err != nil {
		t.Fatalf("reading the journal: %v", err)
	}
	if !strings.Contains(string(raw), deviation) {
		t.Error("the deviation never reached the journal")
	}
}

func TestHumanThreadsBlockConvergence(t *testing.T) {
	f := newFixture(t)
	f.workers.steps = []workerStep{{handoff: doneHandoff}, {handoff: approveHandoff}}
	f.shell.steps = []shellStep{{ok: true}}
	f.hub.unresolved = 2 // a human commented while the loop ran

	out := f.execute(t)

	// Outdated is not answered (PW-177): the reviewer's approval does not
	// converge over standing human threads — the run hands back instead.
	if out.Kind != journal.HandedBack {
		t.Fatalf("outcome %+v, want handed back over the standing threads", out)
	}
	c := f.board.lastComment()
	if !strings.Contains(c, "outdated is not answered") {
		t.Errorf("hand-back does not state the rule:\n%s", c)
	}
	if got := f.board.statusWrites(); !equal(got, []string{stateInProgress, stateNeedsInput}) {
		t.Errorf("status writes %v, want claim then Needs Input — never In Review", got)
	}
	if f.git.removed {
		t.Error("the worktree was removed on a run that did not converge")
	}
}

func TestWorkerSpecsCarryTheContract(t *testing.T) {
	f := newFixture(t)
	f.workers.steps = []workerStep{{handoff: doneHandoff}, {handoff: approveHandoff}}
	f.shell.steps = []shellStep{{ok: true}}

	f.execute(t)

	impl := f.workers.specs[0]
	if impl.Timeout != f.deps.Cov.Caps.WorkerTimeout {
		t.Errorf("implement timeout %v", impl.Timeout)
	}
	if !strings.HasSuffix(impl.Dir, filepath.Join("", "tree")) {
		t.Errorf("implement dir %q is not the run's worktree", impl.Dir)
	}
	if !strings.Contains(strings.Join(impl.Rules, "\n"), "Do not push") {
		t.Errorf("implement rules do not forbid pushing: %v", impl.Rules)
	}
	if !strings.Contains(impl.Prompt, "WND-1  a ticket") {
		t.Errorf("implement prompt lacks the rendered ticket:\n%s", impl.Prompt)
	}

	review := f.workers.specs[1]
	if !strings.Contains(strings.Join(review.Rules, "\n"), "Do not modify the working tree") {
		t.Errorf("review rules do not forbid writes: %v", review.Rules)
	}
	// The reviewer's commands run in the worktree, where a bare "main" may
	// name nothing at all — the diff has to be against the tracking ref.
	if !strings.Contains(review.Prompt, "git diff origin/main...HEAD") {
		t.Errorf("review prompt does not point at the diff:\n%s", review.Prompt)
	}
}

// The base has two forms and they are not interchangeable: everything local
// — the worktree's starting point, the commit count, the reviewer's diff —
// resolves the remote-tracking ref, while GitHub is asked to merge into the
// branch. Swapping them is how a run branches from a stale local main, or
// from nothing at all when the branch was never checked out locally.
func TestTheBaseIsARefLocallyAndABranchOnGitHub(t *testing.T) {
	f := newFixture(t)
	f.shell.steps = []shellStep{{ok: true}}
	f.workers.steps = []workerStep{
		{handoff: `{"status":"done","summary":"did it"}`},
		{handoff: `{"verdict":"approve","summary":"verified the parser against the fixtures"}`},
	}

	out := f.execute(t)
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}
	if f.git.worktreeBase != "origin/main" {
		t.Errorf("worktree branched from %q, want origin/main", f.git.worktreeBase)
	}
	if f.git.aheadBase != "origin/main" {
		t.Errorf("commits counted against %q, want origin/main", f.git.aheadBase)
	}
	if f.hub.openedBase != "main" {
		t.Errorf("PR opened against %q, want the branch name main", f.hub.openedBase)
	}
}

// A journal store that fails after the claim must not leave the ticket
// silently In Progress behind an exit code that promises "nothing to
// sweep": the claim is handed straight back, comment before status.
func TestJournalFailureHandsTheClaimBack(t *testing.T) {
	f := newFixture(t)
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f.store.Root = filepath.Join(blocker, "state") // Create must fail

	_, err := Execute(context.Background(), f.deps, f.store, "WND-1")
	if err == nil {
		t.Fatal("Execute succeeded with a store that cannot open")
	}
	if !strings.Contains(err.Error(), "handed back") {
		t.Errorf("the error does not say the claim was handed back: %v", err)
	}
	if got := f.board.statusWrites(); !equal(got, []string{stateInProgress, stateNeedsInput}) {
		t.Errorf("status writes %v, want claim then Needs Input", got)
	}
	if c := f.board.lastComment(); !strings.Contains(c, "run journal") {
		t.Errorf("the hand-back comment does not name the journal failure:\n%s", c)
	}
}

// A blocked worker that committed nothing must not be described as having
// committed work: a human who trusts "committed on the branch" and deletes
// the preserved worktree would destroy the only copy.
func TestBlockedWithNoCommitsSaysSo(t *testing.T) {
	f := newFixture(t)
	f.git.ahead = 0
	f.workers.steps = []workerStep{{handoff: `{"status": "blocked", "summary": "s", "reason": "which API should this use?"}`}}

	out := f.execute(t)

	if out.Kind != journal.HandedBack {
		t.Fatalf("outcome %+v, want handed back", out)
	}
	c := f.board.lastComment()
	if strings.Contains(c, "committed on branch") {
		t.Errorf("the comment claims committed work on an empty branch:\n%s", c)
	}
	if !strings.Contains(c, "no commits yet") {
		t.Errorf("the comment does not state the branch is empty:\n%s", c)
	}
}

// A pre-push hand-back with commits must say the branch never reached
// origin — the branch name alone reads like something a human can fetch.
func TestPrePushHandbackNamesTheUnpushedBranch(t *testing.T) {
	f := newFixture(t)
	f.workers.steps = []workerStep{{handoff: `{"status": "blocked", "summary": "s", "reason": "stopping"}`}}

	out := f.execute(t) // git.ahead = 1, nothing pushed yet

	if out.Kind != journal.HandedBack {
		t.Fatalf("outcome %+v, want handed back", out)
	}
	if c := f.board.lastComment(); !strings.Contains(c, "has not been pushed") {
		t.Errorf("the comment does not say the branch is local-only:\n%s", c)
	}
}

// Corrections anchor against the ticket as it is at correction time, and
// the prompt-facing render follows: a human's mid-run edit survives the
// correction, and the next reviewer reads the corrected wording — never
// the claim the run itself disproved.
func TestCorrectionsUseTheFreshDescriptionAndRefreshThePrompt(t *testing.T) {
	f := newFixture(t)
	f.board.humanEdit = "\n\nAlso check the TTL." // lands after the claim
	f.workers.steps = []workerStep{
		{handoff: `{"status": "done", "summary": "implemented it",
			"description_corrections": [{"old": "the cache is warm", "new": "the cache starts cold"}]}`},
		{handoff: approveHandoff},
	}
	f.shell.steps = []shellStep{{ok: true}}

	out := f.execute(t)
	if out.Kind != journal.Converged {
		t.Fatalf("outcome %+v", out)
	}
	if !strings.Contains(f.board.issue.Description, "the cache starts cold") {
		t.Errorf("the correction did not land: %q", f.board.issue.Description)
	}
	if !strings.Contains(f.board.issue.Description, "Also check the TTL.") {
		t.Errorf("the human's mid-run edit was clobbered: %q", f.board.issue.Description)
	}
	review := f.workers.specs[1]
	if !strings.Contains(review.Prompt, "the cache starts cold") ||
		strings.Contains(review.Prompt, "the cache is warm") {
		t.Errorf("the review prompt was not re-rendered after the correction:\n%s", review.Prompt)
	}
}

// phaseDetails replays the run's journal and returns the phaseDetail
// carried on every phase.ended record, in order.
func phaseDetails(t *testing.T, f *fixture, runID string) []phaseDetail {
	t.Helper()
	records, err := f.store.Records(runID)
	if err != nil {
		t.Fatalf("reading records: %v", err)
	}
	var details []phaseDetail
	for _, r := range records {
		if r.Kind != journal.KindPhaseEnded {
			continue
		}
		var d phaseDetail
		if err := json.Unmarshal(r.Detail, &d); err != nil {
			t.Fatalf("unmarshaling phase.ended detail: %v", err)
		}
		details = append(details, d)
	}
	return details
}

// A phase's journaled detail carries the operational metrics ledger this
// ticket exists to stop losing — harness, model, wall-clock, tokens, diff
// stat — sourced from Deps and the worker's own report, never estimated.
func TestPhaseDetailCarriesOperationalMetrics(t *testing.T) {
	f := newFixture(t)
	in, out := int64(123), int64(45)
	f.workers.steps = []workerStep{
		{handoff: doneHandoff, usage: &worker.Usage{InputTokens: &in, OutputTokens: &out}},
		{handoff: approveHandoff}, // the reviewer's adapter reports no usage
	}
	f.shell.steps = []shellStep{{ok: true}}
	f.git.diffStat = "1 file changed, 2 insertions(+)"

	out2 := f.execute(t)
	if out2.Kind != journal.Converged {
		t.Fatalf("outcome %+v", out2)
	}

	details := phaseDetails(t, f, out2.RunID)
	if len(details) != 2 {
		t.Fatalf("got %d phase.ended records, want 2", len(details))
	}

	implement := details[0]
	if implement.Harness != "claude-code" {
		t.Errorf("Harness = %q, want claude-code", implement.Harness)
	}
	if implement.WallClock == "" {
		t.Error("WallClock is empty")
	}
	if implement.DiffStat != "1 file changed, 2 insertions(+)" {
		t.Errorf("DiffStat = %q", implement.DiffStat)
	}
	if implement.TokensIn == nil || *implement.TokensIn != 123 {
		t.Errorf("TokensIn = %v, want 123", implement.TokensIn)
	}
	if implement.TokensOut == nil || *implement.TokensOut != 45 {
		t.Errorf("TokensOut = %v, want 45", implement.TokensOut)
	}

	// The reviewer's worker reported no usage: absent, never faked as zero.
	review := details[1]
	if review.TokensIn != nil || review.TokensOut != nil {
		t.Errorf("review phase TokensIn/TokensOut = %v/%v, want both nil (no usage reported)",
			review.TokensIn, review.TokensOut)
	}
	if review.Harness != "claude-code" {
		t.Errorf("Harness = %q, want claude-code", review.Harness)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// WND-69. A park used to be the one terminal outcome that wrote nothing to
// Linear: the ticket stayed In Progress, assigned, with no explanation on
// it, while the reason sat in a journal file on the operator's machine.
// That ticket looks worked and is not, which is the state nothing drains.
func TestAParkIsReportedOnTheTicket(t *testing.T) {
	f := newFixture(t)
	f.workers.steps = []workerStep{{err: errors.New("claude-code timed out after 1m0s")}}

	out := f.execute(t)
	if out.Kind != journal.Parked {
		t.Fatalf("outcome %+v, want parked", out)
	}

	// The claim's own update opens every run; the park report is what
	// follows it, and comment-before-label is the ordering rule.
	var kinds []string
	for _, c := range f.board.calls {
		kinds = append(kinds, c.kind)
	}
	if got := strings.Join(kinds, ","); got != "update,comment,label" {
		t.Fatalf("board calls = %q, want the claim then the park report", got)
	}
	if c := f.board.lastComment(); !strings.Contains(c, "timed out after 1m0s") {
		t.Errorf("the report does not quote the reason:\n%s", c)
	}
	// The mark is a label, not a status: a park is a report that the
	// machine stopped, not a judgment about the work, and demoting here
	// would revoke a human's blessing over an infrastructure failure. The
	// claim's In Progress is the only status this run may write.
	if w := f.board.statusWrites(); len(w) != 1 {
		t.Errorf("status writes = %v, want only the claim's In Progress", w)
	}
}

// The report is a courtesy copy; the journal is the ending. A board that
// refuses the report must not turn one park into two, nor into a panic —
// half the park sites in run.go are Linear failures already.
func TestAParkThatCannotReachLinearIsStillOnePark(t *testing.T) {
	f := newFixture(t)
	f.workers.steps = []workerStep{{err: errors.New("claude-code timed out after 1m0s")}}
	f.board.labels = nil // no parked label anywhere: the report cannot land

	out := f.execute(t)

	if out.Kind != journal.Parked || !strings.Contains(out.Reason, "timed out") {
		t.Fatalf("outcome %+v, want the park to survive a board that refused the report", out)
	}
	if st := f.journalOutcome(t, out.RunID); st.Outcome != journal.Parked {
		t.Errorf("journal outcome = %q, want exactly one parked ending", st.Outcome)
	}
}

// WND-70. Merging is the outcome this loop exists to reach, and a human who
// merges the PR between the reviewer's approval and the final lookup used
// to turn that into a park. Three runs in the reference journal ended this
// way — WND-41, WND-44 and WND-53 — and all three are on main.
func TestAPRMergedMidRunConvergesRatherThanParking(t *testing.T) {
	f := newFixture(t)
	f.workers.steps = []workerStep{{handoff: doneHandoff}, {handoff: approveHandoff}}
	f.shell.steps = []shellStep{{ok: true}}
	f.hub.mergeAtLookup = 2 // open when the PR is ensured, merged by convergence

	out := f.execute(t)

	if out.Kind != journal.Converged {
		t.Fatalf("outcome %+v, want converged over a PR that landed mid-run", out)
	}
	if !strings.Contains(out.Reason, "already merged") {
		t.Errorf("the reason does not say the PR had landed: %q", out.Reason)
	}
	// The merge automation owns the ticket's status now. In Review over a
	// ticket the merge already closed reopens a close, which is a human's
	// call — the same reasoning verbs.refuseIfClosed is built on.
	if w := f.board.statusWrites(); len(w) != 1 {
		t.Errorf("status writes = %v, want only the claim's In Progress", w)
	}
	if c := f.board.lastComment(); !strings.Contains(c, "already merged") {
		t.Errorf("the ticket is not told the work landed:\n%s", c)
	}
	// ready-for-human means "In Review, with a PR a human should read".
	// A merged PR has been read.
	for _, c := range f.board.calls {
		if c.kind == "label" && c.body == "label-rfh" {
			t.Error("a merged PR was labeled ready-for-human")
		}
	}
}

// The park this replaces is still reachable, and still says the same thing:
// a PR that is genuinely absent is not a PR that merged.
func TestAnAbsentPRStillParks(t *testing.T) {
	f := newFixture(t)
	f.workers.steps = []workerStep{{handoff: doneHandoff}, {handoff: approveHandoff}}
	f.shell.steps = []shellStep{{ok: true}}
	f.hub.vanishAtLookup = 2 // opened, then gone by convergence

	out := f.execute(t)

	if out.Kind != journal.Parked || !strings.Contains(out.Reason, "is gone") {
		t.Fatalf("outcome %+v, want a park over the vanished PR", out)
	}
}
