package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// This file pins one live-API lesson for the whole package.
//
// `wand pm` could not complete a single run against a real workspace. It died
// half a second in, at its first board read:
//
//	parked — could not read the team's projects: linear: The query is too
//	complex. Complexity: 16300. Maximum allowed complexity: 10000.
//
// The query was Projects', and the defect was a connection with no `first:`:
//
//	teams(filter: {key: {eq: $team}}) {
//	  nodes { projects(first: 250) { nodes { id name description } } }
//	}
//
// Linear costs a query before it runs it, and a filter is not part of the
// estimate — it cannot know the key matches exactly one team until it looks.
// So an unbounded `teams` is budgeted at its default page of 50, each one
// carrying the nested `projects(first: 250)`: 50 × ~326 = 16300, against a
// 10000 ceiling. Nothing about the workspace mattered; the shape alone was
// over budget, so the verb parked everywhere.
//
// Every test in this package stubs the response body and never looks at the
// request, which is right for the tier but means a defect in the *query* is
// invisible to all of them. The rule below is the smallest one that catches
// the class rather than the instance: every connection carries an explicit
// `first:`. It is deliberately strict. A page size that comes from a server
// default is a page size nobody chose, and Linear's estimator charges for it
// whether or not the filter will narrow the result to one.

// firstArg matches an explicit `first:` argument.
var firstArg = regexp.MustCompile(`\bfirst\s*:`)

// connection is one field in a GraphQL document that returns a Linear
// connection, along with its argument list as written.
type connection struct {
	field string
	args  string
}

// bounded reports whether the connection carries an explicit page size.
func (c connection) bounded() bool { return firstArg.MatchString(c.args) }

// connections returns every connection field in a GraphQL document — a field
// whose own selection set holds a `nodes` or `pageInfo` child.
//
// This is a brace matcher, not a GraphQL parser, and the one subtlety is why
// it can stay that way: argument lists are consumed whole at the first `(`,
// which is what keeps an object-valued argument (`filter: {key: {eq: $k}}`)
// from being mistaken for a selection set. Everything left over is names and
// braces.
func connections(doc string) []connection {
	type frame struct {
		field string
		args  string
		conn  bool // a `nodes`/`pageInfo` child was seen in this selection set
	}
	var stack []frame
	var pending frame
	var found []connection

	isName := func(b byte) bool {
		return b == '_' ||
			b >= 'a' && b <= 'z' ||
			b >= 'A' && b <= 'Z' ||
			b >= '0' && b <= '9'
	}

	for i := 0; i < len(doc); {
		switch c := doc[i]; {
		case isName(c):
			j := i
			for j < len(doc) && isName(doc[j]) {
				j++
			}
			name := doc[i:j]
			i = j
			// A name token is always a direct child of the selection set on
			// top of the stack: a deeper one would have been pushed and
			// popped before we got here.
			if len(stack) > 0 && (name == "nodes" || name == "pageInfo") {
				stack[len(stack)-1].conn = true
			}
			pending = frame{field: name}
		case c == '(':
			depth, j := 0, i
			for j < len(doc) {
				if doc[j] == '(' {
					depth++
				} else if doc[j] == ')' {
					depth--
					if depth == 0 {
						j++
						break
					}
				}
				j++
			}
			pending.args = doc[i:j]
			i = j
		case c == '{':
			stack = append(stack, pending)
			pending = frame{}
			i++
		case c == '}':
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if top.conn {
					found = append(found, connection{field: top.field, args: top.args})
				}
			}
			i++
		default:
			i++
		}
	}
	return found
}

// clientCall is one exported Client method, invoked for its query alone. The
// arguments are whatever gets the method as far as sending; what comes back
// is ignored, because the request is the subject.
type clientCall struct {
	name string
	call func(context.Context, *Client)
}

// clientCalls is every exported method on Client that reaches the network.
// TestTheConnectionLintCoversEveryClientMethod fails if a new one is added
// without a row here, so the lint cannot quietly stop being package-wide.
func clientCalls() []clientCall {
	str := func(s string) *string { return &s }
	return []clientCall{
		{"AddLabel", func(ctx context.Context, c *Client) { c.AddLabel(ctx, "i", "l") }},
		{"CreateBlocker", func(ctx context.Context, c *Client) { c.CreateBlocker(ctx, "i", "b") }},
		{"CreateComment", func(ctx context.Context, c *Client) { c.CreateComment(ctx, "i", "body") }},
		{"CreateGitAutomation", func(ctx context.Context, c *Client) { c.CreateGitAutomation(ctx, "t", "review", "s") }},
		{"CreateIssue", func(ctx context.Context, c *Client) {
			c.CreateIssue(ctx, IssueCreate{TeamID: "t", Title: "x", StateID: "s"})
		}},
		{"CreateLabel", func(ctx context.Context, c *Client) { c.CreateLabel(ctx, "t", "parked", "#000") }},
		{"CreateProject", func(ctx context.Context, c *Client) { c.CreateProject(ctx, "t", "Onboarding", "d") }},
		{"CreateRelation", func(ctx context.Context, c *Client) { c.CreateRelation(ctx, "i", "r", RelationDuplicate) }},
		{"CreateTeam", func(ctx context.Context, c *Client) { c.CreateTeam(ctx, "Wand", "WND", TeamSettings{}) }},
		{"CreateWorkflowState", func(ctx context.Context, c *Client) {
			c.CreateWorkflowState(ctx, "t", WorkflowState{Name: "Scoped", Type: "unstarted"}, "#000")
		}},
		{"DeleteTeam", func(ctx context.Context, c *Client) { c.DeleteTeam(ctx, "t") }},
		{"GitAutomations", func(ctx context.Context, c *Client) { c.GitAutomations(ctx, "t") }},
		{"IssueByIdentifier", func(ctx context.Context, c *Client) { c.IssueByIdentifier(ctx, "WND-1") }},
		{"IssueComments", func(ctx context.Context, c *Client) { c.IssueComments(ctx, "i") }},
		{"LabelByName", func(ctx context.Context, c *Client) { c.LabelByName(ctx, "parked") }},
		{"Labels", func(ctx context.Context, c *Client) { c.Labels(ctx) }},
		{"Projects", func(ctx context.Context, c *Client) { c.Projects(ctx, "WND") }},
		{"RemoveLabel", func(ctx context.Context, c *Client) { c.RemoveLabel(ctx, "i", "l") }},
		{"SearchIssues", func(ctx context.Context, c *Client) { c.SearchIssues(ctx, "WND", "term") }},
		{"TeamByKey", func(ctx context.Context, c *Client) { c.TeamByKey(ctx, "WND") }},
		{"TeamIssuesByLabel", func(ctx context.Context, c *Client) { c.TeamIssuesByLabel(ctx, "WND", "parked") }},
		{"TeamIssuesByState", func(ctx context.Context, c *Client) { c.TeamIssuesByState(ctx, "WND", "Todo") }},
		{"TeamIssuesByStateType", func(ctx context.Context, c *Client) { c.TeamIssuesByStateType(ctx, "WND", "started") }},
		{"TeamStates", func(ctx context.Context, c *Client) { c.TeamStates(ctx, "t") }},
		{"UpdateDescription", func(ctx context.Context, c *Client) { c.UpdateDescription(ctx, "i", "body") }},
		{"UpdateGitAutomation", func(ctx context.Context, c *Client) { c.UpdateGitAutomation(ctx, "a", "s") }},
		{"UpdateIssue", func(ctx context.Context, c *Client) { c.UpdateIssue(ctx, "i", IssueUpdate{Description: str("body")}) }},
		{"UpsertSection", func(ctx context.Context, c *Client) { c.UpsertSection(ctx, "i", "", "plan", "body") }},
		{"Viewer", func(ctx context.Context, c *Client) { c.Viewer(ctx) }},
	}
}

// capture runs one client call against a stub that answers every request with
// an empty data object, and returns the queries it was sent. An empty answer
// is what ends the paginating reads after one page: no pageInfo means no next
// page.
func capture(t *testing.T, call func(context.Context, *Client)) []string {
	t.Helper()
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		sent = append(sent, req.Query)
		w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	call(context.Background(), &Client{APIKey: "k", Endpoint: srv.URL})
	return sent
}

// TestEveryConnectionIsBounded is the lesson from the pm park, pinned: no
// query this package sends may contain a connection without an explicit
// `first:`. See the file comment for the incident.
func TestEveryConnectionIsBounded(t *testing.T) {
	for _, tc := range clientCalls() {
		t.Run(tc.name, func(t *testing.T) {
			queries := capture(t, tc.call)
			if len(queries) == 0 {
				t.Fatalf("%s sent no query; the row's arguments must reach the network", tc.name)
			}
			for _, q := range queries {
				for _, conn := range connections(q) {
					if !conn.bounded() {
						t.Errorf("connection %q has no explicit first:, so Linear budgets it at its default page of 50 — bound it, whatever the filter narrows it to.\nargs: %s\nquery: %s",
							conn.field, conn.args, strings.TrimSpace(q))
					}
				}
			}
		})
	}
}

// TestTheConnectionLintCoversEveryClientMethod keeps clientCalls honest. The
// lint is only package-wide for as long as the table names every method, and
// a method added without a row would otherwise be silently unchecked.
func TestTheConnectionLintCoversEveryClientMethod(t *testing.T) {
	// Do is the transport itself: it sends whatever it is handed, so it has
	// no query of its own to lint.
	covered := map[string]bool{"Do": true}
	for _, tc := range clientCalls() {
		covered[tc.name] = true
	}
	typ := reflect.TypeOf(&Client{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !covered[name] {
			t.Errorf("Client.%s has no row in clientCalls, so no test checks the query it sends", name)
		}
	}
}

// TestTheConnectionLintFlagsTheQueryThatParkedEveryPmRun runs the lint over
// the exact query the incident report quoted. A lint that cannot fail on the
// defect it was written for proves nothing about the ones it passes.
func TestTheConnectionLintFlagsTheQueryThatParkedEveryPmRun(t *testing.T) {
	parked := `
		query($team: String!) {
		  teams(filter: {key: {eq: $team}}) {
		    nodes { projects(first: 250) { nodes { id name description } } }
		  }
		}`

	var unbounded []string
	for _, conn := range connections(parked) {
		if !conn.bounded() {
			unbounded = append(unbounded, conn.field)
		}
	}
	if len(unbounded) != 1 || unbounded[0] != "teams" {
		t.Fatalf("unbounded connections = %v, want just [teams]", unbounded)
	}
}

// TestConnectionsReadsArgumentsWithoutMistakingThemForSelections is the one
// place the brace matcher could plausibly go wrong: Linear's filters are
// object-valued arguments, so a query's braces are not all selection sets.
func TestConnectionsReadsArgumentsWithoutMistakingThemForSelections(t *testing.T) {
	doc := `
		query($team: String!, $first: Int!, $after: String) {
		  issues(
		    first: $first, after: $after,
		    filter: {team: {key: {eq: $team}}, state: {name: {eqIgnoreCase: "Todo"}}}
		  ) {
		    pageInfo { hasNextPage endCursor }
		    nodes { id labels(first: 250) { nodes { name } } state { name type } }
		  }
		}`

	var got []string
	for _, conn := range connections(doc) {
		if !conn.bounded() {
			t.Errorf("connection %q read as unbounded; args: %s", conn.field, conn.args)
		}
		got = append(got, conn.field)
	}
	want := map[string]bool{"issues": true, "labels": true}
	if len(got) != len(want) {
		t.Fatalf("connections = %v, want exactly %v", got, want)
	}
	for _, field := range got {
		if !want[field] {
			t.Errorf("connections included %q; `state` and `pageInfo` are not connections", field)
		}
	}
}
