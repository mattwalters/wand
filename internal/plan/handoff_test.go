package plan_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/plan"
)

// goodDraft is a handoff that satisfies every rule. Each test below breaks
// exactly one thing in it, so what a case is about is the diff from here.
func goodDraft() map[string]any {
	return map[string]any{
		"premise":       "sound",
		"understanding": "The queue must skip issues with unresolved blockers.",
		"approaches": []any{
			map[string]any{"name": "Filter in Vet", "sketch": "Extend the existing vet pass.", "tradeoff": "Vet grows a second responsibility."},
			map[string]any{"name": "Filter in Build", "sketch": "Drop them while ranking.", "tradeoff": "The skip reason is lost."},
		},
		"recommendation": map[string]any{"approach": "Filter in Vet", "why": "One vetting rule, one place."},
		"files": []any{
			map[string]any{"location": "internal/queue/queue.go:104", "note": "Vet, where the reasons are joined."},
		},
		"estimate": 2,
		"plan": map[string]any{
			"steps": []any{"Add the blocker check to Vet.", "Print the reason in Render."},
			"tests": "A table test over an issue with a started blocker; it must skip with a reason.",
		},
		"assumptions": []any{
			map[string]any{"statement": "Blockers arrive on the issue read.", "if_wrong": "Every vet needs a second API call.", "cost": "high"},
		},
		"open_questions": []any{"Should a canceled blocker resolve?"},
	}
}

func raw(t *testing.T, d map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshaling the fixture: %v", err)
	}
	return b
}

func TestParseDraftAcceptsAWholePlan(t *testing.T) {
	d, err := plan.ParseDraft(raw(t, goodDraft()), covenant.Default())
	if err != nil {
		t.Fatalf("ParseDraft: %v", err)
	}
	if d.Recommendation.Approach != "Filter in Vet" {
		t.Errorf("recommendation = %q", d.Recommendation.Approach)
	}
	if d.Estimate == nil || *d.Estimate != 2 {
		t.Errorf("estimate = %v, want 2", d.Estimate)
	}
}

// Every one of these is a draft that reads finished and is not. The whole
// point of refusing is that nothing reaches the ticket: a human blesses the
// plan on the strength of the argument, and nobody re-derives afterwards
// whether the argument held.
func TestParseDraftRefusesWhatIsNotAPlan(t *testing.T) {
	tests := []struct {
		name   string
		break_ func(map[string]any)
		want   string
	}{
		{"no approaches", func(d map[string]any) { d["approaches"] = []any{} }, "no approaches"},
		{"too many approaches", func(d map[string]any) {
			d["approaches"] = []any{
				map[string]any{"name": "A", "sketch": "s", "tradeoff": "t"},
				map[string]any{"name": "B", "sketch": "s", "tradeoff": "t"},
				map[string]any{"name": "C", "sketch": "s", "tradeoff": "t"},
				map[string]any{"name": "D", "sketch": "s", "tradeoff": "t"},
			}
			d["recommendation"] = map[string]any{"approach": "A", "why": "w"}
		}, "more than the 3"},
		{"an approach with no trade-off", func(d map[string]any) {
			d["approaches"].([]any)[0].(map[string]any)["tradeoff"] = "  "
		}, "no trade-off"},
		{"an approach with no sketch", func(d map[string]any) {
			d["approaches"].([]any)[1].(map[string]any)["sketch"] = ""
		}, "says nothing about what it is"},
		{"two approaches with one name", func(d map[string]any) {
			d["approaches"].([]any)[1].(map[string]any)["name"] = "filter in vet"
		}, "both named"},
		{"a recommendation naming nothing offered", func(d map[string]any) {
			d["recommendation"] = map[string]any{"approach": "Filter in Neither", "why": "w"}
		}, "not one of the approaches"},
		{"a recommendation with no argument", func(d map[string]any) {
			d["recommendation"] = map[string]any{"approach": "Filter in Vet", "why": ""}
		}, "no argument"},
		{"no recommendation at all", func(d map[string]any) { delete(d, "recommendation") }, "recommends nothing"},
		{"no files", func(d map[string]any) { d["files"] = []any{} }, "cites no files"},
		// The draft's only citation loses its line number, which is a
		// cosmetic defect and dropped rather than failing the draft — but
		// dropping the only citation leaves none, and a plan with zero
		// citations is still refused, just for that reason instead.
		{"a file with no line, and it was the only one", func(d map[string]any) {
			d["files"].([]any)[0].(map[string]any)["location"] = "internal/queue/queue.go"
		}, "cites no files"},
		{"a file with no note", func(d map[string]any) {
			d["files"].([]any)[0].(map[string]any)["note"] = ""
		}, "why it matters"},
		{"an off-scale estimate", func(d map[string]any) { d["estimate"] = 4 }, "not on the covenant's fibonacci scale"},
		{"no estimate", func(d map[string]any) { delete(d, "estimate") }, "carries no estimate"},
		{"a plan with no steps", func(d map[string]any) {
			d["plan"].(map[string]any)["steps"] = []any{}
		}, "no steps"},
		{"a plan with no test story", func(d map[string]any) {
			d["plan"].(map[string]any)["tests"] = ""
		}, "no test story"},
		{"an assumption with no cost", func(d map[string]any) {
			d["assumptions"].([]any)[0].(map[string]any)["cost"] = "medium-ish"
		}, "orders the questions"},
		{"an assumption that does not say what breaks", func(d map[string]any) {
			d["assumptions"].([]any)[0].(map[string]any)["if_wrong"] = ""
		}, "what changes if it is wrong"},
		{"no understanding of the ticket", func(d map[string]any) { d["understanding"] = "" }, "no understanding"},
		{"a premise verdict that is neither", func(d map[string]any) { d["premise"] = "unsure" }, `is "unsure"`},
		{"a wrong premise with no account", func(d map[string]any) {
			d["premise"] = "wrong"
			d["reason"] = ""
		}, "gives no reason"},
		// A misspelled field silently read as absent is the failure this
		// costs one model call to catch and a whole plan to miss.
		{"a misspelled field", func(d map[string]any) {
			d["open_question"] = []any{"typo"}
		}, "does not match the schema"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := goodDraft()
			tc.break_(d)
			_, err := plan.ParseDraft(raw(t, d), covenant.Default())
			if err == nil {
				t.Fatal("the draft was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v\nwant it to mention %q", err, tc.want)
			}
		})
	}
}

// A wrong premise is a complete handoff on its own: the scout that found
// the ticket untrue owes nothing else, and demanding a plan for work that
// should not happen is how a plan talks itself into one.
func TestParseDraftAcceptsAWrongPremiseAlone(t *testing.T) {
	d, err := plan.ParseDraft(raw(t, map[string]any{
		"premise": "wrong",
		"reason":  "The section writer this asks for already exists in internal/linear/section.go.",
	}), covenant.Default())
	if err != nil {
		t.Fatalf("ParseDraft: %v", err)
	}
	if d.Premise != plan.PremiseWrong {
		t.Errorf("premise = %q", d.Premise)
	}
}

// A citation into one file that recurs at several locations is still a
// citation, not a hedge — the validator must parse the list a scout
// naturally writes rather than reject it as a bare filename.
func TestParseDraftAcceptsMultiLocationCitations(t *testing.T) {
	tests := []string{
		"internal/run/run.go:218,254,296,496",
		"internal/cli/team_key_test.go:25-27,109-117",
		"internal/verbs/verbs.go:122-155,163-191,237-280,305-345",
		"internal/cli/ui.go:98-99,119",
		"docs/content/docs/commands/ui.md:24,110",
	}
	for _, loc := range tests {
		t.Run(loc, func(t *testing.T) {
			d := goodDraft()
			d["files"] = []any{map[string]any{"location": loc, "note": "n"}}
			if _, err := plan.ParseDraft(raw(t, d), covenant.Default()); err != nil {
				t.Errorf("ParseDraft rejected %q: %v", loc, err)
			}
		})
	}
}

// A bare filename is dropped, not fatal. It used to fail the whole draft,
// and that one rule accounted for 18 of the 24 parks in the reference
// journal — six-minute, 1.7M-token research passes discarded because one
// citation of several lacked a line number.
//
// WND-61 widened the grammar once already, for comma-separated lists; the
// same lesson arriving a second time is the argument for fixing the blast
// radius rather than the grammar again. The scout keeps writing bare paths
// because the grammar cannot say "this whole file is the thing", and its
// only alternatives are to invent ":1" or to park.
func TestParseDraftDropsABareFilenameRatherThanDyingOnIt(t *testing.T) {
	d := goodDraft()
	d["files"] = []any{
		map[string]any{"location": "internal/queue/queue.go", "note": "the whole file is the thing"},
		map[string]any{"location": "internal/plan/handoff.go:271", "note": "the gate itself"},
	}

	got, err := plan.ParseDraft(raw(t, d), covenant.Default())
	if err != nil {
		t.Fatalf("a draft whose only defect was one bare citation was refused: %v", err)
	}
	if len(got.Files) != 1 || got.Files[0].Location != "internal/plan/handoff.go:271" {
		t.Fatalf("files = %+v, want only the citation that carried a line", got.Files)
	}
	// Dropped silently is how a plan quietly becomes less than the scout
	// found. Recording it is the whole difference between trimming and
	// losing.
	if len(got.Dropped) != 1 || got.Dropped[0] != "internal/queue/queue.go" {
		t.Errorf("dropped = %v, want the one citation that carried no line", got.Dropped)
	}
}

// Dropping is not a licence to accept a draft that cites nothing usable: a
// plan with zero citations left is still not a plan, however it got to
// zero. The refusal just changes its reason.
func TestParseDraftStillRefusesADraftWhoseOnlyCitationIsBare(t *testing.T) {
	d := goodDraft()
	d["files"] = []any{map[string]any{"location": "internal/queue/queue.go", "note": "n"}}
	_, err := plan.ParseDraft(raw(t, d), covenant.Default())
	if err == nil {
		t.Fatal("a draft with no usable citation was accepted")
	}
	if !strings.Contains(err.Error(), "cites no files") {
		t.Errorf("error = %v\nwant it to mention %q", err, "cites no files")
	}
}

// The split is only worth anything if the structural half stayed fatal. A
// citation with nothing said about why it matters is not fixable by
// dropping a location — the scout did not do that part of the job.
func TestParseDraftKeepsStructuralDefectsFatal(t *testing.T) {
	d := goodDraft()
	d["files"] = []any{
		map[string]any{"location": "internal/plan/handoff.go:271", "note": ""},
		map[string]any{"location": "internal/plan/plan.go:324", "note": "still fine"},
	}
	if _, err := plan.ParseDraft(raw(t, d), covenant.Default()); err == nil {
		t.Fatal("a citation with an empty note was accepted")
	}
}

func TestParseDraftFollowsTheCovenantsScale(t *testing.T) {
	cov := covenant.Default()
	cov.IssueEstimationType = "exponential"
	d := goodDraft()
	d["estimate"] = 4 // off fibonacci, on exponential
	if _, err := plan.ParseDraft(raw(t, d), cov); err != nil {
		t.Errorf("an estimate on the team's own scale was refused: %v", err)
	}

	// A team that does not estimate must not be handed a number: Linear
	// stores it and nothing on the board can explain where it came from.
	cov.IssueEstimationType = "notUsed"
	if _, err := plan.ParseDraft(raw(t, d), cov); err == nil {
		t.Error("an estimate was accepted for a team that does not estimate")
	}
	delete(d, "estimate")
	if _, err := plan.ParseDraft(raw(t, d), cov); err != nil {
		t.Errorf("a plan with no estimate was refused on a team that does not estimate: %v", err)
	}
}

func TestParseDraftRefusesNothing(t *testing.T) {
	if _, err := plan.ParseDraft(nil, covenant.Default()); err == nil {
		t.Error("a missing handoff was accepted")
	}
}

func TestParseCritique(t *testing.T) {
	sound, err := plan.ParseCritique(json.RawMessage(`{"verdict":"sound"}`))
	if err != nil {
		t.Fatalf("ParseCritique: %v", err)
	}
	if len(sound.Objections) != 0 {
		t.Error("a sound verdict carried objections")
	}

	tests := []struct {
		name string
		body string
		want string
	}{
		{"flawed with nothing to say", `{"verdict":"flawed"}`, "raised no objections"},
		{"an unknown verdict", `{"verdict":"meh"}`, `is "meh"`},
		{"an objection with no consequence", `{"verdict":"flawed","objections":[{"target":"the plan","summary":"step 2 cannot work","consequence":""}]}`, "states no consequence"},
		{"an objection about nothing", `{"verdict":"flawed","objections":[{"target":"","summary":"s","consequence":"c"}]}`, "names no target"},
		{"no handoff", ``, "wrote no handoff"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := plan.ParseCritique(json.RawMessage(tc.body))
			if err == nil {
				t.Fatal("the critique was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v\nwant it to mention %q", err, tc.want)
			}
		})
	}
}

func withChanges(d map[string]any, changes string) map[string]any {
	out := map[string]any{}
	for k, v := range d {
		out[k] = v
	}
	out["changes"] = changes
	return out
}

func TestParseReplanAcceptsADraftWithAnAccountOfWhatChanged(t *testing.T) {
	rev, err := plan.ParseReplan(raw(t, withChanges(goodDraft(), "Switched the recommendation because the human said Filter in Build was already tried.")), covenant.Default())
	if err != nil {
		t.Fatalf("ParseReplan: %v", err)
	}
	if rev.Changes == "" {
		t.Error("the changes account was lost")
	}
	if rev.Recommendation.Approach != "Filter in Vet" {
		t.Errorf("the draft itself was lost: recommendation = %q", rev.Recommendation.Approach)
	}
}

// The changes account is the whole point of a re-plan comment: without it
// there is nothing to quote a human comparing this revision to the one
// they just answered.
func TestParseReplanRequiresTheChangesAccountWhenThePremiseIsSound(t *testing.T) {
	_, err := plan.ParseReplan(raw(t, goodDraft()), covenant.Default())
	if err == nil || !strings.Contains(err.Error(), "no account of what changed") {
		t.Fatalf("err = %v, want a refusal naming the missing changes account", err)
	}
}

// A wrong premise withdraws the whole draft; there is nothing left to
// account for, so no changes field is required.
func TestParseReplanAllowsAWrongPremiseWithNoChangesAccount(t *testing.T) {
	rev, err := plan.ParseReplan(raw(t, map[string]any{
		"premise": "wrong",
		"reason":  "This landed already, in WND-4.",
	}), covenant.Default())
	if err != nil {
		t.Fatalf("ParseReplan: %v", err)
	}
	if rev.Premise != plan.PremiseWrong {
		t.Errorf("premise = %q, want wrong", rev.Premise)
	}
}
