package sweep

import (
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/linear"
)

func TestRankOrdersBySeverityThenAge(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	newer := time.Now()

	deadLease := Candidate{Kind: KindDeadLease, Ticket: "WND-1", Since: newer}
	reReviewOld := Candidate{Kind: KindReReview, Ticket: "WND-2", Since: old}
	reReviewNew := Candidate{Kind: KindReReview, Ticket: "WND-3", Since: newer}
	thread := Candidate{Kind: KindUnresolvedThreads, Ticket: "WND-4", Since: old}

	ranked := Rank([]Candidate{thread, reReviewNew, reReviewOld, deadLease})
	want := []string{"WND-1", "WND-2", "WND-3", "WND-4"}
	for i, c := range ranked {
		if c.Ticket != want[i] {
			t.Fatalf("ranked[%d] = %s, want %s (full order: %v)", i, c.Ticket, want[i], ticketsOf(ranked))
		}
	}
}

func ticketsOf(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Ticket
	}
	return out
}

func TestDeadLeaseCandidatesFiltersToThisRepoAndDeadLive(t *testing.T) {
	reports := map[string]journal.Report{
		"r1": {State: journal.State{Meta: journal.Meta{Repo: "/repo/a", Ticket: "WND-1", Verb: "run"}}, Live: journal.Dead},
		"r2": {State: journal.State{Meta: journal.Meta{Repo: "/repo/b", Ticket: "WND-2", Verb: "run"}}, Live: journal.Dead},
		"r3": {State: journal.State{Meta: journal.Meta{Repo: "/repo/a", Ticket: "WND-3", Verb: "run"}}, Live: journal.Alive},
		"r4": {State: journal.State{Meta: journal.Meta{Repo: "/repo/a", Ticket: "WND-4", Verb: "run"}, Outcome: journal.Converged}, Live: journal.Dead},
	}
	got := DeadLeaseCandidates(reports, "/repo/a")
	if len(got) != 1 || got[0].Ticket != "WND-1" || got[0].RunID != "r1" {
		t.Fatalf("DeadLeaseCandidates = %+v, want exactly WND-1/r1", got)
	}
}

func TestReReviewCandidates(t *testing.T) {
	issues := []linear.Issue{{Identifier: "WND-1", CreatedAt: time.Now()}}
	got := ReReviewCandidates(issues)
	if len(got) != 1 || got[0].Kind != KindReReview || got[0].Ticket != "WND-1" {
		t.Fatalf("ReReviewCandidates = %+v", got)
	}
}

func TestZombieReportsFindsUnbackedInProgressIssues(t *testing.T) {
	inProgress := []linear.Issue{
		{Identifier: "WND-1", Title: "backed"},
		{Identifier: "WND-2", Title: "unbacked"},
	}
	reports := map[string]journal.Report{
		"r1": {State: journal.State{Meta: journal.Meta{Repo: "/repo/a", Ticket: "WND-1", Verb: "run"}}, Live: journal.Alive},
	}
	got := ZombieReports(inProgress, reports, "/repo/a")
	if len(got) != 1 || got[0].Ticket != "WND-2" {
		t.Fatalf("ZombieReports = %+v, want only WND-2", got)
	}
}

func TestZombieReportsIgnoresEndedRuns(t *testing.T) {
	inProgress := []linear.Issue{{Identifier: "WND-1"}}
	reports := map[string]journal.Report{
		"r1": {State: journal.State{Meta: journal.Meta{Repo: "/repo/a", Ticket: "WND-1", Verb: "run"}, Outcome: journal.Parked}},
	}
	got := ZombieReports(inProgress, reports, "/repo/a")
	if len(got) != 1 {
		t.Fatalf("ZombieReports = %+v, want WND-1 reported: its only run already ended", got)
	}
}
