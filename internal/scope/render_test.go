package scope_test

import (
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
	"github.com/mattwalters/wand/internal/scope"
)

func parsedDraft(t *testing.T) scope.Draft {
	t.Helper()
	d, err := scope.ParseDraft(raw(t, goodDraft()), covenant.Default())
	if err != nil {
		t.Fatalf("ParseDraft: %v", err)
	}
	return d
}

// The body says what to build; the argument stays in the comment. An
// implementer handed the rejected options re-litigates them, which is the
// opposite of what a blessed plan is for.
func TestPlanMarkdownCarriesThePlanAndNotTheArgument(t *testing.T) {
	got := scope.PlanMarkdown(parsedDraft(t))

	for _, want := range []string{
		"Filter in Vet",                    // the recommended approach
		"1. Add the blocker check to Vet.", // ordered steps, numbered
		"2. Print the reason in Render.",   //
		"table test over an issue",         // the test story
		"`internal/queue/queue.go:104`",    // the file map, cited with lines
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the plan does not carry %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Filter in Build") {
		t.Errorf("the plan carries the rejected approach:\n%s", got)
	}
	if strings.Contains(got, "Trade-off") {
		t.Errorf("the plan carries the trade-offs, which belong in the comment:\n%s", got)
	}
}

// The plan is written into a marker-fenced region of a human's
// description, and a region that cannot be read back is a region the next
// scope appends a second copy below. Round-tripping the real rendering
// through the real fencing is the only check that means anything here.
func TestPlanMarkdownSurvivesTheFence(t *testing.T) {
	plan := scope.PlanMarkdown(parsedDraft(t))
	body := "A human wrote this, and it must still be here afterwards.\n"

	next, err := linear.WithSection(body, scope.PlanSectionID, plan)
	if err != nil {
		t.Fatalf("WithSection: %v", err)
	}
	got, ok, err := linear.ReadSection(next, scope.PlanSectionID)
	if err != nil || !ok {
		t.Fatalf("ReadSection: %v (found %v)", err, ok)
	}
	if got != strings.TrimSpace(plan) {
		t.Errorf("the plan did not survive the fence:\n%s", got)
	}
	if !strings.Contains(next, body) {
		t.Error("the human's own text was lost")
	}

	// A second scope replaces the region rather than stacking below it.
	again, err := linear.WithSection(next, scope.PlanSectionID, plan)
	if err != nil {
		t.Fatalf("WithSection twice: %v", err)
	}
	if again != next {
		t.Error("writing the same plan twice changed the description; the region is not being replaced in place")
	}
}

func TestOptionsCommentArguesAndAsks(t *testing.T) {
	got := scope.OptionsComment(parsedDraft(t), "fibonacci", "Scoped", scope.Provenance{RunID: "WND-9-x", Harness: "claude-code"})

	for _, want := range []string{
		"Filter in Vet",                      // both approaches, by name
		"Filter in Build",                    //
		"— recommended",                      // which one is recommended, marked
		"*Trade-off:*",                       // what each costs
		"One vetting rule, one place.",       // the argument
		"2, on the team's fibonacci scale.",  // the estimate, with the scale it is on
		"Blockers arrive on the issue read.", // what the plan rests on
		"Should a canceled blocker resolve?", // what is still open
		"now in Scoped",                      // the status it says it just moved to
		"promoting it to Todo",               // the ask, and whose act it is
		"WND-9-x",                            // the run, for the journal
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the options comment does not carry %q:\n%s", want, got)
		}
	}
}

// The footer is how a human weighs the scope: one cold session's first
// draft and a draft that survived a critic and an interview are not the
// same artifact, and only this line says which is on the ticket.
func TestProvenanceSaysWhatArguedWithTheDraft(t *testing.T) {
	d := parsedDraft(t)
	plain := scope.OptionsComment(d, "fibonacci", "Scoped", scope.Provenance{RunID: "r", Harness: "codex"})
	if !strings.Contains(plain, "Drafted by a cold codex scout") || strings.Contains(plain, "critic") {
		t.Errorf("a plain scope claims more than happened:\n%s", plain)
	}

	full := scope.OptionsComment(d, "fibonacci", "Scoped", scope.Provenance{
		RunID: "r", Harness: "codex", Critic: true, Objections: 2, Interview: true, Answers: 3,
	})
	for _, want := range []string{"2 objection(s)", "3 answer(s)"} {
		if !strings.Contains(full, want) {
			t.Errorf("the footer does not report %q:\n%s", want, full)
		}
	}

	quiet := scope.OptionsComment(d, "fibonacci", "Scoped", scope.Provenance{RunID: "r", Harness: "codex", Critic: true, Interview: true})
	if !strings.Contains(quiet, "nothing stuck") || !strings.Contains(quiet, "nothing to change") {
		t.Errorf("a critic and an interview that changed nothing are not reported as such:\n%s", quiet)
	}
}

// A team that does not estimate gets no estimate section, rather than a
// heading with nothing under it.
func TestOptionsCommentOmitsAnEstimateThereIsNone(t *testing.T) {
	cov := covenant.Default()
	cov.IssueEstimationType = "notUsed"
	d := goodDraft()
	delete(d, "estimate")
	parsed, err := scope.ParseDraft(raw(t, d), cov)
	if err != nil {
		t.Fatalf("ParseDraft: %v", err)
	}
	if got := scope.OptionsComment(parsed, "notUsed", "Scoped", scope.Provenance{RunID: "r", Harness: "h"}); strings.Contains(got, "### Estimate") {
		t.Errorf("an estimate section was written for a team that does not estimate:\n%s", got)
	}
}

// Validation pairs the recommendation with an approach on the trimmed,
// case-folded name, so every later reader of that pair must match the same
// way. A comparison that trims one side and not the other accepts the
// draft and then renders it as recommending nothing — and tells the human
// the recommended approach is one the draft passed over, which is the
// opposite of what it says.
func TestARecommendationIsMatchedTheWayValidationMatchedIt(t *testing.T) {
	d := goodDraft()
	d["approaches"] = []any{
		map[string]any{"name": "Filter in Vet ", "sketch": "Extend the existing vet pass.", "tradeoff": "Vet grows a second responsibility."},
		map[string]any{"name": "Filter in Build", "sketch": "Drop them while ranking.", "tradeoff": "The skip reason is lost."},
	}
	d["recommendation"] = map[string]any{"approach": "filter in vet", "why": "One vetting rule, one place."}

	draft, err := scope.ParseDraft(raw(t, d), covenant.Default())
	if err != nil {
		t.Fatalf("ParseDraft refused a draft whose recommendation names an approach: %v", err)
	}

	arg := scope.Argument(draft, "fibonacci")
	if !strings.Contains(arg, "**1. Filter in Vet** — recommended") {
		t.Errorf("the argument marks no approach as recommended:\n%s", arg)
	}
	if strings.Contains(arg, "**2. Filter in Build** — recommended") {
		t.Errorf("the argument recommends the approach that was passed over:\n%s", arg)
	}

	for _, q := range scope.Questions(draft) {
		if q.Topic != scope.TopicRecommendation {
			continue
		}
		if strings.Contains(q.Ask, "Filter in Vet") {
			t.Errorf("the human is told the recommended approach was passed over:\n%s", q.Ask)
		}
		if !strings.Contains(q.Ask, "Filter in Build") {
			t.Errorf("the question does not name what was passed over:\n%s", q.Ask)
		}
	}
}
