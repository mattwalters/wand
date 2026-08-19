package ticket

import (
	"strings"
	"testing"
	"time"

	"github.com/mattwalters/wand/internal/linear"
)

func at(d, h int) time.Time {
	return time.Date(2026, 8, d, h, 0, 0, 0, time.UTC)
}

func sampleIssue() linear.Issue {
	return linear.Issue{
		Identifier:  "WND-3",
		Title:       "The read layer",
		Description: "Two read commands.",
		URL:         "https://linear.app/x/issue/WND-3",
		Priority:    2,
		CreatedAt:   at(19, 1),
		State:       linear.IssueState{Name: "Todo", Type: "unstarted"},
		Assignee:    "Matt Walters",
		Labels:      []string{"agent-filed"},
	}
}

func TestRenderFullTicket(t *testing.T) {
	got := render(sampleIssue(), []linear.Comment{
		{ID: "c1", Author: "Matt Walters", CreatedAt: at(19, 2), Body: "A question."},
		{ID: "c2", Author: "Scout", CreatedAt: at(19, 3), Body: "An answer."},
	}, 100)

	want := "WND-3  The read layer\n" +
		"\n" +
		"status:    Todo\n" +
		"priority:  High\n" +
		"assignee:  Matt Walters\n" +
		"labels:    agent-filed\n" +
		"created:   2026-08-19\n" +
		"url:       https://linear.app/x/issue/WND-3\n" +
		"\n" +
		"Two read commands.\n" +
		"\n" +
		"comments (2):\n" +
		"\n" +
		"--- Matt Walters, 2026-08-19 02:00 ---\n" +
		"A question.\n" +
		"\n" +
		"--- Scout, 2026-08-19 03:00 ---\n" +
		"An answer.\n"
	if got != want {
		t.Errorf("render:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// The conversation reads oldest-first whatever order the API returned;
// connection order is an API detail, not a rendering decision.
func TestRenderSortsCommentsOldestFirst(t *testing.T) {
	got := render(sampleIssue(), []linear.Comment{
		{ID: "c2", Author: "B", CreatedAt: at(19, 3), Body: "second"},
		{ID: "c1", Author: "A", CreatedAt: at(19, 2), Body: "first"},
	}, 100)
	if strings.Index(got, "first") > strings.Index(got, "second") {
		t.Errorf("comments out of order:\n%s", got)
	}
}

// The budget is per-comment: a long early comment is cut, and the short
// human answer after it still arrives whole.
func TestRenderTruncatesPerComment(t *testing.T) {
	long := strings.Repeat("x", 50)
	got := render(sampleIssue(), []linear.Comment{
		{ID: "c1", Author: "A", CreatedAt: at(19, 2), Body: long},
		{ID: "c2", Author: "B", CreatedAt: at(19, 3), Body: "the short answer"},
	}, 20)

	if strings.Contains(got, long) {
		t.Error("long comment was not truncated")
	}
	if !strings.Contains(got, "[comment truncated: 20 of 50 chars shown]") {
		t.Errorf("truncation must announce itself:\n%s", got)
	}
	if !strings.Contains(got, "the short answer") {
		t.Errorf("the later short comment must survive whole:\n%s", got)
	}
}

// Truncation counts runes, not bytes: a multibyte body must never be cut
// mid-character.
func TestTruncateIsRuneSafe(t *testing.T) {
	got := truncate(strings.Repeat("é", 50), 10)
	if !strings.HasPrefix(got, strings.Repeat("é", 10)) {
		t.Errorf("truncate cut mid-rune: %q", got)
	}
}

func TestRenderDescriptionNeverTruncated(t *testing.T) {
	issue := sampleIssue()
	issue.Description = strings.Repeat("d", 500)
	got := render(issue, nil, 10)
	if !strings.Contains(got, issue.Description) {
		t.Error("description must be passed whole, whatever the comment budget")
	}
	if !strings.Contains(got, "comments: none") {
		t.Errorf("no comments should be said out loud:\n%s", got)
	}
}

func TestRenderEmptyDescriptionAndBlockers(t *testing.T) {
	issue := sampleIssue()
	issue.Description = ""
	issue.BlockedBy = []linear.Blocker{{Identifier: "WND-8", State: linear.IssueState{Name: "In Progress", Type: "started"}}}
	got := render(issue, nil, 100)
	if !strings.Contains(got, "(no description)") {
		t.Errorf("empty description should be said out loud:\n%s", got)
	}
	if !strings.Contains(got, "blocked:   by WND-8 (In Progress)") {
		t.Errorf("blockers should render in the header:\n%s", got)
	}
}
