// Package plan is the research orchestrator: one process turning one
// blessed-for-research ticket into a plan a human can bless for building.
//
// It is the lowest-risk orchestrator wand has — a read-only scout, no
// worktree, no branch, no PR, no CI — and it still exercises every hard
// mechanism the loop needs: spawning cold workers, validating what they
// hand back, writing a fenced region of a description, writing comments
// and a status, and claiming the ticket nobody else may take at the same
// time.
//
// The rules, and what each is against:
//
//   - **To Plan or nothing.** Research is blessed the same way building
//     is, by a human moving the ticket. A plan run over an unblessed
//     ticket is an agent choosing what the team works on next.
//
//   - **An invalid handoff writes nothing.** A plan is read as a
//     decision: a human blesses the plan in the body on the strength of
//     the argument in the comment, and nobody re-derives afterwards
//     whether the argument held together. A draft missing its trade-offs
//     or recommending an approach it never described reads finished and
//     is not, so it is refused whole rather than written partially.
//
//   - **The fence is absolute.** A plan run writes only the marker-fenced
//     plan region of the description (linear.UpsertSection leaves every
//     other byte alone — see that package). The ticket's goals, problem
//     statement and title are the human's and are checked *against* by
//     the plan, never rewritten by it; that is what lets a human notice a
//     plan that answers the wrong question. A scout that judges the
//     ticket itself wrong has the wrong-premise ending for exactly that —
//     it writes no plan and hands back to a person — rather than a way to
//     correct the body in the same act as planning it.
//
//   - **The deliverables land before the transition that advertises
//     them.** Plan into the description's fenced region, then the options
//     comment, then the estimate, and Plan Review last. Each write is
//     something the next one refers to; the status move says "there is a
//     plan here to read", and it is made only once there is.
//
//     The comment comes before the estimate for the same reason: a ticket
//     carrying the argument for an estimate it does not have is
//     recoverable, and a ticket carrying a number nothing explains is a
//     number nobody can weigh.
//
//   - **A re-plan preserves what it replaces.** The plan region is
//     rewritten whole on every plan run (render.go), so a plan a human
//     read and blessed would otherwise vanish the moment a later plan run
//     happened, surviving nowhere but a closed PR. Before the region is
//     overwritten, whatever is already there is posted as a dated,
//     superseded comment — preservePriorPlan, ahead of UpsertSection in
//     [planning.write] the same way the options comment is ahead of the
//     estimate. Re-planning itself stays legitimate; only the silent loss
//     was the defect.
//
//   - **Every extra pass is a fresh, cold process.** The critic attacks
//     the draft, the reviser rewrites it, and neither is the session that
//     wrote it — a session that has just argued for an approach defends
//     it, and what comes back is the same plan with the objections
//     explained away.
//
//   - **One plan run per ticket at a time.** `wand plan` claims its ticket
//     out of To Plan into In Planning before it touches anything else, the
//     same claim-before-filesystem ordering `wand run` uses for Todo and
//     In Progress (see verbs.Claim and verbs.ClaimForPlanning) — the board
//     is the mutex on both sides now. This is a deliberate reversal of the
//     topology this package originally shipped with, which held its ticket
//     in Scoping (a single unstarted status) for the whole research phase
//     and took an explicit per-ticket lock instead
//     (journal.Store.LockTicket) because it had no board move to lose a
//     race on. That lock was machine-local — it could not stop two
//     machines planning one ticket, which a board claim can — and it is
//     gone now that the board can do the job; see WND-79 and PLAN.md. Two
//     plan runs over one ticket write two plans into one fenced region and
//     argue two recommendations at a human who cannot tell which the
//     estimate belongs to.
//
// Parking writes only the journal, deliberately: a park has to be
// reachable when Linear itself is what failed.
package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/queue"
	"github.com/mattwalters/wand/internal/ticket"
	"github.com/mattwalters/wand/internal/verbs"
	"github.com/mattwalters/wand/internal/worker"
)

// Outcome is how the plan run ended, for the caller and the scheduler.
type Outcome struct {
	// Kind reuses the journal's vocabulary: converged, handed_back, parked.
	Kind   journal.Outcome
	Reason string
	RunID  string
}

// Exit codes, the same contract `wand run` publishes, so one scheduler can
// read both. 1 is deliberately absent: it means the command failed before
// a run existed (bad flags, a ticket outside To Plan, no journal), which is
// fang's generic failure exit.
const (
	// ExitPlanReviewed: the plan landed and the ticket is on a human's desk.
	ExitPlanReviewed = 0
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
		return ExitPlanReviewed
	case journal.HandedBack:
		return ExitHandedBack
	default:
		return ExitParked
	}
}

// Execute researches one ticket.
//
// An error return means no run happened — the ticket was not in To Plan, the
// claim raced and lost, or the journal would not open — and nothing was
// written except possibly the claim itself, which a journal failure hands
// straight back (see the comment on that branch below). Once a run exists,
// every path ends in exactly one Outcome instead, including interrupts,
// which park with the signal as the reason.
func Execute(ctx context.Context, d Deps, store *journal.Store, identifier string) (Outcome, error) {
	if err := d.validate(); err != nil {
		return Outcome{}, err
	}
	if d.Out == nil {
		d.Out = io.Discard
	}

	// Claim before anything touches the filesystem: the status move is the
	// cheapest place to lose a race, and losing it here costs nothing — not
	// even a run directory. Mirrors run.Execute's ordering (verbs.Claim);
	// see the package doc for why this package now claims a started status
	// instead of taking a machine-local lock.
	//
	// claimEither decides whether identifier names a fresh plan or a
	// re-plan and claims it the matching way; see its own doc for why a
	// ticket already In Planning is not automatically the latter.
	issue, isReplan, err := claimEither(ctx, d, identifier)
	if err != nil {
		return Outcome{}, err
	}
	if isReplan {
		fmt.Fprintf(d.Out, "resumed %s for a re-plan: %s\n", issue.Identifier, d.Cov.StatusName("in_planning"))
	} else {
		fmt.Fprintf(d.Out, "claimed %s: %s\n", issue.Identifier, d.Cov.StatusName("in_planning"))
	}

	// The journal's Verb keeps writing "plan" only — a journal run predates
	// this rename and may still carry "scope", but nothing here reads that
	// back for its own sake; the cockpit and dispatch.LanesUsed are the
	// readers, and both take "scope" as a synonym for "plan" rather than
	// this package migrating 60+ existing local runs for a value nothing
	// else needs.
	r, err := store.Create(journal.Meta{
		Ticket:  issue.Identifier,
		Verb:    "plan",
		Repo:    d.Repo,
		Harness: d.Harness,
	})
	if err != nil {
		// The claim already landed, and exit 1 promises a scheduler there is
		// nothing to sweep — so nothing may stay silently claimed. Hand the
		// ticket back with the journal failure as the reason, the same
		// branch run.Execute takes for the same failure.
		comment := fmt.Sprintf("This run claimed the ticket but could not open its run journal, so it stopped before doing any research. The store must be fixed before a rerun: %v", err)
		if _, herr := verbs.Handback(ctx, d.Board, d.Cov, issue.Identifier, comment); herr != nil {
			return Outcome{}, fmt.Errorf(
				"%s is claimed (%s) but the run journal would not open: %w — and the hand-back failed too (%v); hand the ticket back or fix the store before retrying",
				issue.Identifier, d.Cov.StatusName("in_planning"), err, herr)
		}
		return Outcome{}, fmt.Errorf(
			"the run journal would not open: %w — %s was handed back (%s) with that as the reason", err, issue.Identifier, d.Cov.StatusName("needs_input"))
	}
	defer r.Close()
	fmt.Fprintf(d.Out, "planning %s, journaling to %s\n", issue.Identifier, r.Dir())

	s := &planning{d: d, r: r, issue: issue, isReplan: isReplan, prov: Provenance{RunID: r.ID(), Harness: d.Harness}}
	out := s.run(ctx)
	out.RunID = r.ID()
	fmt.Fprintf(d.Out, "run %s ended: %s — %s\n", r.ID(), out.Kind, out.Reason)
	return out, nil
}

// claimEither reads identifier once and claims it as a fresh plan (To Plan,
// via verbs.ClaimForPlanning) or a re-plan (already In Planning, carrying
// verbs.RePlanLabel — the ticket verbs.ReturnToPlanning already handed
// back — via verbs.ClaimForReplanning), returning which it was.
//
// A ticket In Planning with no re-plan label is not treated as either: that
// is what a live plan run's own claim leaves behind, and ClaimForPlanning's
// own "not yours to plan" refusal is the right answer for it — the same
// protection [TestASecondPlanOfOneTicketRefuses] already checks, unchanged
// by this function existing.
func claimEither(ctx context.Context, d Deps, identifier string) (linear.Issue, bool, error) {
	peek, err := d.Board.IssueByIdentifier(ctx, identifier)
	if err != nil {
		return linear.Issue{}, false, err
	}
	if strings.EqualFold(peek.State.Name, d.Cov.StatusName("in_planning")) && hasLabel(peek, verbs.RePlanLabel) {
		issue, err := verbs.ClaimForReplanning(ctx, d.Board, d.Cov, identifier, Vet)
		return issue, true, err
	}
	issue, err := verbs.ClaimForPlanning(ctx, d.Board, d.Cov, identifier, Vet)
	return issue, false, err
}

// hasLabel reports whether issue carries name, case-insensitively.
func hasLabel(issue linear.Issue, name string) bool {
	for _, l := range issue.Labels {
		if strings.EqualFold(l, name) {
			return true
		}
	}
	return false
}

// Vet returns why an issue in To Plan may not be planned, or "" when it may.
// Exported so `wand dispatch` can select To Plan candidates the same way
// `wand queue` selects Todo ones: ranked, then vetted, skips never silent —
// and so verbs.ClaimForPlanning can call it before the claim's own write.
// Deliberately not queue.Vet: that function answers "may an agent start
// building this?", and the two questions differ on blockers. A ticket
// blocked by another is exactly the ticket worth planning early — the
// blocker stops the building, not the reading — so only the human-only and
// parked labels refuse here.
func Vet(issue linear.Issue) string {
	for _, label := range issue.Labels {
		if strings.EqualFold(label, queue.HumanOnlyLabel) {
			return "labeled " + queue.HumanOnlyLabel
		}
	}
	// A plan run that already parked is not worth re-buying blindly: the
	// scout costs a full cold research pass, and the reference journal has
	// the same ticket planned and parked three times over for one defect.
	// Clearing the label is how a person says it is worth another.
	for _, label := range issue.Labels {
		if strings.EqualFold(label, queue.ParkedLabel) {
			return "labeled " + queue.ParkedLabel
		}
	}
	return ""
}

// planning is one run's working state. Methods that can end the run return
// *Outcome; nil means carry on.
type planning struct {
	d     Deps
	r     *journal.Run
	issue linear.Issue
	prov  Provenance

	ticketText string
	treeBefore string

	// isReplan marks a run claimed via claimEither's re-plan branch: the
	// ticket was already In Planning, carrying a plan a human commented on,
	// and s.run takes the revise-in-place path (s.runReplan) instead of the
	// first-plan pipeline.
	isReplan bool
}

// phaseDetail is what EndPhase journals about a worker, bounded so the
// journal stays readable. Cross-harness comparison data lives here for
// free — this struct, journaled once per phase, is the plan-verb half of
// the stable ledger schema described in the package doc of
// internal/journal. It carries the same operational fields as run's own
// phaseDetail (internal/run/run.go) except DiffStat: a plan run has no
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
	// Attempt is which spawn of this phase this record ends, counting from
	// 1; above 1 only after a transient failure was retried.
	Attempt int `json:"attempt,omitempty"`
	// Transient is what the adapter made of a failure: true when the
	// harness itself reported infrastructure rather than the work.
	Transient bool `json:"transient,omitempty"`
}

// retryNote is the detail on the journal note a transient failure writes —
// see the run package's twin. Written whether or not the retry happens.
type retryNote struct {
	Phase   string `json:"phase"`
	Round   int    `json:"round"`
	Attempt int    `json:"attempt"`
	Retries int    `json:"retries"`
	Error   string `json:"error"`
	// Tree says why the untouched-checkout check refused the retry;
	// absent when the checkout was not what stopped it.
	Tree string `json:"tree,omitempty"`
}

// journalTail bounds the output tail a journal record keeps.
const journalTail = 4 << 10

func tailOf(s string) string {
	if len(s) <= journalTail {
		return s
	}
	return "[… clipped …]\n" + s[len(s)-journalTail:]
}

func (s *planning) run(ctx context.Context) Outcome {
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

	if s.isReplan {
		return s.runReplan(ctx, comments)
	}

	// --- the draft ----------------------------------------------------
	draft, out := s.draft(ctx)
	if out != nil {
		return *out
	}
	if out := s.wrongPremise(ctx, draft); out != nil {
		return *out
	}

	// --- the critic, when the covenant asks for one --------------------
	if s.d.Cov.Toggles.PlanCritic {
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
		// human over a plan with no understanding, no approaches and no
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
	// wrong, and writing a plan over that would write the plan the last
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
func (s *planning) wrongPremise(ctx context.Context, draft Draft) *Outcome {
	if draft.Premise != PremiseWrong {
		return nil
	}
	return s.handback(ctx,
		premiseComment(draft.Reason, s.prov),
		fmt.Sprintf("the ticket's premise was judged wrong: %s", firstLine(draft.Reason)))
}

// draft spawns the scout and validates what it wrote. Validation failure
// parks: a plan nobody validated is not a plan, and there is nothing here
// to hand a human but a broken handoff.
func (s *planning) draft(ctx context.Context) (Draft, *Outcome) {
	res, out := s.work(ctx, "scout", 1, scoutRules(), scoutPrompt(s.ticketText, s.d.Cov))
	if out != nil {
		return Draft{}, out
	}
	return s.parse(ctx, res.Handoff, "scout")
}

// persistRejected saves a handoff that failed validation to the run's
// scratch directory before the caller parks: worker.collect deletes a
// worker's own copy the instant it reads it, so this is the only remaining
// chance to keep the rejected bytes recoverable rather than a total loss.
// Every validation site calls this before s.park — see [planning.parse],
// [planning.parseRevision], [planning.critique], and [planning.parseReplan].
func (s *planning) persistRejected(raw json.RawMessage, phase string) {
	if werr := os.WriteFile(s.r.RejectedHandoffPath(), raw, 0o644); werr != nil {
		fmt.Fprintf(s.d.Out, "could not persist the rejected %s handoff: %v\n", phase, werr)
	}
}

// parse validates a draft, journals it, and checks the tree the worker was
// told not to touch — in that order, so the research survives in the
// journal even when the run then parks on what the worker did to the
// checkout.
//
// A ParseDraft failure is persisted to the run's scratch directory before
// the park: the worker's own copy is already gone by the time this runs
// (worker.collect deletes it right after reading), so this is the only
// remaining chance to keep the rejected handoff recoverable rather than a
// total loss.
func (s *planning) parse(ctx context.Context, raw json.RawMessage, phase string) (Draft, *Outcome) {
	draft, err := ParseDraft(raw, s.d.Cov)
	if err != nil {
		s.persistRejected(raw, phase)
		return Draft{}, s.park(ctx, fmt.Sprintf("the %s's handoff is unusable, so nothing was written to the ticket: %v", phase, err))
	}
	if len(draft.Dropped) > 0 {
		s.note(phase+" citations dropped for carrying no line", draft.Dropped)
	}
	s.note(phase+" handoff", draft)
	if out := s.requireUntouched(ctx, phase); out != nil {
		return Draft{}, out
	}
	return draft, nil
}

// parseRevision validates a reviser's handoff to a critique the same way
// [planning.parse] validates a draft — persisting a rejected handoff before
// parking, journaling what came back, then checking the tree — except
// against [ParseRevision], which additionally demands one resolution per
// objection the critic raised.
func (s *planning) parseRevision(ctx context.Context, raw json.RawMessage, objections int) (Revision, *Outcome) {
	rev, err := ParseRevision(raw, s.d.Cov, objections)
	if err != nil {
		s.persistRejected(raw, "reviser")
		return Revision{}, s.park(ctx, fmt.Sprintf("the reviser's handoff is unusable, so nothing was written to the ticket: %v", err))
	}
	if len(rev.Dropped) > 0 {
		s.note("reviser citations dropped for carrying no line", rev.Dropped)
	}
	s.note("reviser handoff", rev)
	if out := s.requireUntouched(ctx, "reviser"); out != nil {
		return Revision{}, out
	}
	return rev, nil
}

// critique runs the cold critic and, if anything stuck, a cold reviser.
//
// What the reviser says about each objection is routed, not just carried
// along inside the new draft: an objection it resolved goes into the
// options comment's reasoning trail (challengesSection, via prov.Challenges)
// as what was challenged and what changed; one it could not resolve is
// promoted into the draft's own open questions, so it reaches the human at
// Plan Review the same way a scout's unanswered question does. A draft
// that survived nothing is not thereby sound, and a human should be able
// to see which it was.
func (s *planning) critique(ctx context.Context, draft Draft) (Draft, *Outcome) {
	rendered := renderDraft(draft, s.d.Cov)
	res, out := s.work(ctx, "critic", 1, criticRules(), criticPrompt(s.ticketText, rendered))
	if out != nil {
		return draft, out
	}
	critique, err := ParseCritique(res.Handoff)
	if err != nil {
		s.persistRejected(res.Handoff, "critic")
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

	prompt := reviseAfterCritiquePrompt(s.ticketText, rendered, critique, s.d.Cov)
	res, out = s.work(ctx, "revise", 1, scoutRules(), prompt)
	if out != nil {
		return draft, out
	}
	rev, out := s.parseRevision(ctx, res.Handoff, len(critique.Objections))
	if out != nil {
		return draft, out
	}

	revised := rev.Draft
	if revised.Premise == PremiseWrong {
		// The whole draft is being withdrawn; wrongPremise ends the run
		// right after this returns, so there is nothing left to resolve
		// and nothing to route.
		return revised, nil
	}
	resolved, openQuestions := routeResolutions(critique.Objections, rev.Resolutions)
	s.prov.Challenges = resolved
	revised.OpenQuestions = append(revised.OpenQuestions, openQuestions...)
	if len(openQuestions) > 0 {
		fmt.Fprintf(s.d.Out, "critic: %d objection(s) unresolved, promoted to open questions\n", len(openQuestions))
	}
	return revised, nil
}

// routeResolutions splits what the reviser said about each objection into
// the reasoning trail (resolved: what was challenged and what changed) and
// the human's open questions (not resolved: the reviser could not settle
// it either, so a person decides). objections and resolutions are the same
// length, paired positionally — [ParseRevision] refuses anything else.
func routeResolutions(objections []Objection, resolutions []Resolution) (resolved []Challenge, openQuestions []string) {
	for i, o := range objections {
		r := resolutions[i]
		if r.Resolved {
			resolved = append(resolved, Challenge{
				Target:      o.Target,
				Summary:     o.Summary,
				Explanation: r.Explanation,
			})
			continue
		}
		openQuestions = append(openQuestions, fmt.Sprintf(
			"A cold critic objected to %s: %s. Not resolved: %s",
			strings.TrimSpace(o.Target), strings.TrimSpace(o.Summary), strings.TrimSpace(r.Explanation)))
	}
	return resolved, openQuestions
}

// interview puts the draft to the human and, if they said anything, hands
// the answers to a cold reviser. A human who answers nothing has approved
// the draft as it stands, and spending a model call to be told so would
// only give a session the chance to talk itself into changes nobody asked
// for.
func (s *planning) interview(ctx context.Context, draft Draft) (Draft, *Outcome) {
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

// revise spawns a fresh session over the draft and what a human said
// against it in the interview. The result replaces the draft whole and is
// validated identically: a revision is not allowed to be a weaker plan
// than the one it replaces. The critic's own revision round is
// [planning.critique]'s, not this one — it additionally demands a
// resolution for every objection, which an interview's free-form answers
// have no equivalent of.
func (s *planning) revise(ctx context.Context, draft Draft, round int, objections, source string) (Draft, *Outcome) {
	prompt := revisePrompt(s.ticketText, renderDraft(draft, s.d.Cov), objections, s.d.Cov, source)
	res, out := s.work(ctx, "revise", round, scoutRules(), prompt)
	if out != nil {
		return draft, out
	}
	revised, out := s.parse(ctx, res.Handoff, "reviser")
	if out != nil {
		// Falling back to the draft would silently discard what the critic
		// or the human just said, and write a plan they had already
		// argued with.
		return draft, out
	}
	return revised, nil
}

// write lands the deliverables in the fixed order, each before the
// transition that advertises it. Every failure past the first write parks
// naming exactly what did land: the ticket is still in In Planning, so
// nothing is advertised that is not there, and a human reading the journal
// knows what to expect on the ticket.
func (s *planning) write(ctx context.Context, draft Draft) Outcome {
	// Resolve the status first. It is a pure read, and it is the one thing
	// that can refuse for reasons nothing here can fix — a drifted board,
	// or the guard — so it happens while nothing has been written. Plan
	// Review, never Needs Input: that status is the scout's other ending,
	// reserved for a blocking question ([wrongPremise], through
	// verbs.Handback). A finished plan is a different kind of "ask a
	// human" — judge, not answer — and Plan Review is what tells the
	// cockpit which queue it belongs in.
	stateID, err := verbs.ResolveState(ctx, s.d.Board, s.d.Cov, s.issue.TeamID, "plan_review")
	if err != nil {
		return *s.park(ctx, fmt.Sprintf("could not resolve the %s status: %v", s.d.Cov.StatusName("plan_review"), err))
	}

	if out := s.preservePriorPlan(ctx); out != nil {
		return *out
	}

	if _, _, err := s.d.Board.UpsertSection(ctx, s.issue.ID, s.issue.Description, PlanSectionID, PlanMarkdown(draft)); err != nil {
		return *s.park(ctx, fmt.Sprintf("the plan could not be written into the description: %v", err))
	}
	fmt.Fprintln(s.d.Out, "wrote the plan into the ticket's description")

	comment := OptionsComment(draft, s.d.Cov.IssueEstimationType, s.d.Cov.StatusName("plan_review"), s.prov)
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
		return *s.park(ctx, fmt.Sprintf("every deliverable is on the ticket, but the move to %s failed: %v — the plan is readable, it just is not on anyone's desk", s.d.Cov.StatusName("plan_review"), err))
	}

	reason := fmt.Sprintf("plan review: %s recommended, plan and options on the ticket, %s for a human to judge",
		strings.TrimSpace(draft.Recommendation.Approach), s.d.Cov.StatusName("plan_review"))
	if err := s.r.Converged(reason); err != nil {
		fmt.Fprintf(s.d.Out, "journal: %v\n", err)
	}
	return Outcome{Kind: journal.Converged, Reason: reason}
}

// preservePriorPlan posts the description's existing plan region as a dated,
// superseded comment before the write that is about to replace it. A plan
// run over a ticket that already carries a plan is a legitimate, deliberate
// human act — moving it back to To Plan and letting a fresh plan run claim
// it — but the plan region is rewritten whole on every plan run, so without
// this step the plan a human read and blessed vanishes the moment the new
// one lands, leaving no trace on the ticket that it ever existed. Called
// before [Board.UpsertSection], the same comment-before-write discipline
// [planning.write] uses everywhere else: what could be destroyed is made
// safe before the write that would destroy it runs. A ticket with no prior
// plan posts nothing.
func (s *planning) preservePriorPlan(ctx context.Context) *Outcome {
	prior, ok, err := linear.ReadSection(s.issue.Description, PlanSectionID)
	if err != nil {
		return s.park(ctx, fmt.Sprintf("could not read the description's existing plan region: %v", err))
	}
	if !ok || strings.TrimSpace(prior) == "" {
		return nil
	}
	if err := s.d.Board.CreateComment(ctx, s.issue.ID, supersededComment(prior, s.prov)); err != nil {
		return s.park(ctx, fmt.Sprintf(
			"the ticket already carries a plan, and preserving it as a comment before replacing it failed: %v — nothing was overwritten", err))
	}
	fmt.Fprintln(s.d.Out, "preserved the ticket's previous plan as a comment before replacing it")
	return nil
}

// runReplan is the re-plan cycle's own path through the loop: read the plan
// already on the ticket and everything a human said about it since, hand
// both to a fresh cold reviser, and revise the plan in place rather than
// drafting one from nothing. No critic, no interview — a re-plan is meant
// to converge on the plan a human is already looking at, not spend another
// full research pass arguing with itself.
//
// No round cap, deliberately, unlike the build loop's review-round and
// CI-attempt caps: each cycle here costs one human act (a comment and the
// re-plan label) before a cycle even starts, where the build loop's rounds
// are machine-paced and need a cap to stop exhaustion becoming a false
// convergence. A human still typing "re-plan" after the third round is not
// runaway machinery; it is the loop working as designed.
func (s *planning) runReplan(ctx context.Context, comments []linear.Comment) Outcome {
	prior, ok, err := linear.ReadSection(s.issue.Description, PlanSectionID)
	if err != nil {
		return *s.park(ctx, fmt.Sprintf("could not read the description's plan region: %v", err))
	}
	if !ok || strings.TrimSpace(prior) == "" {
		return *s.park(ctx, "this ticket is a re-plan, already in "+s.d.Cov.StatusName("in_planning")+
			", but its description carries no plan to revise")
	}

	since := sinceLastPlan(comments)
	if len(since) == 0 {
		return *s.park(ctx, "this ticket was labeled for a re-plan, but no comment followed the plan already "+
			"on it — there is nothing to revise against")
	}
	s.prov.RePlan = true
	s.prov.Comments = len(since)

	res, out := s.work(ctx, "replan", 1, scoutRules(), replanPrompt(s.ticketText, prior, repliesTranscript(since), s.d.Cov))
	if out != nil {
		return *out
	}
	rev, out := s.parseReplan(ctx, res.Handoff)
	if out != nil {
		return *out
	}
	if out := s.wrongPremise(ctx, rev.Draft); out != nil {
		return *out
	}
	return s.writeReplan(ctx, rev)
}

// parseReplan validates a reviser's re-plan handoff the same way
// [planning.parse] validates a first draft — persisting a rejected handoff
// before parking, journaling what came back, then checking the tree —
// except against [ParseReplan], which additionally demands the changes
// account a re-plan comment is built from.
func (s *planning) parseReplan(ctx context.Context, raw json.RawMessage) (Replan, *Outcome) {
	rev, err := ParseReplan(raw, s.d.Cov)
	if err != nil {
		s.persistRejected(raw, "reviser")
		return Replan{}, s.park(ctx, fmt.Sprintf("the reviser's handoff is unusable, so nothing was written to the ticket: %v", err))
	}
	if len(rev.Dropped) > 0 {
		s.note("reviser citations dropped for carrying no line", rev.Dropped)
	}
	s.note("replan handoff", rev)
	if out := s.requireUntouched(ctx, "reviser"); out != nil {
		return Replan{}, out
	}
	return rev, nil
}

// writeReplan lands the re-plan's deliverables: the revised plan region in
// place — no preservePriorPlan, deliberately (see the package doc's note on
// WND-77) — then the changes comment, the estimate, and Plan Review last,
// the same before-the-transition-that-advertises-it ordering [planning.write]
// uses for a first plan.
func (s *planning) writeReplan(ctx context.Context, rev Replan) Outcome {
	stateID, err := verbs.ResolveState(ctx, s.d.Board, s.d.Cov, s.issue.TeamID, "plan_review")
	if err != nil {
		return *s.park(ctx, fmt.Sprintf("could not resolve the %s status: %v", s.d.Cov.StatusName("plan_review"), err))
	}

	if _, _, err := s.d.Board.UpsertSection(ctx, s.issue.ID, s.issue.Description, PlanSectionID, PlanMarkdown(rev.Draft)); err != nil {
		return *s.park(ctx, fmt.Sprintf("the revised plan could not be written into the description: %v", err))
	}
	fmt.Fprintln(s.d.Out, "revised the plan in place")

	comment := RePlanComment(rev.Draft, s.d.Cov.IssueEstimationType, s.d.Cov.StatusName("plan_review"), rev.Changes, s.prov)
	if err := s.d.Board.CreateComment(ctx, s.issue.ID, comment); err != nil {
		return *s.park(ctx, fmt.Sprintf("the revised plan is in the description, but the comment naming what changed failed: %v — the ticket is still in %s, carrying a plan nothing explains", err, s.issue.State.Name))
	}
	fmt.Fprintln(s.d.Out, "posted what changed and why")

	if rev.Draft.Estimate != nil {
		if err := s.d.Board.UpdateIssue(ctx, s.issue.ID, linear.IssueUpdate{Estimate: rev.Draft.Estimate}); err != nil {
			return *s.park(ctx, fmt.Sprintf("the revised plan and its account are on the ticket, but the estimate failed: %v", err))
		}
		fmt.Fprintf(s.d.Out, "set the estimate to %d\n", *rev.Draft.Estimate)
	}

	if err := s.d.Board.UpdateIssue(ctx, s.issue.ID, linear.IssueUpdate{StateID: stateID}); err != nil {
		return *s.park(ctx, fmt.Sprintf("every deliverable is on the ticket, but the move to %s failed: %v — the revised plan is readable, it just is not on anyone's desk", s.d.Cov.StatusName("plan_review"), err))
	}

	reason := fmt.Sprintf("re-plan: %s recommended, plan revised in place, %s for a human to judge",
		strings.TrimSpace(rev.Draft.Recommendation.Approach), s.d.Cov.StatusName("plan_review"))
	if err := s.r.Converged(reason); err != nil {
		fmt.Fprintf(s.d.Out, "journal: %v\n", err)
	}
	return Outcome{Kind: journal.Converged, Reason: reason}
}

// optionsCommentHeader and rePlanCommentHeader are how OptionsComment and
// RePlanComment respectively open — the two comment shapes [sinceLastPlan]
// recognizes as "a plan was posted here" when it looks for the most recent
// one. supersededComment's own header ("## Plan superseded, ...") never
// matches either: it is a snapshot of a plan that has already been
// replaced, not the argument for the one currently on the ticket.
const (
	optionsCommentHeader = "## Plan\n\n"
	rePlanCommentHeader  = "## Plan revised\n\n"
)

// sinceLastPlan returns the comments posted after the most recent plan
// comment this package itself posted — the human's answers to the plan
// currently on the ticket, and nothing this package already argued. Comments
// arrive oldest first (see linear.Client.IssueComments), so the last match
// by index is the most recent.
func sinceLastPlan(comments []linear.Comment) []linear.Comment {
	last := -1
	for i, c := range comments {
		if strings.HasPrefix(c.Body, optionsCommentHeader) || strings.HasPrefix(c.Body, rePlanCommentHeader) {
			last = i
		}
	}
	if last < 0 {
		return nil
	}
	return comments[last+1:]
}

// repliesTranscript renders the human's comments since the last plan for
// the reviser: who said it and what, in order — the re-plan cycle's
// equivalent of [Transcript], sourced from the board instead of an
// interactive session.
func repliesTranscript(comments []linear.Comment) string {
	var b strings.Builder
	for i, c := range comments {
		if i > 0 {
			b.WriteString("\n")
		}
		author := c.Author
		if author == "" {
			author = "a human"
		}
		fmt.Fprintf(&b, "%s wrote:\n\n%s\n", author, blockquote(c.Body))
	}
	return b.String()
}

// work journals a phase, spawns its worker, and journals what came back. A
// worker failure — no handoff, a timeout, a spawn error — parks: without
// the report there is nothing to tell a crash from a success.
//
// The one exception is a failure the harness itself reported as
// infrastructure rather than as anything about the research (see
// [worker.Retryable]), which respawns the scout up to Caps.WorkerRetries
// times at the same round. A scout costs a whole model call and produces
// nothing until it hands off, so a provider error is the most expensive
// possible thing to treat as a verdict.
func (s *planning) work(ctx context.Context, phase string, round int, rules []string, prompt string) (worker.Result, *Outcome) {
	for attempt := 0; ; attempt++ {
		if attempt == 0 {
			fmt.Fprintf(s.d.Out, "phase %s: spawning a cold worker (%s)\n", phase, s.d.Harness)
		} else {
			fmt.Fprintf(s.d.Out, "phase %s: respawning a cold worker (%s), retry %d of %d\n",
				phase, s.d.Harness, attempt, s.d.Cov.Caps.WorkerRetries)
		}
		if err := s.r.StartPhase(phase, round); err != nil {
			// An unjournaled phase must not run; that is the journal's one rule.
			return worker.Result{}, s.park(ctx, fmt.Sprintf("could not journal phase %s: %v", phase, err))
		}
		// Built after StartPhase, every time round: HandoffPath is named
		// for the journal's *open* phase and round, so a spec built before
		// the phase opened would point the scout at the previous phase's
		// handoff file.
		spec := worker.Spec{
			Mode:        phase + " (read-only research; no worktree, no branch, no CI)",
			Rules:       rules,
			Prompt:      prompt,
			Dir:         s.d.Repo,
			ScratchDir:  s.r.ScratchDir(),
			HandoffPath: s.r.HandoffPath(),
			Timeout:     s.d.Cov.Caps.Timeout(phase),
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
			Attempt:   attempt + 1,
		}
		if res.Usage != nil {
			detail.TokensIn = res.Usage.InputTokens
			detail.TokensOut = res.Usage.OutputTokens
		}
		if err != nil {
			detail.Error = err.Error()
			detail.OutputTail = tailOf(res.Output)
			detail.Transient = res.Transient
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
		if err == nil {
			return res, nil
		}
		if !s.mayRetry(ctx, phase, round, attempt, res, err) {
			return res, s.park(ctx, fmt.Sprintf("the %s worker failed: %v", phase, err))
		}
	}
}

// mayRetry decides whether a failed phase gets another scout, and says so in
// both the journal and the narration either way. [worker.Retryable] answers
// "was this failure about the work"; everything here is about whether
// retrying is safe in this repository.
//
// The safety check is the plan run's own: a scout is told to read, not write, and
// s.d.Repo is usually a person's own checkout rather than a worktree this
// run owns. If a dying scout left a change behind, requireUntouched is
// going to park and hand that checkout back to its owner — so respawning a
// second scout into it first would be writing more into a directory
// somebody is about to be asked to look at. A status git cannot read counts
// as changed: an unknown checkout is not an untouched one.
func (s *planning) mayRetry(ctx context.Context, phase string, round, attempt int, res worker.Result, err error) bool {
	if !worker.Retryable(res, err, ctx.Err() != nil) {
		return false
	}
	left := s.d.Cov.Caps.WorkerRetries - attempt
	if left <= 0 {
		fmt.Fprintf(s.d.Out, "phase %s: the harness called this failure infrastructure, but %d retries are already spent; parking\n",
			phase, s.d.Cov.Caps.WorkerRetries)
		s.note("transient worker failure, out of retries", retryNote{
			Phase: phase, Round: round, Attempt: attempt + 1,
			Retries: s.d.Cov.Caps.WorkerRetries, Error: err.Error(),
		})
		return false
	}
	after, serr := s.d.Tree.Status(ctx, s.d.Repo)
	if serr != nil || after != s.treeBefore {
		why := "the checkout is no longer as the scout found it"
		if serr != nil {
			why = fmt.Sprintf("the checkout could not be read (%v)", serr)
		}
		fmt.Fprintf(s.d.Out, "phase %s: the harness called this failure infrastructure, but %s, so nothing is respawned into %s; parking\n",
			phase, why, s.d.Repo)
		s.note("transient worker failure, checkout not untouched", retryNote{
			Phase: phase, Round: round, Attempt: attempt + 1,
			Retries: s.d.Cov.Caps.WorkerRetries, Error: err.Error(), Tree: why,
		})
		return false
	}
	fmt.Fprintf(s.d.Out, "phase %s: the harness reported infrastructure, not the research; retrying (%d left)\n", phase, left)
	s.note("transient worker failure, retrying", retryNote{
		Phase: phase, Round: round, Attempt: attempt + 1,
		Retries: s.d.Cov.Caps.WorkerRetries, Error: err.Error(),
	})
	return true
}

// heartbeat returns the worker.Spec.OnHeartbeat callback for one phase: a
// closure that renews the run's lease on every tick, so a long single phase
// keeps looking alive to a lease reader instead of going stale the moment it
// passes its first minute. A renewal failure is narrated, never fatal — the
// worker is mid-flight, and a lease write hiccup is not a reason to kill it.
func (s *planning) heartbeat(phase string, round int) func() {
	return func() {
		if err := s.r.Heartbeat(); err != nil {
			fmt.Fprintf(s.d.Out, "phase %s round %d: heartbeat renewal failed: %v\n", phase, round, err)
		}
	}
}

// requireUntouched parks if a worker changed the repository it was told to
// read.
//
// A plan run has no worktree of its own: the scout reads the checkout the
// command was run from, which is usually a person's. So a stray edit is not
// a mess in a directory this run owns and can throw away — it is a change
// in somebody's working copy that they did not make and will not expect.
// Parking is what puts it in front of them, and it is why the handoff is
// journaled before this runs: the research survives the park.
func (s *planning) requireUntouched(ctx context.Context, phase string) *Outcome {
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
func (s *planning) handback(ctx context.Context, comment, reason string) *Outcome {
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
func (s *planning) park(ctx context.Context, reason string) *Outcome {
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
func (s *planning) note(message string, detail any) {
	if err := s.r.Note(message, detail); err != nil {
		fmt.Fprintf(s.d.Out, "journal: %v\n", err)
	}
}
