package dispatch

import (
	"sort"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/queue"
	"github.com/mattwalters/wand/internal/scope"
)

// Verb names which orchestrator a winner runs through.
type Verb string

const (
	VerbRun   Verb = "run"
	VerbScope Verb = "scope"
)

// Winner is the one ticket a pass runs, and how.
type Winner struct {
	Issue linear.Issue
	Verb  Verb
}

// Select picks the one ticket a dispatch pass runs: the highest-ranked,
// vetted Todo issue when a lane is free, falling back to the
// highest-ranked, vetted Scoping issue otherwise.
//
// A Scoping winner is the fallback in two cases that read identically from
// here — no lane is free, or Todo simply has nothing startable — and that
// is deliberate: a scope needs no lane (see [LanesUsed]), so an eligible
// Scoping ticket dispatches even at full lane occupancy, and research is
// never left idle just because Todo is momentarily empty either.
//
// Pure: every input is already read, so a test can hold the whole decision
// without a board or a store. Ranking and vetting reuse the read layer —
// queue.Build for Todo, the same discipline for Scoping — so the order an
// agent starts work in is the order `wand queue` would have printed it.
func Select(todo, scoping []linear.Issue, laneFree bool) (winner Winner, ok bool, todoSkips, scopingSkips []queue.Skip) {
	todoReady, todoSkips := queue.Build(todo)
	scopingReady, scopingSkips := rankScoping(scoping)

	if laneFree && len(todoReady) > 0 {
		return Winner{Issue: todoReady[0], Verb: VerbRun}, true, todoSkips, scopingSkips
	}
	if len(scopingReady) > 0 {
		return Winner{Issue: scopingReady[0], Verb: VerbScope}, true, todoSkips, scopingSkips
	}
	return Winner{}, false, todoSkips, scopingSkips
}

// rankScoping ranks and vets Scoping issues the way queue.Build does for
// Todo, with scope's own vet: a ticket blocked by another is exactly the
// ticket worth scoping early, so only the human-only label refuses here.
func rankScoping(issues []linear.Issue) (ready []linear.Issue, skips []queue.Skip) {
	ranked := make([]linear.Issue, len(issues))
	copy(ranked, issues)
	sort.SliceStable(ranked, func(i, j int) bool { return queue.Less(ranked[i], ranked[j]) })
	for _, issue := range ranked {
		if reason := scope.Vet(issue); reason != "" {
			skips = append(skips, queue.Skip{Issue: issue, Reason: reason})
			continue
		}
		ready = append(ready, issue)
	}
	return ready, skips
}

// LanesUsed counts the reports that occupy a lane of repo: a live "run"
// verb that has not ended. A "scope" run never occupies one — a scope needs
// no lane — and neither does a dead one: a report whose lease liveness is
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
