package run

import (
	"fmt"
	"strings"

	"github.com/mattwalters/wand/internal/linear"
)

// TitleFor composes the PR title: the bracketed identifier, then the title —
// the worker's suggestion when it made one, the ticket's otherwise. Written
// at open and repaired later by the same function: the convention lives in
// code (the PW-189 rule), not in any agent's memory.
func TitleFor(identifier, suggested, ticketTitle string) string {
	title := strings.TrimSpace(suggested)
	if title == "" {
		title = strings.TrimSpace(ticketTitle)
	}
	return RepairTitle(identifier, title)
}

// RepairTitle makes an existing title lead with the bracketed identifier,
// leaving it alone when it already does. Matching is case-insensitive on the
// identifier, because a hand-edited "[wnd-10] …" is the convention held, not
// broken.
func RepairTitle(identifier, title string) string {
	title = strings.TrimSpace(title)
	prefix := "[" + identifier + "]"
	if len(title) >= len(prefix) && strings.EqualFold(title[:len(prefix)], prefix) {
		return title
	}
	return prefix + " " + title
}

// PRBody composes the PR description: the worker's summary, any plan
// deviations, and a glossed reference to the ticket — identifier plus title
// plus link, never a bare "WND-10" a reader must go resolve.
func PRBody(issue linear.Issue, summary string, deviations []string) string {
	var b strings.Builder
	if s := strings.TrimSpace(summary); s != "" {
		b.WriteString(s)
		b.WriteString("\n")
	}
	if len(deviations) > 0 {
		b.WriteString("\nDeviations from the ticket's plan:\n")
		for _, d := range deviations {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(d))
		}
	}
	fmt.Fprintf(&b, "\nTicket: %s\n", TicketGloss(issue))
	return b.String()
}

// TicketGloss renders a ticket reference with its title attached, linked
// when the issue has a URL.
func TicketGloss(issue linear.Issue) string {
	text := fmt.Sprintf("%s — %s", issue.Identifier, issue.Title)
	if issue.URL == "" {
		return text
	}
	return fmt.Sprintf("[%s](%s)", text, issue.URL)
}
