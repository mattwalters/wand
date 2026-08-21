package cockpit

import (
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/linear"
)

func issue(id string, priority int, created int) linear.Issue {
	return linear.Issue{
		ID:         "uuid-" + id,
		Identifier: id,
		Title:      id + " title",
		Priority:   priority,
		CreatedAt:  time.Date(2026, 3, created, 0, 0, 0, 0, time.UTC),
		State:      linear.IssueState{Name: "Triage", Type: "triage"},
	}
}

// The board always has all five sections, whatever is in them: a queue that
// vanishes when it drains teaches you to stop looking for it.
func TestBuildAlwaysHasFiveSections(t *testing.T) {
	b := Build(Snapshot{Team: "WND"})
	if len(b.Sections) != 5 {
		t.Fatalf("sections = %d, want 5", len(b.Sections))
	}
	want := []Kind{KindTriage, KindPlanReview, KindNeedsInput, KindReadyForHuman, KindLanes}
	for i, k := range want {
		if b.Sections[i].Kind != k {
			t.Errorf("section %d = %q, want %q", i, b.Sections[i].Kind, k)
		}
		if b.Sections[i].Empty == "" {
			t.Errorf("section %q has no empty message; an empty queue and an unread one are different answers", k)
		}
	}
	if b.Waiting() != 0 {
		t.Errorf("waiting = %d, want 0", b.Waiting())
	}
}

// The judgment queues are ranked the way the agent queue is ranked. Two
// orderings would mean the ticket a human blessed first is not the one an
// agent starts first, which makes the ranking they did meaningless.
func TestTriageIsRankedLikeTheQueue(t *testing.T) {
	b := Build(Snapshot{Triage: []linear.Issue{
		issue("WND-1", 0, 1), // No priority sorts last despite being oldest
		issue("WND-2", 3, 5),
		issue("WND-3", 1, 9),
		issue("WND-4", 3, 2),
	}})

	var got []string
	for _, row := range b.Sections[0].Rows {
		got = append(got, row.Issue.Identifier)
	}
	want := []string{"WND-3", "WND-4", "WND-2", "WND-1"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("triage order = %v, want %v", got, want)
		}
	}
}

// Vetting is deliberately not applied here. A human-only ticket is precisely
// one waiting on a human, and a blocked one is still theirs to judge.
func TestVettedIssuesStayOnTheBoard(t *testing.T) {
	blocked := issue("WND-5", 2, 1)
	blocked.Labels = []string{"human-only"}
	blocked.BlockedBy = []linear.Blocker{{
		Identifier: "WND-4",
		State:      linear.IssueState{Name: "In Progress", Type: "started"},
	}}

	b := Build(Snapshot{Triage: []linear.Issue{blocked}})
	if n := len(b.Sections[0].Rows); n != 1 {
		t.Fatalf("triage rows = %d, want 1: vetting belongs to the agent queue, not this one", n)
	}
}

// The ready-for-human label outlives the merge that answered it, so a board
// that showed every labeled issue would fill up with finished work.
func TestReadyForHumanDropsClosedWork(t *testing.T) {
	open := issue("WND-6", 2, 1)
	open.State = linear.IssueState{Name: "In Review", Type: "started"}
	done := issue("WND-7", 2, 1)
	done.State = linear.IssueState{Name: "Done", Type: "completed"}
	canceled := issue("WND-8", 2, 1)
	canceled.State = linear.IssueState{Name: "Canceled", Type: "canceled"}

	b := Build(Snapshot{ReadyForHuman: []linear.Issue{open, done, canceled}})
	rows := b.Sections[3].Rows
	if len(rows) != 1 || rows[0].Issue.Identifier != "WND-6" {
		t.Errorf("ready-for-human rows = %v, want only the open one", rows)
	}
}

func TestDispositions(t *testing.T) {
	tests := []struct {
		kind Kind
		want int
	}{
		{kind: KindTriage, want: 6},
		{kind: KindNeedsInput, want: 6},
		{kind: KindPlanReview, want: 2},
		{kind: KindReadyForHuman, want: 0},
		{kind: KindLanes, want: 0},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			if got := len(Dispositions(Row{Kind: tt.kind})); got != tt.want {
				t.Errorf("dispositions = %d, want %d", got, tt.want)
			}
		})
	}
}

// A parked lane is the one lane kind that offers a disposition: the act its
// own park comment names. The other three stay read-only.
func TestParkedLaneOffersClearDisposition(t *testing.T) {
	row := Row{Kind: KindLanes, Lane: Lane{Kind: LaneParked, Ticket: "WND-33"}}
	disps := Dispositions(row)
	if len(disps) != 1 || disps[0].Key != ClearParked.Key {
		t.Errorf("dispositions = %v, want just ClearParked", disps)
	}
}

func TestOtherLaneKindsOfferNoDisposition(t *testing.T) {
	for _, k := range []LaneKind{LaneStuck, LaneOrphaned, LaneUnclear} {
		t.Run(string(k), func(t *testing.T) {
			row := Row{Kind: KindLanes, Lane: Lane{Kind: k}}
			if got := len(Dispositions(row)); got != 0 {
				t.Errorf("dispositions = %d, want 0", got)
			}
		})
	}
}

// Every disposition must be reachable by a key, and no two may share one
// within the row kind that offers them — DispositionByKey only ever
// searches one row's own list, so a collision across lists is not a bug,
// but a collision within one silently shadows a judgment.
func TestDispositionKeysAreUnique(t *testing.T) {
	for name, disps := range map[string][]Disposition{"judgments": judgments, "planReviewJudgments": planReviewJudgments, "laneJudgments": laneJudgments} {
		seen := map[string]string{}
		for _, d := range disps {
			if d.Key == "" {
				t.Errorf("%s: %q has no key", name, d.Name)
			}
			if prev, dup := seen[d.Key]; dup {
				t.Errorf("%s: key %q is bound to both %q and %q", name, d.Key, prev, d.Name)
			}
			seen[d.Key] = d.Name
			if d.Gravity == "" {
				t.Errorf("%s: %q has no gravity sentence; the confirmation would say nothing", name, d.Name)
			}
		}
	}
}

// The disposition keys must not collide with the screen's own navigation.
// A key that both moves the cursor and starts a cancellation is a key that
// will one day cancel a ticket somebody was only scrolling past.
func TestDispositionKeysAvoidNavigation(t *testing.T) {
	for _, nav := range []string{"j", "k", "q", "enter", "esc", "r", "up", "down"} {
		for name, disps := range map[string][]Disposition{"judgments": judgments, "planReviewJudgments": planReviewJudgments, "laneJudgments": laneJudgments} {
			for _, d := range disps {
				if d.Key == nav {
					t.Errorf("%s: %q is bound to %q, which the screen uses for navigation", name, d.Name, nav)
				}
			}
		}
	}
}

// PlanSection reads exactly the region `wand plan` fences, leaving whatever a
// human wrote around it alone — the same contract [linear.WithSection]
// promises the other direction.
func TestPlanSection(t *testing.T) {
	desc := "A human's own words.\n\n" +
		"<!-- wand:plan -->\n## Implementation plan\n\nDo the thing.\n<!-- /wand:plan -->\n\n" +
		"More of the human's words, after."

	text, ok, err := PlanSection(linear.Issue{Description: desc})
	if err != nil {
		t.Fatalf("PlanSection: %v", err)
	}
	if !ok {
		t.Fatal("PlanSection reported no plan section on a description that has one")
	}
	want := "## Implementation plan\n\nDo the thing."
	if text != want {
		t.Errorf("plan = %q, want %q", text, want)
	}
}

// A ticket that has never been planned — or whose fence a human broke by hand
// — is reported rather than guessed at.
func TestPlanSectionAbsent(t *testing.T) {
	_, ok, err := PlanSection(linear.Issue{Description: "just a plain description"})
	if err != nil {
		t.Fatalf("PlanSection: %v", err)
	}
	if ok {
		t.Error("PlanSection reported a plan on a description with no fence")
	}
}

func TestIntentReady(t *testing.T) {
	subject := issue("WND-9", 0, 1)

	tests := []struct {
		name string
		in   Intent
		want bool
	}{
		{name: "bless needs nothing", in: Intent{Issue: subject, Disp: BlessTodo}, want: true},
		{name: "unranked needs nothing", in: Intent{Issue: subject, Disp: ToBacklogUnranked}, want: true},
		{name: "ranked with no rank", in: Intent{Issue: subject, Disp: ToBacklog}},
		{name: "ranked with 0", in: Intent{Issue: subject, Disp: ToBacklog, Priority: 0}},
		{name: "ranked with 5", in: Intent{Issue: subject, Disp: ToBacklog, Priority: 5}},
		{name: "ranked with 2", in: Intent{Issue: subject, Disp: ToBacklog, Priority: 2}, want: true},
		{name: "duplicate of nothing", in: Intent{Issue: subject, Disp: AsDuplicate}},
		{name: "duplicate of itself", in: Intent{Issue: subject, Disp: AsDuplicate, Text: "WND-9"}},
		{name: "duplicate of itself, cased", in: Intent{Issue: subject, Disp: AsDuplicate, Text: "wnd-9"}},
		{name: "duplicate of a sentence", in: Intent{Issue: subject, Disp: AsDuplicate, Text: "the other one"}},
		{name: "duplicate of an issue", in: Intent{Issue: subject, Disp: AsDuplicate, Text: "WND-3"}, want: true},
		{name: "duplicate, padded", in: Intent{Issue: subject, Disp: AsDuplicate, Text: "  WND-3 "}, want: true},
		{name: "cancel with no reason", in: Intent{Issue: subject, Disp: Cancel}},
		{name: "cancel with whitespace", in: Intent{Issue: subject, Disp: Cancel, Text: "   \n "}},
		{name: "cancel with a reason", in: Intent{Issue: subject, Disp: Cancel, Text: "obsolete"}, want: true},
		{name: "reject with no reason", in: Intent{Issue: subject, Disp: RejectPlan}},
		{name: "reject with a reason", in: Intent{Issue: subject, Disp: RejectPlan, Text: "wrong approach"}, want: true},
		{name: "clear parked with no lane", in: Intent{Disp: ClearParked}},
		{name: "clear parked with a lane", in: Intent{Lane: Lane{Ticket: "WND-33"}, Disp: ClearParked}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, why := tt.in.Ready()
			if got != tt.want {
				t.Errorf("ready = %v (%q), want %v", got, why, tt.want)
			}
			if !got && why == "" {
				t.Error("refused with no explanation")
			}
		})
	}
}
