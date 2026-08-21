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

// RemoveLabel goes through issueRemoveLabel, not issueUpdate's full-set
// labelIds, for the same reason AddLabel does: removing one label must not
// race another writer's label changes by replacing the whole set.
func TestRemoveLabelUsesTheRemoveMutation(t *testing.T) {
	var query string
	var vars map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		query, vars = req.Query, req.Variables
		w.Write([]byte(`{"data":{"issueRemoveLabel":{"success":true}}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	if err := c.RemoveLabel(context.Background(), "issue-1", "label-1"); err != nil {
		t.Fatalf("removeLabel: %v", err)
	}
	if !strings.Contains(query, "issueRemoveLabel") {
		t.Errorf("mutation does not use issueRemoveLabel:\n%s", query)
	}
	if vars["id"] != "issue-1" || vars["labelId"] != "label-1" {
		t.Errorf("variables %v", vars)
	}
}

func TestRemoveLabelSurfacesRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"issueRemoveLabel":{"success":false}}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	if err := c.RemoveLabel(context.Background(), "issue-1", "label-1"); err == nil {
		t.Fatal("a refused label remove reported success")
	}
}

// Priority 0 is "No priority", which home sets deliberately when a
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

// captureCreateInput runs one CreateIssue against a stub server and returns
// the raw issueCreate input object as sent on the wire.
func captureCreateInput(t *testing.T, in IssueCreate) map[string]json.RawMessage {
	t.Helper()
	var input map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables struct {
				Input map[string]json.RawMessage `json:"input"`
			} `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck // stub
		input = req.Variables.Input
		w.Write([]byte(`{"data":{"issueCreate":{"success":true,"issue":{"id":"i1","identifier":"T-1"}}}}`)) //nolint:errcheck // stub
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	if _, err := c.CreateIssue(context.Background(), in); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	return input
}

// An empty projectId sent to Linear's API either errors or silently
// associates the issue with an unintended project, so unset must omit the
// field entirely rather than send "".
func TestCreateIssueOmitsAnUnsetProjectID(t *testing.T) {
	input := captureCreateInput(t, IssueCreate{TeamID: "team-1", Title: "t", StateID: "st-1"})
	if _, present := input["projectId"]; present {
		t.Error("projectId sent on a create with no ProjectID set")
	}
}

func TestCreateIssueSendsProjectIDWhenSet(t *testing.T) {
	input := captureCreateInput(t, IssueCreate{TeamID: "team-1", Title: "t", StateID: "st-1", ProjectID: "proj-1"})
	if string(input["projectId"]) != `"proj-1"` {
		t.Errorf("projectId = %s, want %q", input["projectId"], "proj-1")
	}
}

// CreateBlocker's whole purpose is pinning blocks' direction so a call site
// naming arguments "blocked, blocker" cannot land them backwards on the
// wire — the same class of bug issue.go's inverseRelations comment records
// as already having shipped once.
func TestCreateBlockerDirection(t *testing.T) {
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
	if err := c.CreateBlocker(context.Background(), "uuid-blocked", "uuid-blocker"); err != nil {
		t.Fatalf("CreateBlocker: %v", err)
	}
	if got := string(input["issueId"]); got != `"uuid-blocker"` {
		t.Errorf("issueId = %s, want the blocker", got)
	}
	if got := string(input["relatedIssueId"]); got != `"uuid-blocked"` {
		t.Errorf("relatedIssueId = %s, want the blocked issue", got)
	}
	if got := string(input["type"]); got != `"blocks"` {
		t.Errorf("type = %s", got)
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
