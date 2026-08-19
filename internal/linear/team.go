package linear

import "context"

// WorkflowState is one status column on a team's board.
type WorkflowState struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"` // triage|backlog|unstarted|started|completed|canceled|duplicate
	Position float64 `json:"position"`
}

// Label is an issue label, team-scoped or workspace-scoped.
type Label struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GitAutomation is one "On PR <event> → set status" rule.
// Event is one of: draft, start, review, mergeable, merge.
type GitAutomation struct {
	ID    string `json:"id"`
	Event string `json:"event"`
	State *struct {
		Name string `json:"name"`
	} `json:"state"`
}

// Team is the slice of team fields wand cares about.
type Team struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Key                 string `json:"key"`
	TriageEnabled       bool   `json:"triageEnabled"`
	IssueEstimationType string `json:"issueEstimationType"`
}

// TeamSettings are the team-level fields wand sets at creation.
type TeamSettings struct {
	TriageEnabled       bool
	IssueEstimationType string // e.g. "fibonacci"
}

// CreateTeam creates a team and returns it.
func (c *Client) CreateTeam(ctx context.Context, name, key string, s TeamSettings) (Team, error) {
	var out struct {
		TeamCreate struct {
			Team Team `json:"team"`
		} `json:"teamCreate"`
	}
	err := c.Do(ctx, `
		mutation($input: TeamCreateInput!) {
		  teamCreate(input: $input) { team { id name key triageEnabled issueEstimationType } }
		}`,
		map[string]any{"input": map[string]any{
			"name":                name,
			"key":                 key,
			"triageEnabled":       s.TriageEnabled,
			"issueEstimationType": s.IssueEstimationType,
		}}, &out)
	return out.TeamCreate.Team, err
}

// TeamByKey returns the team with the given key, or a zero Team if none exists.
func (c *Client) TeamByKey(ctx context.Context, key string) (Team, error) {
	var out struct {
		Teams struct {
			Nodes []Team `json:"nodes"`
		} `json:"teams"`
	}
	err := c.Do(ctx, `
		query($key: String!) {
		  teams(filter: {key: {eq: $key}}) { nodes { id name key triageEnabled issueEstimationType } }
		}`,
		map[string]any{"key": key}, &out)
	if err != nil || len(out.Teams.Nodes) == 0 {
		return Team{}, err
	}
	return out.Teams.Nodes[0], nil
}

// DeleteTeam deletes a team. Destructive; wand only ever calls this on a
// scratch team it created itself in the same run.
func (c *Client) DeleteTeam(ctx context.Context, teamID string) error {
	return c.Do(ctx, `
		mutation($id: String!) { teamDelete(id: $id) { success } }`,
		map[string]any{"id": teamID}, nil)
}

// TeamStates returns a team's workflow states.
func (c *Client) TeamStates(ctx context.Context, teamID string) ([]WorkflowState, error) {
	var out struct {
		Team struct {
			States struct {
				Nodes []WorkflowState `json:"nodes"`
			} `json:"states"`
		} `json:"team"`
	}
	err := c.Do(ctx, `
		query($id: String!) {
		  team(id: $id) { states { nodes { id name type position } } }
		}`,
		map[string]any{"id": teamID}, &out)
	return out.Team.States.Nodes, err
}

// CreateWorkflowState adds a status to a team's board.
func (c *Client) CreateWorkflowState(ctx context.Context, teamID string, s WorkflowState, color string) (WorkflowState, error) {
	var out struct {
		WorkflowStateCreate struct {
			WorkflowState WorkflowState `json:"workflowState"`
		} `json:"workflowStateCreate"`
	}
	err := c.Do(ctx, `
		mutation($input: WorkflowStateCreateInput!) {
		  workflowStateCreate(input: $input) { workflowState { id name type position } }
		}`,
		map[string]any{"input": map[string]any{
			"teamId":   teamID,
			"name":     s.Name,
			"type":     s.Type,
			"position": s.Position,
			"color":    color,
		}}, &out)
	return out.WorkflowStateCreate.WorkflowState, err
}

// Labels returns every label visible in the workspace — team-scoped and
// workspace-scoped both. Label names are unique across that whole set, so
// ensure-style callers must check here, not just on the team.
func (c *Client) Labels(ctx context.Context) ([]Label, error) {
	var out struct {
		IssueLabels struct {
			Nodes []Label `json:"nodes"`
		} `json:"issueLabels"`
	}
	err := c.Do(ctx, `
		query { issueLabels(first: 250) { nodes { id name } } }`, nil, &out)
	return out.IssueLabels.Nodes, err
}

// CreateLabel creates a team-scoped label.
func (c *Client) CreateLabel(ctx context.Context, teamID, name, color string) (Label, error) {
	var out struct {
		IssueLabelCreate struct {
			IssueLabel Label `json:"issueLabel"`
		} `json:"issueLabelCreate"`
	}
	err := c.Do(ctx, `
		mutation($input: IssueLabelCreateInput!) {
		  issueLabelCreate(input: $input) { issueLabel { id name } }
		}`,
		map[string]any{"input": map[string]any{"teamId": teamID, "name": name, "color": color}}, &out)
	return out.IssueLabelCreate.IssueLabel, err
}

// GitAutomations returns a team's PR automation rules.
func (c *Client) GitAutomations(ctx context.Context, teamID string) ([]GitAutomation, error) {
	var out struct {
		Team struct {
			GitAutomationStates struct {
				Nodes []GitAutomation `json:"nodes"`
			} `json:"gitAutomationStates"`
		} `json:"team"`
	}
	err := c.Do(ctx, `
		query($id: String!) {
		  team(id: $id) { gitAutomationStates { nodes { id event state { name } } } }
		}`,
		map[string]any{"id": teamID}, &out)
	return out.Team.GitAutomationStates.Nodes, err
}

// CreateGitAutomation adds a PR automation rule for an event with none.
func (c *Client) CreateGitAutomation(ctx context.Context, teamID, event, stateID string) error {
	return c.Do(ctx, `
		mutation($input: GitAutomationStateCreateInput!) {
		  gitAutomationStateCreate(input: $input) { gitAutomationState { id } }
		}`,
		map[string]any{"input": map[string]any{"teamId": teamID, "event": event, "stateId": stateID}}, nil)
}

// UpdateGitAutomation repoints an existing PR automation rule. A fresh team
// ships with default rules already installed, so ensure means update, not
// create — creating over an existing event is an API conflict error.
func (c *Client) UpdateGitAutomation(ctx context.Context, automationID, stateID string) error {
	return c.Do(ctx, `
		mutation($id: String!, $input: GitAutomationStateUpdateInput!) {
		  gitAutomationStateUpdate(id: $id, input: $input) { gitAutomationState { id } }
		}`,
		map[string]any{"id": automationID, "input": map[string]any{"stateId": stateID}}, nil)
}
