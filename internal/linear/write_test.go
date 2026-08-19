package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captureInput runs one UpdateIssue against a stub server and returns the
// raw issueUpdate input object as sent on the wire.
func captureInput(t *testing.T, u IssueUpdate) map[string]json.RawMessage {
	t.Helper()
	var input map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables struct {
				Input map[string]json.RawMessage `json:"input"`
			} `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		input = req.Variables.Input
		w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	if err := c.UpdateIssue(context.Background(), "uuid-1", u); err != nil {
		t.Fatalf("updateIssue: %v", err)
	}
	return input
}

// Unassigning and leaving-alone are different wire shapes: an explicit
// `"assigneeId": null` clears the assignee, an omitted field keeps them.
// Collapsing the two — the natural bug in a map built from zero values —
// makes abandon silently keep the ticket owned, or every update silently
// unassign. Both directions are pinned here.
func TestUpdateIssueUnassignSendsExplicitNull(t *testing.T) {
	input := captureInput(t, IssueUpdate{StateID: "st-1", Unassign: true})
	raw, present := input["assigneeId"]
	if !present {
		t.Fatal("assigneeId omitted; unassigning needs an explicit null")
	}
	if string(raw) != "null" {
		t.Errorf("assigneeId = %s, want null", raw)
	}
}

func TestUpdateIssueSendsOnlyWhatWasSet(t *testing.T) {
	input := captureInput(t, IssueUpdate{StateID: "st-1"})
	if _, present := input["assigneeId"]; present {
		t.Error("assigneeId sent on a status-only update; that would clear or churn the assignee")
	}
	if _, present := input["description"]; present {
		t.Error("description sent on a status-only update; that would clobber the body")
	}
	if string(input["stateId"]) != `"st-1"` {
		t.Errorf("stateId = %s", input["stateId"])
	}
}

func TestUpdateIssueRefusesAnEmptyUpdate(t *testing.T) {
	c := &Client{APIKey: "k", Endpoint: "http://127.0.0.1:0"}
	if err := c.UpdateIssue(context.Background(), "uuid-1", IssueUpdate{}); err == nil {
		t.Fatal("an update with nothing to change must refuse before the network")
	}
}
