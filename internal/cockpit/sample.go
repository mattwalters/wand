package cockpit

import (
	"time"

	"github.com/mattwalters/wand/internal/linear"
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
