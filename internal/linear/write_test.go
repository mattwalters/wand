package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// AddLabel goes through issueAddLabel, not issueUpdate's full-set labelIds:
// adding one label must not race another writer's label changes by
// replacing the whole set.
func TestAddLabelUsesTheAddMutation(t *testing.T) {
	var query string
	var vars map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		query, vars = req.Query, req.Variables
		w.Write([]byte(`{"data":{"issueAddLabel":{"success":true}}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	if err := c.AddLabel(context.Background(), "issue-1", "label-1"); err != nil {
		t.Fatalf("addLabel: %v", err)
	}
	if !strings.Contains(query, "issueAddLabel") {
		t.Errorf("mutation does not use issueAddLabel:\n%s", query)
	}
	if vars["id"] != "issue-1" || vars["labelId"] != "label-1" {
		t.Errorf("variables %v", vars)
	}
}

func TestAddLabelSurfacesRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"issueAddLabel":{"success":false}}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	if err := c.AddLabel(context.Background(), "issue-1", "label-1"); err == nil {
		t.Fatal("a refused label add reported success")
	}
}

// Zero is an estimate Linear accepts, so it cannot double as "leave this
// alone". A plain int field would make an unset estimate indistinguishable
// from a deliberate zero, and every status-only update would quietly wipe
// the ticket's size.
func TestUpdateIssueEstimateIsOptionalAndCanBeZero(t *testing.T) {
	if _, present := captureInput(t, IssueUpdate{StateID: "st-1"})["estimate"]; present {
		t.Error("estimate sent on a status-only update; that would wipe the ticket's size")
	}

	zero := 0
	input := captureInput(t, IssueUpdate{Estimate: &zero})
	if string(input["estimate"]) != "0" {
		t.Errorf("estimate = %s, want 0 sent explicitly", input["estimate"])
	}

	three := 3
	input = captureInput(t, IssueUpdate{Estimate: &three})
	if string(input["estimate"]) != "3" {
		t.Errorf("estimate = %s, want 3", input["estimate"])
	}
}
