package linear

import (
	"context"
	"fmt"
)

// Project is the slice of Linear project fields wand's pm verb needs: enough
// to name it, describe it, and reuse it as a ProposedTicket's ProjectRef
// target.
type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Projects returns every project visible to a team — the board-read stage's
// source for "does a project by this name already exist", so `wand pm`
// proposes a new project only when nothing on the board already covers it.
// An unknown team key returns an empty slice rather than an error, matching
// TeamByKey's own "not found" shape.
func (c *Client) Projects(ctx context.Context, teamKey string) ([]Project, error) {
	var out struct {
		Teams struct {
			Nodes []struct {
				Projects struct {
					Nodes []Project `json:"nodes"`
				} `json:"projects"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	err := c.Do(ctx, `
		query($team: String!) {
		  teams(filter: {key: {eq: $team}}) {
		    nodes { projects(first: 250) { nodes { id name description } } }
		  }
		}`,
		map[string]any{"team": teamKey}, &out)
	if err != nil {
		return nil, err
	}
	if len(out.Teams.Nodes) == 0 {
		return nil, nil
	}
	return out.Teams.Nodes[0].Projects.Nodes, nil
}

// CreateProject creates a project on one team and returns it as created.
func (c *Client) CreateProject(ctx context.Context, teamID, name, description string) (Project, error) {
	var out struct {
		ProjectCreate struct {
			Success bool     `json:"success"`
			Project *Project `json:"project"`
		} `json:"projectCreate"`
	}
	err := c.Do(ctx, `
		mutation($input: ProjectCreateInput!) {
		  projectCreate(input: $input) { success project { id name description } }
		}`,
		map[string]any{"input": map[string]any{
			"name":        name,
			"description": description,
			"teamIds":     []string{teamID},
		}}, &out)
	if err != nil {
		return Project{}, err
	}
	if !out.ProjectCreate.Success || out.ProjectCreate.Project == nil {
		return Project{}, fmt.Errorf("linear: refused the project create")
	}
	return *out.ProjectCreate.Project, nil
}
