package cockpit

import (
	"time"

	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/scope"
)

// Sample is a fixed board: the same rows every time, on no team that exists.
//
// It ships in the binary because `wand ui --dump-screen` renders it rather
// than reading Linear, and that is what makes the promise in AGENTS.md true
// — the dump and the golden screens are byte-identical because they are the
// same board through the same renderer. A dump that read a live team would
// produce a screen no two people could compare, and would need an API key to
// look at a user interface.
//
// Every timestamp is fixed and in UTC for the same reason.
func Sample() Snapshot {
	at := func(day int) time.Time {
		return time.Date(2026, 3, day, 9, 0, 0, 0, time.UTC)
	}
	return Snapshot{
		Team: "WND",
		Triage: []linear.Issue{
			{
				Identifier: "WND-41", Title: "guard: a raw state UUID is not matched",
				State:     linear.IssueState{Name: "Triage", Type: "triage"},
				Labels:    []string{"agent-filed"},
				CreatedAt: at(2),
				Description: "The guard matches state names and types, not UUIDs. Filed while " +
					"working WND-40; the known-gap note in guard.go argues it is acceptable, " +
					"so this may be a duplicate of the note rather than a bug.",
			},
			{
				Identifier: "WND-42", Title: "doctor prints an empty drift section on a clean board",
				State: linear.IssueState{Name: "Triage", Type: "triage"}, Priority: 4,
				Labels: []string{"agent-filed"}, CreatedAt: at(3),
			},
		},
		Scoped: []linear.Issue{
			{
				Identifier: "WND-44", Title: "cockpit: a fifth queue for plans awaiting blessing",
				State: linear.IssueState{Name: "Scoped", Type: "unstarted"}, Priority: 1,
				Assignee: "Matt Walters", CreatedAt: at(2),
				Description: samplePlanDescription(),
			},
		},
		NeedsInput: []linear.Issue{
			{
				Identifier: "WND-38", Title: "Second harness adapter: which one?",
				State: linear.IssueState{Name: "Needs Input", Type: "unstarted"}, Priority: 2,
				Assignee: "Matt Walters", CreatedAt: at(1),
				Description: "Scoped three candidates. The recommendation is in the comments; " +
					"the estimate assumes the isolation suite passes unchanged.",
			},
		},
		ReadyForHuman: []linear.Issue{
			{
				Identifier: "WND-35", Title: "Run journal and lease store",
				State: linear.IssueState{Name: "In Review", Type: "started"}, Priority: 2,
				Labels: []string{"ready-for-human"}, Assignee: "Matt Walters", CreatedAt: at(1),
			},
		},
		Lanes: []Lane{
			{
				Kind: LaneStuck, RunID: "20260304-1120-wnd-36", Ticket: "WND-36",
				Verb: "run", Repo: "/Users/you/src/wand", Since: at(4),
				Reason: "held by pid 48213 on studio.local, which is gone; the run stopped in " +
					"implement (round 2) and nothing is driving it",
			},
			{
				Kind: LaneParked, RunID: "20260303-0902-wnd-33", Ticket: "WND-33",
				Verb: "run", Repo: "/Users/you/src/wand", Since: at(3),
				Reason: "the worktree was dirty at handoff; refusing to park noise into the ticket",
			},
		},
	}
}

// samplePlanDescription builds a description shaped exactly like one scope
// leaves behind: a human's own text, then the marker-fenced plan region. The
// markers come from [linear.WithSection] rather than typed by hand, so the
// sample can never drift from what the real fence looks like.
func samplePlanDescription() string {
	human := "The cockpit only has four sections. A blessed plan has nowhere to be judged " +
		"from but Linear itself, which is the review surface this tool exists to replace."
	plan := "## Implementation plan\n\n" +
		"**Add a Scoped section.** Triage and Scoped are both authorization judgments; " +
		"putting them next to each other reads as one job, not two.\n\n" +
		"### Steps\n\n" +
		"1. Read the Scoped state into the snapshot.\n" +
		"2. Render the plan section on the detail screen.\n" +
		"3. Wire Bless → Todo and Backlog, with reason.\n\n" +
		"### How it is proven\n\n" +
		"Golden screens at two widths, plus the existing cockpit test tiers.\n\n" +
		"### Where the code is\n\n" +
		"- `internal/cockpit/cockpit.go` — the fifth section and its two judgments\n" +
		"- `internal/tui/view.go` — the plan rendered in place\n\n" +
		"The next scope of this ticket rewrites this region whole; notes of your own " +
		"live outside it, where nothing machine-written touches them.\n"
	desc, err := linear.WithSection(human, scope.PlanSectionID, plan)
	if err != nil {
		// The body above is a static literal with no markers of its own; an
		// error here would mean this function itself is broken, not the data
		// it was given.
		panic(err)
	}
	return desc
}
