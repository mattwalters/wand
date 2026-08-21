// Package scope is the research orchestrator: one process turning one
// blessed-for-research ticket into a plan a human can bless for building.
//
// It is the lowest-risk orchestrator wand has — a read-only scout, no
// worktree, no branch, no PR, no CI — and it still exercises every hard
// mechanism the loop needs: spawning cold workers, validating what they
// hand back, writing a fenced region of a description, writing comments
// and a status, and holding a lock nobody else may take.
//
// The rules, and what each is against:
//
//   - **Scoping or nothing.** Research is blessed the same way building
//     is, by a human moving the ticket. A scope of an unblessed ticket is
//     an agent choosing what the team works on next.
//
//   - **An invalid handoff writes nothing.** A scope is read as a
//     decision: a human blesses the plan in the body on the strength of
//     the argument in the comment, and nobody re-derives afterwards
//     whether the argument held together. A draft missing its trade-offs
//     or recommending an approach it never described reads finished and
//     is not, so it is refused whole rather than written partially.
//
//   - **The deliverables land before the transition that advertises
//     them.** Plan into the description's fenced region, then the options
//     comment, then the estimate, and Scoped last. Each write is
//     something the next one refers to; the status move says "there is a
//     scope here to read", and it is made only once there is.
//
//     The comment comes before the estimate for the same reason: a ticket
//     carrying the argument for an estimate it does not have is
//     recoverable, and a ticket carrying a number nothing explains is a
//     number nobody can weigh.
//
//   - **Every extra pass is a fresh, cold process.** The critic attacks
//     the draft, the reviser rewrites it, and neither is the session that
//     wrote it — a session that has just argued for an approach defends
//     it, and what comes back is the same plan with the objections
//     explained away.
//
//   - **One scope per ticket at a time.** `wand run` claims its ticket out
//     of Todo, so the board is its mutex; a scope's ticket sits in Scoping
//     from the first read to the last write, so it has no such move and
//     takes an explicit per-ticket lock instead (journal.Store.LockTicket).
//     Two scopes of one ticket write two plans into one fenced region and
//     argue two recommendations at a human who cannot tell which the
//     estimate belongs to.
//
// Parking writes only the journal, deliberately: a park has to be
// reachable when Linear itself is what failed.
package scope

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/queue"
	"github.com/mattwalters/wand/internal/ticket"
	"github.com/mattwalters/wand/internal/verbs"
	"github.com/mattwalters/wand/internal/worker"
)

// Outcome is how the scope ended, for the caller and the scheduler.
type Outcome struct {
	// Kind reuses the journal's vocabulary: converged, handed_back, parked.
	Kind   journal.Outcome
	Reason string
	RunID  string
}

// Exit codes, the same contract `wand run` publishes, so one scheduler can
// read both. 1 is deliberately absent: it means the command failed before
// a run existed (bad flags, a ticket outside Scoping, no journal), which is
// fang's generic failure exit.
const (
	// ExitScoped: the scope landed and the ticket is on a human's desk.
	ExitScoped = 0
	// ExitHandedBack: the scout found the ticket's premise wrong; its
	// account is on the ticket and no plan was written.
	ExitHandedBack = 2
	// ExitParked: the run stopped without deciding. The journal says why.
	ExitParked = 3
)

// ExitCode maps the outcome onto the contract.
func (o Outcome) ExitCode() int {
	switch o.Kind {
	case journal.Converged:
		return ExitScoped
	case journal.HandedBack:
		return ExitHandedBack
	default:
		return ExitParked
	}
}

// Execute scopes one ticket.
//
// An error return means no run happened — the ticket was not in Scoping,
// another process holds it, or the journal would not open — and nothing was
// written. Once a run exists, every path ends in exactly one Outcome
// instead, including interrupts, which park with the signal as the reason.
func Execute(ctx context.Context, d Deps, store *journal.Store, identifier string) (Outcome, error) {
	if err := d.validate(); err != nil {
		return Outcome{}, err
	}
	if d.Out == nil {
		d.Out = io.Discard
	}

	issue, err := d.Board.IssueByIdentifier(ctx, identifier)
	if err != nil {
		return Outcome{}, err
	}
	if err := vet(issue, d.Cov.StatusName("scoping")); err != nil {
		return Outcome{}, err
	}

	// The lock before the journal: a refusal here must cost nothing, not
	// leave a run directory behind for a scope that never began.
	lock, err := store.LockTicket(issue.Identifier)
	if err != nil {
		return Outcome{}, err
	}
	defer lock.Release()

	r, err := store.Create(journal.Meta{
		Ticket:  issue.Identifier,
		Verb:    "scope",
		Repo:    d.Repo,
		Harness: d.Harness,
	})
	if err != nil {
		return Outcome{}, err
	}
	defer r.Close()
	fmt.Fprintf(d.Out, "scoping %s (%s), journaling to %s\n", issue.Identifier, issue.State.Name, r.Dir())

	s := &scoping{d: d, r: r, issue: issue, prov: Provenance{RunID: r.ID(), Harness: d.Harness}}
	out := s.run(ctx)
	out.RunID = r.ID()
	fmt.Fprintf(d.Out, "run %s ended: %s — %s\n", r.ID(), out.Kind, out.Reason)
	return out, nil
}

// vet refuses the tickets a scope may not take. It is deliberately not
// queue.Vet: that function answers "may an agent start building this?",
// and the two questions differ on blockers. A ticket blocked by another is
// exactly the ticket worth scoping early — the blocker stops the building,
// not the reading — so only the human-only label refuses here, and it
// refuses absolutely.
func vet(issue linear.Issue, scoping string) error {
	if !strings.EqualFold(issue.State.Name, scoping) {
		return fmt.Errorf(
			"%s is in %q, not %q: scope researches blessed work, and blessing research is a human act — an issue outside %q is not yours to scope",
			issue.Identifier, issue.State.Name, scoping, scoping)
	}
	if reason := Vet(issue); reason != "" {
		return fmt.Errorf("%s may not be scoped: %s", issue.Identifier, reason)
	}
	return nil
}

// Vet returns why an issue in Scoping may not be scoped, or "" when it may.
// Exported so `wand dispatch` can select Scoping candidates the same way
// `wand queue` selects Todo ones: ranked, then vetted, skips never silent.
// Deliberately not queue.Vet, for the same reason [vet] is not: a ticket
// blocked by another is exactly the ticket worth scoping early, so only the
// human-only label refuses here.
func Vet(issue linear.Issue) string {
	for _, label := range issue.Labels {
		if strings.EqualFold(label, queue.HumanOnlyLabel) {
			return "labeled " + queue.HumanOnlyLabel
		}
	}
	return ""
}

// scoping is one run's working state. Methods that can end the run return
// *Outcome; nil means carry on.
type scoping struct {
	d     Deps
	r     *journal.Run
	issue linear.Issue
	prov  Provenance

	ticketText string
	treeBefore string
}

// phaseDetail is what EndPhase journals about a worker, bounded so the
// journal stays readable. Cross-harness comparison data lives here for
// free — this struct, journaled once per phase, is the scope-verb half of
// the stable ledger schema described in the package doc of
// internal/journal. It carries the same operational fields as run's own
// phaseDetail (internal/run/run.go) except DiffStat: a scope has no
// worktree, so there is nothing for git diff to summarize.
//
// A metric a harness or this phase cannot report is omitted, never
// estimated — TokensIn/TokensOut are pointers so a reported zero and "the
// harness didn't say" stay distinguishable.
type phaseDetail struct {
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Handoff    bool   `json:"handoff"`
	Error      string `json:"error,omitempty"`
	OutputTail string `json:"output_tail,omitempty"`

	Harness   string `json:"harness,omitempty"`
	Model     string `json:"model,omitempty"`
	TokensIn  *int64 `json:"tokens_in,omitempty"`
	TokensOut *int64 `json:"tokens_out,omitempty"`
	WallClock string `json:"wall_clock,omitempty"`
}

// journalTail bounds the output tail a journal record keeps.
const journalTail = 4 << 10

func tailOf(s string) string {
	if len(s) <= journalTail {
		return s
	}
	return "[… clipped …]\n" + s[len(s)-journalTail:]
}

func (s *scoping) run(ctx context.Context) Outcome {
	comments, err := s.d.Board.IssueComments(ctx, s.issue.ID)
	if err != nil {
		return *s.park(ctx, fmt.Sprintf("could not read the ticket's comments: %v", err))
	}
	s.ticketText = ticket.Render(s.issue, comments)

	// The tree as it stood before any worker ran. The scout is read-only
	// by instruction, and this is what checks it: a human's own
	// uncommitted work is fine, a worker's is not.
	before, err := s.d.Tree.Status(ctx, s.d.Repo)
	if err != nil {
		return *s.park(ctx, fmt.Sprintf("could not read the repository's state: %v", err))
	}
	s.treeBefore = before

	// --- the draft ----------------------------------------------------
	draft, out := s.draft(ctx)
	if out != nil {
		return *out
	}
	if out := s.wrongPremise(ctx, draft); out != nil {
		return *out
	}

	// --- the critic, when the covenant asks for one --------------------
	if s.d.Cov.Toggles.ScopeCritic {
		revised, out := s.critique(ctx, draft)
		if out != nil {
			return *out
		}
		draft = revised
		// Checked after every stage that can replace the draft, not once
		// at the end. A reviser may reach the verdict the scout did not,
		// because the critic showed the premise was wrong — and that
		// verdict is as terminal coming from the reviser as from the
		// scout. Carrying such a draft into the interview would grill a
		// human over a scope with no understanding, no approaches and no
		// recommendation left in it, then spend another revision round on
		// whatever they said to the blanks.
		if out := s.wrongPremise(ctx, draft); out != nil {
			return *out
		}
	}

	// --- the interview, when there is a human to hold it ---------------
	if s.d.Interactive {
		revised, out := s.interview(ctx, draft)
		if out != nil {
			return *out
		}
		draft = revised
	}

	// The same check after the last stage that can replace the draft: a
	// human can be the one who says the thing that makes the premise
	// wrong, and writing a plan over that would write the scope the last
	// two stages just argued out of existence.
	if out := s.wrongPremise(ctx, draft); out != nil {
		return *out
	}
	return s.write(ctx, draft)
}

// wrongPremise hands back when a draft reports the ticket built on
// something untrue, and returns nil when it does not. Nothing is written to
// the description and no estimate is set: the account is the whole
// deliverable, and a plan for work that should not happen is worse than no
// plan at all.
func (s *scoping) wrongPremise(ctx context.Context, draft Draft) *Outcome {
	if draft.Premise != PremiseWrong {
		return nil
	}
	return s.handback(ctx,
		premiseComment(draft.Reason, s.prov),
		fmt.Sprintf("the ticket's premise was judged wrong: %s", firstLine(draft.Reason)))
}

// draft spawns the scout and validates what it wrote. Validation failure
// parks: a scope nobody validated is not a scope, and there is nothing here
// to hand a human but a broken handoff.
func (s *scoping) draft(ctx context.Context) (Draft, *Outcome) {
	res, out := s.work(ctx, "scout", 1, scoutRules(), scoutPrompt(s.ticketText, s.d.Cov))
	if out != nil {
		return Draft{}, out
	}
	return s.parse(ctx, res.Handoff, "scout")
}

// parse validates a draft, journals it, and checks the tree the worker was
// told not to touch — in that order, so the research survives in the
// journal even when the run then parks on what the worker did to the
// checkout.
func (s *scoping) parse(ctx context.Context, raw json.RawMessage, phase string) (Draft, *Outcome) {
	draft, err := ParseDraft(raw, s.d.Cov)
	if err != nil {
		return Draft{}, s.park(ctx, fmt.Sprintf("the %s's handoff is unusable, so nothing was written to the ticket: %v", phase, err))
	}
	s.note(phase+" handoff", draft)
	if out := s.requireUntouched(ctx, phase); out != nil {
		return Draft{}, out
	}
	return draft, nil
}

// critique runs the cold critic and, if anything stuck, a cold reviser.
func (s *scoping) critique(ctx context.Context, draft Draft) (Draft, *Outcome) {
	rendered := renderDraft(draft, s.d.Cov)
	res, out := s.work(ctx, "critic", 1, criticRules(), criticPrompt(s.ticketText, rendered))
	if out != nil {
		return draft, out
	}
	critique, err := ParseCritique(res.Handoff)
	if err != nil {
		return draft, s.park(ctx, fmt.Sprintf("the critic's handoff is unusable: %v — a draft nobody could attack is not thereby sound, so nothing was written", err))
	}
	s.note("critique", critique)
	if out := s.requireUntouched(ctx, "critic"); out != nil {
		return draft, out
	}

	s.prov.Critic = true
	s.prov.Objections = len(critique.Objections)
	if len(critique.Objections) == 0 {
		fmt.Fprintln(s.d.Out, "critic: nothing stuck")
		return draft, nil
	}
	fmt.Fprintf(s.d.Out, "critic: %d objection(s), revising\n", len(critique.Objections))
	return s.revise(ctx, draft, 1, renderObjections(critique), "a cold critic's objections to it")
}

// interview puts the draft to the human and, if they said anything, hands
// the answers to a cold reviser. A human who answers nothing has approved
// the draft as it stands, and spending a model call to be told so would
// only give a session the chance to talk itself into changes nobody asked
// for.
func (s *scoping) interview(ctx context.Context, draft Draft) (Draft, *Outcome) {
	s.prov.Interview = true
	answers, err := Interview(s.d.In, s.d.Out, Questions(draft))
	if err != nil {
		return draft, s.park(ctx, fmt.Sprintf("the interview could not be held: %v", err))
	}
	s.note("interview", answers)
	s.prov.Answers = len(answers)
	if len(answers) == 0 {
		return draft, nil
	}
	return s.revise(ctx, draft, 2, Transcript(answers), "what a human said when it was put to them question by question")
}

// revise spawns a fresh session over the draft and whatever was said
// against it. The result replaces the draft whole and is validated
// identically: a revision is not allowed to be a weaker scope than the one
// it replaces.
func (s *scoping) revise(ctx context.Context, draft Draft, round int, objections, source string) (Draft, *Outcome) {
	prompt := revisePrompt(s.ticketText, renderDraft(draft, s.d.Cov), objections, s.d.Cov, source)
	res, out := s.work(ctx, "revise", round, scoutRules(), prompt)
	if out != nil {
		return draft, out
	}
	revised, out := s.parse(ctx, res.Handoff, "reviser")
	if out != nil {
		// Falling back to the draft would silently discard what the critic
		// or the human just said, and write a scope they had already
		// argued with.
		return draft, out
	}
	return revised, nil
}

// write lands the deliverables in the fixed order, each before the
// transition that advertises it. Every failure past the first write parks
// naming exactly what did land: the ticket is still in Scoping, so nothing
// is advertised that is not there, and a human reading the journal knows
// what to expect on the ticket.
func (s *scoping) write(ctx context.Context, draft Draft) Outcome {
	// Resolve the status first. It is a pure read, and it is the one thing
	// that can refuse for reasons nothing here can fix — a drifted board,
	// or the guard — so it happens while nothing has been written. Scoped,
	// never Needs Input: that status is the scout's other ending, reserved
	// for a blocking question ([wrongPremise], through verbs.Handback). A
	// finished plan is a different kind of "ask a human" — judge, not
	// answer — and Scoped is what tells the cockpit which queue it belongs
	// in.
	stateID, err := verbs.ResolveState(ctx, s.d.Board, s.d.Cov, s.issue.TeamID, "scoped")
	if err != nil {
		return *s.park(ctx, fmt.Sprintf("could not resolve the %s status: %v", s.d.Cov.StatusName("scoped"), err))
	}

	if _, _, err := s.d.Board.UpsertSection(ctx, s.issue.ID, s.issue.Description, PlanSectionID, PlanMarkdown(draft)); err != nil {
		return *s.park(ctx, fmt.Sprintf("the plan could not be written into the description: %v", err))
	}
	fmt.Fprintln(s.d.Out, "wrote the plan into the ticket's description")

	comment := OptionsComment(draft, s.d.Cov.IssueEstimationType, s.d.Cov.StatusName("scoped"), s.prov)
	if err := s.d.Board.CreateComment(ctx, s.issue.ID, comment); err != nil {
		return *s.park(ctx, fmt.Sprintf("the plan is in the description, but the options comment failed: %v — the ticket is still in %s, carrying a plan nothing argues for", err, s.issue.State.Name))
	}
	fmt.Fprintln(s.d.Out, "posted the options comment")

	if draft.Estimate != nil {
		if err := s.d.Board.UpdateIssue(ctx, s.issue.ID, linear.IssueUpdate{Estimate: draft.Estimate}); err != nil {
			return *s.park(ctx, fmt.Sprintf("the plan and the options are on the ticket, but the estimate failed: %v", err))
		}
		fmt.Fprintf(s.d.Out, "set the estimate to %d\n", *draft.Estimate)
	}

	if err := s.d.Board.UpdateIssue(ctx, s.issue.ID, linear.IssueUpdate{StateID: stateID}); err != nil {
		return *s.park(ctx, fmt.Sprintf("every deliverable is on the ticket, but the move to %s failed: %v — the scope is readable, it just is not on anyone's desk", s.d.Cov.StatusName("scoped"), err))
	}

	reason := fmt.Sprintf("scoped: %s recommended, plan and options on the ticket, %s for a human to judge",
		strings.TrimSpace(draft.Recommendation.Approach), s.d.Cov.StatusName("scoped"))
	if err := s.r.Converged(reason); err != nil {
		fmt.Fprintf(s.d.Out, "journal: %v\n", err)
	}
	return Outcome{Kind: journal.Converged, Reason: reason}
}

// work journals a phase, spawns its worker, and journals what came back. A
// worker failure — no handoff, a timeout, a spawn error — parks: without
// the report there is nothing to tell a crash from a success.
func (s *scoping) work(ctx context.Context, phase string, round int, rules []string, prompt string) (worker.Result, *Outcome) {
	fmt.Fprintf(s.d.Out, "phase %s: spawning a cold worker (%s)\n", phase, s.d.Harness)
	if err := s.r.StartPhase(phase, round); err != nil {
		// An unjournaled phase must not run; that is the journal's one rule.
		return worker.Result{}, s.park(ctx, fmt.Sprintf("could not journal phase %s: %v", phase, err))
	}
	spec := worker.Spec{
		Mode:        phase + " (read-only research; no worktree, no branch, no CI)",
		Rules:       rules,
		Prompt:      prompt,
		Dir:         s.d.Repo,
		ScratchDir:  s.r.ScratchDir(),
		HandoffPath: s.r.HandoffPath(),
		Timeout:     s.d.Cov.Caps.WorkerTimeout,
		Model:       s.d.Model,
		Effort:      s.d.Effort,
		Out:         s.d.Out,
		Label:       fmt.Sprintf("%s round %d", phase, round),
		OnHeartbeat: s.heartbeat(phase, round),
	}
	start := time.Now()
	res, err := s.d.Workers.Run(ctx, spec)
	elapsed := time.Since(start)
	detail := phaseDetail{
		ExitCode:  res.ExitCode,
		TimedOut:  res.TimedOut,
		Handoff:   res.Handoff != nil,
		Harness:   s.d.Harness,
		Model:     s.d.Model,
		WallClock: elapsed.String(),
	}
	if res.Usage != nil {
		detail.TokensIn = res.Usage.InputTokens
		detail.TokensOut = res.Usage.OutputTokens
	}
	if err != nil {
		detail.Error = err.Error()
		detail.OutputTail = tailOf(res.Output)
	}
	if jerr := s.r.EndPhase(detail); jerr != nil {
		return res, s.park(ctx, fmt.Sprintf("could not journal the end of phase %s: %v", phase, jerr))
	}
	if ctx.Err() != nil {
		// Load-bearing even though park re-derives the same sentence: a
		// worker that returned cleanly just as the cancel landed has a nil
		// err, and falling through would report success for a run the
		// operator already stopped.
		return res, s.park(ctx, context.Cause(ctx).Error())
	}
	if err != nil {
		return res, s.park(ctx, fmt.Sprintf("the %s worker failed: %v", phase, err))
	}
	return res, nil
}

// heartbeat returns the worker.Spec.OnHeartbeat callback for one phase: a
// closure that renews the run's lease on every tick, so a long single phase
// keeps looking alive to a lease reader instead of going stale the moment it
// passes its first minute. A renewal failure is narrated, never fatal — the
// worker is mid-flight, and a lease write hiccup is not a reason to kill it.
func (s *scoping) heartbeat(phase string, round int) func() {
	return func() {
		if err := s.r.Heartbeat(); err != nil {
			fmt.Fprintf(s.d.Out, "phase %s round %d: heartbeat renewal failed: %v\n", phase, round, err)
		}
	}
}

// requireUntouched parks if a worker changed the repository it was told to
// read.
//
// A scope has no worktree of its own: the scout reads the checkout the
// command was run from, which is usually a person's. So a stray edit is not
// a mess in a directory this run owns and can throw away — it is a change
// in somebody's working copy that they did not make and will not expect.
// Parking is what puts it in front of them, and it is why the handoff is
// journaled before this runs: the research survives the park.
func (s *scoping) requireUntouched(ctx context.Context, phase string) *Outcome {
	after, err := s.d.Tree.Status(ctx, s.d.Repo)
	if err != nil {
		return s.park(ctx, fmt.Sprintf("could not check the repository after the %s ran: %v", phase, err))
	}
	if after != s.treeBefore {
		return s.park(ctx, fmt.Sprintf(
			"the %s changed the repository it was told only to read; %s is your checkout, not a workspace this run owns, so nothing was written to the ticket and the change is left for you to look at (`git status` in %s). The %s's handoff is in this run's journal",
			phase, s.d.Repo, s.d.Repo, phase))
	}
	return nil
}

// handback ends the run on a human's desk, through the verb that encodes
// comment-before-status. If the writes fail, the run parks instead,
// carrying both what it wanted to say and why it could not.
func (s *scoping) handback(ctx context.Context, comment, reason string) *Outcome {
	if _, err := verbs.Handback(ctx, s.d.Board, s.d.Cov, s.issue.Identifier, comment); err != nil {
		return s.park(ctx, fmt.Sprintf("hand-back failed: %v (the run was handing back because: %s)", err, reason))
	}
	if err := s.r.HandedBack(reason); err != nil {
		fmt.Fprintf(s.d.Out, "journal: %v\n", err)
	}
	return &Outcome{Kind: journal.HandedBack, Reason: reason}
}

// park ends the run without deciding, preferring the interrupt's own
// sentence when the context is what killed the operation — "interrupted by
// SIGTERM" explains a run; "Post …: context canceled" does not.
//
// One function rather than the park/parkCtx pair this used to be, and the
// same shape as run's: every ending path now carries the context, so no
// call site can pick the variant that forgets to tell the ticket.
//
// The journal is written first and is the run's real ending: it is
// reachable even when Linear is what broke, which is what most of the park
// sites here are. [verbs.ReportPark] then puts the same sentence on the
// ticket, best-effort — it cannot fail this function and cannot re-enter
// it.
func (s *scoping) park(ctx context.Context, reason string) *Outcome {
	if ctx.Err() != nil {
		reason = context.Cause(ctx).Error()
	}
	if err := s.r.Parked(reason); err != nil {
		fmt.Fprintf(s.d.Out, "journal: %v\n", err)
	}
	verbs.ReportPark(ctx, s.d.Board, s.d.Out, s.issue.ID, reason)
	return &Outcome{Kind: journal.Parked, Reason: reason}
}

// note journals something worth reading later; failing to write one is
// worth a line, never the run.
func (s *scoping) note(message string, detail any) {
	if err := s.r.Note(message, detail); err != nil {
		fmt.Fprintf(s.d.Out, "journal: %v\n", err)
	}
}
