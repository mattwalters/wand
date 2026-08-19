// Package ticket renders one issue whole, for prompts and humans alike: the
// cold-reader view a worker gets pasted and a person skims. Pure: it takes
// an issue and its comments already read; all I/O stays in the caller.
package ticket

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattwalters/wand/internal/linear"
)

// CommentBudget is the per-comment rune budget. The budget is per-comment,
// not per-ticket: the comment that matters most is usually the short answer
// a human left last on a parked ticket, and a whole-ticket budget spent on
// early discussion would truncate exactly that one. The description is never
// truncated at all — it is the ticket.
const CommentBudget = 2000

// Render prints the full ticket: header, description passed whole, comments
// oldest-first with each body held to budget runes.
func Render(issue linear.Issue, comments []linear.Comment) string {
	return render(issue, comments, CommentBudget)
}

// render is Render with the budget explicit, so tests can hold small ones.
func render(issue linear.Issue, comments []linear.Comment, budget int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s  %s\n\n", issue.Identifier, issue.Title)
	fmt.Fprintf(&b, "status:    %s\n", issue.State.Name)
	fmt.Fprintf(&b, "priority:  %s\n", linear.PriorityName(issue.Priority))
	if issue.Assignee != "" {
		fmt.Fprintf(&b, "assignee:  %s\n", issue.Assignee)
	}
	if len(issue.Labels) > 0 {
		fmt.Fprintf(&b, "labels:    %s\n", strings.Join(issue.Labels, ", "))
	}
	if len(issue.BlockedBy) > 0 {
		parts := make([]string, len(issue.BlockedBy))
		for i, blocker := range issue.BlockedBy {
			parts[i] = fmt.Sprintf("%s (%s)", blocker.Identifier, blocker.State.Name)
		}
		fmt.Fprintf(&b, "blocked:   by %s\n", strings.Join(parts, ", "))
	}
	fmt.Fprintf(&b, "created:   %s\n", issue.CreatedAt.UTC().Format("2006-01-02"))
	if issue.URL != "" {
		fmt.Fprintf(&b, "url:       %s\n", issue.URL)
	}

	b.WriteString("\n")
	if desc := strings.TrimSpace(issue.Description); desc != "" {
		b.WriteString(desc)
		b.WriteString("\n")
	} else {
		b.WriteString("(no description)\n")
	}

	if len(comments) == 0 {
		b.WriteString("\ncomments: none\n")
		return b.String()
	}

	// Oldest-first by our own sort, not connection order: Linear's default
	// comment order is an API detail, and the reading order of a
	// conversation is not negotiable. ID breaks exact-timestamp ties so two
	// readers print one ticket.
	ordered := make([]linear.Comment, len(comments))
	copy(ordered, comments)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
		}
		return ordered[i].ID < ordered[j].ID
	})

	fmt.Fprintf(&b, "\ncomments (%d):\n", len(ordered))
	for _, comment := range ordered {
		author := comment.Author
		if author == "" {
			author = "(unknown)"
		}
		fmt.Fprintf(&b, "\n--- %s, %s ---\n", author, comment.CreatedAt.UTC().Format("2006-01-02 15:04"))
		b.WriteString(truncate(comment.Body, budget))
		b.WriteString("\n")
	}
	return b.String()
}

// truncate holds a body to budget runes. A cut is never silent: the marker
// says how much is missing, so a cold reader knows to open Linear rather
// than assume they saw everything.
func truncate(body string, budget int) string {
	body = strings.TrimSpace(body)
	runes := []rune(body)
	if len(runes) <= budget {
		return body
	}
	return fmt.Sprintf("%s\n[comment truncated: %d of %d chars shown]",
		strings.TrimSpace(string(runes[:budget])), budget, len(runes))
}
