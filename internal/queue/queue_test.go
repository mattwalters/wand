package queue

import (
	"strings"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/linear"
)

func day(d int) time.Time {
	return time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC)
}

func issue(id string, priority int, created time.Time) linear.Issue {
	return linear.Issue{Identifier: id, Title: "title " + id, Priority: priority, CreatedAt: created}
}

func identifiers(issues []linear.Issue) []string {
	ids := make([]string, len(issues))
	for i, issue := range issues {
		ids[i] = issue.Identifier
	}
	return ids
}

func TestOrderPriorityAscendingNoPriorityLast(t *testing.T) {
	ready, _ := Build([]linear.Issue{
		issue("WND-1", 0, day(1)), // No priority: numerically first, ranks last
		issue("WND-2", 4, day(1)),
		issue("WND-3", 1, day(1)),
		issue("WND-4", 2, day(1)),
	})
	got := identifiers(ready)
	want := []string{"WND-3", "WND-4", "WND-2", "WND-1"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestOrderTiesBreakOldestThenIdentifier(t *testing.T) {
	ready, _ := Build([]linear.Issue{
		issue("WND-10", 2, day(2)), // same priority, newer: after the day-1 pair
		issue("WND-9", 2, day(1)),  // same createdAt as WND-2: identifier decides,
		issue("WND-2", 2, day(1)),  // numerically, so WND-2 before WND-9 before WND-10
	})
	got := identifiers(ready)
	want := []string{"WND-2", "WND-9", "WND-10"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// WND-9 vs WND-10 as strings would order "10" before "9"; the tiebreak must
// compare issue numbers as numbers.
func TestIdentifierLessIsNumeric(t *testing.T) {
	if !identifierLess("WND-9", "WND-10") {
		t.Error("WND-9 should sort before WND-10")
	}
	if identifierLess("WND-10", "WND-9") {
		t.Error("WND-10 should not sort before WND-9")
	}
}

func TestVetHumanOnly(t *testing.T) {
	tagged := issue("WND-5", 2, day(1))
	tagged.Labels = []string{"agent-filed", "Human-Only"} // case-insensitive, like Linear

	ready, skips := Build([]linear.Issue{tagged, issue("WND-6", 2, day(2))})
	if len(ready) != 1 || ready[0].Identifier != "WND-6" {
		t.Fatalf("ready = %v", identifiers(ready))
	}
	if len(skips) != 1 || !strings.Contains(skips[0].Reason, "human-only") {
		t.Fatalf("skips = %+v, want a human-only reason", skips)
	}
}

func TestVetBlockersOnlyResolvedByCompletedOrCanceled(t *testing.T) {
	blocked := issue("WND-7", 1, day(1))
	blocked.BlockedBy = []linear.Blocker{
		{Identifier: "WND-8", State: linear.IssueState{Name: "In Progress", Type: "started"}},
		{Identifier: "WND-4", State: linear.IssueState{Name: "Done", Type: "completed"}},
		{Identifier: "WND-2", State: linear.IssueState{Name: "Canceled", Type: "canceled"}},
	}

	ready, skips := Build([]linear.Issue{blocked})
	if len(ready) != 0 {
		t.Fatalf("a blocked issue must not be ready; ready = %v", identifiers(ready))
	}
	if len(skips) != 1 {
		t.Fatalf("skips = %+v", skips)
	}
	reason := skips[0].Reason
	// A started blocker is still a blocker: "it's already In Progress" is
	// the exact race blocked-by exists to prevent.
	if !strings.Contains(reason, "WND-8 (In Progress)") {
		t.Errorf("reason %q should name the started blocker", reason)
	}
	if strings.Contains(reason, "WND-4") || strings.Contains(reason, "WND-2") {
		t.Errorf("reason %q names resolved blockers", reason)
	}
}

func TestRenderQueueAndSkips(t *testing.T) {
	tagged := issue("WND-5", 3, day(1))
	tagged.Labels = []string{"human-only"}
	ready, skips := Build([]linear.Issue{
		issue("WND-12", 2, day(2)),
		issue("WND-3", 2, day(1)),
		tagged,
	})

	got := Render(ready, skips)
	want := "WND-3   High  title WND-3\n" +
		"WND-12  High  title WND-12\n" +
		"\n" +
		"skipped:\n" +
		"WND-5  labeled human-only\n"
	if got != want {
		t.Errorf("render:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderEmptyStatesAreDistinct(t *testing.T) {
	if got := Render(nil, nil); !strings.Contains(got, "Todo is empty") {
		t.Errorf("empty queue render = %q", got)
	}
	skipped := issue("WND-5", 2, day(1))
	skipped.Labels = []string{"human-only"}
	_, skips := Build([]linear.Issue{skipped})
	got := Render(nil, skips)
	if !strings.Contains(got, "nothing startable") || !strings.Contains(got, "WND-5") {
		t.Errorf("vetted-out render = %q, want the distinction and the skip printed", got)
	}
}

// WND-71. A parked ticket keeps its place in the lifecycle — a park reports
// that the machine stopped, not that the work was judged — so nothing about
// its status keeps the next pass from starting it again. Vetting on the
// label is what makes a park cost one run instead of a stream of them:
// WND-54 parked three times that way, WND-37 three times, each repeat the
// identical failure re-purchased.
func TestVetParked(t *testing.T) {
	parked := issue("WND-5", 2, day(1))
	parked.Labels = []string{"agent-filed", "Parked"} // case-insensitive, like Linear

	ready, skips := Build([]linear.Issue{parked, issue("WND-6", 2, day(2))})
	if len(ready) != 1 || ready[0].Identifier != "WND-6" {
		t.Fatalf("ready = %v, want the parked ticket left out", identifiers(ready))
	}
	if len(skips) != 1 || !strings.Contains(skips[0].Reason, "parked") {
		t.Fatalf("skips = %+v, want a parked reason", skips)
	}
	// Skipped, never dropped: a queue that quietly comes up short reads as
	// a queue in order, and the whole point here is that a person can see
	// why nothing is happening.
	if !strings.Contains(skips[0].Reason, "clear the label") {
		t.Errorf("the skip does not say how to undo it: %q", skips[0].Reason)
	}
	// One line, in the register of the other reasons: `wand queue` prints
	// these inline beside an identifier, so a paragraph here is a broken
	// column for everyone reading the queue.
	if len(skips[0].Reason) > 60 {
		t.Errorf("the skip reason is too long for the queue's one-line format: %q", skips[0].Reason)
	}
}

// Clearing the label is the retry. It is a person's act, which is the same
// thing the covenant says about every other authorization.
func TestClearingTheParkedLabelMakesItStartableAgain(t *testing.T) {
	was := issue("WND-5", 2, day(1))
	was.Labels = []string{"agent-filed"} // the human cleared it

	ready, skips := Build([]linear.Issue{was})
	if len(ready) != 1 || len(skips) != 0 {
		t.Fatalf("ready = %v, skips = %+v; want it startable once the label is gone", identifiers(ready), skips)
	}
}
