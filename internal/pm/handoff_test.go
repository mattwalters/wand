package pm_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/pm"
)

func marshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func goodDraft() map[string]any {
	return map[string]any{
		"premise": "sound",
		"projects": []any{
			map[string]any{"name": "Onboarding", "description": "New-user flow work"},
		},
		"tickets": []any{
			map[string]any{
				"title":   "Add signup form validation",
				"body":    "Goal: validate email format client-side.\n\nOpen questions: none.",
				"project": "Onboarding",
			},
			map[string]any{
				"title":      "Wire signup form to the API",
				"body":       "Goal: submit the validated form.\n\nOpen questions: retry policy.",
				"project":    "Onboarding",
				"blocked_by": []any{"Add signup form validation"},
			},
		},
	}
}

func TestParseDraftAcceptsAWellFormedDraft(t *testing.T) {
	d, err := pm.ParseDraft(marshal(t, goodDraft()))
	if err != nil {
		t.Fatalf("ParseDraft: %v", err)
	}
	if len(d.Tickets) != 2 {
		t.Fatalf("tickets = %d, want 2", len(d.Tickets))
	}
}

func TestParseDraftRejectsUnknownFields(t *testing.T) {
	bad := goodDraft()
	bad["extra_field"] = "surprise"
	if _, err := pm.ParseDraft(marshal(t, bad)); err == nil {
		t.Fatal("expected an error for an unknown field")
	}
}

func TestParseDraftRejectsEmptyHandoff(t *testing.T) {
	if _, err := pm.ParseDraft(nil); err == nil {
		t.Fatal("expected an error for an empty handoff")
	}
}

func TestParseDraftRejectsBadPremise(t *testing.T) {
	bad := goodDraft()
	bad["premise"] = "maybe"
	if _, err := pm.ParseDraft(marshal(t, bad)); err == nil {
		t.Fatal("expected an error for an invalid premise")
	}
}

func TestParseDraftWrongPremiseNeedsAReason(t *testing.T) {
	if _, err := pm.ParseDraft(marshal(t, map[string]any{"premise": "wrong"})); err == nil {
		t.Fatal("expected an error for a wrong premise with no reason")
	}
}

func TestParseDraftWrongPremiseNeedsNoTickets(t *testing.T) {
	d, err := pm.ParseDraft(marshal(t, map[string]any{"premise": "wrong", "reason": "already tracked by WND-9"}))
	if err != nil {
		t.Fatalf("ParseDraft: %v", err)
	}
	if d.Premise != pm.PremiseWrong {
		t.Fatalf("premise = %q", d.Premise)
	}
}

func TestParseDraftRejectsNoTickets(t *testing.T) {
	if _, err := pm.ParseDraft(marshal(t, map[string]any{"premise": "sound"})); err == nil {
		t.Fatal("expected an error for a sound premise with no tickets")
	}
}

func TestParseDraftRejectsEmptyTitle(t *testing.T) {
	bad := goodDraft()
	tickets := bad["tickets"].([]any)
	tickets[0].(map[string]any)["title"] = "  "
	if _, err := pm.ParseDraft(marshal(t, bad)); err == nil {
		t.Fatal("expected an error for an empty title")
	}
}

func TestParseDraftRejectsEmptyBody(t *testing.T) {
	bad := goodDraft()
	tickets := bad["tickets"].([]any)
	tickets[0].(map[string]any)["body"] = ""
	if _, err := pm.ParseDraft(marshal(t, bad)); err == nil {
		t.Fatal("expected an error for an empty body")
	}
}

func TestParseDraftRejectsDuplicateTitles(t *testing.T) {
	bad := goodDraft()
	tickets := bad["tickets"].([]any)
	tickets[1].(map[string]any)["title"] = tickets[0].(map[string]any)["title"]
	tickets[1].(map[string]any)["blocked_by"] = []any{}
	if _, err := pm.ParseDraft(marshal(t, bad)); err == nil {
		t.Fatal("expected an error for duplicate titles")
	}
}

func TestParseDraftRejectsDuplicateProjectNames(t *testing.T) {
	bad := goodDraft()
	bad["projects"] = []any{
		map[string]any{"name": "Onboarding", "description": "a"},
		map[string]any{"name": "onboarding", "description": "b"},
	}
	if _, err := pm.ParseDraft(marshal(t, bad)); err == nil {
		t.Fatal("expected an error for duplicate project names (case-insensitive)")
	}
}

func TestParseDraftRejectsProjectWithNoDescription(t *testing.T) {
	bad := goodDraft()
	bad["projects"] = []any{map[string]any{"name": "Onboarding", "description": ""}}
	if _, err := pm.ParseDraft(marshal(t, bad)); err == nil {
		t.Fatal("expected an error for a project with no description")
	}
}

func TestParseDraftRejectsDanglingBlockedBy(t *testing.T) {
	bad := goodDraft()
	tickets := bad["tickets"].([]any)
	tickets[1].(map[string]any)["blocked_by"] = []any{"Some ticket nobody proposed"}
	_, err := pm.ParseDraft(marshal(t, bad))
	if err == nil {
		t.Fatal("expected an error for a dangling blocked_by reference")
	}
	if !strings.Contains(err.Error(), "not one of the proposed titles") {
		t.Errorf("error = %v", err)
	}
}

func TestParseDraftRejectsSelfBlockedBy(t *testing.T) {
	bad := goodDraft()
	tickets := bad["tickets"].([]any)
	title := tickets[0].(map[string]any)["title"]
	tickets[0].(map[string]any)["blocked_by"] = []any{title}
	_, err := pm.ParseDraft(marshal(t, bad))
	if err == nil {
		t.Fatal("expected an error for a self-reference")
	}
	if !strings.Contains(err.Error(), "cannot block itself") {
		t.Errorf("error = %v", err)
	}
}

func TestParseDraftRejectsACycle(t *testing.T) {
	draft := map[string]any{
		"premise": "sound",
		"tickets": []any{
			map[string]any{"title": "A", "body": "goal", "blocked_by": []any{"B"}},
			map[string]any{"title": "B", "body": "goal", "blocked_by": []any{"A"}},
		},
	}
	_, err := pm.ParseDraft(marshal(t, draft))
	if err == nil {
		t.Fatal("expected an error for a cycle")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %v", err)
	}
}

// A proposal on disk is a schema-versioned file, but the scout's own
// handoff carries none — the running binary is what defines the schema a
// fresh scout talks to, so there is nothing for it to declare.
func TestParseProposalRequiresASchemaVersion(t *testing.T) {
	if _, err := pm.ParseProposal(marshal(t, goodDraft())); err == nil {
		t.Fatal("expected an error for a file with no schema_version")
	}
}

func TestParseProposalAcceptsTheCurrentSchema(t *testing.T) {
	d := goodDraft()
	d["schema_version"] = pm.SchemaVersion
	if _, err := pm.ParseProposal(marshal(t, d)); err != nil {
		t.Fatalf("ParseProposal: %v", err)
	}
}

func TestParseProposalRefusesANewerSchema(t *testing.T) {
	d := goodDraft()
	d["schema_version"] = pm.SchemaVersion + 1
	_, err := pm.ParseProposal(marshal(t, d))
	if err == nil {
		t.Fatal("expected an error for a newer schema")
	}
	if !strings.Contains(err.Error(), "newer wand") {
		t.Errorf("error = %v", err)
	}
}

func TestTopoOrderPutsBlockersBeforeWhatTheyBlock(t *testing.T) {
	tickets := []pm.ProposedTicket{
		{Title: "Wire signup form to the API", BlockedBy: []string{"Add signup form validation"}},
		{Title: "Add signup form validation"},
	}
	order := pm.TopoOrder(tickets)
	if len(order) != 2 || order[0].Title != "Add signup form validation" || order[1].Title != "Wire signup form to the API" {
		var titles []string
		for _, t := range order {
			titles = append(titles, t.Title)
		}
		t.Fatalf("order = %v, want the blocker first", titles)
	}
}
