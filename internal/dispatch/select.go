package dispatch

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/plan"
	"github.com/mattwalters/wand/internal/queue"
)

// Verb names which orchestrator a winner runs through.
type Verb string

const (
	VerbRun  Verb = "run"
	VerbPlan Verb = "plan"
)

// Winner is the one ticket a pass runs, and how.
type Winner struct {
	Issue linear.Issue
	Verb  Verb
}

// Select picks the one ticket a dispatch pass runs: the highest-ranked,
// vetted Todo issue when a lane is free, falling back to the
// highest-ranked, vetted To Plan issue otherwise.
//
// A To Plan winner is the fallback in two cases that read identically from
// here — no lane is free, or Todo simply has nothing startable — and that
// is deliberate: a plan run needs no lane (see [LanesUsed]), so an eligible
// To Plan ticket dispatches even at full lane occupancy, and research is
// never left idle just because Todo is momentarily empty either.
//
// Pure: every input is already read, so a test can hold the whole decision
// without a board or a store. Ranking and vetting reuse the read layer —
// queue.Build for Todo, the same discipline for To Plan — so the order an
// agent starts work in is the order `wand queue` would have printed it.
func Select(todo, toPlan []linear.Issue, laneFree bool) (winner Winner, ok bool, todoSkips, toPlanSkips []queue.Skip) {
	todoReady, todoSkips := queue.Build(todo)
	toPlanReady, toPlanSkips := rankToPlan(toPlan)

	if laneFree && len(todoReady) > 0 {
		return Winner{Issue: todoReady[0], Verb: VerbRun}, true, todoSkips, toPlanSkips
	}
	if len(toPlanReady) > 0 {
		return Winner{Issue: toPlanReady[0], Verb: VerbPlan}, true, todoSkips, toPlanSkips
	}
	return Winner{}, false, todoSkips, toPlanSkips
}

// rankToPlan ranks and vets To Plan issues the way queue.Build does for
// Todo, with plan's own vet: a ticket blocked by another is exactly the
// ticket worth planning early, so only the human-only label refuses here.
func rankToPlan(issues []linear.Issue) (ready []linear.Issue, skips []queue.Skip) {
	ranked := make([]linear.Issue, len(issues))
	copy(ranked, issues)
	sort.SliceStable(ranked, func(i, j int) bool { return queue.Less(ranked[i], ranked[j]) })
	for _, issue := range ranked {
		if reason := plan.Vet(issue); reason != "" {
			skips = append(skips, queue.Skip{Issue: issue, Reason: reason})
			continue
		}
		ready = append(ready, issue)
	}
	return ready, skips
}

// LanesUsed counts the reports that occupy a lane of repo: a live "run"
// verb that has not ended. A "plan" run never occupies one — a plan run
// needs no lane — and neither does a dead one: a report whose lease liveness is
// [journal.Dead] is gc'd from the count right here, read-only, because a
// zombie does not hold a lane, whatever phase its journal last opened.
//
// [journal.Unknown] still counts. A run on another host, or one whose lock
// this process could not examine, may well be alive — the same honesty
// [journal.Report.Zombie] insists on — and undercounting capacity is the
// direction it is safe to be wrong in.
func LanesUsed(reports []journal.Report, repo string) int {
	n := 0
	for _, r := range reports {
		if r.State.Meta.Repo != repo || r.State.Meta.Verb != string(VerbRun) {
			continue
		}
		if r.State.Ended() {
			continue
		}
		if r.Live == journal.Dead {
			continue
		}
		n++
	}
	return n
}

// IdleReason says why a pass dispatched nothing, as the tail
// [sweptSummary] folds into the watcher's one-line report — so it carries no
// "dispatch:" prefix of its own.
//
// `--watch` prints one line per state change, so the cause has to travel in
// the line: "idle" on its own describes the dispatcher while the lane count
// beside it describes the repo, and a reader is left to guess which of
// several unrelated situations produced them. The single-pass path already
// says this much across several lines (see [Deps.selectWinner]); this is the
// same account compressed to the one line a watcher gets.
//
// Two causes reach here and they are not the same situation. Lanes full
// means Todo is locked out and only research may still dispatch — the repo
// is working, and the watcher is waiting for a lane. Nothing startable means
// both queues came back with nothing a pass could take, and the repo is
// genuinely idle. Skips are named on top of either, because a skip is the
// actionable kind of idle: a human-only label or an unresolved blocker is
// something a person can clear, and a count with no identifier tells them
// there is something to clear without saying what.
//
// Pure, like [Select], so the sentence a watcher prints is testable without
// a board, a store or a clock.
func IdleReason(used, lanes int, laneFree bool, skips []queue.Skip) string {
	var b strings.Builder
	b.WriteString("idle — ")
	if laneFree {
		b.WriteString("nothing startable in Todo or To Plan")
	} else {
		fmt.Fprintf(&b, "%d/%d lanes in use, and no To Plan ticket is eligible", used, lanes)
	}
	if s := skipSummary(skips); s != "" {
		b.WriteString("; " + s)
	}
	return b.String()
}

// skipSummary names the skipped tickets, at most two of them and then a
// count. The whole list would make the line unreadable on a busy board and
// would churn the watcher's state-change check on every reordering; two
// identifiers and a total is enough for a person to know whether the idle
// is one they can do something about.
func skipSummary(skips []queue.Skip) string {
	if len(skips) == 0 {
		return ""
	}
	named := make([]string, 0, 2)
	for _, s := range skips {
		if len(named) == 2 {
			break
		}
		named = append(named, fmt.Sprintf("%s (%s)", s.Issue.Identifier, s.Reason))
	}
	out := fmt.Sprintf("%d skipped: %s", len(skips), strings.Join(named, ", "))
	if len(skips) > len(named) {
		out += fmt.Sprintf(", and %d more", len(skips)-len(named))
	}
	return out
}
