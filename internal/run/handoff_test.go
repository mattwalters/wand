package run

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseWorkDone(t *testing.T) {
	h, err := ParseWork(json.RawMessage(`{
		"status": "done",
		"summary": "implemented the thing",
		"title": "add the thing",
		"description_corrections": [{"old": "wrong", "new": "right"}],
		"plan_deviations": ["skipped step 3; already true"]
	}`))
	if err != nil {
		t.Fatalf("ParseWork: %v", err)
	}
	if h.Status != "done" || h.Title != "add the thing" {
		t.Errorf("parsed %+v", h)
	}
	if len(h.DescriptionCorrections) != 1 || h.DescriptionCorrections[0].Old != "wrong" {
		t.Errorf("corrections %+v", h.DescriptionCorrections)
	}
	if len(h.PlanDeviations) != 1 {
		t.Errorf("deviations %+v", h.PlanDeviations)
	}
}

func TestParseWorkRefusals(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string // fragment of the error
	}{
		{"empty", "", "no handoff"},
		{"unknown status", `{"status": "finished", "summary": "s"}`, `"finished"`},
		{"done without summary", `{"status": "done"}`, "no summary"},
		// The blocked reason is quoted verbatim to a human; a blocked
		// handoff without one is unusable, never defaulted (PW-190).
		{"blocked without reason", `{"status": "blocked", "summary": "s"}`, "no reason"},
		// A misspelled field must fail loudly, not silently drop what the
		// worker meant to say.
		{"unknown field", `{"status": "done", "summary": "s", "titel": "t"}`, "schema"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseWork(json.RawMessage(tc.raw))
			if err == nil {
				t.Fatal("parsed, want refusal")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestParseReviewApproveNeedsEvidence(t *testing.T) {
	// Convergence only on positive evidence: an approval that states
	// nothing it verified is not parseable as an approval.
	_, err := ParseReview(json.RawMessage(`{"verdict": "approve"}`))
	if err == nil || !strings.Contains(err.Error(), "positive evidence") {
		t.Fatalf("error %v, want a positive-evidence refusal", err)
	}
	h, err := ParseReview(json.RawMessage(`{"verdict": "approve", "summary": "ran the suite, read the diff"}`))
	if err != nil {
		t.Fatalf("ParseReview: %v", err)
	}
	if h.Verdict != "approve" {
		t.Errorf("verdict %q", h.Verdict)
	}
}

func TestParseReviewReviseNeedsFindings(t *testing.T) {
	_, err := ParseReview(json.RawMessage(`{"verdict": "revise"}`))
	if err == nil || !strings.Contains(err.Error(), "no findings") {
		t.Fatalf("error %v, want a no-findings refusal", err)
	}
}

func TestParseReviewUnknownVerdict(t *testing.T) {
	_, err := ParseReview(json.RawMessage(`{"verdict": "lgtm", "summary": "s"}`))
	if err == nil || !strings.Contains(err.Error(), `"lgtm"`) {
		t.Fatalf("error %v, want the verdict named", err)
	}
}

// schemaNode is a loosely-typed view of a shape-only schema, for asserting
// what internal/schema generated without re-implementing a JSON Schema
// parser in the test.
type schemaNode struct {
	Type                 string                     `json:"type"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Required             []string                   `json:"required"`
	AdditionalProperties *bool                      `json:"additionalProperties"`
}

func decodeSchemaNode(t *testing.T, raw json.RawMessage) schemaNode {
	t.Helper()
	var n schemaNode
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatalf("generated schema is not valid JSON: %v\n%s", err, raw)
	}
	return n
}

// TestWorkSchemaRequiresOnlyStatus holds the reasoning WorkSchema's own doc
// states: Summary and Reason are each required on only one of "done" or
// "blocked", so neither may be required by the schema — a model with
// nothing legitimate to say on the other branch would invent filler
// instead of admitting it (WND-97).
func TestWorkSchemaRequiresOnlyStatus(t *testing.T) {
	top := decodeSchemaNode(t, WorkSchema)
	if top.Type != "object" {
		t.Fatalf("type = %q, want object", top.Type)
	}
	if top.AdditionalProperties == nil || *top.AdditionalProperties {
		t.Error("additionalProperties must be false — this is what catches a renamed field")
	}
	if len(top.Required) != 1 || top.Required[0] != "status" {
		t.Errorf("required = %v, want [status]", top.Required)
	}
	for _, want := range []string{"status", "summary", "reason", "title", "description_corrections", "plan_deviations"} {
		if _, ok := top.Properties[want]; !ok {
			t.Errorf("missing property %q", want)
		}
	}
}

// TestReviewSchemaRequiresOnlyVerdict is ReviewSchema's counterpart: Summary
// and Findings are each required on only one of "approve" or "revise".
func TestReviewSchemaRequiresOnlyVerdict(t *testing.T) {
	top := decodeSchemaNode(t, ReviewSchema)
	if top.AdditionalProperties == nil || *top.AdditionalProperties {
		t.Error("additionalProperties must be false")
	}
	if len(top.Required) != 1 || top.Required[0] != "verdict" {
		t.Errorf("required = %v, want [verdict]", top.Required)
	}
	for _, want := range []string{"verdict", "summary", "findings"} {
		if _, ok := top.Properties[want]; !ok {
			t.Errorf("missing property %q", want)
		}
	}
}

func TestConcreteDropsScenariolessFindings(t *testing.T) {
	kept, dropped := Concrete([]Finding{
		{Summary: "real bug", FailureScenario: "call f(nil): panics"},
		{Summary: "vague unease", FailureScenario: "   "},
		{Summary: "", FailureScenario: "orphan scenario"},
	})
	if dropped != 2 {
		t.Errorf("dropped %d, want 2", dropped)
	}
	if len(kept) != 1 || kept[0].Summary != "real bug" {
		t.Errorf("kept %+v", kept)
	}
}
