package plan

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The two deliverables, composed pure.
//
// They are split the way the ticket is read: the description says what to
// build, the comment says why and asks for the blessing. Keeping the
// argument out of the body is not tidiness — the body is the thing a
// future implementer is handed as the specification, and an implementer
// reading the rejected options re-litigates them.

// PlanSectionID is the marker-fenced region of the description the plan run
// owns. Every plan of a ticket replaces it whole; every byte outside it
// belongs to whoever wrote it.
const PlanSectionID = "plan"

// PlanMarkdown renders the plan region: the recommended approach, the
// ordered steps, the test story and the file map.
func PlanMarkdown(d Draft) string {
	var b strings.Builder
	b.WriteString("## Implementation plan\n\n")
	fmt.Fprintf(&b, "**%s.** %s\n\n", strings.TrimSpace(d.Recommendation.Approach), strings.TrimSpace(d.Recommendation.Why))

	b.WriteString("### Steps\n\n")
	for i, s := range d.Plan.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(s))
	}

	b.WriteString("\n### How it is proven\n\n")
	b.WriteString(strings.TrimSpace(d.Plan.Tests))
	b.WriteString("\n\n### Where the code is\n\n")
	for _, f := range d.Files {
		fmt.Fprintf(&b, "- `%s` — %s\n", strings.TrimSpace(f.Location), strings.TrimSpace(f.Note))
	}
	// A dropped citation is said out loud. The scout found that file and
	// this plan no longer mentions it, and the only other way to notice
	// would be to diff against a handoff nobody kept.
	if len(d.Dropped) > 0 {
		b.WriteString("\n> Citations dropped for carrying no line number, and so not listed above: ")
		for i, loc := range d.Dropped {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "`%s`", strings.TrimSpace(loc))
		}
		b.WriteString(". The scout cited them; the file map takes only citations a reader can jump to.\n")
	}
	b.WriteString("\nThe next plan run of this ticket rewrites this region whole; notes of your own " +
		"live outside it, where nothing machine-written touches them.\n")
	return b.String()
}

// OptionsComment renders the argument and the ask: what the scout took the
// ticket to be asking, every approach with its trade-off, the
// recommendation, the estimate, what the plan rests on, and what is still
// unanswered — then the explicit request for a blessing an agent may not
// grant itself. statusName is the covenant's display name for the terminal
// status the plan run is about to move the ticket to (its "scoped" key), so
// the comment names whatever a covenant file calls it rather than a
// hardcoded default.
func OptionsComment(d Draft, scale, statusName string, prov Provenance) string {
	var b strings.Builder
	b.WriteString("## Plan\n\n")
	b.WriteString(Argument(d, scale))
	b.WriteString("\n### Over to you\n\n")
	fmt.Fprintf(&b, "The plan is in this ticket's description, in the region `wand plan` owns. "+
		"This ticket is now in %s: promoting it to Todo is what blesses building it, "+
		"and that is a human's act — an agent does not bless its own plan. "+
		"If the recommendation is wrong, say so here and plan it again.\n", statusName)
	b.WriteString("\n")
	b.WriteString(prov.line())
	return b.String()
}

// Argument renders the case for the plan: the reading of the ticket, every
// approach with its trade-off, the recommendation, the estimate, what the
// plan rests on and what is still unanswered.
//
// It is separate from the comment around it because the critic and the
// reviser are shown exactly this — the argument as a human would read it,
// without the ask addressed to the human or the provenance of a plan run
// that has not happened yet. Judging the artifact is the point; judging its
// JSON encoding is not.
func Argument(d Draft, scale string) string {
	var b strings.Builder
	b.WriteString("**What I take this ticket to be asking.** ")
	b.WriteString(strings.TrimSpace(d.Understanding))
	b.WriteString("\n\n### Approaches\n")

	for i, a := range d.Approaches {
		fmt.Fprintf(&b, "\n**%d. %s**", i+1, strings.TrimSpace(a.Name))
		if isRecommended(d, a) {
			b.WriteString(" — recommended")
		}
		fmt.Fprintf(&b, "\n\n%s\n\n*Trade-off:* %s\n", strings.TrimSpace(a.Sketch), strings.TrimSpace(a.Tradeoff))
	}

	fmt.Fprintf(&b, "\n### Why %s\n\n%s\n", strings.TrimSpace(d.Recommendation.Approach), strings.TrimSpace(d.Recommendation.Why))

	if d.Estimate != nil {
		fmt.Fprintf(&b, "\n### Estimate\n\n%d, on the team's %s scale.\n", *d.Estimate, scale)
	}

	if len(d.Assumptions) > 0 {
		b.WriteString("\n### What this rests on\n\n")
		for _, a := range SortedAssumptions(d) {
			fmt.Fprintf(&b, "- **%s** (%s cost if wrong) — %s\n",
				strings.TrimSpace(a.Statement), a.Cost, strings.TrimSpace(a.IfWrong))
		}
	}

	if len(d.OpenQuestions) > 0 {
		b.WriteString("\n### Still open\n\n")
		for _, q := range d.OpenQuestions {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(q))
		}
	}
	return b.String()
}

// Provenance is how the draft got here, printed under the argument. A
// human weighing a plan wants to know whether anything argued with it:
// one cold scout's first draft and a draft that survived a critic and an
// interview are not the same artifact, and only the footer says which this
// is.
type Provenance struct {
	RunID string
	// Critic and Interview report whether those stages ran; Objections and
	// Answers say how much came back from them.
	Critic     bool
	Objections int
	Interview  bool
	Answers    int
	Harness    string
}

func (p Provenance) line() string {
	parts := []string{"drafted by a cold " + p.Harness + " scout"}
	switch {
	case p.Critic && p.Objections > 0:
		parts = append(parts, fmt.Sprintf("attacked by a cold critic (%d objection(s), revised)", p.Objections))
	case p.Critic:
		parts = append(parts, "attacked by a cold critic (nothing stuck)")
	}
	switch {
	case p.Interview && p.Answers > 0:
		parts = append(parts, fmt.Sprintf("revised after %d answer(s) from you", p.Answers))
	case p.Interview:
		parts = append(parts, "put to you question by question (nothing to change)")
	}
	return fmt.Sprintf("*%s. Run `%s`.*\n", capitalize(strings.Join(parts, "; ")), p.RunID)
}

// SortedAssumptions orders assumptions costliest-first, keeping the scout's
// own order within a cost. It is exported because the interview asks in
// exactly this order and the comment prints in exactly this order: one
// ordering, used twice, so what a human reads matches what they were asked.
func SortedAssumptions(d Draft) []Assumption {
	out := make([]Assumption, len(d.Assumptions))
	copy(out, d.Assumptions)
	sort.SliceStable(out, func(i, j int) bool { return costRank(out[i].Cost) < costRank(out[j].Cost) })
	return out
}

// premiseComment is the hand-back for a scout that found the ticket built
// on something untrue. The scout's account is quoted verbatim: the whole
// value of the verdict is that a person can read what was actually found
// and judge it, not a summary of it.
func premiseComment(reason string, prov Provenance) string {
	var b strings.Builder
	b.WriteString("I did not plan this ticket. The scout reading the code judged the ticket's " +
		"premise wrong, and planning around a wrong premise produces a well-argued plan for the " +
		"wrong work. Its account, verbatim:\n\n")
	b.WriteString(blockquote(reason))
	b.WriteString("\n\nNothing was written to the description and no estimate was set. " +
		"If the premise holds after all, say so here and plan it again; if it does not, " +
		"the ticket needs rewriting or closing, and both are yours.\n\n")
	b.WriteString(prov.line())
	return b.String()
}

// supersededComment is posted before a plan run overwrites a plan region
// that already carries one. Every plan run of a ticket rewrites
// [PlanSectionID] whole — the next paragraph in this file's package doc
// says so — which means the plan a human read and blessed otherwise
// vanishes the moment a later plan run happens, surviving only in whatever
// PR was already merged from it. Dated and marked superseded, quoted
// verbatim, so the ticket keeps its own history instead of only its newest
// answer.
func supersededComment(prior string, prov Provenance) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Plan superseded, %s\n\n", time.Now().UTC().Format(time.RFC3339))
	b.WriteString("This ticket is being planned again, and the plan below is about to be replaced " +
		"in the description — every plan run rewrites that region whole. It is kept here, verbatim, " +
		"so the ticket does not disagree with its own history:\n\n")
	b.WriteString(blockquote(prior))
	fmt.Fprintf(&b, "\n\nSuperseded by run `%s`.\n", prov.RunID)
	return b.String()
}

// blockquote renders text as a Markdown quote — the form every verbatim
// worker account takes, so a reader can tell the worker's words from the
// orchestrator's.
func blockquote(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("> "+l, " ")
	}
	return strings.Join(lines, "\n")
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// firstLine trims a verbatim account down to something a journal reason can
// carry; the full text lives in the comment.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 200
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
