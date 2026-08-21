package dispatch

import (
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
)

func issue(id string, priority int, age time.Duration) linear.Issue {
	return linear.Issue{
		ID:         "id-" + id,
		Identifier: id,
		Title:      "title " + id,
		Priority:   priority,
		CreatedAt:  time.Now().Add(-age),
	}
}

func TestSelectPrefersTodoWhenALaneIsFree(t *testing.T) {
	todo := []linear.Issue{issue("WND-2", 2, time.Hour), issue("WND-1", 1, time.Hour)}
	toPlan := []linear.Issue{issue("WND-9", 1, time.Hour)}

	winner, ok, _, _ := Select(todo, toPlan, true)
	if !ok {
		t.Fatal("expected a winner")
	}
	if winner.Verb != VerbRun || winner.Issue.Identifier != "WND-1" {
		t.Errorf("winner = %+v, want the highest-ranked Todo issue via run", winner)
	}
}

func TestSelectFallsBackToToPlanWhenLanesAreFull(t *testing.T) {
	todo := []linear.Issue{issue("WND-1", 1, time.Hour)}
	toPlan := []linear.Issue{issue("WND-9", 1, time.Hour)}

	winner, ok, _, _ := Select(todo, toPlan, false)
	if !ok {
		t.Fatal("expected a winner")
	}
	if winner.Verb != VerbPlan || winner.Issue.Identifier != "WND-9" {
		t.Errorf("winner = %+v, want the To Plan issue via plan — a plan run needs no lane", winner)
	}
}

func TestSelectFallsBackToToPlanWhenTodoIsEmpty(t *testing.T) {
	toPlan := []linear.Issue{issue("WND-9", 1, time.Hour)}

	winner, ok, _, _ := Select(nil, toPlan, true)
	if !ok {
		t.Fatal("expected a winner")
	}
	if winner.Verb != VerbPlan {
		t.Errorf("winner.Verb = %s, want plan: a free lane with nothing to build should not leave research idle", winner.Verb)
	}
}

func TestSelectNothingWhenBothAreEmpty(t *testing.T) {
	if _, ok, _, _ := Select(nil, nil, true); ok {
		t.Error("expected no winner")
	}
}

func TestSelectVetsToPlanCandidates(t *testing.T) {
	blocked := issue("WND-9", 1, time.Hour)
	blocked.Labels = []string{"human-only"}

	winner, ok, _, toPlanSkips := Select(nil, []linear.Issue{blocked}, false)
	if ok {
		t.Fatalf("expected no winner, got %+v", winner)
	}
	if len(toPlanSkips) != 1 || toPlanSkips[0].Issue.Identifier != "WND-9" {
		t.Errorf("toPlanSkips = %+v, want WND-9 skipped", toPlanSkips)
	}
}

func TestSelectNoLaneStillSkipsUnstartableTodo(t *testing.T) {
	blocked := issue("WND-1", 1, time.Hour)
	blocked.Labels = []string{"human-only"}

	_, ok, todoSkips, _ := Select([]linear.Issue{blocked}, nil, true)
	if ok {
		t.Fatal("expected no winner: the only Todo issue is human-only")
	}
	if len(todoSkips) != 1 || todoSkips[0].Reason == "" {
		t.Errorf("todoSkips = %+v, want one skip with a reason", todoSkips)
	}
}

func report(repo, verb string, ended bool, live journal.Liveness) journal.Report {
	s := journal.State{Meta: journal.Meta{Repo: repo, Verb: verb}}
	if ended {
		s.Outcome = journal.Converged
	}
	return journal.Report{State: s, Live: live}
}

func TestLanesUsedCountsOnlyLiveRunsForThisRepo(t *testing.T) {
	reports := []journal.Report{
		report("/repo/a", "run", false, journal.Alive),
		report("/repo/a", "run", false, journal.Alive),
		report("/repo/b", "run", false, journal.Alive),   // a different repo
		report("/repo/a", "plan", false, journal.Alive),  // a plan run needs no lane
		report("/repo/a", "scope", false, journal.Alive), // pre-rename journal value, same as "plan": still no lane
		report("/repo/a", "run", true, journal.Alive),    // ended: not occupying anything
		report("/repo/a", "run", false, journal.Dead),    // gc'd: provably dead
	}
	if got := LanesUsed(reports, "/repo/a"); got != 2 {
		t.Errorf("LanesUsed = %d, want 2", got)
	}
}

func TestLanesUsedCountsUnknownLiveness(t *testing.T) {
	reports := []journal.Report{
		report("/repo/a", "run", false, journal.Unknown),
	}
	if got := LanesUsed(reports, "/repo/a"); got != 1 {
		t.Errorf("LanesUsed = %d, want 1 — an unknown holder may well be alive, and undercounting capacity is the safe direction to be wrong in", got)
	}
}

// WND-71. The park costs one run, not a stream of them. A plan run is a full
// cold research pass, and the reference journal has the same ticket planned
// and parked three times over for one defect — three passes bought, one
// failure. Selection is where that stops.
func TestSelectSkipsAParkedToPlanTicket(t *testing.T) {
	parked := issue("WND-9", 1, time.Hour)
	parked.Labels = []string{"parked"}
	fresh := issue("WND-10", 2, time.Hour)

	winner, ok, _, toPlanSkips := Select(nil, []linear.Issue{parked, fresh}, true)
	if !ok {
		t.Fatal("expected a winner")
	}
	// WND-9 outranks WND-10 on priority and would have won but for the label.
	if winner.Issue.Identifier != "WND-10" {
		t.Errorf("winner = %s, want the parked higher-priority ticket passed over", winner.Issue.Identifier)
	}
	if len(toPlanSkips) != 1 || toPlanSkips[0].Issue.Identifier != "WND-9" {
		t.Fatalf("skips = %+v, want WND-9 skipped with a reason", toPlanSkips)
	}
	if toPlanSkips[0].Reason != "labeled parked" {
		t.Errorf("skip reason = %q", toPlanSkips[0].Reason)
	}
}

// A parked ticket is the only one on the board: nothing runs, and the pass
// says why rather than looking like an empty queue.
func TestSelectFindsNoWinnerWhenEveryCandidateIsParked(t *testing.T) {
	parked := issue("WND-9", 1, time.Hour)
	parked.Labels = []string{"parked"}

	_, ok, _, toPlanSkips := Select(nil, []linear.Issue{parked}, true)
	if ok {
		t.Fatal("a parked ticket was selected")
	}
	if len(toPlanSkips) != 1 {
		t.Fatalf("skips = %+v, want the refusal reported rather than silent", toPlanSkips)
	}
}
