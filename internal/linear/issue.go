package linear

import (
	"context"
	"fmt"
	"time"
)

// pageSize is the page size for every paginated read here. Linear allows up
// to 250; a smaller page keeps responses comfortably under proxy limits and
// the loop logic is identical either way.
const pageSize = 50

// Priority names, in Linear's own numbering. Zero is "No priority" — the
// number sorts first but the meaning sorts last, which is why ordering never
// compares raw priority values directly (see the queue package).
var priorityNames = map[int]string{
	0: "No priority",
	1: "Urgent",
	2: "High",
	3: "Medium",
	4: "Low",
}

// PriorityName renders a Linear priority number for humans.
func PriorityName(p int) string {
	if name, ok := priorityNames[p]; ok {
		return name
	}
	return fmt.Sprintf("Priority %d", p)
}

// IssueState is the status an issue currently sits in.
type IssueState struct {
	Name string `json:"name"`
	Type string `json:"type"` // triage|backlog|unstarted|started|completed|canceled|duplicate
}

// Blocker is an issue standing in front of another: the origin side of a
// `blocks` relation, seen from the blocked issue.
type Blocker struct {
	Identifier string
	State      IssueState
}

// Issue is the slice of issue fields wand's read commands render.
type Issue struct {
	ID          string
	Identifier  string
	Title       string
	Description string
	URL         string
	BranchName  string // the branch Linear names for this issue
	TeamID      string
	Priority    int
	CreatedAt   time.Time
	State       IssueState
	Assignee    string // display name; empty when unassigned
	Labels      []string
	BlockedBy   []Blocker
}

// Comment is one issue comment.
type Comment struct {
	ID        string
	Author    string // the person's name, or the agent's; empty if Linear sent neither
	CreatedAt time.Time
	Body      string
}

// issueFields is the GraphQL selection shared by both issue reads, so the two
// cannot drift apart field by field.
//
// Blockers are read from inverseRelations: a relation `X blocks Y` lives on X
// with Y as relatedIssue, so from Y — the side being vetted — it is an
// *inverse* relation whose `issue` is the blocker. The outgoing `relations`
// connection holds the opposite direction (issues Y blocks), and reading it
// here would vet every ticket against the wrong list. The reference system
// shipped that exact regression; this comment and TestTeamIssuesInTodo are
// where the lesson is pinned.
const issueFields = `
	id identifier title description url branchName priority createdAt
	team { id }
	state { name type }
	assignee { name }
	labels(first: 250) { nodes { name } }
	inverseRelations(first: 250) {
	  nodes { type issue { identifier state { name type } } }
	}`

// issueNode is the wire shape of one issue; flatten turns it into an Issue.
type issueNode struct {
	ID          string     `json:"id"`
	Identifier  string     `json:"identifier"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	URL         string     `json:"url"`
	BranchName  string     `json:"branchName"`
	Priority    int        `json:"priority"`
	CreatedAt   time.Time  `json:"createdAt"`
	State       IssueState `json:"state"`
	Team        *struct {
		ID string `json:"id"`
	} `json:"team"`
	Assignee *struct {
		Name string `json:"name"`
	} `json:"assignee"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	InverseRelations struct {
		Nodes []struct {
			Type  string `json:"type"`
			Issue struct {
				Identifier string     `json:"identifier"`
				State      IssueState `json:"state"`
			} `json:"issue"`
		} `json:"nodes"`
	} `json:"inverseRelations"`
}

func (n issueNode) flatten() Issue {
	issue := Issue{
		ID:          n.ID,
		Identifier:  n.Identifier,
		Title:       n.Title,
		Description: n.Description,
		URL:         n.URL,
		BranchName:  n.BranchName,
		Priority:    n.Priority,
		CreatedAt:   n.CreatedAt,
		State:       n.State,
	}
	if n.Team != nil {
		issue.TeamID = n.Team.ID
	}
	if n.Assignee != nil {
		issue.Assignee = n.Assignee.Name
	}
	for _, l := range n.Labels.Nodes {
		issue.Labels = append(issue.Labels, l.Name)
	}
	for _, r := range n.InverseRelations.Nodes {
		if r.Type != "blocks" {
			continue // related/duplicate links say nothing about readiness
		}
		issue.BlockedBy = append(issue.BlockedBy, Blocker{
			Identifier: r.Issue.Identifier,
			State:      r.Issue.State,
		})
	}
	return issue
}

// pageInfo is Linear's cursor envelope.
type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

// TeamIssuesByState returns every issue on a team sitting in the named
// status, following pagination to the end — a queue cut off at one page
// would silently rank only the issues Linear happened to send first.
// Name matching is case-insensitive, mirroring Linear's own semantics.
func (c *Client) TeamIssuesByState(ctx context.Context, teamKey, stateName string) ([]Issue, error) {
	return c.issuePages(ctx, `
		query($team: String!, $state: String!, $first: Int!, $after: String) {
		  issues(
		    first: $first, after: $after,
		    filter: {team: {key: {eq: $team}}, state: {name: {eqIgnoreCase: $state}}}
		  ) {
		    pageInfo { hasNextPage endCursor }
		    nodes {`+issueFields+`}
		  }
		}`,
		map[string]any{"team": teamKey, "state": stateName})
}

// IssueByIdentifier fetches one issue by its human identifier ("WND-3");
// Linear's issue query accepts identifiers as well as UUIDs.
func (c *Client) IssueByIdentifier(ctx context.Context, identifier string) (Issue, error) {
	var out struct {
		Issue *issueNode `json:"issue"`
	}
	err := c.Do(ctx, `
		query($id: String!) {
		  issue(id: $id) {`+issueFields+`}
		}`,
		map[string]any{"id": identifier}, &out)
	if err != nil {
		return Issue{}, err
	}
	if out.Issue == nil {
		return Issue{}, fmt.Errorf("linear: no issue %q", identifier)
	}
	return out.Issue.flatten(), nil
}

// IssueComments returns an issue's comments, following pagination to the
// end. The reference system read one page of 100 and stopped, which on long
// tickets silently dropped the newest comments — usually the human's answer.
// Order is as Linear returns them; a renderer that needs oldest-first sorts
// by CreatedAt itself rather than trusting connection order.
func (c *Client) IssueComments(ctx context.Context, issueID string) ([]Comment, error) {
	var comments []Comment
	var after *string
	for {
		var out struct {
			Issue *struct {
				Comments struct {
					PageInfo pageInfo `json:"pageInfo"`
					Nodes    []struct {
						ID        string    `json:"id"`
						Body      string    `json:"body"`
						CreatedAt time.Time `json:"createdAt"`
						User      *struct {
							Name string `json:"name"`
						} `json:"user"`
						BotActor *struct {
							Name string `json:"name"`
						} `json:"botActor"`
					} `json:"nodes"`
				} `json:"comments"`
			} `json:"issue"`
		}
		err := c.Do(ctx, `
			query($id: String!, $first: Int!, $after: String) {
			  issue(id: $id) {
			    comments(first: $first, after: $after) {
			      pageInfo { hasNextPage endCursor }
			      nodes {
			        id body createdAt
			        user { name }
			        botActor { name }
			      }
			    }
			  }
			}`,
			map[string]any{"id": issueID, "first": pageSize, "after": after}, &out)
		if err != nil {
			return nil, err
		}
		if out.Issue == nil {
			return nil, fmt.Errorf("linear: no issue %q", issueID)
		}
		for _, n := range out.Issue.Comments.Nodes {
			comment := Comment{ID: n.ID, Body: n.Body, CreatedAt: n.CreatedAt}
			switch {
			case n.User != nil:
				comment.Author = n.User.Name
			case n.BotActor != nil:
				comment.Author = n.BotActor.Name
			}
			comments = append(comments, comment)
		}
		page := out.Issue.Comments.PageInfo
		if !page.HasNextPage || page.EndCursor == "" {
			return comments, nil
		}
		cursor := page.EndCursor
		after = &cursor
	}
}

// TeamIssuesByStateType returns every issue on a team whose status is of the
// given Linear state *type* ("started", "triage", …), following pagination
// to the end.
//
// By type rather than by name because the caller that needs it — home,
// asking which tickets are actually being worked — means "any
// started column", and a covenant may name two of them (In Progress, In
// Review). Asking by name would need one query per column and would go
// stale the moment a covenant adds a third.
func (c *Client) TeamIssuesByStateType(ctx context.Context, teamKey, stateType string) ([]Issue, error) {
	return c.issuePages(ctx, `
		query($team: String!, $type: String!, $first: Int!, $after: String) {
		  issues(
		    first: $first, after: $after,
		    filter: {team: {key: {eq: $team}}, state: {type: {eq: $type}}}
		  ) {
		    pageInfo { hasNextPage endCursor }
		    nodes {`+issueFields+`}
		  }
		}`,
		map[string]any{"team": teamKey, "type": stateType})
}

// TeamIssuesByLabel returns every issue on a team carrying the named label,
// following pagination to the end. Closed issues are included; a caller that
// wants only open ones filters on State.Type, because "which of these counts
// as closed" is the caller's question, not this layer's.
func (c *Client) TeamIssuesByLabel(ctx context.Context, teamKey, label string) ([]Issue, error) {
	return c.issuePages(ctx, `
		query($team: String!, $label: String!, $first: Int!, $after: String) {
		  issues(
		    first: $first, after: $after,
		    filter: {team: {key: {eq: $team}}, labels: {name: {eqIgnoreCase: $label}}}
		  ) {
		    pageInfo { hasNextPage endCursor }
		    nodes {`+issueFields+`}
		  }
		}`,
		map[string]any{"team": teamKey, "label": label})
}

// issuePages runs a paginated `issues` query to exhaustion. The query must
// select `issues { pageInfo { hasNextPage endCursor } nodes { … } }` and take
// $first and $after; vars carries everything else.
//
// Shared so that a read added later cannot quietly stop at one page — the
// bug this loop exists to prevent is invisible in its results, which look
// exactly like a short board.
func (c *Client) issuePages(ctx context.Context, query string, vars map[string]any) ([]Issue, error) {
	var issues []Issue
	var after *string
	for {
		var out struct {
			Issues struct {
				PageInfo pageInfo    `json:"pageInfo"`
				Nodes    []issueNode `json:"nodes"`
			} `json:"issues"`
		}
		args := make(map[string]any, len(vars)+2)
		for k, v := range vars {
			args[k] = v
		}
		args["first"], args["after"] = pageSize, after
		if err := c.Do(ctx, query, args, &out); err != nil {
			return nil, err
		}
		for _, n := range out.Issues.Nodes {
			issues = append(issues, n.flatten())
		}
		if !out.Issues.PageInfo.HasNextPage || out.Issues.PageInfo.EndCursor == "" {
			return issues, nil
		}
		cursor := out.Issues.PageInfo.EndCursor
		after = &cursor
	}
}
