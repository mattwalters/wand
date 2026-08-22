package home

import (
	"time"

	"github.com/mattwalters/wand/internal/ledger"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/plan"
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
	day := func(d int) time.Time {
		return time.Date(2026, 3, d, 0, 0, 0, 0, time.UTC)
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
		PlanReview: []linear.Issue{
			{
				Identifier: "WND-44", Title: "home: a fifth queue for plans awaiting blessing",
				State: linear.IssueState{Name: "Plan Review", Type: "unstarted"}, Priority: 1,
				Assignee: "Matt Walters", CreatedAt: at(2),
				Description: samplePlanDescription(),
			},
		},
		NeedsInput: []linear.Issue{
			{
				Identifier: "WND-38", Title: "Second harness adapter: which one?",
				State: linear.IssueState{Name: "Needs Input", Type: "unstarted"}, Priority: 2,
				Assignee: "Matt Walters", CreatedAt: at(1),
				Description: "Planned three candidates. The recommendation is in the comments; " +
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
		Stalled: []StalledRun{
			{
				Kind: StallStuck, RunID: "20260304-1120-wnd-36", Ticket: "WND-36",
				Verb: "run", Repo: "/Users/you/src/wand", Since: at(4),
				Reason: "held by pid 48213 on studio.local, which is gone; the run stopped in " +
					"implement (round 2) and nothing is driving it",
			},
			{
				Kind: StallParked, RunID: "20260303-0902-wnd-33", Ticket: "WND-33",
				Verb: "run", Repo: "/Users/you/src/wand", Since: at(3),
				Reason: "the worktree was dirty at handoff; refusing to park noise into the ticket",
			},
		},
		Usage: Usage{
			Since: day(1).Add(-6 * 24 * time.Hour),
			Velocity: []ledger.VelocityBucket{
				{Day: day(1), TokensIn: sampleTokens(1200), TokensOut: sampleTokens(900)},
				{Day: day(2), TokensIn: sampleTokens(4000), TokensOut: sampleTokens(2600)},
				{Day: day(3), TokensIn: sampleTokens(2200), TokensOut: sampleTokens(1500)},
				{Day: day(4), TokensIn: sampleTokens(6100), TokensOut: sampleTokens(3900)},
			},
			Outcomes: ledger.OutcomeCounts{Converged: 5, Parked: 2, HandedBack: 1},
			Harness: []ledger.HarnessTotal{
				{Harness: "claude-code", TokensIn: sampleTokens(9500), TokensOut: sampleTokens(6200)},
				{Harness: "codex", TokensIn: sampleTokens(4000), TokensOut: sampleTokens(2700)},
			},
		},
	}
}

// sampleTokens is a token count fixture as the *int64 the ledger types
// carry, so an absent count and a reported zero stay distinguishable here
// the same way they do in real journal data.
func sampleTokens(v int64) *int64 { return &v }

// samplePlanDescription builds a description shaped exactly like one plan
// run leaves behind: a human's own text, then the marker-fenced plan region. The
// markers come from [linear.WithSection] rather than typed by hand, so the
// sample can never drift from what the real fence looks like.
func samplePlanDescription() string {
	human := "Home only has four sections. A blessed plan has nowhere to be judged " +
		"from but Linear itself, which is the review surface this tool exists to replace."
	planText := "## Implementation plan\n\n" +
		"**Add a Scoped section.** Triage and Scoped are both authorization judgments; " +
		"putting them next to each other reads as one job, not two.\n\n" +
		"### Steps\n\n" +
		"1. Read the Scoped state into the snapshot.\n" +
		"2. Render the plan section on the detail screen.\n" +
		"3. Wire Bless → Todo and Backlog, with reason.\n\n" +
		"### How it is proven\n\n" +
		"Golden screens at two widths, plus the existing home test tiers.\n\n" +
		"### Where the code is\n\n" +
		"- `internal/home/home.go` — the fifth section and its two judgments\n" +
		"- `internal/tui/view.go` — the plan rendered in place\n\n" +
		"The next plan run of this ticket rewrites this region whole; notes of your own " +
		"live outside it, where nothing machine-written touches them.\n"
	desc, err := linear.WithSection(human, plan.PlanSectionID, planText)
	if err != nil {
		// The body above is a static literal with no markers of its own; an
		// error here would mean this function itself is broken, not the data
		// it was given.
		panic(err)
	}
	return desc
}
