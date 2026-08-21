package pm

import (
	"fmt"
	"strings"

	"github.com/mattwalters/wand/internal/linear"
)

// BriefSectionID is the marker-fenced region of a filed ticket's description
// that pm owns — the same convention as plan.PlanSectionID, applied to a
// ticket pm itself is creating rather than one it is annotating. Fencing it
// even on a brand-new issue means a later pm proposal that touches the same
// ticket (a correction, a re-file) replaces only what pm wrote, leaving
// anything a human has since added outside the region alone.
const BriefSectionID = "brief"

// TicketDescription renders a ProposedTicket's body into the fenced region
// Bless writes as the created issue's description.
func TicketDescription(t ProposedTicket) (string, error) {
	return linear.WithSection("", BriefSectionID, strings.TrimSpace(t.Body))
}

// ProposalMarkdown renders a whole draft the way a human reviews it before
// blessing: the projects it asks to create, then every ticket with its
// body, project and blockers. It is also exactly what gets written beside
// the JSON proposal file, so a reviewer never has to read the JSON to judge
// what they are approving.
func ProposalMarkdown(d Draft, dupes map[string][]linear.Issue) string {
	var b strings.Builder
	if d.Premise == PremiseWrong {
		b.WriteString("## Premise judged wrong\n\n")
		b.WriteString(strings.TrimSpace(d.Reason))
		b.WriteString("\n\nNothing is proposed.\n")
		return b.String()
	}

	b.WriteString("## Proposed tickets\n\n")
	if len(d.Projects) > 0 {
		b.WriteString("### New projects\n\n")
		for _, p := range d.Projects {
			fmt.Fprintf(&b, "- **%s** — %s\n", strings.TrimSpace(p.Name), strings.TrimSpace(p.Description))
		}
		b.WriteString("\n")
	}

	for i, t := range d.Tickets {
		fmt.Fprintf(&b, "### %d. %s\n\n", i+1, strings.TrimSpace(t.Title))
		if t.Project != "" {
			fmt.Fprintf(&b, "*Project:* %s\n\n", strings.TrimSpace(t.Project))
		}
		if len(t.BlockedBy) > 0 {
			fmt.Fprintf(&b, "*Blocked by:* %s\n\n", strings.Join(t.BlockedBy, ", "))
		}
		b.WriteString(strings.TrimSpace(t.Body))
		b.WriteString("\n")
		if hits := dupes[strings.ToLower(strings.TrimSpace(t.Title))]; len(hits) > 0 {
			b.WriteString("\n*Possible duplicates already on the board:*\n")
			for _, h := range hits {
				fmt.Fprintf(&b, "- %s (%s) — %s\n", h.Identifier, h.State.Name, h.Title)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}
