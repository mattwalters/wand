package linear

import (
	"context"
	"fmt"
)

// User is the slice of user fields wand needs: enough to assign an issue and
// say who it went to.
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Viewer returns the user the API key belongs to — the single writer. The
// lifecycle verbs assign claimed issues to this user, because the key holder
// is the one accountable for the session's writes.
func (c *Client) Viewer(ctx context.Context) (User, error) {
	var out struct {
		Viewer User `json:"viewer"`
	}
	err := c.Do(ctx, `query { viewer { id name } }`, nil, &out)
	if err != nil {
		return User{}, err
	}
	if out.Viewer.ID == "" {
		return User{}, fmt.Errorf("linear: the API returned no viewer for this key")
	}
	return out.Viewer, nil
}

// CreateComment posts one comment on an issue.
func (c *Client) CreateComment(ctx context.Context, issueID, body string) error {
	var out struct {
		CommentCreate struct {
			Success bool `json:"success"`
		} `json:"commentCreate"`
	}
	err := c.Do(ctx, `
		mutation($input: CommentCreateInput!) {
		  commentCreate(input: $input) { success }
		}`,
		map[string]any{"input": map[string]any{"issueId": issueID, "body": body}}, &out)
	if err != nil {
		return err
	}
	if !out.CommentCreate.Success {
		return fmt.Errorf("linear: refused the comment")
	}
	return nil
}

// IssueUpdate is the slice of issueUpdate fields the lifecycle verbs set.
// Zero values mean "leave unchanged" — the mutation input carries only what
// was asked for, so an update never resets a field as a side effect.
type IssueUpdate struct {
	StateID     string  // workflow state UUID; "" leaves the status alone
	AssigneeID  string  // user UUID; "" leaves the assignee alone
	Unassign    bool    // true sends an explicit null: omitting the field would leave the assignee in place
	Description *string // nil leaves the description alone
	// Estimate is the issue's size in points. A pointer because zero is a
	// value Linear accepts ("no estimate"), so it cannot double as "leave
	// this alone". The caller checks the number against the covenant's
	// scale first — Linear adjusts an off-scale estimate to fit rather
	// than refusing it, and a silently adjusted number is one nobody chose.
	Estimate *int
}

// UpdateIssue applies one IssueUpdate in a single mutation. Fields that must
// move together — abandon's description correction, status move and
// unassignment — go through one call here, so a failure cannot land half of
// them.
func (c *Client) UpdateIssue(ctx context.Context, issueID string, u IssueUpdate) error {
	if u.AssigneeID != "" && u.Unassign {
		return fmt.Errorf("linear: an update that both assigns and unassigns; the caller must pick one")
	}
	input := map[string]any{}
	if u.StateID != "" {
		input["stateId"] = u.StateID
	}
	if u.AssigneeID != "" {
		input["assigneeId"] = u.AssigneeID
	}
	if u.Unassign {
		input["assigneeId"] = nil
	}
	if u.Description != nil {
		input["description"] = *u.Description
	}
	if u.Estimate != nil {
		input["estimate"] = *u.Estimate
	}
	if len(input) == 0 {
		return fmt.Errorf("linear: an issue update with nothing to change")
	}

	var out struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	err := c.Do(ctx, `
		mutation($id: String!, $input: IssueUpdateInput!) {
		  issueUpdate(id: $id, input: $input) { success }
		}`,
		map[string]any{"id": issueID, "input": input}, &out)
	if err != nil {
		return err
	}
	if !out.IssueUpdate.Success {
		return fmt.Errorf("linear: refused the issue update")
	}
	return nil
}

// IssueCreate is the slice of issueCreate fields `wand file` sets. There is
// deliberately no priority and no assignee: an agent-filed issue enters
// Triage unowned and unranked, because ranking work is part of blessing it.
type IssueCreate struct {
	TeamID      string
	Title       string
	Description string
	StateID     string
	LabelIDs    []string
}

// CreateIssue files one issue and returns it as created.
func (c *Client) CreateIssue(ctx context.Context, in IssueCreate) (Issue, error) {
	input := map[string]any{
		"teamId":  in.TeamID,
		"title":   in.Title,
		"stateId": in.StateID,
	}
	if in.Description != "" {
		input["description"] = in.Description
	}
	if len(in.LabelIDs) > 0 {
		input["labelIds"] = in.LabelIDs
	}

	var out struct {
		IssueCreate struct {
			Success bool       `json:"success"`
			Issue   *issueNode `json:"issue"`
		} `json:"issueCreate"`
	}
	err := c.Do(ctx, `
		mutation($input: IssueCreateInput!) {
		  issueCreate(input: $input) { success issue {`+issueFields+`} }
		}`,
		map[string]any{"input": input}, &out)
	if err != nil {
		return Issue{}, err
	}
	if !out.IssueCreate.Success || out.IssueCreate.Issue == nil {
		return Issue{}, fmt.Errorf("linear: refused the issue create")
	}
	return out.IssueCreate.Issue.flatten(), nil
}

// searchFields is the reduced selection for search hits: enough to recognize
// a duplicate — identity, title, where it sits — and nothing that invites
// rendering a search result as if it were a read ticket.
const searchFields = `
	id identifier title url priority createdAt
	state { name type }`

// SearchIssues returns the issues matching a free-text term, scoped to one
// team, best matches first. One page only, deliberately: this feeds the
// near-duplicate check in `wand file`, which needs the top hits, not the
// long tail — a duplicate that is not in Linear's first page of relevance
// was never going to be recognized as one.
func (c *Client) SearchIssues(ctx context.Context, teamKey, term string) ([]Issue, error) {
	var out struct {
		SearchIssues struct {
			Nodes []issueNode `json:"nodes"`
		} `json:"searchIssues"`
	}
	err := c.Do(ctx, `
		query($term: String!, $team: String!, $first: Int!) {
		  searchIssues(term: $term, first: $first, filter: {team: {key: {eq: $team}}}) {
		    nodes {`+searchFields+`}
		  }
		}`,
		map[string]any{"term": term, "team": teamKey, "first": 10}, &out)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, n := range out.SearchIssues.Nodes {
		issues = append(issues, n.flatten())
	}
	return issues, nil
}
