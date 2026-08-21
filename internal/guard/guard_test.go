package guard

import (
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
)

// The conformance suite, ported assertion-for-assertion from the reference
// system's test-linear-guards.mjs. These cases are not really about the
// code: they are the difference between a rule that exists and a rule that
// holds — the rule they cover was written down in prose twice and was still
// broken.

// The connector id is whatever an install's Linear MCP server is called. The
// guard must not depend on the value — reconnecting Linear changes it, and a
// rule pinned to yesterday's id allows everything with no sign that it has.
const (
	linearTool      = "mcp__79e4e202-24ab-40dc-b0d9-91f6f3b6201f__save_issue"
	reconnectedTool = "mcp__00000000-1111-2222-3333-444444444444__save_issue"
)

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		input map[string]any
		block bool
	}{
		// --- the transition the guard exists for -------------------------
		// A transition to Needs Input failed and the ticket landed here.
		{"Todo", linearTool, map[string]any{"id": "WND-1", "state": "Todo"}, true},
		{"Todo after a reconnect changed the id", reconnectedTool, map[string]any{"id": "WND-1", "state": "Todo"}, true},
		{"todo lowercase", linearTool, map[string]any{"id": "WND-1", "state": "todo"}, true},
		{"TODO shouted", linearTool, map[string]any{"id": "WND-1", "state": "TODO"}, true},
		{"To Do spelled with a space", linearTool, map[string]any{"id": "WND-1", "state": "To Do"}, true},
		{"Todo with stray whitespace", linearTool, map[string]any{"id": "WND-1", "state": "  Todo "}, true},
		// Creating straight into Todo is the same promotion, just without
		// the ticket having existed first — an agent blessing its own work.
		{"created directly in Todo", linearTool, map[string]any{"title": "x", "team": "WND", "state": "Todo"}, true},

		// --- Scoping, the same promotion one rung lower ------------------
		{"Scoping", linearTool, map[string]any{"id": "WND-1", "state": "Scoping"}, true},
		{"scoping lowercase", linearTool, map[string]any{"id": "WND-1", "state": "scoping"}, true},
		{"Scoping with stray whitespace", linearTool, map[string]any{"id": "WND-1", "state": " Scoping  "}, true},
		{"created directly in Scoping", linearTool, map[string]any{"title": "x", "team": "WND", "state": "Scoping"}, true},

		// --- closing, which is equally the human's call ------------------
		{"Done", linearTool, map[string]any{"id": "WND-1", "state": "Done"}, true},
		{"Canceled", linearTool, map[string]any{"id": "WND-1", "state": "Canceled"}, true},
		{"Cancelled, the other spelling", linearTool, map[string]any{"id": "WND-1", "state": "Cancelled"}, true},
		{"Duplicate", linearTool, map[string]any{"id": "WND-1", "state": "Duplicate"}, true},

		// --- state TYPES, which Linear accepts in the same argument ------
		// `state` takes "a state type, name, or ID", so the type spellings
		// are a live way to reach a forbidden status without naming it.
		{"completed type", linearTool, map[string]any{"id": "WND-1", "state": "completed"}, true},
		{"canceled type", linearTool, map[string]any{"id": "WND-1", "state": "canceled"}, true},
		// Todo, Needs Input and Scoping are ALL unstarted, so this resolves
		// to the team default — Todo — and reads as a hand-back while
		// landing as a promotion.
		{"unstarted type is ambiguous", linearTool, map[string]any{"id": "WND-1", "state": "unstarted"}, true},

		// --- every status an agent legitimately sets ---------------------
		{"In Progress (claim)", linearTool, map[string]any{"id": "WND-1", "state": "In Progress"}, false},
		{"started type", linearTool, map[string]any{"id": "WND-1", "state": "started"}, false},
		{"In Review (ship)", linearTool, map[string]any{"id": "WND-1", "state": "In Review"}, false},
		{"Needs Input (handback)", linearTool, map[string]any{"id": "WND-1", "state": "Needs Input"}, false},
		{"needs input, loosely typed", linearTool, map[string]any{"id": "WND-1", "state": "needs input"}, false},
		// Scoped is the research-side analog of In Review: the plan
		// orchestrator's terminal write, allowed the same way In Review is.
		{"Scoped (plan's terminal write)", linearTool, map[string]any{"id": "WND-1", "state": "Scoped"}, false},
		{"scoped, loosely typed", linearTool, map[string]any{"id": "WND-1", "state": "scoped"}, false},
		{"Backlog (abandon)", linearTool, map[string]any{"id": "WND-1", "state": "Backlog"}, false},
		{"backlog type", linearTool, map[string]any{"id": "WND-1", "state": "backlog"}, false},
		{"Triage (file)", linearTool, map[string]any{"id": "WND-1", "state": "Triage"}, false},

		// --- writes that set no state at all -----------------------------
		// Assigning, relabelling and editing a description are ordinary
		// work, and a guard that blocked them would be routed around
		// within a day.
		{"assignee only", linearTool, map[string]any{"id": "WND-1", "assignee": "me"}, false},
		{"unassigning", linearTool, map[string]any{"id": "WND-1", "assignee": nil}, false},
		{"labels only", linearTool, map[string]any{"id": "WND-1", "labels": []any{"agent-filed"}}, false},
		{"no state key", linearTool, map[string]any{"id": "WND-1", "description": "Todo: fix this"}, false},
		{"empty state", linearTool, map[string]any{"id": "WND-1", "state": ""}, false},
		{"non-string state", linearTool, map[string]any{"id": "WND-1", "state": 3}, false},
		{"missing input", linearTool, nil, false},

		// --- other tools are none of this guard's business ---------------
		// A ticket that merely mentions Todo, and the read tools every
		// session leans on.
		{"a comment about Todo", "mcp__79e4e202-24ab-40dc-b0d9-91f6f3b6201f__save_comment",
			map[string]any{"issueId": "WND-1", "body": "moved to Todo by a human"}, false},
		{"listing issues in Todo", "mcp__79e4e202-24ab-40dc-b0d9-91f6f3b6201f__list_issues",
			map[string]any{"state": "Todo"}, false},
		{"a Bash call", "Bash", map[string]any{"command": "git status"}, false},
		{"a tool whose name merely contains save_issue", "save_issue", map[string]any{"state": "Todo"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, blocked := Evaluate(tt.tool, tt.input)
			if blocked != tt.block {
				t.Fatalf("Evaluate(%q, %v) blocked = %v, want %v", tt.tool, tt.input, blocked, tt.block)
			}
			if blocked && reason == "" {
				t.Fatal("blocked with an empty reason; the reason is most of the value of a block")
			}
			if !blocked && reason != "" {
				t.Fatalf("allowed but carrying a reason: %q", reason)
			}
		})
	}
}

// The message is the whole value of a block: an agent that is stopped
// without being told where to go next improvises, which is how the ticket
// reached Todo in the first place.
func TestReasonsNameTheAlternative(t *testing.T) {
	reasonFor := func(state string) string {
		t.Helper()
		reason, blocked := CheckState(state)
		if !blocked {
			t.Fatalf("CheckState(%q) unexpectedly allowed", state)
		}
		return reason
	}

	todo := reasonFor("Todo")
	if !strings.Contains(todo, "In Progress") {
		t.Error("Todo block does not name In Progress as the answer")
	}
	if !strings.Contains(todo, "Backlog") {
		t.Error("Todo block does not point at the Backlog hand-back")
	}

	if done := reasonFor("Done"); !strings.Contains(done, "Backlog") {
		t.Error("close block does not point at Backlog")
	}

	// A scout blocked here is one transition from finishing correctly, so
	// the message has to name that transition. Told only "no", it
	// improvises — and the nearest wrong answer, Todo, is itself blocked,
	// which leaves it with nothing.
	scoping := reasonFor("Scoping")
	if !strings.Contains(scoping, "Needs Input") {
		t.Error("Scoping block does not name Needs Input as the answer")
	}
	if !strings.Contains(scoping, "human promotes it") {
		t.Error("Scoping block does not say a human promotes it")
	}

	if unstarted := reasonFor("unstarted"); !strings.Contains(unstarted, "Scoping") {
		t.Error("unstarted block does not name all three unstarted statuses")
	}
}

// The forbidden set is hardcoded policy — which transitions are the human's
// call — while the covenant is topology — which states exist. This test is
// the seam between them: if the topology drifts, it fails, and a human
// reconciles policy with topology deliberately instead of the guard changing
// behavior on its own.
func TestForbiddenSetMatchesCovenant(t *testing.T) {
	cov := covenant.Default()

	byName := map[string]covenant.Status{}
	for _, s := range cov.Statuses {
		byName[normalize(s.Name)] = s
	}

	// Every forbidden name is a real covenant status, modulo the deliberate
	// aliases ("to do" and the double-l "cancelled").
	aliases := map[string]bool{"to do": true, "cancelled": true}
	for name := range forbiddenNames {
		if aliases[name] {
			continue
		}
		if _, ok := byName[name]; !ok {
			t.Errorf("forbidden name %q is not a covenant status; reconcile policy with topology", name)
		}
	}

	// The ambiguity message for the unstarted type claims exactly four
	// unstarted statuses: Todo, Needs Input, Scoping, Scoped.
	unstarted := map[string]bool{}
	for _, s := range cov.Statuses {
		if s.Type == "unstarted" {
			unstarted[normalize(s.Name)] = true
		}
	}
	want := map[string]bool{"todo": true, "needs input": true, "scoping": true, "scoped": true}
	if len(unstarted) != len(want) {
		t.Errorf("covenant has %d unstarted statuses %v, the unstarted reason claims %v", len(unstarted), unstarted, want)
	}
	for name := range want {
		if !unstarted[name] {
			t.Errorf("covenant is missing unstarted status %q that the unstarted reason claims", name)
		}
	}

	// The closing state types map to statuses whose names are forbidden:
	// blocking the type must not be reachable around by the name.
	for _, s := range cov.Statuses {
		switch s.Type {
		case "completed", "canceled", "duplicate":
			if _, ok := forbiddenNames[normalize(s.Name)]; !ok {
				t.Errorf("closing status %q (%s) is not forbidden by name", s.Name, s.Type)
			}
		}
	}
}
