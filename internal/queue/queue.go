// Package queue ranks and vets a team's Todo issues into the startable
// queue. Pure: it takes issues already read and decides order and
// eligibility; all I/O stays in the caller.
//
// The discipline is the reference system's, kept deliberately:
//
//   - Priority ascending, except 0 ("No priority") sorts LAST — Linear's
//     numbering puts unprioritized work at 0, and a queue that read the
//     number naively would start it first.
//   - createdAt oldest-first breaks priority ties: age is the fairest queue.
//   - The identifier breaks exact createdAt ties, numerically by issue
//     number, so two racing readers agree on one order.
//
// Vetting refuses rather than reorders: a skipped issue is printed with its
// reason, never silently dropped. A queue that quietly comes up short reads
// as a queue in order.
package queue

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mattwalters/wand/internal/linear"
)

// HumanOnlyLabel marks work no agent may take, whatever its status says.
// The label is covenant topology, not a parameter — no covenant file renames
// it (see covenant.Default).
const HumanOnlyLabel = "human-only"

// Skip is an issue vetted out of the queue, with the reason it was.
type Skip struct {
	Issue  linear.Issue
	Reason string
}

// Build ranks issues into queue order, then vets each: the ready list is
// what an agent may start, in the order it should start it; every issue
// vetted out lands in skips with its reason, in the same queue order.
func Build(issues []linear.Issue) (ready []linear.Issue, skips []Skip) {
	ranked := make([]linear.Issue, len(issues))
	copy(ranked, issues)
	sort.SliceStable(ranked, func(i, j int) bool { return less(ranked[i], ranked[j]) })

	for _, issue := range ranked {
		if reason := Vet(issue); reason != "" {
			skips = append(skips, Skip{Issue: issue, Reason: reason})
			continue
		}
		ready = append(ready, issue)
	}
	return ready, skips
}

// less is the queue order. Deterministic all the way down: two readers
// holding the same issues must print the same queue.
func less(a, b linear.Issue) bool {
	if pa, pb := effectivePriority(a.Priority), effectivePriority(b.Priority); pa != pb {
		return pa < pb
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return identifierLess(a.Identifier, b.Identifier)
}

// effectivePriority maps Linear's numbering to sort order: 1 (Urgent) through
// 4 (Low) keep their places, 0 (No priority) moves behind them all.
func effectivePriority(p int) int {
	if p == 0 {
		return 5
	}
	return p
}

// identifierLess compares issue identifiers numerically by issue number
// ("WND-9" before "WND-10"), falling back to a plain string compare for
// anything not of that shape.
func identifierLess(a, b string) bool {
	ta, na, oka := splitIdentifier(a)
	tb, nb, okb := splitIdentifier(b)
	if oka && okb && ta == tb {
		return na < nb
	}
	return a < b
}

func splitIdentifier(id string) (team string, number int, ok bool) {
	i := strings.LastIndex(id, "-")
	if i < 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(id[i+1:])
	if err != nil {
		return "", 0, false
	}
	return id[:i], n, true
}

// Vet returns why an issue may not be started, or "" when it may. Exported
// because `wand claim` must refuse for exactly the reasons the queue skips:
// two vetting rules would drift, and the claim path is the one that acts.
func Vet(issue linear.Issue) string {
	var reasons []string
	for _, label := range issue.Labels {
		if strings.EqualFold(label, HumanOnlyLabel) {
			reasons = append(reasons, "labeled "+HumanOnlyLabel)
			break
		}
	}
	if unresolved := unresolvedBlockers(issue); len(unresolved) > 0 {
		parts := make([]string, len(unresolved))
		for i, b := range unresolved {
			parts[i] = fmt.Sprintf("%s (%s)", b.Identifier, b.State.Name)
		}
		reasons = append(reasons, "blocked by "+strings.Join(parts, ", "))
	}
	return strings.Join(reasons, "; ")
}

// unresolvedBlockers returns the blockers still standing. Only completed and
// canceled resolve a blocker; everything else — started very much included —
// leaves it standing. "It's already In Progress" is precisely the race the
// blocked-by relation exists to prevent.
func unresolvedBlockers(issue linear.Issue) []linear.Blocker {
	var unresolved []linear.Blocker
	for _, b := range issue.BlockedBy {
		switch b.State.Type {
		case "completed", "canceled":
		default:
			unresolved = append(unresolved, b)
		}
	}
	return unresolved
}

// Render prints the queue as text: the ready list in start order, then every
// skip with its reason. It says when there is nothing, and why the nothing —
// an empty Todo and a fully vetted-out Todo are different answers.
func Render(ready []linear.Issue, skips []Skip) string {
	var b strings.Builder

	switch {
	case len(ready) == 0 && len(skips) == 0:
		b.WriteString("Todo is empty: nothing for you right now.\n")
	case len(ready) == 0:
		b.WriteString("nothing startable: every Todo issue is vetted out below.\n")
	default:
		idWidth, prWidth := 0, 0
		for _, issue := range ready {
			idWidth = max(idWidth, len(issue.Identifier))
			prWidth = max(prWidth, len(linear.PriorityName(issue.Priority)))
		}
		for _, issue := range ready {
			fmt.Fprintf(&b, "%-*s  %-*s  %s\n",
				idWidth, issue.Identifier,
				prWidth, linear.PriorityName(issue.Priority),
				issue.Title)
		}
	}

	if len(skips) > 0 {
		b.WriteString("\nskipped:\n")
		idWidth := 0
		for _, s := range skips {
			idWidth = max(idWidth, len(s.Issue.Identifier))
		}
		for _, s := range skips {
			fmt.Fprintf(&b, "%-*s  %s\n", idWidth, s.Issue.Identifier, s.Reason)
		}
	}
	return b.String()
}
