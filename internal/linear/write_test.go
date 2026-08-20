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

// One wire field, two struct fields: without this refusal the Unassign
// branch would silently overwrite AssigneeID with null, and a caller that
// set both would unassign with success reported.
func TestUpdateIssueRefusesAssignPlusUnassign(t *testing.T) {
	c := &Client{APIKey: "k", Endpoint: "http://127.0.0.1:0"}
	err := c.UpdateIssue(context.Background(), "uuid-1", IssueUpdate{AssigneeID: "u1", Unassign: true})
	if err == nil {
		t.Fatal("an update that both assigns and unassigns must refuse before the network")
	}
}

func TestUpdateIssueRefusesAnEmptyUpdate(t *testing.T) {
	c := &Client{APIKey: "k", Endpoint: "http://127.0.0.1:0"}
	if err := c.UpdateIssue(context.Background(), "uuid-1", IssueUpdate{}); err == nil {
		t.Fatal("an update with nothing to change must refuse before the network")
	}
}

// Priority 0 is "No priority", which the cockpit sets deliberately when a
// human judges a ticket worth keeping and not worth ranking. A plain int
// field would make that indistinguishable from "leave it alone", and the
// ticket would carry its old rank into the pool — the exact opposite of the
// judgment. Both directions are pinned here.
func TestUpdateIssuePriorityZeroIsSent(t *testing.T) {
	none := 0
	input := captureInput(t, IssueUpdate{StateID: "st-1", Priority: &none})
	raw, present := input["priority"]
	if !present {
		t.Fatal("priority omitted; an explicit No priority has to reach the wire")
	}
	if string(raw) != "0" {
		t.Errorf("priority = %s, want 0", raw)
	}
}

func TestUpdateIssueOmitsAnUnsetPriority(t *testing.T) {
	input := captureInput(t, IssueUpdate{StateID: "st-1"})
	if _, present := input["priority"]; present {
		t.Error("priority sent on a status-only update; that would reset the rank")
	}
}

// The duplicate relation has a direction, and getting it backwards records
// the canonical issue as a duplicate of the throwaway.
func TestCreateRelationDirection(t *testing.T) {
	var input map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables struct {
				Input map[string]json.RawMessage `json:"input"`
			} `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck // stub
		input = req.Variables.Input
		w.Write([]byte(`{"data":{"issueRelationCreate":{"success":true}}}`)) //nolint:errcheck // stub
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	if err := c.CreateRelation(context.Background(), "uuid-dupe", "uuid-canonical", RelationDuplicate); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	if got := string(input["issueId"]); got != `"uuid-dupe"` {
		t.Errorf("issueId = %s, want the duplicate", got)
	}
	if got := string(input["relatedIssueId"]); got != `"uuid-canonical"` {
		t.Errorf("relatedIssueId = %s, want the canonical issue", got)
	}
	if got := string(input["type"]); got != `"duplicate"` {
		t.Errorf("type = %s", got)
	}
}

func TestCreateRelationRefusesSelfLink(t *testing.T) {
	c := &Client{APIKey: "k", Endpoint: "http://127.0.0.1:0"}
	err := c.CreateRelation(context.Background(), "uuid-1", "uuid-1", RelationDuplicate)
	if err == nil {
		t.Fatal("CreateRelation succeeded, want a refusal to link an issue to itself")
	}
}
