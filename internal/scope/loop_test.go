package scope_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/scope"
	"github.com/mattwalters/wand/internal/worker"
)

// The loop, against fakes and a real journal store. What these tests are
// for is the order of the writes and the emptiness of the ticket when
// anything goes wrong: both are promises a human reads the ticket
// expecting, and neither is visible in any single function.

// board is a fake Linear, recording every write in the order it was made.
type board struct {
	issue linear.Issue
	calls []string

	description string
	estimate    *int
	stateID     string
	comments    []string

	failComment  bool
	failEstimate bool
	failState    bool
}

func (b *board) log(call string) { b.calls = append(b.calls, call) }

func (b *board) IssueByIdentifier(ctx context.Context, identifier string) (linear.Issue, error) {
	return b.issue, nil
}

func (b *board) IssueComments(ctx context.Context, issueID string) ([]linear.Comment, error) {
	return nil, nil
}

func (b *board) TeamStates(ctx context.Context, teamID string) ([]linear.WorkflowState, error) {
	return []linear.WorkflowState{
		{ID: "state-needs-input", Name: "Needs Input", Type: "unstarted"},
		{ID: "state-scoping", Name: "Scoping", Type: "unstarted"},
	}, nil
}

func (b *board) CreateComment(ctx context.Context, issueID, body string) error {
	if b.failComment {
		return fmt.Errorf("linear is down")
	}
	b.log("comment")
	b.comments = append(b.comments, body)
	return nil
}

func (b *board) UpdateIssue(ctx context.Context, issueID string, u linear.IssueUpdate) error {
	switch {
	case u.Estimate != nil:
		if b.failEstimate {
			return fmt.Errorf("linear is down")
		}
		b.log("estimate")
		b.estimate = u.Estimate
	case u.StateID != "":
		if b.failState {
			return fmt.Errorf("linear is down")
		}
		b.log("state=" + u.StateID)
		b.stateID = u.StateID
	default:
		b.log("update")
	}
	return nil
}

func (b *board) UpsertSection(ctx context.Context, issueID, description, id, markdown string) (string, bool, error) {
	next, err := linear.WithSection(description, id, markdown)
	if err != nil {
		return "", false, err
	}
	b.log("section=" + id)
	b.description = next
	return next, next != description, nil
}

// The rest of verbs.Linear, which the premise hand-back reaches through
// verbs.Handback. A scope never files or assigns.
func (b *board) Viewer(ctx context.Context) (linear.User, error) {
	return linear.User{ID: "u", Name: "Key Holder"}, nil
}
func (b *board) TeamByKey(ctx context.Context, key string) (linear.Team, error) {
	return linear.Team{ID: "team", Key: key}, nil
}
func (b *board) LabelByName(ctx context.Context, name string) (linear.Label, bool, error) {
	return linear.Label{ID: "label", Name: name}, true, nil
}
func (b *board) CreateIssue(ctx context.Context, in linear.IssueCreate) (linear.Issue, error) {
	return linear.Issue{}, fmt.Errorf("a scope files nothing")
}
func (b *board) SearchIssues(ctx context.Context, teamKey, term string) ([]linear.Issue, error) {
	return nil, nil
}

// workers hands out canned results in order and records what it was asked
// to run.
type workers struct {
	results []workerResult
	modes   []string
	prompts []string
}

type workerResult struct {
	handoff any // marshaled to the handoff; a string is passed through raw
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

// tree reports the repository's uncommitted state. changeAfter makes a
// worker look like it wrote to the checkout it was told to read.
type tree struct {
	status      string
	calls       int
	changeAfter int
}

func (t *tree) Status(ctx context.Context, dir string) (string, error) {
	t.calls++
	if t.changeAfter > 0 && t.calls > t.changeAfter {
		return t.status + "?? scout-notes.md\n", nil
	}
	return t.status, nil
}

func scopingIssue() linear.Issue {
	return linear.Issue{
		ID:          "issue-1",
		Identifier:  "WND-9",
		Title:       "wand scope",
		Description: "A human wrote this.\n",
		TeamID:      "team",
		State:       linear.IssueState{Name: "Scoping", Type: "unstarted"},
	}
}

// harness wires the fakes to a real journal store in a temp directory. The
// store is real because the ordering guarantees this package makes are
// guarantees about what the journal ends up saying.
type harness struct {
	deps  scope.Deps
	board *board
	work  *workers
	tree  *tree
	store *journal.Store
	out   *strings.Builder
}

func newHarness(t *testing.T, results ...workerResult) *harness {
	t.Helper()
	b := &board{issue: scopingIssue()}
	w := &workers{results: results}
	tr := &tree{status: " M README.md\n"}
	store := journal.New(t.TempDir())
	out := &strings.Builder{}
	cov := covenant.Default()
	return &harness{
		board: b, work: w, tree: tr, store: store, out: out,
		deps: scope.Deps{
			Board:   b,
			Cov:     cov,
			Workers: w,
			Tree:    tr,
			Repo:    t.TempDir(),
			Harness: "claude-code",
			Out:     out,
		},
	}
}

func (h *harness) run(t *testing.T) (scope.Outcome, error) {
	t.Helper()
	return scope.Execute(context.Background(), h.deps, h.store, "WND-9")
}

func draftHandoff() map[string]any { return goodDraft() }

// The deliverables land in one order, and the status that advertises them
// lands last. A ticket in Needs Input promises a scope to read; every
// earlier write is part of that scope, and the comment precedes the
// estimate because a number nothing explains is worse than an argument
// with no number.
func TestScopeWritesInTheFixedOrder(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}
	if out.ExitCode() != scope.ExitScoped {
		t.Errorf("exit code = %d, want %d", out.ExitCode(), scope.ExitScoped)
	}

	want := []string{"section=plan", "comment", "estimate", "state=state-needs-input"}
	if strings.Join(h.board.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("writes = %v\nwant  %v", h.board.calls, want)
	}
	if h.board.estimate == nil || *h.board.estimate != 2 {
		t.Errorf("estimate = %v, want 2", h.board.estimate)
	}
	if !strings.Contains(h.board.description, "A human wrote this.") {
		t.Error("the human's description was lost")
	}
	if !strings.Contains(h.board.description, "Add the blocker check to Vet.") {
		t.Error("the plan is not in the description")
	}
	if len(h.board.comments) != 1 || !strings.Contains(h.board.comments[0], "Filter in Vet") {
		t.Errorf("comments = %v", h.board.comments)
	}

	// The journal is the record the sweeper and a human read afterwards.
	state, err := h.store.State(out.RunID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state.Outcome != journal.Converged {
		t.Errorf("journal outcome = %s, want converged", state.Outcome)
	}
	if state.Meta.Verb != "scope" {
		t.Errorf("journal verb = %q", state.Meta.Verb)
	}
}

// The one rule the whole package is built around: a handoff that fails
// validation writes nothing at all. Half a scope on a ticket is worse than
// none, because it reads like a whole one.
func TestAnInvalidHandoffWritesNothing(t *testing.T) {
	bad := draftHandoff()
	bad["recommendation"] = map[string]any{"approach": "Something Else Entirely", "why": "w"}
	h := newHarness(t, workerResult{handoff: bad})

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s (%s), want parked", out.Kind, out.Reason)
	}
	if len(h.board.calls) != 0 {
		t.Fatalf("the ticket was written to anyway: %v", h.board.calls)
	}
	if !strings.Contains(out.Reason, "not one of the approaches") {
		t.Errorf("the park does not say what was wrong with the handoff: %s", out.Reason)
	}
}

// A scout that never reported cannot be told from one that crashed, so the
// run parks rather than guessing.
func TestAWorkerFailureParks(t *testing.T) {
	h := newHarness(t, workerResult{err: fmt.Errorf("claude: exited 1 without a usable handoff")})

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked || len(h.board.calls) != 0 {
		t.Fatalf("outcome = %s, writes = %v; want a park with nothing written", out.Kind, h.board.calls)
	}
	if out.ExitCode() != scope.ExitParked {
		t.Errorf("exit code = %d, want %d", out.ExitCode(), scope.ExitParked)
	}
}

// A wrong premise is a hand-back, not a scope: the scout's own account
// reaches the ticket verbatim, the description is not touched, and the
// comment precedes the status move (verbs.Handback's rule, reused).
func TestAWrongPremiseHandsBackWithoutAPlan(t *testing.T) {
	h := newHarness(t, workerResult{handoff: map[string]any{
		"premise": "wrong",
		"reason":  "internal/linear/section.go already does this, and has since WND-4.",
	}})

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.HandedBack {
		t.Fatalf("outcome = %s (%s), want handed back", out.Kind, out.Reason)
	}
	if out.ExitCode() != scope.ExitHandedBack {
		t.Errorf("exit code = %d, want %d", out.ExitCode(), scope.ExitHandedBack)
	}
	want := []string{"comment", "state=state-needs-input"}
	if strings.Join(h.board.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("writes = %v\nwant  %v", h.board.calls, want)
	}
	if !strings.Contains(h.board.comments[0], "already does this, and has since WND-4") {
		t.Errorf("the scout's account was not quoted verbatim:\n%s", h.board.comments[0])
	}
	if h.board.estimate != nil {
		t.Error("a ticket with no plan was given an estimate")
	}
}

// The scout reads the checkout the command was run from, which is usually
// a person's. A change it made is not a mess in a directory this run owns:
// it is in somebody's working copy, and it goes in front of them rather
// than into a scope nobody knows was written over it.
func TestAWorkerThatTouchesTheCheckoutParks(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	h.tree.changeAfter = 1

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s (%s), want parked", out.Kind, out.Reason)
	}
	if len(h.board.calls) != 0 {
		t.Fatalf("the ticket was written to anyway: %v", h.board.calls)
	}
	if !strings.Contains(out.Reason, "changed the repository") {
		t.Errorf("the park does not say what happened: %s", out.Reason)
	}

	// The research is not lost with the run: it was journaled before the
	// tree was checked, which is why that order is what it is.
	records, err := h.store.Records(out.RunID)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	var kept bool
	for _, r := range records {
		if r.Kind == journal.KindNote && strings.Contains(string(r.Detail), "Filter in Vet") {
			kept = true
		}
	}
	if !kept {
		t.Error("the scout's handoff is not in the journal; the park threw the research away")
	}
}

// Blessing research is a human act. A ticket nobody moved into Scoping is
// not a ticket to scope, and refusing costs nothing: no run, no journal, no
// lock.
func TestScopeRefusesATicketNobodyBlessed(t *testing.T) {
	h := newHarness(t)
	h.board.issue.State = linear.IssueState{Name: "Backlog", Type: "backlog"}

	_, err := h.run(t)
	if err == nil {
		t.Fatal("a Backlog ticket was scoped")
	}
	if !strings.Contains(err.Error(), "not yours to scope") {
		t.Errorf("error = %v", err)
	}
	if ids, _ := h.store.List(); len(ids) != 0 {
		t.Errorf("a refused scope left a run behind: %v", ids)
	}
}

func TestScopeRefusesHumanOnlyWork(t *testing.T) {
	h := newHarness(t)
	h.board.issue.Labels = []string{"human-only"}

	_, err := h.run(t)
	if err == nil {
		t.Fatal("a human-only ticket was scoped")
	}
	if !strings.Contains(err.Error(), "human-only") {
		t.Errorf("error = %v", err)
	}
}

// A blocked ticket is exactly the one worth scoping early: the blocker
// stops the building, not the reading. This is the deliberate divergence
// from queue.Vet, and it is worth a test so nobody "fixes" it.
func TestScopeTakesABlockedTicket(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	h.board.issue.BlockedBy = []linear.Blocker{{Identifier: "WND-4", State: linear.IssueState{Name: "In Progress", Type: "started"}}}

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}
}

// Two scopes of one ticket write two plans into one fenced region and
// argue two recommendations at a human who cannot tell which the estimate
// belongs to. The ticket's status never moves while a scope works, so the
// board cannot be the mutex and the lock is.
func TestASecondScopeOfOneTicketRefuses(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	held, err := h.store.LockTicket("WND-9")
	if err != nil {
		t.Fatalf("LockTicket: %v", err)
	}
	defer held.Release()

	_, err = h.run(t)
	if err == nil {
		t.Fatal("a second scope of the same ticket started")
	}
	if !strings.Contains(err.Error(), "already being worked") {
		t.Errorf("error = %v", err)
	}
	if len(h.board.calls) != 0 {
		t.Errorf("the refused scope wrote to the ticket: %v", h.board.calls)
	}
}

// The plan landed and the argument for it did not. The ticket stays in
// Scoping — nothing advertises a scope that is not there — and the park
// says exactly what a human will find.
func TestAFailedCommentParksAfterThePlanLanded(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	h.board.failComment = true

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s, want parked", out.Kind)
	}
	if strings.Join(h.board.calls, ",") != "section=plan" {
		t.Fatalf("writes = %v, want the plan alone", h.board.calls)
	}
	if !strings.Contains(out.Reason, "plan is in the description") {
		t.Errorf("the park does not say what landed: %s", out.Reason)
	}
}

func TestAFailedEstimateParksBeforeTheStatusMove(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	h.board.failEstimate = true

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s, want parked", out.Kind)
	}
	if h.board.stateID != "" {
		t.Error("the ticket was moved to Needs Input with a deliverable missing")
	}
}

// The critic is a second cold process, and what it finds goes to a third:
// the session that wrote the draft is never asked to judge or repair it.
func TestTheCriticRunsWhenTheCovenantAsksAndItsFindingsAreRevised(t *testing.T) {
	revised := draftHandoff()
	revised["recommendation"] = map[string]any{"approach": "Filter in Build", "why": "The critic was right about Vet."}

	h := newHarness(t,
		workerResult{handoff: draftHandoff()},
		workerResult{handoff: map[string]any{"verdict": "flawed", "objections": []any{
			map[string]any{"target": "the recommendation", "summary": "Vet cannot see blockers", "consequence": "every scope of a blocked ticket is wrong"},
		}}},
		workerResult{handoff: revised},
	)
	h.deps.Cov.Toggles.ScopeCritic = true

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}
	if len(h.work.modes) != 3 {
		t.Fatalf("spawned %d workers (%v), want scout, critic, reviser", len(h.work.modes), h.work.modes)
	}
	if !strings.HasPrefix(h.work.modes[1], "critic") || !strings.HasPrefix(h.work.modes[2], "revise") {
		t.Errorf("phases = %v", h.work.modes)
	}
	// The reviser is handed the objection, not asked to imagine one.
	if !strings.Contains(h.work.prompts[2], "Vet cannot see blockers") {
		t.Error("the reviser was not given the critic's objection")
	}
	// What reaches the ticket is the revision, and the footer says so.
	if !strings.Contains(h.board.comments[0], "The critic was right about Vet.") {
		t.Error("the draft was written instead of the revision")
	}
	if !strings.Contains(h.board.comments[0], "1 objection(s)") {
		t.Errorf("the provenance does not report the critic:\n%s", h.board.comments[0])
	}
}

// A critic that found nothing costs one call and no revision: there is
// nothing to revise against, and a reviser given no objections is a
// session invited to change its mind about a draft nobody argued with.
func TestACriticThatFindsNothingSkipsTheRevision(t *testing.T) {
	h := newHarness(t,
		workerResult{handoff: draftHandoff()},
		workerResult{handoff: map[string]any{"verdict": "sound"}},
	)
	h.deps.Cov.Toggles.ScopeCritic = true

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}
	if len(h.work.modes) != 2 {
		t.Fatalf("spawned %d workers (%v), want scout and critic only", len(h.work.modes), h.work.modes)
	}
	if !strings.Contains(h.board.comments[0], "nothing stuck") {
		t.Errorf("the provenance does not report the critic:\n%s", h.board.comments[0])
	}
}

// The interview's answers go to a fresh session, never back to the one
// that wrote the draft: a session that has just argued for an approach
// defends it, and what comes back is the same plan with the objections
// explained away.
func TestTheInterviewsAnswersReachAFreshReviser(t *testing.T) {
	revised := draftHandoff()
	revised["understanding"] = "It is about the queue, not the vet pass."

	h := newHarness(t,
		workerResult{handoff: draftHandoff()},
		workerResult{handoff: revised},
	)
	h.deps.Interactive = true
	h.deps.In = strings.NewReader("it is about the queue, not the vet pass\n\n")

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}
	if len(h.work.modes) != 2 {
		t.Fatalf("spawned %d workers (%v), want scout and reviser", len(h.work.modes), h.work.modes)
	}
	if !strings.Contains(h.work.prompts[1], "it is about the queue, not the vet pass") {
		t.Error("the reviser was not given what the human said")
	}
	if !strings.Contains(h.board.comments[0], "1 answer(s)") {
		t.Errorf("the provenance does not report the interview:\n%s", h.board.comments[0])
	}
	if !strings.Contains(h.out.String(), "worst-consequence first") {
		t.Error("the human was never told what they were being asked")
	}
}

// Silence is approval. Spending a model call to be told nothing changed
// would only give a fresh session the chance to change something nobody
// asked it to.
func TestAnInterviewNobodyAnsweredSkipsTheRevision(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	h.deps.Interactive = true
	h.deps.In = strings.NewReader("\n\n\n\n\n\n\n\n")

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}
	if len(h.work.modes) != 1 {
		t.Fatalf("spawned %d workers (%v), want the scout alone", len(h.work.modes), h.work.modes)
	}
}

// The covenant says whether this repo's lifecycle has an interview at all;
// the flag says whether this invocation has a human. Passing the flag
// against a covenant that turned the stage off is a contradiction, and
// resolving it silently would mean one of the two was never load-bearing.
func TestInteractiveAgainstACovenantThatTurnedItOffRefuses(t *testing.T) {
	h := newHarness(t)
	h.deps.Interactive = true
	h.deps.In = strings.NewReader("")
	h.deps.Cov.Toggles.ScopeInterview = false

	if _, err := h.run(t); err == nil || !strings.Contains(err.Error(), "scope_interview") {
		t.Fatalf("error = %v, want a refusal naming the toggle", err)
	}
}

// A revision that fails validation must not fall back to the draft: the
// draft is the thing a human or a critic just argued with, and writing it
// anyway would write a scope over their objection.
func TestAnUnusableRevisionParksRatherThanKeepingTheDraft(t *testing.T) {
	h := newHarness(t,
		workerResult{handoff: draftHandoff()},
		workerResult{handoff: `{"premise":"sound"}`},
	)
	h.deps.Interactive = true
	h.deps.In = strings.NewReader("change the recommendation\n\n")

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s (%s), want parked", out.Kind, out.Reason)
	}
	if len(h.board.calls) != 0 {
		t.Fatalf("the draft was written over the human's objection: %v", h.board.calls)
	}
}

// The scout thought the ticket sound and the human knew better. The
// reviser's verdict is as terminal as the scout's would have been: writing
// a plan over it would write the scope the interview just argued out of
// existence.
func TestARevisionCanFindThePremiseWrong(t *testing.T) {
	h := newHarness(t,
		workerResult{handoff: draftHandoff()},
		workerResult{handoff: map[string]any{
			"premise": "wrong",
			"reason":  "You are right — this landed in WND-4 and the ticket predates it.",
		}},
	)
	h.deps.Interactive = true
	h.deps.In = strings.NewReader("this was already built\n\n")

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.HandedBack {
		t.Fatalf("outcome = %s (%s), want handed back", out.Kind, out.Reason)
	}
	want := []string{"comment", "state=state-needs-input"}
	if strings.Join(h.board.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("writes = %v\nwant  %v", h.board.calls, want)
	}
	if !strings.Contains(h.board.comments[0], "this landed in WND-4") {
		t.Errorf("the reviser's account was not quoted:\n%s", h.board.comments[0])
	}
}

// A premise the critic's reviser found wrong ends the run there. The
// interview must not follow it: a wrong-premise draft has no
// understanding, no approaches and no recommendation left in it, so the
// questions would quote blanks at a human and their answers would buy a
// revision round over a scope that has already been argued out of
// existence.
func TestACriticsRevisionThatFindsThePremiseWrongSkipsTheInterview(t *testing.T) {
	h := newHarness(t,
		workerResult{handoff: draftHandoff()},
		workerResult{handoff: map[string]any{"verdict": "flawed", "objections": []any{
			map[string]any{"target": "the premise", "summary": "this landed in WND-4", "consequence": "a sprint spent rebuilding it"},
		}}},
		workerResult{handoff: map[string]any{
			"premise": "wrong",
			"reason":  "The critic is right: WND-4 shipped this, and the ticket predates it.",
		}},
	)
	h.deps.Cov.Toggles.ScopeCritic = true
	h.deps.Interactive = true
	h.deps.In = strings.NewReader("")

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.HandedBack {
		t.Fatalf("outcome = %s (%s), want handed back", out.Kind, out.Reason)
	}
	if len(h.work.modes) != 3 {
		t.Fatalf("spawned %d workers (%v), want scout, critic, reviser and no more", len(h.work.modes), h.work.modes)
	}
	// No interview was held, so nothing was printed to ask about.
	if strings.Contains(h.out.String(), "question(s) about the draft") {
		t.Errorf("a human was grilled over a draft the reviser had already withdrawn:\n%s", h.out.String())
	}
	want := []string{"comment", "state=state-needs-input"}
	if strings.Join(h.board.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("writes = %v\nwant  %v", h.board.calls, want)
	}
	if !strings.Contains(h.board.comments[0], "WND-4 shipped this") {
		t.Errorf("the reviser's account was not quoted:\n%s", h.board.comments[0])
	}
}

// Every worker is told what run it is in rather than left to work it out,
// and a scope tells them there is no workspace — the thing a harness's own
// defaults would otherwise let it assume.
func TestWorkersAreToldTheRunIsReadOnly(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	if _, err := h.run(t); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(h.work.modes[0], "read-only") {
		t.Errorf("the scout's mode does not say the run is read-only: %q", h.work.modes[0])
	}
}

// The estimate scale reaches the scout, so it produces a number the
// validator will accept instead of learning the scale by being refused.
func TestTheScoutIsToldTheTeamsEstimateScale(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	h.deps.Cov.IssueEstimationType = "exponential"
	// The default draft's estimate of 2 is on the exponential scale too.
	if _, err := h.run(t); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(h.work.prompts[0], "1, 2, 4, 8, 16") {
		t.Errorf("the scout was not told the scale it must estimate on")
	}
}

// A run interrupted mid-phase parks quoting the signal. "Context canceled"
// tells the next reader nothing about whether a person pressed ctrl-c or a
// supervisor cycled the host.
func TestAnInterruptParksWithItsOwnReason(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(fmt.Errorf("interrupted by terminated"))

	out, err := scope.Execute(ctx, h.deps, h.store, "WND-9")
	if err != nil {
		// The read of the ticket itself may fail first with a real client;
		// the fake board does not check the context, so the run gets far
		// enough to park.
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked || !strings.Contains(out.Reason, "interrupted by terminated") {
		t.Fatalf("outcome = %s (%s), want a park quoting the signal", out.Kind, out.Reason)
	}
	if len(h.board.calls) != 0 {
		t.Errorf("an interrupted run still wrote to the ticket: %v", h.board.calls)
	}
}
