// Package run is the core orchestrator: one process owning one ticket from
// claim to a terminal state, spawning a cold worker per phase — implement,
// fix-CI, review, revise — with phases and caps from the covenant.
//
// The rules here were each paid for in the reference system:
//
//   - The orchestrator makes every Linear and GitHub write; workers are
//     mute. Workers commit in the worktree; the orchestrator pushes, opens
//     the PR, titles it, labels the ticket and moves its status. Two
//     writers on one ticket cannot be reconciled afterwards, so the second
//     writer is not allowed to exist (and the worker package proves it
//     cannot).
//
//   - Exactly one terminal state per run: In Review plus ready-for-human,
//     Needs Input plus a reason, or parked plus a reason. The run journal
//     guarantees the record (its Close backstop parks any path nobody
//     wrote an ending for), and this loop guarantees the meaning.
//
//   - Comment-before-status on every hand-back, via verbs.Handback — the
//     ordering is reused, not re-derived.
//
//   - Convergence only on positive evidence, never on the exhaustion of a
//     counter. A reviewer's approval must state what it verified; a cap
//     running out is a hand-back that says so, with the final round's
//     findings quoted whole (the PW-176 lesson: a cap that mislabels a
//     real finding as "a disagreement" hands work back unattempted).
//
//   - A reviewer that produces no parseable handoff parks the run.
//     Converging there would turn every reviewer crash into a clean pass.
//
//   - Findings without a concrete failure scenario are dropped in code
//     before anything downstream — a revise prompt, a hand-back comment —
//     ever sees them.
//
//   - Conventions the orchestrator can make true are made true in code:
//     PR titles lead with the bracketed identifier, written at open and
//     repaired at convergence; ticket references in PR bodies carry a
//     gloss (the PW-189 lesson).
//
//   - Hand-backs carry the worker's own account, verbatim (the PW-190
//     lesson), and the handoff schema carries description corrections and
//     plan deviations, applied to the ticket by the orchestrator (the
//     PW-191 lesson).
//
// Parking writes only the journal, deliberately: a park must be reachable
// when Linear itself is what failed.
package run

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/ticket"
	"github.com/mattwalters/wand/internal/verbs"
	"github.com/mattwalters/wand/internal/worker"
)

// Outcome is how the run ended, for the caller and the scheduler.
type Outcome struct {
	// Kind reuses the journal's vocabulary: converged, handed_back, parked.
	Kind   journal.Outcome
	Reason string
	PRURL  string
	RunID  string
}

// Exit codes are a contract a scheduler can read. 1 is deliberately absent:
// it means the command failed before a run existed (bad flags, refused
// claim, no journal), which is fang's generic failure exit.
const (
	ExitConverged  = 0
	ExitHandedBack = 2
	ExitParked     = 3
)

// ExitCode maps the outcome onto the contract.
func (o Outcome) ExitCode() int {
	switch o.Kind {
	case journal.Converged:
		return ExitConverged
	case journal.HandedBack:
		return ExitHandedBack
	default:
		return ExitParked
	}
}

// Execute owns one ticket from claim to a terminal state.
//
// An error return means no run happened — and nothing is left claimed: a
// refused claim wrote nothing, and a journal that would not open hands the
// claim straight back before the error surfaces. Exit 1's "nothing to
// sweep" promise is kept in code, not by hoping the store never fails.
// Once a run exists, every path ends in exactly one Outcome instead —
// including interrupts, which park with the signal as the reason.
func Execute(ctx context.Context, d Deps, store *journal.Store, identifier string) (Outcome, error) {
	if err := d.validate(); err != nil {
		return Outcome{}, err
	}
	if d.Out == nil {
		d.Out = io.Discard
	}

	// Claim before anything touches the filesystem: the status move is the
	// cheapest place to lose a race, and losing it here costs nothing —
	// not even a run directory.
	claimed, err := verbs.Claim(ctx, d.Board, d.Cov, identifier)
	if err != nil {
		return Outcome{}, err
	}
	issue := claimed.Issue
	fmt.Fprintf(d.Out, "claimed %s: %s, assigned to %s\n",
		issue.Identifier, d.Cov.StatusName("in_progress"), claimed.Assignee)

	r, err := store.Create(journal.Meta{
		Ticket:  issue.Identifier,
		Verb:    "run",
		Repo:    d.Repo,
		Harness: d.Harness,
	})
	if err != nil {
		// The claim already landed, and exit 1 promises a scheduler there
		// is nothing to sweep — so nothing may stay silently claimed. Hand
		// the ticket back with the journal failure as the reason.
		comment := fmt.Sprintf("This run claimed the ticket but could not open its run journal, so it stopped before doing any work. The store must be fixed before a rerun: %v", err)
		if _, herr := verbs.Handback(ctx, d.Board, d.Cov, issue.Identifier, comment); herr != nil {
			return Outcome{}, fmt.Errorf(
				"%s is claimed (In Progress, assigned) but the run journal would not open: %w — and the hand-back failed too (%v); hand the ticket back or fix the store before retrying", issue.Identifier, err, herr)
		}
		return Outcome{}, fmt.Errorf(
			"the run journal would not open: %w — %s was handed back (Needs Input) with that as the reason", err, issue.Identifier)
	}
	defer r.Close()
	fmt.Fprintf(d.Out, "run %s journaling to %s\n", r.ID(), r.Dir())

	l := &loop{d: d, r: r, issue: issue}
	out := l.run(ctx)
	out.RunID = r.ID()
	fmt.Fprintf(d.Out, "run %s ended: %s — %s\n", r.ID(), out.Kind, out.Reason)
	return out, nil
}

// loop is one run's working state. Methods that can end the run return
// *Outcome; nil means carry on.
type loop struct {
	d     Deps
	r     *journal.Run
	issue linear.Issue

	tree   string // the worktree
	branch string
	base   Base
	prURL  string
	pushed bool // the branch has reached origin at least once

	ticketText string
	ciFailures int
	deviations []string
}

// phaseDetail is what EndPhase journals about a worker, bounded so the
// journal stays readable. Cross-harness comparison data lives here for
// free — this struct, journaled once per phase, is the run-verb half of
// the stable ledger schema described in the package doc of
// internal/journal.
//
// The operational fields (Harness through DiffStat) follow one rule: a
// metric a harness or this phase cannot report is omitted, never
// estimated. TokensIn/TokensOut are pointers for the same reason worker.
// Usage's are — a reported zero and "the harness didn't say" must stay
// distinguishable — so a nil pointer here means the harness carries no
// usage adapter, or its output didn't parse, not that zero tokens were
// spent.
type phaseDetail struct {
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Handoff    bool   `json:"handoff"`
	Error      string `json:"error,omitempty"`
	OutputTail string `json:"output_tail,omitempty"`

	// Harness and Model are what Deps chose for every phase; journaled per
	// phase rather than only once on the run, since the ledger's readers
	// (wand stats, the UI usage panel, wand analyze) read the journal a
	// phase at a time.
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
	// TokensIn and TokensOut come from worker.Result.Usage, when the
	// adapter reported one. Nil when it did not.
	TokensIn  *int64 `json:"tokens_in,omitempty"`
	TokensOut *int64 `json:"tokens_out,omitempty"`
	// WallClock is how long Workers.Run took, as a Go duration string
	// ("1m2.5s") — the journal's own convention (see internal/journal's
	// package doc) is that a human reading the file is the reader it is
	// for, and a duration string reads directly where a millisecond count
	// would need converting.
	WallClock string `json:"wall_clock,omitempty"`
	// DiffStat is git's own --shortstat of the worktree against the run's
	// base, taken at the end of every phase — the run verb only, since
	// a plan run has no worktree.
	DiffStat string `json:"diff_stat,omitempty"`
	// Attempt is which spawn of this phase-and-round this record ends,
	// counting from 1. It is above 1 only when an earlier attempt failed
	// transiently and was retried, so a reader who has never seen a retry
	// never sees the field.
	Attempt int `json:"attempt,omitempty"`
	// Transient is what the adapter made of a failure: true when the
	// harness itself reported infrastructure rather than the work. Kept on
	// the failing record so a later reader can tell a retried park from
	// one that never had the option.
	Transient bool `json:"transient,omitempty"`
}

// retryNote is the detail on the journal note a transient failure writes:
// what failed, which attempt it was, and how much budget the covenant
// allowed. Written whether or not the retry happens, because "we could have
// retried and did not" is the more surprising of the two outcomes and the
// one a reader will want explained.
type retryNote struct {
	Phase   string `json:"phase"`
	Round   int    `json:"round"`
	Attempt int    `json:"attempt"`
	Retries int    `json:"retries"`
	Error   string `json:"error"`
	// Tree says why a clean-tree check refused the retry; absent when the
	// tree was not what stopped it.
	Tree string `json:"tree,omitempty"`
}

// journalTail bounds the output tail a journal record keeps.
const journalTail = 4 << 10

func (l *loop) run(ctx context.Context) Outcome {
	caps := l.d.Cov.Caps

	// --- workspace ---------------------------------------------------
	base, err := l.d.Git.DefaultBranch(ctx, l.d.Repo)
	if err != nil {
		return *l.park(ctx, fmt.Sprintf("could not resolve the default branch: %v", err))
	}
	branch := l.issue.BranchName
	if branch == "" {
		branch = "wand/" + strings.ToLower(l.issue.Identifier)
	}
	tree := filepath.Join(l.r.Dir(), "tree")
	// The worktree starts from the remote-tracking ref, never the bare
	// branch name: the local branch may not exist, and where it does it is
	// only as fresh as the last pull.
	if err := l.d.Git.AddWorktree(ctx, l.d.Repo, tree, branch, base.Ref); err != nil {
		return *l.park(ctx, fmt.Sprintf("could not create the worktree: %v", err))
	}
	l.tree, l.branch, l.base = tree, branch, base
	fmt.Fprintf(l.d.Out, "worktree %s on branch %s (base %s)\n", tree, branch, base.Ref)

	if p := l.d.Cov.Commands.Provision; p != "" {
		ok, output, err := l.shell(ctx, p)
		if err != nil {
			return *l.park(ctx, fmt.Sprintf("the provision command could not run: %v", err))
		}
		if !ok {
			return *l.park(ctx, fmt.Sprintf("the provision command failed:\n%s", worker.Clip(output, journalTail)))
		}
	}

	comments, err := l.d.Board.IssueComments(ctx, l.issue.ID)
	if err != nil {
		return *l.park(ctx, fmt.Sprintf("could not read the ticket's comments: %v", err))
	}
	l.ticketText = ticket.Render(l.issue, comments)

	// --- implement ---------------------------------------------------
	res, out := l.work(ctx, "implement", 1, l.workRules(), implementPrompt(l.ticketText))
	if out != nil {
		return *out
	}
	impl, err := ParseWork(res.Handoff)
	if err != nil {
		return *l.park(ctx, fmt.Sprintf("the implement worker's handoff is unusable: %v", err))
	}
	if out := l.afterWork(ctx, "implement", impl); out != nil {
		return *out
	}
	if out := l.requireClean(ctx, "implement"); out != nil {
		return *out
	}
	ahead, err := l.d.Git.CommitsAhead(ctx, l.tree, l.base.Ref)
	if err != nil {
		return *l.park(ctx, fmt.Sprintf("could not count the branch's commits: %v", err))
	}
	if ahead == 0 {
		return *l.park(ctx, `the implement worker reported "done" but made no commits; a run with nothing on its branch has nothing to review`)
	}

	// --- CI, then the PR ---------------------------------------------
	if out := l.green(ctx); out != nil {
		return *out
	}
	if out := l.push(ctx); out != nil {
		return *out
	}
	title := TitleFor(l.issue.Identifier, impl.Title, l.issue.Title)
	if out := l.ensurePR(ctx, title, PRBody(l.issue, impl.Summary, l.deviations)); out != nil {
		return *out
	}

	// --- review rounds -----------------------------------------------
	for round := 1; ; round++ {
		res, out := l.work(ctx, "review", round, reviewRules(), reviewPrompt(l.ticketText, l.base.Ref))
		if out != nil {
			return *out
		}
		review, err := ParseReview(res.Handoff)
		if err != nil {
			// The one explicitly-parked failure: a converging default here
			// would turn every reviewer crash into a clean pass.
			return *l.park(ctx, fmt.Sprintf("the round-%d reviewer produced no parseable handoff: %v — a run does not converge on a reviewer it could not read", round, err))
		}
		if out := l.requireClean(ctx, "review"); out != nil {
			return *out
		}

		if review.Verdict == "approve" {
			return l.converge(ctx, round, review.Summary)
		}

		findings, dropped := Concrete(review.Findings)
		if dropped > 0 {
			l.note(fmt.Sprintf("dropped %d review finding(s) without a concrete failure scenario", dropped), nil)
			fmt.Fprintf(l.d.Out, "review round %d: dropped %d finding(s) with no concrete failure scenario\n", round, dropped)
		}

		if round == caps.ReviewRounds {
			// Exhaustion, named as exhaustion. The final round's findings
			// are real findings; hand them to a human whole.
			if len(findings) == 0 {
				return *l.handback(ctx,
					vagueReviewComment(caps.ReviewRounds, l.branch, l.prURL, l.tree, l.workState(ctx)),
					fmt.Sprintf("the review-round cap (%d) ran out; the final reviewer withheld approval but raised no concrete findings", caps.ReviewRounds))
			}
			return *l.handback(ctx,
				reviewCapComment(caps.ReviewRounds, findings, l.branch, l.prURL, l.tree, l.workState(ctx)),
				fmt.Sprintf("the review-round cap (%d) ran out with %d finding(s) still standing", caps.ReviewRounds, len(findings)))
		}
		if len(findings) == 0 {
			// Nothing concrete to revise against; the next cold reviewer
			// either approves or states a scenario.
			continue
		}

		res, out = l.work(ctx, "revise", round, l.workRules(), revisePrompt(l.ticketText, findings))
		if out != nil {
			return *out
		}
		rev, err := ParseWork(res.Handoff)
		if err != nil {
			return *l.park(ctx, fmt.Sprintf("the round-%d revise worker's handoff is unusable: %v", round, err))
		}
		if out := l.afterWork(ctx, "revise", rev); out != nil {
			return *out
		}
		if out := l.requireClean(ctx, "revise"); out != nil {
			return *out
		}
		if out := l.green(ctx); out != nil {
			return *out
		}
		if out := l.push(ctx); out != nil {
			return *out
		}
	}
}

// work journals a phase, spawns its worker, and journals what came back.
// A worker failure — no handoff, timeout, spawn error — parks: the loop
// cannot tell a crash from a success without the report.
//
// The one exception is a failure the harness itself reported as
// infrastructure rather than as anything about the work (see
// [worker.Retryable]): a provider error, a host that suspended
// mid-response. That phase respawns, up to Caps.WorkerRetries times, and
// parks with the original reason once the budget is out.
//
// Every attempt runs at the *same* round, journalled as its own
// StartPhase/EndPhase pair with a note in between saying why it happened
// again. Bumping the round instead would spend review or CI budget on a
// closed laptop lid, which is the opposite of the point. Reusing the round
// also reuses HandoffPath, which is safe twice over: collect deletes a
// handoff the moment it reads it, and worker.Run clears anything left at
// the path before it spawns.
func (l *loop) work(ctx context.Context, phase string, round int, rules []string, prompt string) (worker.Result, *Outcome) {
	for attempt := 0; ; attempt++ {
		if attempt == 0 {
			fmt.Fprintf(l.d.Out, "phase %s round %d: spawning worker (%s)\n", phase, round, l.d.Harness)
		} else {
			fmt.Fprintf(l.d.Out, "phase %s round %d: respawning worker (%s), retry %d of %d\n",
				phase, round, l.d.Harness, attempt, l.d.Cov.Caps.WorkerRetries)
		}
		if err := l.r.StartPhase(phase, round); err != nil {
			// An unjournaled phase must not run; that is the package's one rule.
			return worker.Result{}, l.park(ctx, fmt.Sprintf("could not journal phase %s: %v", phase, err))
		}
		// Built after StartPhase, every time round: HandoffPath is named
		// for the journal's *open* phase and round, so a spec built before
		// the phase opened would point the worker at the previous phase's
		// handoff file — or, on the first phase, at the run's bare
		// handoff.json.
		spec := worker.Spec{
			Mode:        phase,
			Rules:       rules,
			Prompt:      prompt,
			Dir:         l.tree,
			ScratchDir:  l.r.ScratchDir(),
			HandoffPath: l.r.HandoffPath(),
			Timeout:     l.d.Cov.Caps.WorkerTimeout,
			Model:       l.d.Model,
			Effort:      l.d.Effort,
			Out:         l.d.Out,
			Label:       fmt.Sprintf("%s round %d", phase, round),
			OnHeartbeat: l.heartbeat(phase, round),
		}
		start := time.Now()
		res, err := l.d.Workers.Run(ctx, spec)
		elapsed := time.Since(start)
		detail := phaseDetail{
			ExitCode:  res.ExitCode,
			TimedOut:  res.TimedOut,
			Handoff:   res.Handoff != nil,
			Harness:   l.d.Harness,
			Model:     l.d.Model,
			WallClock: elapsed.String(),
			Attempt:   attempt + 1,
		}
		if res.Usage != nil {
			detail.TokensIn = res.Usage.InputTokens
			detail.TokensOut = res.Usage.OutputTokens
		}
		// Best-effort, like workState: a metrics call that cannot run (no
		// commits yet, a transient git failure) must not park a run over a
		// diff stat, so a failure here just leaves the field absent.
		if stat, derr := l.d.Git.DiffStat(ctx, l.tree, l.base.Ref); derr == nil {
			detail.DiffStat = stat
		}
		if err != nil {
			detail.Error = err.Error()
			detail.OutputTail = worker.Clip(res.Output, journalTail)
			detail.Transient = res.Transient
		}
		if jerr := l.r.EndPhase(detail); jerr != nil {
			return res, l.park(ctx, fmt.Sprintf("could not journal the end of phase %s: %v", phase, jerr))
		}
		if ctx.Err() != nil {
			return res, l.park(ctx, "the run was interrupted")
		}
		if err == nil {
			return res, nil
		}
		if !l.mayRetry(ctx, phase, round, attempt, res, err) {
			return res, l.park(ctx, fmt.Sprintf("the %s worker (round %d) failed: %v", phase, round, err))
		}
	}
}

// mayRetry decides whether a failed phase gets another worker, and says so
// in both the journal and the narration either way. It is the impure half
// of the policy: [worker.Retryable] answers "was this failure about the
// work", and everything this adds is about whether retrying is safe *here*.
//
// A dirty tree is the check that matters. Work phases commit, so a worker
// that died mid-edit leaves uncommitted changes, and requireClean already
// parks on those — but only *after* work returns, which a failing phase
// never reaches. Without this check a transient death would respawn a
// second worker on top of the first one's half-finished edits, in a tree
// nobody has looked at. A tree git cannot read counts as dirty: an unknown
// tree is not a clean one.
func (l *loop) mayRetry(ctx context.Context, phase string, round, attempt int, res worker.Result, err error) bool {
	if !worker.Retryable(res, err, ctx.Err() != nil) {
		return false
	}
	left := l.d.Cov.Caps.WorkerRetries - attempt
	if left <= 0 {
		fmt.Fprintf(l.d.Out, "phase %s round %d: the harness called this failure infrastructure, but %d retries are already spent; parking\n",
			phase, round, l.d.Cov.Caps.WorkerRetries)
		l.note("transient worker failure, out of retries", retryNote{
			Phase: phase, Round: round, Attempt: attempt + 1,
			Retries: l.d.Cov.Caps.WorkerRetries, Error: err.Error(),
		})
		return false
	}
	if l.tree != "" {
		dirty, derr := l.d.Git.Dirty(ctx, l.tree)
		if derr != nil || dirty {
			why := "the tree is dirty"
			if derr != nil {
				why = fmt.Sprintf("the tree could not be read (%v)", derr)
			}
			fmt.Fprintf(l.d.Out, "phase %s round %d: the harness called this failure infrastructure, but %s, so the work is at risk; parking with the worktree preserved at %s\n",
				phase, round, why, l.tree)
			l.note("transient worker failure, tree not clean", retryNote{
				Phase: phase, Round: round, Attempt: attempt + 1,
				Retries: l.d.Cov.Caps.WorkerRetries, Error: err.Error(), Tree: why,
			})
			return false
		}
	}
	fmt.Fprintf(l.d.Out, "phase %s round %d: the harness reported infrastructure, not the work; retrying (%d left)\n",
		phase, round, left)
	l.note("transient worker failure, retrying", retryNote{
		Phase: phase, Round: round, Attempt: attempt + 1,
		Retries: l.d.Cov.Caps.WorkerRetries, Error: err.Error(),
	})
	return true
}

// heartbeat returns the worker.Spec.OnHeartbeat callback for one phase: a
// closure that renews the run's lease on every tick, so a long single phase
// keeps looking alive to a lease reader instead of going stale the moment it
// passes its first minute. A renewal failure is narrated, never fatal — the
// worker is mid-flight, and a lease write hiccup is not a reason to kill it.
func (l *loop) heartbeat(phase string, round int) func() {
	return func() {
		if err := l.r.Heartbeat(); err != nil {
			fmt.Fprintf(l.d.Out, "phase %s round %d: heartbeat renewal failed: %v\n", phase, round, err)
		}
	}
}

// afterWork applies what a work handoff carries beyond its status: plan
// deviations and description corrections, then the blocked hand-back if the
// worker stopped.
func (l *loop) afterWork(ctx context.Context, phase string, h WorkHandoff) *Outcome {
	if len(h.PlanDeviations) > 0 {
		// Journaled as they arrive, not only carried: a park writes nothing
		// but the journal, and a deviation that reached only the ticket
		// would still die on every parked run.
		l.deviations = append(l.deviations, h.PlanDeviations...)
		l.note(fmt.Sprintf("the %s worker reported %d plan deviation(s)", phase, len(h.PlanDeviations)),
			map[string][]string{"deviations": h.PlanDeviations})
	}
	l.applyCorrections(ctx, phase, h.DescriptionCorrections)
	if h.Status == "blocked" {
		return l.handback(ctx,
			blockedComment(phase, h.Reason, l.branch, l.prURL, l.tree, l.workState(ctx)),
			fmt.Sprintf("the %s worker reported blocked: %s", phase, firstLine(h.Reason)))
	}
	return nil
}

// applyCorrections lands the worker's description corrections on the
// ticket: the quote-comment first, then the edit, one writer throughout.
// Best-effort by design — a correction that cannot anchor or land is
// journaled and skipped, never a reason to lose the run.
func (l *loop) applyCorrections(ctx context.Context, phase string, corrs []Correction) {
	if len(corrs) == 0 {
		return
	}
	// Anchor against the ticket as it is now, not the claim-time snapshot:
	// a human may have edited the description while the worker ran, and an
	// update built on a stale base would silently erase their words.
	fresh, err := l.d.Board.IssueByIdentifier(ctx, l.issue.Identifier)
	if err != nil {
		l.note("description corrections skipped: could not re-read the ticket", map[string]string{"error": err.Error()})
		return
	}
	desc := fresh.Description
	var applied []Correction
	for _, c := range corrs {
		next, err := linear.WithReplacement(desc, c.Old, c.New)
		if err != nil {
			l.note("description correction skipped", map[string]string{"old": c.Old, "error": err.Error()})
			continue
		}
		desc = next
		applied = append(applied, c)
	}
	if len(applied) == 0 {
		return
	}
	// Comment first, quoting the disproven wording — the comment is where
	// it survives — then the edit it promises.
	if err := l.d.Board.CreateComment(ctx, l.issue.ID, correctionsComment(phase, applied)); err != nil {
		l.note("description corrections not posted", map[string]string{"error": err.Error()})
		return
	}
	if err := l.d.Board.UpdateIssue(ctx, l.issue.ID, linear.IssueUpdate{Description: &desc}); err != nil {
		l.note("description corrections commented but not applied", map[string]string{"error": err.Error()})
		return
	}
	l.issue.Description = desc
	l.refreshTicketText(ctx)
	fmt.Fprintf(l.d.Out, "%s: applied %d description correction(s)\n", phase, len(applied))
}

// refreshTicketText re-renders the prompt-facing ticket after the run
// itself changed it: a reviewer must not be handed wording the run already
// disproved and corrected. Best-effort — on failure the old render stands,
// which is exactly the state before the correction existed.
func (l *loop) refreshTicketText(ctx context.Context) {
	comments, err := l.d.Board.IssueComments(ctx, l.issue.ID)
	if err != nil {
		l.note("ticket text not refreshed after corrections", map[string]string{"error": err.Error()})
		return
	}
	l.ticketText = ticket.Render(l.issue, comments)
}

// requireClean parks on a dirty tree. Work phases must commit; the reviewer
// must not touch the tree at all. Either way, uncommitted state is work at
// risk, and parking is what preserves it.
func (l *loop) requireClean(ctx context.Context, phase string) *Outcome {
	dirty, err := l.d.Git.Dirty(ctx, l.tree)
	if err != nil {
		return l.park(ctx, fmt.Sprintf("could not check the tree after %s: %v", phase, err))
	}
	if dirty {
		return l.park(ctx, fmt.Sprintf("the %s worker left the tree dirty; uncommitted changes are work at risk, so the run parks with the worktree preserved at %s", phase, l.tree))
	}
	return nil
}

// workState reads what a hand-back can truthfully say about where the work
// sits. Best-effort — a hand-back must not fail because git could not be
// read — so !known means the comment says less, never that it guesses.
func (l *loop) workState(ctx context.Context) workState {
	s := workState{pushed: l.pushed}
	if l.tree == "" {
		return s
	}
	ahead, aerr := l.d.Git.CommitsAhead(ctx, l.tree, l.base.Ref)
	dirty, derr := l.d.Git.Dirty(ctx, l.tree)
	if aerr != nil || derr != nil {
		return s
	}
	s.known, s.ahead, s.dirty = true, ahead, dirty
	return s
}

// green runs the verify command until it passes, spawning a fix-CI worker
// per failure, capped. The cap counts across the whole run: a revise that
// re-breaks the build draws from the same budget.
func (l *loop) green(ctx context.Context) *Outcome {
	verify := l.d.Cov.Commands.Verify
	caps := l.d.Cov.Caps
	for {
		fmt.Fprintf(l.d.Out, "verify: %s\n", verify)
		ok, output, err := l.shell(ctx, verify)
		if err != nil {
			return l.park(ctx, fmt.Sprintf("the verify command could not run: %v", err))
		}
		if ok {
			fmt.Fprintln(l.d.Out, "verify: green")
			return nil
		}
		l.ciFailures++
		fmt.Fprintf(l.d.Out, "verify: red (failure %d, cap %d)\n", l.ciFailures, caps.CIAttempts)
		if l.ciFailures > caps.CIAttempts {
			return l.handback(ctx,
				ciCapComment(caps.CIAttempts, verify, output, l.branch, l.prURL, l.tree, l.workState(ctx)),
				fmt.Sprintf("the fix-CI cap (%d) ran out with verify still failing", caps.CIAttempts))
		}
		res, out := l.work(ctx, "fix-ci", l.ciFailures, l.workRules(), fixCIPrompt(verify, output))
		if out != nil {
			return out
		}
		h, err := ParseWork(res.Handoff)
		if err != nil {
			return l.park(ctx, fmt.Sprintf("the fix-CI worker's handoff is unusable: %v", err))
		}
		if out := l.afterWork(ctx, "fix-ci", h); out != nil {
			return out
		}
		if out := l.requireClean(ctx, "fix-ci"); out != nil {
			return out
		}
	}
}

// shell runs one covenant command in the worktree, bounded by the worker
// timeout — a hung verify is a zombie factory too.
func (l *loop) shell(ctx context.Context, command string) (bool, string, error) {
	cctx, cancel := context.WithTimeout(ctx, l.d.Cov.Caps.WorkerTimeout)
	defer cancel()
	return l.d.Shell.Run(cctx, l.tree, command)
}

func (l *loop) push(ctx context.Context) *Outcome {
	if err := l.d.Git.Push(ctx, l.tree, l.branch); err != nil {
		return l.park(ctx, fmt.Sprintf("push failed: %v", err))
	}
	l.pushed = true
	return nil
}

// ensurePR opens the PR, or repairs the title of one already open — the
// convention lives in this code path, entered at open and again at
// convergence.
func (l *loop) ensurePR(ctx context.Context, title, body string) *Outcome {
	pr, found, err := l.d.Hub.PRForBranch(ctx, l.tree, l.branch)
	if err != nil {
		return l.park(ctx, fmt.Sprintf("could not look up the branch's PR: %v", err))
	}
	// Only an open PR is one to repair. A merged or closed PR on this
	// branch belongs to a cycle that already ended; this one needs its own.
	if !found || pr.State != PRStateOpen {
		// GitHub is asked to merge into the branch, not into the
		// remote-tracking ref that names it locally.
		url, err := l.d.Hub.OpenPR(ctx, l.tree, l.base.Name, l.branch, title, body)
		if err != nil {
			return l.park(ctx, fmt.Sprintf("could not open the PR: %v", err))
		}
		l.prURL = url
		fmt.Fprintf(l.d.Out, "opened PR %s\n", url)
		return nil
	}
	return l.repairTitle(ctx, pr)
}

// convergeOnAMergedPR is convergence for a run whose PR landed while it was
// still finishing — a human merged it between the reviewer's approval and
// this lookup, which is a race the loop cannot prevent and should not lose.
//
// It is the ordinary converge minus everything that only makes sense while
// a PR is open:
//
//   - No title repair. This repo squash-merges, so the subject is already
//     on the default branch; retitling a merged PR changes nothing but the
//     PR page.
//   - No unresolved-thread check. Merging is a human's answer to their own
//     threads, and handing the ticket back over them would ask a question
//     that has been settled by the strongest means available.
//   - No ready-for-human label. It means "In Review, with a PR a human
//     should read", and a merged PR has been read.
//   - **No status move.** The merge automation owns this ticket's status
//     now, and In Review over a ticket the merge already closed reopens a
//     close — which is a human's call, the same reasoning verbs.refuseIfClosed
//     is built on.
//
// What is left is the part that was always the point: say on the ticket
// what the run did, and journal one terminal record saying it converged.
func (l *loop) convergeOnAMergedPR(ctx context.Context, round int, reviewSummary string, pr PR) Outcome {
	l.prURL = pr.URL

	comment := mergedComment(round, reviewSummary, pr.URL, l.deviations)
	if err := l.d.Board.CreateComment(ctx, l.issue.ID, comment); err != nil {
		return *l.park(ctx, fmt.Sprintf("the PR merged and the run converged, but the summary comment failed: %v", err))
	}

	if err := l.d.Git.RemoveWorktree(ctx, l.d.Repo, l.tree); err != nil {
		fmt.Fprintf(l.d.Out, "note: could not remove the worktree: %v\n", err)
	}

	reason := fmt.Sprintf("reviewer approved on round %d; PR %s had already merged", round, pr.URL)
	if err := l.r.Converged(reason); err != nil {
		fmt.Fprintf(l.d.Out, "journal: %v\n", err)
	}
	return Outcome{Kind: journal.Converged, Reason: reason, PRURL: pr.URL}
}

// repairTitle is the convention pass: the title checked and repaired in
// code, at open and again at convergence, never remembered by an agent.
func (l *loop) repairTitle(ctx context.Context, pr PR) *Outcome {
	l.prURL = pr.URL
	if want := RepairTitle(l.issue.Identifier, pr.Title); want != pr.Title {
		if err := l.d.Hub.RetitlePR(ctx, l.tree, pr.Number, want); err != nil {
			return l.park(ctx, fmt.Sprintf("could not repair the PR title: %v", err))
		}
		fmt.Fprintf(l.d.Out, "repaired PR title to %q\n", want)
	}
	return nil
}

// converge is the terminal success: title repaired, comment, label, then
// In Review — in that order, so a failure part-way never leaves a status
// the ticket's own text cannot explain.
func (l *loop) converge(ctx context.Context, round int, reviewSummary string) Outcome {
	pr, found, err := l.d.Hub.PRForBranch(ctx, l.tree, l.branch)
	if err != nil {
		return *l.park(ctx, fmt.Sprintf("could not look up the branch's PR: %v", err))
	}
	// Merged while the run was still finishing: the work landed, which is
	// the outcome this whole loop exists to reach. Treating it as absence
	// is what parked three runs that had in fact succeeded.
	if found && pr.State == PRStateMerged {
		return l.convergeOnAMergedPR(ctx, round, reviewSummary, pr)
	}
	if !found || pr.State == PRStateClosed {
		return *l.park(ctx, fmt.Sprintf("the PR for branch %s is gone; it was open earlier in this run", l.branch))
	}
	if out := l.repairTitle(ctx, pr); out != nil {
		return *out
	}

	// Outdated is not answered (PW-177): a human thread on the PR counts
	// as unresolved for convergence even when a revision outdated the code
	// it hangs on. The reviewer's approval does not answer a human.
	threads, err := l.d.Hub.UnresolvedThreads(ctx, l.tree, pr.Number)
	if err != nil {
		return *l.park(ctx, fmt.Sprintf("could not read the PR's review threads: %v", err))
	}
	if threads > 0 {
		return *l.handback(ctx,
			humanThreadsComment(threads, reviewSummary, l.prURL),
			fmt.Sprintf("the reviewer approved but %d human review thread(s) on the PR are unresolved", threads))
	}

	// Resolve everything checkable before the first write.
	stateID, err := verbs.ResolveState(ctx, l.d.Board, l.d.Cov, l.issue.TeamID, "in_review")
	if err != nil {
		return *l.park(ctx, fmt.Sprintf("could not resolve the In Review status: %v", err))
	}
	label, found, err := l.d.Board.LabelByName(ctx, ReadyForHumanLabel)
	if err != nil {
		return *l.park(ctx, fmt.Sprintf("could not resolve the %s label: %v", ReadyForHumanLabel, err))
	}
	if !found {
		return *l.park(ctx, fmt.Sprintf("no %q label anywhere in the workspace; run `wand init` to bring the team to the covenant", ReadyForHumanLabel))
	}

	comment := convergedComment(round, reviewSummary, l.prURL, l.deviations)
	if err := l.d.Board.CreateComment(ctx, l.issue.ID, comment); err != nil {
		return *l.park(ctx, fmt.Sprintf("converged, but the summary comment failed: %v", err))
	}
	if err := l.d.Board.AddLabel(ctx, l.issue.ID, label.ID); err != nil {
		return *l.park(ctx, fmt.Sprintf("converged and commented, but labeling failed: %v", err))
	}
	if err := l.d.Board.UpdateIssue(ctx, l.issue.ID, linear.IssueUpdate{StateID: stateID}); err != nil {
		return *l.park(ctx, fmt.Sprintf("converged, commented and labeled, but the In Review move failed: %v", err))
	}

	// The branch is pushed and the tree is clean; the worktree has nothing
	// left to preserve. Failing to remove it is worth a line, not the run.
	if err := l.d.Git.RemoveWorktree(ctx, l.d.Repo, l.tree); err != nil {
		fmt.Fprintf(l.d.Out, "note: could not remove the worktree: %v\n", err)
	}

	reason := fmt.Sprintf("reviewer approved on round %d; PR %s is ready for a human", round, l.prURL)
	if err := l.r.Converged(reason); err != nil {
		fmt.Fprintf(l.d.Out, "journal: %v\n", err)
	}
	return Outcome{Kind: journal.Converged, Reason: reason, PRURL: l.prURL}
}

// handback ends the run on a human's desk: the comment first, Needs Input
// second, both through the verb that encodes that order. If the writes
// fail, the run parks instead — carrying both what it wanted to say and why
// it could not.
func (l *loop) handback(ctx context.Context, comment, reason string) *Outcome {
	// Deviations are appended here rather than by each comment builder, so
	// no hand-back can forget them. The PR body carries only the ones known
	// when it was composed — every revise round's arrive after — and a
	// hand-back is exactly the ending where a human is about to read what
	// the run did (the PW-191 lesson).
	comment = withDeviations(comment, l.deviations)
	if _, err := verbs.Handback(ctx, l.d.Board, l.d.Cov, l.issue.Identifier, comment); err != nil {
		return l.park(ctx, fmt.Sprintf("hand-back failed: %v (the run was handing back because: %s)", err, reason))
	}
	if err := l.r.HandedBack(reason); err != nil {
		fmt.Fprintf(l.d.Out, "journal: %v\n", err)
	}
	return &Outcome{Kind: journal.HandedBack, Reason: reason, PRURL: l.prURL}
}

// park ends the run without deciding. When the context is what killed the
// operation, the interrupt's own sentence wins — "interrupted by SIGTERM"
// explains a run; "Post …: context canceled" does not — and the choice
// lives here, in the one function every ending path calls, so no call site
// can forget it.
//
// The journal is written first and is the run's real ending: it is
// reachable even when Linear is what broke, which is the case for half the
// park sites in this file. [verbs.ReportPark] then puts the same sentence
// on the ticket, best-effort — it cannot fail this function and cannot
// re-enter it, because a park that parked on its own report would recurse
// exactly when Linear is already failing.
func (l *loop) park(ctx context.Context, reason string) *Outcome {
	if ctx.Err() != nil {
		reason = context.Cause(ctx).Error()
	}
	if err := l.r.Parked(reason); err != nil {
		fmt.Fprintf(l.d.Out, "journal: %v\n", err)
	}
	verbs.ReportPark(ctx, l.d.Board, l.d.Out, l.issue.ID, reason)
	return &Outcome{Kind: journal.Parked, Reason: reason, PRURL: l.prURL}
}

// note journals a non-transition worth reading later; failing to write one
// is worth a line, never the run.
func (l *loop) note(message string, detail any) {
	if err := l.r.Note(message, detail); err != nil {
		fmt.Fprintf(l.d.Out, "journal: %v\n", err)
	}
}

func (l *loop) workRules() []string {
	return workRules(l.issue.Identifier, l.d.Cov.Commands.Verify)
}

// firstLine trims a verbatim account down to something a journal reason can
// carry; the full text lives in the comment.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 200
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
