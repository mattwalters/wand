package plan_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/plan"
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
	labels      []string

	// existingComments is what IssueComments returns — the ticket's own
	// comment history, scripted by a re-plan test rather than written by
	// this run. Distinct from comments, which is what this run itself
	// posts.
	existingComments []linear.Comment

	failComment  bool
	failEstimate bool
	failState    bool
	failLabel    bool

	// claimed marks whether the first StateID write — plan.Execute's claim
	// into In Planning, mirroring run's claim-before-filesystem ordering —
	// has already happened. It is logged distinctly ("claim:state=...")
	// so the many tests asserting the *rest* of a run's writes do not have
	// to repeat the claim in every want list; TestPlanClaimsInPlanning
	// checks the claim itself.
	claimed bool
}

func (b *board) log(call string) { b.calls = append(b.calls, call) }

func (b *board) IssueByIdentifier(ctx context.Context, identifier string) (linear.Issue, error) {
	return b.issue, nil
}

func (b *board) IssueComments(ctx context.Context, issueID string) ([]linear.Comment, error) {
	return b.existingComments, nil
}

func (b *board) TeamStates(ctx context.Context, teamID string) ([]linear.WorkflowState, error) {
	return []linear.WorkflowState{
		{ID: "state-needs-input", Name: "Needs Input", Type: "unstarted"},
		{ID: "state-to-plan", Name: "To Plan", Type: "unstarted"},
		{ID: "state-in-planning", Name: "In Planning", Type: "started"},
		{ID: "state-plan-review", Name: "Plan Review", Type: "unstarted"},
	}, nil
}

func (b *board) CreateComment(ctx context.Context, issueID, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.failComment {
		return fmt.Errorf("linear is down")
	}
	// The park report is a write about the run, not about the plan. It is
	// logged apart so the many "a park writes nothing to the ticket" tests
	// can keep asserting what they have always meant — that no *plan*
	// reached the ticket — now that a park also explains itself there.
	if strings.HasPrefix(body, parkedCommentPrefix) {
		b.log("park:comment")
	} else {
		b.log("comment")
	}
	b.comments = append(b.comments, body)
	return nil
}

// parkedCommentPrefix is how verbs.ParkedComment opens. Asserted in
// TestAParkIsReportedOnTheTicket, so a reworded report fails there loudly
// rather than quietly reclassifying every write in this file.
const parkedCommentPrefix = "**This run parked.**"

// deliverables is everything the scout wrote as a plan — plan, options,
// estimate, status — with the park report and the entry claim filtered out.
func (b *board) deliverables() []string {
	var out []string
	for _, c := range b.calls {
		if strings.HasPrefix(c, "park:") || strings.HasPrefix(c, "claim:") {
			continue
		}
		out = append(out, c)
	}
	return out
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
		// The very first status write of a run is plan.Execute's claim
		// into In Planning — logged distinctly so it reads apart from the
		// terminal status move a run may make later, the same ticket's
		// two status writes otherwise being indistinguishable in this log.
		if !b.claimed {
			b.claimed = true
			b.log("claim:state=" + u.StateID)
			b.stateID = u.StateID
			return nil
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
// verbs.Handback. A plan run never files or assigns.
func (b *board) Viewer(ctx context.Context) (linear.User, error) {
	return linear.User{ID: "u", Name: "Key Holder"}, nil
}
func (b *board) TeamByKey(ctx context.Context, key string) (linear.Team, error) {
	return linear.Team{ID: "team", Key: key}, nil
}
func (b *board) LabelByName(ctx context.Context, name string) (linear.Label, bool, error) {
	return linear.Label{ID: "label-" + name, Name: name}, true, nil
}
func (b *board) AddLabel(ctx context.Context, issueID, labelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.failLabel {
		return fmt.Errorf("linear is down")
	}
	b.log("park:label")
	b.labels = append(b.labels, labelID)
	return nil
}

// RemoveLabel is a re-plan's claim: verbs.ClaimForReplanning removes the
// re-plan label instead of writing a status, since the ticket is already
// In Planning. Logged with the same "claim:" prefix the first-plan status
// write gets, and for the same reason: the many tests asserting the *rest*
// of a run's writes should not have to repeat the claim in every want list.
func (b *board) RemoveLabel(ctx context.Context, issueID, labelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.failLabel {
		return fmt.Errorf("linear is down")
	}
	b.log("claim:unlabel=" + labelID)
	b.claimed = true
	return nil
}
func (b *board) CreateIssue(ctx context.Context, in linear.IssueCreate) (linear.Issue, error) {
	return linear.Issue{}, fmt.Errorf("a plan run files nothing")
}
func (b *board) SearchIssues(ctx context.Context, teamKey, term string) ([]linear.Issue, error) {
	return nil, nil
}

// workers hands out canned results in order and records what it was asked
// to run.
type workers struct {
	results  []workerResult
	modes    []string
	prompts  []string
	timeouts []time.Duration
}

type workerResult struct {
	handoff any // marshaled to the handoff; a string is passed through raw
	err     error
	usage   *worker.Usage
	// transient is what a TransienceAdapter would have said about this
	// failure — worker.Run sets Result.Transient on the way past, so a
	// fake that always left it false could not test either side of the
	// retry decision.
	transient bool
}

func (w *workers) Run(ctx context.Context, spec worker.Spec) (worker.Result, error) {
	w.modes = append(w.modes, spec.Mode)
	w.prompts = append(w.prompts, spec.Prompt)
	w.timeouts = append(w.timeouts, spec.Timeout)
	if len(w.results) == 0 {
		return worker.Result{}, fmt.Errorf("the loop spawned more workers than the test scripted")
	}
	next := w.results[0]
	w.results = w.results[1:]
	if next.err != nil {
		return worker.Result{ExitCode: 1, Transient: next.transient}, next.err
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
	return worker.Result{Handoff: raw, Usage: next.usage}, nil
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

func toPlanIssue() linear.Issue {
	return linear.Issue{
		ID:          "issue-1",
		Identifier:  "WND-9",
		Title:       "wand plan",
		Description: "A human wrote this.\n",
		TeamID:      "team",
		State:       linear.IssueState{Name: "To Plan", Type: "unstarted"},
	}
}

// harness wires the fakes to a real journal store in a temp directory. The
// store is real because the ordering guarantees this package makes are
// guarantees about what the journal ends up saying.
type harness struct {
	deps  plan.Deps
	board *board
	work  *workers
	tree  *tree
	store *journal.Store
	out   *strings.Builder
}

func newHarness(t *testing.T, results ...workerResult) *harness {
	t.Helper()
	b := &board{issue: toPlanIssue()}
	w := &workers{results: results}
	tr := &tree{status: " M README.md\n"}
	store := journal.New(t.TempDir())
	out := &strings.Builder{}
	// The critic defaults on in the stock covenant (WND-81), but most of
	// this suite is about behavior the critic is orthogonal to — write
	// order, retries, the interview — and scripts exactly the workers each
	// test needs. Tests of the critic itself turn the toggle back on.
	cov := covenant.Default()
	cov.Toggles.PlanCritic = false
	return &harness{
		board: b, work: w, tree: tr, store: store, out: out,
		deps: plan.Deps{
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

func (h *harness) run(t *testing.T) (plan.Outcome, error) {
	t.Helper()
	return plan.Execute(context.Background(), h.deps, h.store, "WND-9")
}

func draftHandoff() map[string]any { return goodDraft() }

// The deliverables land in one order, and the status that advertises them
// lands last. A ticket in Plan Review promises a finished plan to judge;
// every earlier write is part of that plan, and the comment precedes the
// estimate because a number nothing explains is worse than an argument
// with no number.
func TestPlanWritesInTheFixedOrder(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}
	if out.ExitCode() != plan.ExitPlanReviewed {
		t.Errorf("exit code = %d, want %d", out.ExitCode(), plan.ExitPlanReviewed)
	}

	want := []string{"claim:state=state-in-planning", "section=plan", "comment", "estimate", "state=state-plan-review"}
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
	if state.Meta.Verb != "plan" {
		t.Errorf("journal verb = %q", state.Meta.Verb)
	}
}

// planPhaseDetail is the subset of the plan package's own (unexported)
// phaseDetail this test needs — the journal's Detail field is opaque by
// design (internal/journal), so an external test reads it back the same
// way any other consumer (wand stats, the UI usage panel) would.
type planPhaseDetail struct {
	Harness   string `json:"harness"`
	Model     string `json:"model"`
	WallClock string `json:"wall_clock"`
	TokensIn  *int64 `json:"tokens_in"`
	TokensOut *int64 `json:"tokens_out"`
}

// The journal's phase.ended records carry the operational metrics ledger —
// harness, model, wall-clock, tokens — sourced from Deps and the worker's
// own report, with tokens left absent (never faked) when the worker
// reports none.
func TestPhaseDetailCarriesOperationalMetrics(t *testing.T) {
	in, out := int64(200), int64(50)
	h := newHarness(t, workerResult{handoff: draftHandoff(), usage: &worker.Usage{InputTokens: &in, OutputTokens: &out}})

	scopeOut, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if scopeOut.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", scopeOut.Kind, scopeOut.Reason)
	}

	records, err := h.store.Records(scopeOut.RunID)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	var found bool
	for _, r := range records {
		if r.Kind != journal.KindPhaseEnded {
			continue
		}
		var d planPhaseDetail
		if err := json.Unmarshal(r.Detail, &d); err != nil {
			t.Fatalf("unmarshaling phase.ended detail: %v", err)
		}
		found = true
		if d.Harness != "claude-code" {
			t.Errorf("Harness = %q, want claude-code", d.Harness)
		}
		if d.WallClock == "" {
			t.Error("WallClock is empty")
		}
		if d.TokensIn == nil || *d.TokensIn != 200 {
			t.Errorf("TokensIn = %v, want 200", d.TokensIn)
		}
		if d.TokensOut == nil || *d.TokensOut != 50 {
			t.Errorf("TokensOut = %v, want 50", d.TokensOut)
		}
	}
	if !found {
		t.Fatal("no phase.ended record found")
	}
}

// The one rule the whole package is built around: a handoff that fails
// validation writes nothing at all. Half a plan on a ticket is worse than
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
	if len(h.board.deliverables()) != 0 {
		t.Fatalf("the ticket was written to anyway: %v", h.board.deliverables())
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
	if out.Kind != journal.Parked || len(h.board.deliverables()) != 0 {
		t.Fatalf("outcome = %s, writes = %v; want a park with no plan written", out.Kind, h.board.deliverables())
	}
	if out.ExitCode() != plan.ExitParked {
		t.Errorf("exit code = %d, want %d", out.ExitCode(), plan.ExitParked)
	}
}

// A wrong premise is a hand-back, not a plan: the scout's own account
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
	if out.ExitCode() != plan.ExitHandedBack {
		t.Errorf("exit code = %d, want %d", out.ExitCode(), plan.ExitHandedBack)
	}
	want := []string{"claim:state=state-in-planning", "comment", "state=state-needs-input"}
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
// than into a plan nobody knows was written over it.
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
	if len(h.board.deliverables()) != 0 {
		t.Fatalf("the ticket was written to anyway: %v", h.board.deliverables())
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

// Blessing research is a human act. A ticket nobody moved into To Plan is
// not a ticket to plan, and refusing costs nothing: no run, no journal, no
// claim.
func TestPlanRefusesATicketNobodyBlessed(t *testing.T) {
	h := newHarness(t)
	h.board.issue.State = linear.IssueState{Name: "Backlog", Type: "backlog"}

	_, err := h.run(t)
	if err == nil {
		t.Fatal("a Backlog ticket was planned")
	}
	if !strings.Contains(err.Error(), "not yours to plan") {
		t.Errorf("error = %v", err)
	}
	if ids, _ := h.store.List(); len(ids) != 0 {
		t.Errorf("a refused plan left a run behind: %v", ids)
	}
}

func TestPlanRefusesHumanOnlyWork(t *testing.T) {
	h := newHarness(t)
	h.board.issue.Labels = []string{"human-only"}

	_, err := h.run(t)
	if err == nil {
		t.Fatal("a human-only ticket was planned")
	}
	if !strings.Contains(err.Error(), "human-only") {
		t.Errorf("error = %v", err)
	}
}

// A blocked ticket is exactly the one worth planning early: the blocker
// stops the building, not the reading. This is the deliberate divergence
// from queue.Vet, and it is worth a test so nobody "fixes" it.
func TestPlanTakesABlockedTicket(t *testing.T) {
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

// Two plan runs over one ticket write two plans into one fenced region and
// argue two recommendations at a human who cannot tell which the estimate
// belongs to. The claim is what stops the second one now: the first run's
// claim moves the ticket out of To Plan and into In Planning, so a second
// run finds it no longer blessed for a *fresh* plan — the same status-is-
// the-mutex mechanism `run` uses for Todo, given to research now that
// In Planning exists for it to move into (WND-79; this package used to take
// an explicit per-ticket lock instead, since removed).
func TestASecondPlanOfOneTicketRefuses(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	h.board.issue.State = linear.IssueState{Name: "In Planning", Type: "started"} // another run already claimed it

	_, err := h.run(t)
	if err == nil {
		t.Fatal("a second plan run of the already-claimed ticket started")
	}
	if !strings.Contains(err.Error(), "not yours to plan") {
		t.Errorf("error = %v", err)
	}
	if len(h.board.calls) != 0 {
		t.Errorf("the refused plan run wrote to the ticket: %v", h.board.calls)
	}
}

// A re-plan is a legitimate, deliberate human act — moving a Plan Review
// ticket back to To Plan asks for a fresh look, and that has to keep
// working. What it must not do is destroy the plan already there: render.go says every
// plan run rewrites the plan region whole, so the previous plan is posted as a
// comment — before the region that held it is overwritten — or it survives
// nowhere but a closed PR.
func TestAReplanPreservesThePriorPlanAsACommentBeforeReplacingIt(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	priorPlan := "## Implementation plan\n\n**Exempt the plan verb.** This is the plan an earlier plan run wrote and a human already read.\n"
	desc, err := linear.WithSection(h.board.issue.Description, plan.PlanSectionID, priorPlan)
	if err != nil {
		t.Fatalf("WithSection: %v", err)
	}
	h.board.issue.Description = desc

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}

	// The prior plan is safe on the ticket before the write that would
	// destroy it runs, not after.
	want := []string{"claim:state=state-in-planning", "comment", "section=plan", "comment", "estimate", "state=state-plan-review"}
	if strings.Join(h.board.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("writes = %v\nwant  %v", h.board.calls, want)
	}

	if len(h.board.comments) != 2 {
		t.Fatalf("comments = %v, want the supersede comment and the options comment", h.board.comments)
	}
	supersede := h.board.comments[0]
	for _, want := range []string{"superseded", "Exempt the plan verb", "already read"} {
		if !strings.Contains(supersede, want) {
			t.Errorf("the supersede comment does not carry %q:\n%s", want, supersede)
		}
	}
	if !strings.Contains(h.board.comments[1], "Filter in Vet") {
		t.Errorf("the second comment is not the new options comment:\n%s", h.board.comments[1])
	}

	if !strings.Contains(h.board.description, "Add the blocker check to Vet.") {
		t.Error("the new plan is not in the description")
	}
	if strings.Contains(h.board.description, "Exempt the plan verb") {
		t.Error("the old plan was left in the description instead of being replaced")
	}
}

// A ticket planned for the first time has no prior plan to lose, so nothing
// is posted about one — the comment that follows the plan is the options
// comment alone, the same shape [TestPlanWritesInTheFixedOrder] already
// asserts.
func TestAFirstPlanPostsNoSupersedeComment(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})

	if _, err := h.run(t); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(h.board.comments) != 1 {
		t.Fatalf("comments = %v, want exactly the options comment", h.board.comments)
	}
	if strings.Contains(h.board.comments[0], "superseded") {
		t.Errorf("a ticket with no prior plan still posted a supersede comment:\n%s", h.board.comments[0])
	}
}

// A comment failure while preserving the prior plan must leave the
// description untouched: the whole point is that the old plan is safe
// before the new one can overwrite it, so a failure here must stop before
// the overwrite, not after it.
func TestAFailedPriorPlanCommentParksBeforeTheDescriptionIsTouched(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	desc, err := linear.WithSection(h.board.issue.Description, plan.PlanSectionID, "## Implementation plan\n\nAn earlier plan.\n")
	if err != nil {
		t.Fatalf("WithSection: %v", err)
	}
	h.board.issue.Description = desc
	h.board.failComment = true

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s, want parked", out.Kind)
	}
	if len(h.board.calls) != 1 || h.board.calls[0] != "claim:state=state-in-planning" {
		t.Errorf("the ticket was written to before the prior plan could be preserved: %v", h.board.calls)
	}
	if h.board.description != "" {
		t.Errorf("the description was overwritten despite the preserving comment failing: %q", h.board.description)
	}
}

// The plan landed and the argument for it did not. The ticket stays in
// In Planning — nothing advertises a plan that is not there — and the park
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
	if strings.Join(h.board.calls, ",") != "claim:state=state-in-planning,section=plan" {
		t.Fatalf("writes = %v, want the claim then the plan alone", h.board.calls)
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
	if h.board.stateID != "state-in-planning" {
		t.Error("the ticket was moved to Plan Review with a deliverable missing")
	}
}

// The critic is a second cold process, and what it finds goes to a third:
// the session that wrote the draft is never asked to judge or repair it.
func TestTheCriticRunsWhenTheCovenantAsksAndItsFindingsAreRevised(t *testing.T) {
	revised := draftHandoff()
	revised["recommendation"] = map[string]any{"approach": "Filter in Build", "why": "The critic was right about Vet."}
	revised["resolutions"] = []any{
		map[string]any{"resolved": true, "explanation": "Switched the recommendation to Filter in Build, which does see blockers."},
	}

	h := newHarness(t,
		workerResult{handoff: draftHandoff()},
		workerResult{handoff: map[string]any{"verdict": "flawed", "objections": []any{
			map[string]any{"target": "the recommendation", "summary": "Vet cannot see blockers", "consequence": "every plan over a blocked ticket is wrong"},
		}}},
		workerResult{handoff: revised},
	)
	h.deps.Cov.Toggles.PlanCritic = true

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
	// The resolved objection is named in the comment as what was
	// challenged and what changed — the reasoning trail, not just a count.
	if !strings.Contains(h.board.comments[0], "What the critic challenged") ||
		!strings.Contains(h.board.comments[0], "Vet cannot see blockers") ||
		!strings.Contains(h.board.comments[0], "Switched the recommendation to Filter in Build") {
		t.Errorf("the comment does not name what was challenged and what changed:\n%s", h.board.comments[0])
	}
}

// An objection the reviser could not resolve does not disappear into the
// revision the way a resolved one does — it is promoted to an open
// question on the review comment, exactly the material a human is asked to
// judge at Plan Review.
func TestAnUnresolvedObjectionBecomesAnOpenQuestion(t *testing.T) {
	revised := draftHandoff()
	revised["resolutions"] = []any{
		map[string]any{"resolved": false, "explanation": "Both approaches still need a lock I could not confirm is safe to drop."},
	}

	h := newHarness(t,
		workerResult{handoff: draftHandoff()},
		workerResult{handoff: map[string]any{"verdict": "flawed", "objections": []any{
			map[string]any{"target": "the plan", "summary": "dropping the lock may race", "consequence": "two runs could claim the same ticket"},
		}}},
		workerResult{handoff: revised},
	)
	h.deps.Cov.Toggles.PlanCritic = true

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}
	if strings.Contains(h.board.comments[0], "What the critic challenged") {
		t.Errorf("nothing was resolved, so there should be nothing in the challenged section:\n%s", h.board.comments[0])
	}
	if !strings.Contains(h.board.comments[0], "Still open") ||
		!strings.Contains(h.board.comments[0], "dropping the lock may race") ||
		!strings.Contains(h.board.comments[0], "Both approaches still need a lock I could not confirm is safe to drop") {
		t.Errorf("the unresolved objection was not promoted to an open question:\n%s", h.board.comments[0])
	}
	if !strings.Contains(h.board.comments[0], "1 objection(s): 0 resolved, 1 open") {
		t.Errorf("the provenance does not report the unresolved objection:\n%s", h.board.comments[0])
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
	h.deps.Cov.Toggles.PlanCritic = true

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

// A configured per-phase override reaches the worker spec for that phase
// alone; scout keeps the global WorkerTimeout when only critic is
// configured. This is the regression the phase-timeout table exists to
// catch: an override configured but never read, or read by the wrong phase.
func TestPhaseTimeoutOverrideReachesWorkerSpec(t *testing.T) {
	h := newHarness(t,
		workerResult{handoff: draftHandoff()},
		workerResult{handoff: map[string]any{"verdict": "sound"}},
	)
	h.deps.Cov.Toggles.PlanCritic = true
	h.deps.Cov.Caps.PhaseTimeouts = map[string]time.Duration{"critic": 5 * time.Minute}

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}
	if len(h.work.timeouts) != 2 {
		t.Fatalf("spawned %d workers, want scout and critic only", len(h.work.timeouts))
	}
	if h.work.timeouts[0] != h.deps.Cov.Caps.WorkerTimeout {
		t.Errorf("scout timeout %v, want the unconfigured global %v", h.work.timeouts[0], h.deps.Cov.Caps.WorkerTimeout)
	}
	if h.work.timeouts[1] != 5*time.Minute {
		t.Errorf("critic timeout %v, want the configured override %v", h.work.timeouts[1], 5*time.Minute)
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
	h.deps.Cov.Toggles.PlanInterview = false

	if _, err := h.run(t); err == nil || !strings.Contains(err.Error(), "plan_interview") {
		t.Fatalf("error = %v, want a refusal naming the toggle", err)
	}
}

// A revision that fails validation must not fall back to the draft: the
// draft is the thing a human or a critic just argued with, and writing it
// anyway would write a plan over their objection.
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
	if len(h.board.deliverables()) != 0 {
		t.Fatalf("the draft was written over the human's objection: %v", h.board.deliverables())
	}
}

// The scout thought the ticket sound and the human knew better. The
// reviser's verdict is as terminal as the scout's would have been: writing
// a plan over it would write the plan the interview just argued out of
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
	want := []string{"claim:state=state-in-planning", "comment", "state=state-needs-input"}
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
// revision round over a plan that has already been argued out of
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
	h.deps.Cov.Toggles.PlanCritic = true
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
	want := []string{"claim:state=state-in-planning", "comment", "state=state-needs-input"}
	if strings.Join(h.board.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("writes = %v\nwant  %v", h.board.calls, want)
	}
	if !strings.Contains(h.board.comments[0], "WND-4 shipped this") {
		t.Errorf("the reviser's account was not quoted:\n%s", h.board.comments[0])
	}
}

// Every worker is told what run it is in rather than left to work it out,
// and a plan run tells them there is no workspace — the thing a harness's own
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

	out, err := plan.Execute(ctx, h.deps, h.store, "WND-9")
	if err != nil {
		// The read of the ticket itself may fail first with a real client;
		// the fake board does not check the context, so the run gets far
		// enough to park.
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked || !strings.Contains(out.Reason, "interrupted by terminated") {
		t.Fatalf("outcome = %s (%s), want a park quoting the signal", out.Kind, out.Reason)
	}
	if len(h.board.deliverables()) != 0 {
		t.Errorf("an interrupted run still wrote a plan to the ticket: %v", h.board.deliverables())
	}
}

// WND-69. Seventeen of the twenty-four parks in the reference journal were
// scopes rejected at the handoff gate. Under the topology this package
// shipped with then, every one of them left the ticket exactly as it found
// it — sitting in Scoping with no sign a run had ever happened, so the next
// dispatch pass picked it up and paid for the same failure again. WND-79
// changes what "as it found it" means: the claim into In Planning happens
// before the worker runs, so a park now leaves the ticket visibly claimed
// (not reverted to To Plan) in addition to the label and the comment — the
// same shape a parked build run leaves In Progress in.
func TestAParkIsReportedOnTheTicket(t *testing.T) {
	h := newHarness(t, workerResult{err: fmt.Errorf("claude: exited 1 without a usable handoff")})

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s (%s), want parked", out.Kind, out.Reason)
	}
	if got := strings.Join(h.board.calls, ","); got != "claim:state=state-in-planning,park:comment,park:label" {
		t.Fatalf("board calls = %q, want the claim then the park report", got)
	}
	if c := h.board.comments[0]; !strings.Contains(c, "without a usable handoff") {
		t.Errorf("the report does not quote the reason:\n%s", c)
	}
	// The ticket stays claimed (In Planning) where the run left it; a plan
	// run never advances its own status past the claim on a park.
	if h.board.stateID != "state-in-planning" {
		t.Errorf("a parked plan run's ticket status = %q, want it to stay claimed", h.board.stateID)
	}
}

// The interrupt is the most common park there is, and it arrives as a
// canceled context — the very one every Linear call would ride. The report
// runs on a context that outlives it, so a run being torn down still gets
// to say why.
func TestAnInterruptStillReportsThePark(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(fmt.Errorf("interrupted by terminated"))

	out, err := plan.Execute(ctx, h.deps, h.store, "WND-9")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s (%s), want parked", out.Kind, out.Reason)
	}
	if got := strings.Join(h.board.calls, ","); got != "claim:state=state-in-planning,park:comment,park:label" {
		t.Fatalf("an interrupted run could not report its park: calls = %q", got)
	}
	if c := h.board.comments[0]; !strings.Contains(c, "interrupted by terminated") {
		t.Errorf("the report does not name the signal:\n%s", c)
	}
}

// The journal is the ending; the ticket gets a courtesy copy. A board that
// refuses it must not turn one park into two.
func TestAParkThatCannotReachLinearIsStillOnePark(t *testing.T) {
	h := newHarness(t, workerResult{err: fmt.Errorf("claude: exited 1 without a usable handoff")})
	h.board.failComment = true

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked || !strings.Contains(out.Reason, "without a usable handoff") {
		t.Fatalf("outcome = %s (%s), want the park to survive a refused report", out.Kind, out.Reason)
	}
	if len(h.board.labels) != 0 {
		t.Errorf("the label went on without an explanation beside it: %v", h.board.labels)
	}
}

// A rejected handoff survives the park. The worker's own copy is gone by
// the time validation runs — worker.collect deletes it immediately after
// reading — so before this the bytes a validator refused existed nowhere:
// six minutes and 1.7M tokens of research, unrecoverable, with only the
// validator's one-line complaint left to explain it.
//
// Keeping them is what makes a park diagnosable rather than merely
// reported. The reason names what was wrong; only the bytes let anyone
// judge whether the validator or the scout was.
//
// This is a table over every phase that can reject a handoff — scout,
// critic, reviser, and replan — not just the scout. WND-95 was exactly
// this test covering one phase and missing that the critic's own gate
// (plan.go's critique, not parse/parseRevision/parseReplan) never persisted
// at all; a per-phase table is what would have caught it, a second
// single-phase test would not.
func TestARejectedHandoffIsKeptForTheHuman(t *testing.T) {
	cases := []struct {
		name     string
		wantFile string
		setup    func(t *testing.T) (*harness, map[string]any)
	}{
		{
			name:     "scout",
			wantFile: "scout-1.rejected.json",
			setup: func(t *testing.T) (*harness, map[string]any) {
				bad := goodDraft()
				delete(bad, "approaches") // structural: still fatal, still discarded
				return newHarness(t, workerResult{handoff: bad}), bad
			},
		},
		{
			name:     "critic",
			wantFile: "critic-1.rejected.json",
			setup: func(t *testing.T) (*harness, map[string]any) {
				// The exact shape WND-45 parked on: an objection field the
				// schema does not know, refused by strictUnmarshal.
				bad := map[string]any{"verdict": "flawed", "objections": []any{
					map[string]any{"target": "the recommendation", "summary": "Vet cannot see blockers",
						"consequence": "every plan over a blocked ticket is wrong", "failure_scenario": "bogus extra field"},
				}}
				h := newHarness(t, workerResult{handoff: draftHandoff()}, workerResult{handoff: bad})
				h.deps.Cov.Toggles.PlanCritic = true
				return h, bad
			},
		},
		{
			name:     "reviser",
			wantFile: "revise-1.rejected.json",
			setup: func(t *testing.T) (*harness, map[string]any) {
				bad := goodDraft()
				delete(bad, "approaches") // structural: still fatal, still discarded
				h := newHarness(t,
					workerResult{handoff: draftHandoff()},
					workerResult{handoff: map[string]any{"verdict": "flawed", "objections": []any{
						map[string]any{"target": "the recommendation", "summary": "Vet cannot see blockers", "consequence": "every plan over a blocked ticket is wrong"},
					}}},
					workerResult{handoff: bad},
				)
				h.deps.Cov.Toggles.PlanCritic = true
				return h, bad
			},
		},
		{
			name:     "replan",
			wantFile: "replan-1.rejected.json",
			setup: func(t *testing.T) (*harness, map[string]any) {
				bad := goodDraft()
				delete(bad, "approaches") // structural: still fatal, still discarded
				h := newHarness(t, workerResult{handoff: bad})
				h.board.issue = rePlanIssue(t)
				h.board.existingComments = []linear.Comment{
					{Author: "wand", CreatedAt: time.Now().Add(-time.Hour),
						Body: plan.OptionsComment(parsedGoodDraft(t), "fibonacci", "Plan Review", plan.Provenance{RunID: "r-1", Harness: "claude-code"})},
					{Author: "Matt Walters", CreatedAt: time.Now(),
						Body: "Vet already carries too much; put the filter in Build instead."},
				}
				return h, bad
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, bad := tc.setup(t)

			out, err := h.run(t)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if out.Kind != journal.Parked {
				t.Fatalf("outcome = %s (%s), want parked", out.Kind, out.Reason)
			}

			path := filepath.Join(h.store.Dir(out.RunID), "scratch", tc.wantFile)
			kept, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("the rejected handoff was not kept: %v", rerr)
			}
			// The bytes, not a summary of them: a re-render loses whatever
			// made the validator refuse it. Round-trip both sides through
			// JSON so map key order and Go type quirks do not matter.
			var got, want map[string]any
			if jerr := json.Unmarshal(kept, &got); jerr != nil {
				t.Fatalf("what was kept is not the handoff's own JSON: %v", jerr)
			}
			wantBytes, werr := json.Marshal(bad)
			if werr != nil {
				t.Fatalf("json.Marshal(bad): %v", werr)
			}
			if jerr := json.Unmarshal(wantBytes, &want); jerr != nil {
				t.Fatalf("json.Unmarshal(wantBytes): %v", jerr)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the kept handoff is not the one the worker wrote:\ngot:  %s\nwant: %s", kept, wantBytes)
			}
		})
	}
}

// --- transient scout failures ---------------------------------------------
//
// A scout costs a whole model call and produces nothing at all until it
// hands off, so a provider error is the most expensive possible thing to
// mistake for a verdict. See internal/worker for the captured harness
// output this models.

var transientErr = errors.New("worker: claude-code exited 1 without a usable handoff: no handoff file was written")

// notes returns every journal note message the run wrote, in order.
func (h *harness) notes(t *testing.T, id string) []string {
	t.Helper()
	records, err := h.store.Records(id)
	if err != nil {
		t.Fatalf("reading the journal: %v", err)
	}
	var out []string
	for _, r := range records {
		if r.Kind == journal.KindNote {
			out = append(out, r.Reason)
		}
	}
	return out
}

func (h *harness) phaseStarts(t *testing.T, id string) int {
	t.Helper()
	records, err := h.store.Records(id)
	if err != nil {
		t.Fatalf("reading the journal: %v", err)
	}
	n := 0
	for _, r := range records {
		if r.Kind == journal.KindPhaseStarted {
			n++
		}
	}
	return n
}

func TestScoutRetriesATransientFailureAndPlans(t *testing.T) {
	h := newHarness(t,
		workerResult{err: transientErr, transient: true},
		workerResult{handoff: draftHandoff()},
	)
	h.deps.Cov.Caps.WorkerRetries = 1

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged — a provider error is not a research finding", out.Kind, out.Reason)
	}
	if got := h.phaseStarts(t, out.RunID); got != 2 {
		t.Errorf("%d phase starts, want 2 — both attempts belong in the journal", got)
	}
	if notes := h.notes(t, out.RunID); !hasSubstring(notes, "retrying") {
		t.Errorf("notes %v carry no record of why the scout ran twice", notes)
	}
}

func TestScoutParksOnceRetriesAreSpent(t *testing.T) {
	h := newHarness(t,
		workerResult{err: transientErr, transient: true},
		workerResult{err: transientErr, transient: true},
	)
	h.deps.Cov.Caps.WorkerRetries = 1

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s (%s), want parked", out.Kind, out.Reason)
	}
	if !strings.Contains(out.Reason, "no handoff file was written") {
		t.Errorf("park reason %q dropped the worker's own error", out.Reason)
	}
	if notes := h.notes(t, out.RunID); !hasSubstring(notes, "out of retries") {
		t.Errorf("notes %v do not say the budget ran out", notes)
	}
}

func TestScoutDoesNotRetryANonTransientFailure(t *testing.T) {
	h := newHarness(t, workerResult{err: transientErr})
	h.deps.Cov.Caps.WorkerRetries = 3

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s (%s), want parked", out.Kind, out.Reason)
	}
	if got := h.phaseStarts(t, out.RunID); got != 1 {
		t.Errorf("%d phase starts, want 1 — a failure that might be the research gets no second try", got)
	}
}

// s.d.Repo is usually a person's own checkout, not a worktree this run
// owns. A scout that changed it on its way down is about to have that
// change handed back to its owner by requireUntouched; respawning a second
// scout into the same directory first would write more into a checkout
// somebody is about to be asked to look at.
func TestScoutDoesNotRetryIntoATouchedCheckout(t *testing.T) {
	h := newHarness(t, workerResult{err: transientErr, transient: true})
	h.deps.Cov.Caps.WorkerRetries = 3
	// Status is read once at setup; every later read sees the change.
	h.tree.changeAfter = 1

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s (%s), want parked", out.Kind, out.Reason)
	}
	if got := h.phaseStarts(t, out.RunID); got != 1 {
		t.Errorf("%d phase starts, want 1 — nothing may be respawned into a checkout the scout touched", got)
	}
	if notes := h.notes(t, out.RunID); !hasSubstring(notes, "checkout not untouched") {
		t.Errorf("notes %v do not say the checkout is why it parked", notes)
	}
}

func hasSubstring(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

// A journal store that fails after the claim must not leave the ticket
// silently claimed (In Planning) behind an error a caller could mistake for
// "nothing happened": the claim is handed straight back, comment before
// status, the same branch run.Execute takes for the identical failure
// against Todo/In Progress. This is new failure surface for this package —
// WND-79 gave it a claim to fail after, where the machine-local lock it
// used to take instead had no such window: losing the lock cost nothing.
func TestJournalFailureHandsThePlanningClaimBack(t *testing.T) {
	h := newHarness(t, workerResult{handoff: draftHandoff()})
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h.store.Root = filepath.Join(blocker, "state") // Create must fail

	_, err := h.run(t)
	if err == nil {
		t.Fatal("Execute succeeded with a store that cannot open")
	}
	if !strings.Contains(err.Error(), "handed back") {
		t.Errorf("the error does not say the claim was handed back: %v", err)
	}
	want := []string{"claim:state=state-in-planning", "comment", "state=state-needs-input"}
	if strings.Join(h.board.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("writes = %v\nwant  %v", h.board.calls, want)
	}
	if c := h.board.comments[0]; !strings.Contains(c, "run journal") {
		t.Errorf("the hand-back comment does not name the journal failure:\n%s", c)
	}
}

// --- the re-plan cycle (WND-82) --------------------------------------------
//
// A re-plan ticket is already In Planning, carrying the re-plan label
// wand sweep's own hand-back left, and its description already carries a
// plan a human commented on. plan.Execute must tell this apart from a
// fresh To Plan claim and revise the existing plan in place instead of
// drafting a new one.

// rePlanIssue is a ticket sweep has already handed back for another
// planning cycle: In Planning, labeled, and carrying the plan a prior run
// wrote.
func rePlanIssue(t *testing.T) linear.Issue {
	t.Helper()
	priorPlan := plan.PlanMarkdown(parsedGoodDraft(t))
	desc, err := linear.WithSection("A human wrote this.\n", plan.PlanSectionID, priorPlan)
	if err != nil {
		t.Fatalf("WithSection: %v", err)
	}
	return linear.Issue{
		ID:          "issue-1",
		Identifier:  "WND-9",
		Title:       "wand plan",
		Description: desc,
		TeamID:      "team",
		State:       linear.IssueState{Name: "In Planning", Type: "started"},
		Labels:      []string{"re-plan"},
	}
}

// parsedGoodDraft mirrors render_test.go's parsedDraft, duplicated here
// because that helper lives in a file this one does not need the rest of.
func parsedGoodDraft(t *testing.T) plan.Draft {
	t.Helper()
	d, err := plan.ParseDraft(raw(t, goodDraft()), covenant.Default())
	if err != nil {
		t.Fatalf("ParseDraft: %v", err)
	}
	return d
}

// A re-plan run reads the human's comments since the last plan and revises
// the plan in place — the payoff this ticket is named for: a plan review
// loop mediated by the board instead of a terminal.
func TestARePlanReadsCommentsAndRevisesThePlanInPlace(t *testing.T) {
	revised := withChanges(draftHandoff(), "You said Vet already carries too much, so I moved the filter into Build instead.")
	revised["recommendation"] = map[string]any{"approach": "Filter in Build", "why": "Vet should not grow a second responsibility."}

	h := newHarness(t, workerResult{handoff: revised})
	h.board.issue = rePlanIssue(t)
	h.board.existingComments = []linear.Comment{
		{Author: "wand", CreatedAt: time.Now().Add(-time.Hour),
			Body: plan.OptionsComment(parsedGoodDraft(t), "fibonacci", "Plan Review", plan.Provenance{RunID: "r-1", Harness: "claude-code"})},
		{Author: "Matt Walters", CreatedAt: time.Now(),
			Body: "Vet already carries too much; put the filter in Build instead."},
	}

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Converged {
		t.Fatalf("outcome = %s (%s), want converged", out.Kind, out.Reason)
	}

	// The claim is the label removal, not a status write — the ticket is
	// already In Planning — and the deliverables land in the same fixed
	// order a first plan's do, minus the estimate this draft carries too.
	want := []string{"claim:unlabel=label-re-plan", "section=plan", "comment", "estimate", "state=state-plan-review"}
	if strings.Join(h.board.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("writes = %v\nwant  %v", h.board.calls, want)
	}

	if !strings.Contains(h.board.description, "Filter in Build") {
		t.Errorf("the revised plan's fenced region does not reflect the human's comment:\n%s", h.board.description)
	}
	if strings.Contains(h.board.description, "Vet grows a second responsibility") {
		t.Error("the old recommendation's argument was left in the fenced region")
	}

	if len(h.board.comments) != 1 {
		t.Fatalf("comments = %v, want exactly the what-changed comment", h.board.comments)
	}
	for _, want := range []string{
		"Plan revised",
		"You said Vet already carries too much, so I moved the filter into Build instead.",
	} {
		if !strings.Contains(h.board.comments[0], want) {
			t.Errorf("the comment does not name what changed and why (%q):\n%s", want, h.board.comments[0])
		}
	}

	// The reviser was handed the human's comment as input, not asked to
	// imagine one, and it read the plan already on the ticket rather than
	// researching from nothing.
	if !strings.Contains(h.work.prompts[0], "Vet already carries too much") {
		t.Error("the reviser was not given the human's comment")
	}
	if !strings.Contains(h.work.prompts[0], "Filter in Vet") {
		t.Error("the reviser was not given the plan already on the ticket")
	}
	if len(h.work.modes) != 1 {
		t.Fatalf("spawned %d workers (%v), want the reviser alone — no critic, no interview on a re-plan cycle", len(h.work.modes), h.work.modes)
	}
}

// A ticket labeled for a re-plan but with no comment since the plan it
// would revise has nothing to read: parking rather than guessing at what
// the human meant leaves the research budget unspent.
func TestARePlanWithNoCommentsSinceTheLastPlanParks(t *testing.T) {
	h := newHarness(t)
	h.board.issue = rePlanIssue(t)
	h.board.existingComments = []linear.Comment{
		{Author: "wand", CreatedAt: time.Now(),
			Body: plan.OptionsComment(parsedGoodDraft(t), "fibonacci", "Plan Review", plan.Provenance{RunID: "r-1", Harness: "claude-code"})},
	}

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Kind != journal.Parked {
		t.Fatalf("outcome = %s (%s), want parked", out.Kind, out.Reason)
	}
	if !strings.Contains(out.Reason, "nothing to revise against") {
		t.Errorf("the park does not say why: %s", out.Reason)
	}
	if len(h.board.deliverables()) != 0 {
		t.Errorf("a re-plan with nothing to read still wrote to the ticket: %v", h.board.deliverables())
	}
}

// A ticket In Planning with no re-plan label is what a live plan run's own
// claim leaves behind, not a re-plan waiting for a cycle — claimEither must
// not treat it as one, the same refusal TestASecondPlanOfOneTicketRefuses
// already covers for the fresh-plan side.
func TestARePlanLabelIsRequiredToResumeInPlanning(t *testing.T) {
	h := newHarness(t)
	h.board.issue = rePlanIssue(t)
	h.board.issue.Labels = nil

	_, err := h.run(t)
	if err == nil {
		t.Fatal("a ticket In Planning with no re-plan label was resumed")
	}
	// claimEither falls back to the fresh-plan claim, which refuses the
	// same way TestASecondPlanOfOneTicketRefuses already checks: a ticket
	// In Planning is not To Plan, whatever labels it carries.
	if !strings.Contains(err.Error(), "not yours to plan") {
		t.Errorf("error = %v, want the fresh-plan claim's own refusal", err)
	}
	if len(h.board.calls) != 0 {
		t.Errorf("the refused resume wrote to the ticket: %v", h.board.calls)
	}
}
