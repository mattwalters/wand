// Package bootstrap turns a covenant into a Linear team's actual settings.
//
// The split is planner/executor: Plan is a pure function from (covenant,
// current team state) to a list of actions, and Apply executes actions
// against the API. Everything the spike against the live API taught is
// encoded in Plan, where a test can hold it:
//
//   - a fresh team already carries most covenant statuses (and Triage when
//     enabled), so statuses are ensured by name, never blindly created;
//   - label names are unique workspace-wide, so a label existing anywhere in
//     the workspace satisfies the covenant — creating a shadowing team label
//     is an API error;
//   - a fresh team ships with default PR automations, so an automation whose
//     event already has a rule is updated in place — creating over it is an
//     API conflict error.
package bootstrap

import (
	"context"
	"fmt"
	"strings"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
)

// Op is what an action does.
type Op string

const (
	CreateStatus     Op = "create-status"
	CreateLabel      Op = "create-label"
	CreateAutomation Op = "create-automation"
	UpdateAutomation Op = "update-automation"
)

// Action is one API write the plan calls for.
type Action struct {
	Op Op
	// Status is set for CreateStatus.
	Status covenant.Status
	// Label is set for CreateLabel.
	Label covenant.Label
	// Event and StatusName are set for the automation ops; AutomationID is
	// set for UpdateAutomation and names the existing rule to repoint.
	Event        string
	StatusName   string
	AutomationID string
}

func (a Action) String() string {
	switch a.Op {
	case CreateStatus:
		return fmt.Sprintf("create status %q (%s)", a.Status.Name, a.Status.Type)
	case CreateLabel:
		return fmt.Sprintf("create label %q", a.Label.Name)
	case CreateAutomation:
		return fmt.Sprintf("create PR automation: %s → %s", a.Event, a.StatusName)
	case UpdateAutomation:
		return fmt.Sprintf("repoint PR automation: %s → %s", a.Event, a.StatusName)
	}
	return string(a.Op)
}

// Current is a team's observed state, as read from the API.
type Current struct {
	States      []linear.WorkflowState
	Labels      []linear.Label // workspace-wide, not just the team's
	Automations []linear.GitAutomation
}

// Plan computes the writes that make current satisfy the covenant.
// Matching is by name, case-insensitively, mirroring Linear's own semantics.
func Plan(cov covenant.Covenant, current Current) []Action {
	var actions []Action

	haveState := map[string]bool{}
	for _, s := range current.States {
		haveState[strings.ToLower(s.Name)] = true
	}
	for _, s := range cov.Statuses {
		if !haveState[strings.ToLower(s.Name)] {
			actions = append(actions, Action{Op: CreateStatus, Status: s})
		}
	}

	haveLabel := map[string]bool{}
	for _, l := range current.Labels {
		haveLabel[strings.ToLower(l.Name)] = true
	}
	for _, l := range cov.Labels {
		if !haveLabel[strings.ToLower(l.Name)] {
			actions = append(actions, Action{Op: CreateLabel, Label: l})
		}
	}

	haveAutomation := map[string]linear.GitAutomation{}
	for _, a := range current.Automations {
		haveAutomation[a.Event] = a
	}
	for _, want := range cov.Automations {
		got, exists := haveAutomation[want.Event]
		switch {
		case !exists:
			actions = append(actions, Action{Op: CreateAutomation, Event: want.Event, StatusName: want.Status})
		case got.State == nil || !strings.EqualFold(got.State.Name, want.Status):
			actions = append(actions, Action{Op: UpdateAutomation, Event: want.Event, StatusName: want.Status, AutomationID: got.ID})
		}
	}

	return actions
}

// Client is the slice of the Linear client Apply needs, an interface so tests
// can fake it without a network.
type Client interface {
	TeamStates(ctx context.Context, teamID string) ([]linear.WorkflowState, error)
	CreateWorkflowState(ctx context.Context, teamID string, s linear.WorkflowState, color string) (linear.WorkflowState, error)
	Labels(ctx context.Context) ([]linear.Label, error)
	CreateLabel(ctx context.Context, teamID, name, color string) (linear.Label, error)
	GitAutomations(ctx context.Context, teamID string) ([]linear.GitAutomation, error)
	CreateGitAutomation(ctx context.Context, teamID, event, stateID string) error
	UpdateGitAutomation(ctx context.Context, automationID, stateID string) error
}

// Read fetches the Current state of a team.
func Read(ctx context.Context, cl Client, teamID string) (Current, error) {
	states, err := cl.TeamStates(ctx, teamID)
	if err != nil {
		return Current{}, err
	}
	labels, err := cl.Labels(ctx)
	if err != nil {
		return Current{}, err
	}
	automations, err := cl.GitAutomations(ctx, teamID)
	if err != nil {
		return Current{}, err
	}
	return Current{States: states, Labels: labels, Automations: automations}, nil
}

// Apply executes a plan against a team, in order: statuses first, because a
// later automation action may target a status this same plan creates.
func Apply(ctx context.Context, cl Client, teamID string, actions []Action) error {
	stateID := map[string]string{}
	resolve := func(name string) (string, error) {
		if id, ok := stateID[strings.ToLower(name)]; ok {
			return id, nil
		}
		states, err := cl.TeamStates(ctx, teamID)
		if err != nil {
			return "", err
		}
		for _, s := range states {
			stateID[strings.ToLower(s.Name)] = s.ID
		}
		id, ok := stateID[strings.ToLower(name)]
		if !ok {
			return "", fmt.Errorf("bootstrap: automation targets status %q, which does not exist on the team", name)
		}
		return id, nil
	}

	for _, a := range actions {
		switch a.Op {
		case CreateStatus:
			created, err := cl.CreateWorkflowState(ctx, teamID, linear.WorkflowState{
				Name: a.Status.Name, Type: a.Status.Type, Position: a.Status.Position,
			}, a.Status.Color)
			if err != nil {
				return fmt.Errorf("bootstrap: %s: %w", a, err)
			}
			stateID[strings.ToLower(created.Name)] = created.ID
		case CreateLabel:
			if _, err := cl.CreateLabel(ctx, teamID, a.Label.Name, a.Label.Color); err != nil {
				return fmt.Errorf("bootstrap: %s: %w", a, err)
			}
		case CreateAutomation:
			id, err := resolve(a.StatusName)
			if err != nil {
				return err
			}
			if err := cl.CreateGitAutomation(ctx, teamID, a.Event, id); err != nil {
				return fmt.Errorf("bootstrap: %s: %w", a, err)
			}
		case UpdateAutomation:
			id, err := resolve(a.StatusName)
			if err != nil {
				return err
			}
			if err := cl.UpdateGitAutomation(ctx, a.AutomationID, id); err != nil {
				return fmt.Errorf("bootstrap: %s: %w", a, err)
			}
		}
	}
	return nil
}
