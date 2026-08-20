package linear

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// graphqlRequest decodes the request a test server received.
type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func decodeRequest(t *testing.T, r *http.Request) graphqlRequest {
	t.Helper()
	var req graphqlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return req
}

func TestTeamIssuesByStateReadsInverseRelations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		// The blocker read must be the INVERSE direction. The outgoing
		// `relations` connection is this issue blocking others — vetting
		// against it was a real regression in the reference system.
		if !strings.Contains(req.Query, "inverseRelations") {
			t.Errorf("query does not read inverseRelations:\n%s", req.Query)
		}
		fmt.Fprint(w, `{"data":{"issues":{
			"pageInfo":{"hasNextPage":false,"endCursor":""},
			"nodes":[{
				"id":"i1","identifier":"WND-7","title":"Blocked one",
				"description":"","url":"https://linear.app/x/issue/WND-7",
				"priority":2,"createdAt":"2026-08-01T00:00:00.000Z",
				"state":{"name":"Todo","type":"unstarted"},
				"assignee":null,
				"labels":{"nodes":[{"name":"agent-filed"}]},
				"inverseRelations":{"nodes":[
					{"type":"blocks","issue":{"identifier":"WND-8","state":{"name":"In Progress","type":"started"}}},
					{"type":"related","issue":{"identifier":"WND-9","state":{"name":"Todo","type":"unstarted"}}}
				]}
			}]
		}}}`)
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	issues, err := c.TeamIssuesByState(context.Background(), "WND", "Todo")
	if err != nil {
		t.Fatalf("teamIssuesByState: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	issue := issues[0]
	if issue.Identifier != "WND-7" || issue.Priority != 2 {
		t.Errorf("issue = %+v", issue)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "agent-filed" {
		t.Errorf("labels = %v", issue.Labels)
	}
	// Only the `blocks` relation counts; `related` says nothing about readiness.
	if len(issue.BlockedBy) != 1 {
		t.Fatalf("blockedBy = %+v, want exactly the blocks relation", issue.BlockedBy)
	}
	if b := issue.BlockedBy[0]; b.Identifier != "WND-8" || b.State.Type != "started" {
		t.Errorf("blocker = %+v", b)
	}
}

func TestTeamIssuesByStateFollowsPagination(t *testing.T) {
	var afters []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		afters = append(afters, req.Variables["after"])
		node := func(id string) string {
			return fmt.Sprintf(`{"id":%[1]q,"identifier":%[1]q,"title":"t",
				"description":"","url":"","priority":3,"createdAt":"2026-08-01T00:00:00.000Z",
				"state":{"name":"Todo","type":"unstarted"},"assignee":null,
				"labels":{"nodes":[]},"inverseRelations":{"nodes":[]}}`, id)
		}
		if req.Variables["after"] == nil {
			fmt.Fprint(w, `{"data":{"issues":{"pageInfo":{"hasNextPage":true,"endCursor":"cur1"},"nodes":[`+node("WND-1")+`]}}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"issues":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[`+node("WND-2")+`]}}}`)
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	issues, err := c.TeamIssuesByState(context.Background(), "WND", "Todo")
	if err != nil {
		t.Fatalf("teamIssuesByState: %v", err)
	}
	if len(issues) != 2 || issues[0].ID != "WND-1" || issues[1].ID != "WND-2" {
		t.Errorf("issues = %+v, want both pages joined in order", issues)
	}
	if len(afters) != 2 || afters[0] != nil || afters[1] != "cur1" {
		t.Errorf("cursors sent = %v, want [nil cur1]", afters)
	}
}

func TestIssueByIdentifier(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		if req.Variables["id"] != "WND-3" {
			t.Errorf("id variable = %v", req.Variables["id"])
		}
		fmt.Fprint(w, `{"data":{"issue":{
			"id":"uuid-3","identifier":"WND-3","title":"The read layer",
			"description":"Two read commands.","url":"https://linear.app/x/issue/WND-3",
			"priority":2,"createdAt":"2026-08-19T01:35:39.866Z",
			"state":{"name":"Todo","type":"unstarted"},
			"assignee":{"name":"Matt Walters"},
			"labels":{"nodes":[]},
			"inverseRelations":{"nodes":[]}
		}}}`)
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	issue, err := c.IssueByIdentifier(context.Background(), "WND-3")
	if err != nil {
		t.Fatalf("issueByIdentifier: %v", err)
	}
	if issue.Assignee != "Matt Walters" || issue.Description != "Two read commands." {
		t.Errorf("issue = %+v", issue)
	}
}

func TestIssueByIdentifierMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"issue":null}}`)
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	if _, err := c.IssueByIdentifier(context.Background(), "WND-999"); err == nil {
		t.Fatal("want an error for a missing issue, got nil")
	}
}

// The reference system read one page of comments and stopped; the newest
// comments — usually the human's answer — silently fell off long tickets.
func TestIssueCommentsFollowsPagination(t *testing.T) {
	var afters []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		afters = append(afters, req.Variables["after"])
		if req.Variables["after"] == nil {
			fmt.Fprint(w, `{"data":{"issue":{"comments":{
				"pageInfo":{"hasNextPage":true,"endCursor":"cur1"},
				"nodes":[{"id":"c1","body":"first","createdAt":"2026-08-01T00:00:00.000Z",
					"user":{"name":"Matt Walters"},"botActor":null}]
			}}}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"issue":{"comments":{
			"pageInfo":{"hasNextPage":false,"endCursor":""},
			"nodes":[{"id":"c2","body":"second","createdAt":"2026-08-02T00:00:00.000Z",
				"user":null,"botActor":{"name":"Scout"}}]
		}}}}`)
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	comments, err := c.IssueComments(context.Background(), "uuid-3")
	if err != nil {
		t.Fatalf("issueComments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want both pages", len(comments))
	}
	if comments[0].Author != "Matt Walters" || comments[1].Author != "Scout" {
		t.Errorf("authors = %q, %q", comments[0].Author, comments[1].Author)
	}
	if len(afters) != 2 || afters[1] != "cur1" {
		t.Errorf("cursors sent = %v, want the second request to carry cur1", afters)
	}
}

func TestPriorityName(t *testing.T) {
	for p, want := range map[int]string{0: "No priority", 1: "Urgent", 4: "Low"} {
		if got := PriorityName(p); got != want {
			t.Errorf("PriorityName(%d) = %q, want %q", p, got, want)
		}
	}
}

// The label and state-type reads share the pagination loop with the state
// read, but sharing is not the property under test — following the cursor
// is. A read that stopped at one page would return a short board that looks
// exactly like a quiet one, which is the bug this loop exists to prevent.
func TestIssueReadsFollowPagination(t *testing.T) {
	tests := []struct {
		name string
		read func(*Client) ([]Issue, error)
		want string // the filter variable the query must carry
	}{
		{
			name: "by label",
			read: func(c *Client) ([]Issue, error) {
				return c.TeamIssuesByLabel(context.Background(), "WND", "ready-for-human")
			},
			want: "ready-for-human",
		},
		{
			name: "by state type",
			read: func(c *Client) ([]Issue, error) {
				return c.TeamIssuesByStateType(context.Background(), "WND", "started")
			},
			want: "started",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var afters []any
			var sawFilter bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				req := decodeRequest(t, r)
				afters = append(afters, req.Variables["after"])
				for _, v := range req.Variables {
					if v == tt.want {
						sawFilter = true
					}
				}
				node := func(id string) string {
					return fmt.Sprintf(`{"id":%[1]q,"identifier":%[1]q,"title":"t",
						"description":"","url":"","priority":0,"createdAt":"2026-08-01T00:00:00.000Z",
						"state":{"name":"In Review","type":"started"},"assignee":null,
						"labels":{"nodes":[]},"inverseRelations":{"nodes":[]}}`, id)
				}
				if req.Variables["after"] == nil {
					fmt.Fprint(w, `{"data":{"issues":{"pageInfo":{"hasNextPage":true,"endCursor":"cur1"},"nodes":[`+node("WND-1")+`]}}}`)
					return
				}
				fmt.Fprint(w, `{"data":{"issues":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[`+node("WND-2")+`]}}}`)
			}))
			defer srv.Close()

			issues, err := tt.read(&Client{APIKey: "k", Endpoint: srv.URL})
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if len(issues) != 2 || issues[0].ID != "WND-1" || issues[1].ID != "WND-2" {
				t.Errorf("issues = %+v, want both pages joined in order", issues)
			}
			if len(afters) != 2 || afters[0] != nil || afters[1] != "cur1" {
				t.Errorf("cursors sent = %v, want [nil cur1]", afters)
			}
			if !sawFilter {
				t.Errorf("the query never carried %q; it is filtering on something else", tt.want)
			}
		})
	}
}
