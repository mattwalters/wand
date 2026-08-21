package bootstrap

import (
	"context"
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/linear"
)

// freshTeam is what the live API returned for a just-created team with triage
// enabled, as observed in the bootstrap spike on 2026-08-18: eight default
// statuses, three default PR automations, and no draft rule.
func freshTeam() Current {
	states := []linear.WorkflowState{}
	for i, s := range []struct {
		name, typ string
	}{
		{"Triage", "triage"}, {"Backlog", "backlog"}, {"Todo", "unstarted"},
		{"In Progress", "started"}, {"In Review", "started"},
		{"Done", "completed"}, {"Canceled", "canceled"}, {"Duplicate", "duplicate"},
	} {
		states = append(states, linear.WorkflowState{ID: "st-" + s.name, Name: s.name, Type: s.typ, Position: float64(i)})
	}
	auto := func(id, event, state string) linear.GitAutomation {
		a := linear.GitAutomation{ID: id, Event: event}
		a.State = &struct {
			Name string `json:"name"`
		}{Name: state}
		return a
	}
	return Current{
		States: states,
		Labels: []linear.Label{{ID: "l1", Name: "Bug"}, {ID: "l2", Name: "Feature"}},
		Automations: []linear.GitAutomation{
			auto("ga-start", "start", "In Progress"),
			auto("ga-review", "review", "In Review"),
			auto("ga-merge", "merge", "Done"),
		},
	}
}

// existingSchema1Team is what a team bootstrapped under WND-32's original
// covenant — before Scoped existed, and before WND-79's To Plan/In
// Planning/Plan Review topology replaced Scoping/Scoped entirely — looks
// like today: every status the earliest covenant created, none of the
// research-track additions since. This is the shape both WND-48 (Scoped's
// addition) and WND-79 (this file's own topology change) are about: an
// existing team converging onto a topology addition, not a fresh one being
// created.
func existingSchema1Team() Current {
	states := []linear.WorkflowState{}
	for _, s := range []struct {
		name, typ string
		pos       float64
	}{
		{"Triage", "triage", 0}, {"Backlog", "backlog", 0},
		{"Scoping", "unstarted", -50}, {"Todo", "unstarted", 1},
		{"Needs Input", "unstarted", 50}, {"In Progress", "started", 2},
		{"In Review", "started", 3}, {"Done", "completed", 4},
		{"Canceled", "canceled", 5}, {"Duplicate", "duplicate", 6},
	} {
		states = append(states, linear.WorkflowState{ID: "st-" + s.name, Name: s.name, Type: s.typ, Position: s.pos})
	}
	return Current{
		States: states,
		Labels: []linear.Label{
			{ID: "l1", Name: "human-only"}, {ID: "l2", Name: "agent-filed"},
			{ID: "l3", Name: "ready-for-human"}, {ID: "l4", Name: "re-review"},
			{ID: "l5", Name: "parked"},
		},
		Automations: []linear.GitAutomation{
			{ID: "ga-draft", Event: "draft", State: &struct {
				Name string `json:"name"`
			}{Name: "In Progress"}},
			{ID: "ga-start", Event: "start", State: &struct {
				Name string `json:"name"`
			}{Name: "In Progress"}},
			{ID: "ga-review", Event: "review", State: &struct {
				Name string `json:"name"`
			}{Name: "In Review"}},
			{ID: "ga-merge", Event: "merge", State: &struct {
				Name string `json:"name"`
			}{Name: "Done"}},
		},
	}
}

// The team's old "Scoping" status shares no name with any status the
// current covenant wants, so it is simply ignored — Plan is purely
// additive and never renames or removes a status a team already has (see
// Plan's own doc comment). That is what makes this the same fixture WND-48
// used for the Scoped addition and WND-79 uses for this one: the topology
// changed twice under it and it never had to.
func TestPlanAddsToPlanInPlanningAndPlanReviewToAnExistingTeam(t *testing.T) {
	actions := Plan(covenant.Default(), existingSchema1Team())

	want := []string{
		`create status "To Plan" (unstarted)`,
		`create status "In Planning" (started)`,
		`create status "Plan Review" (unstarted)`,
	}
	if got := ops(actions); got != strings.Join(want, "; ") {
		t.Fatalf("plan for existing schema-1 team:\n got %s\nwant %s", got, strings.Join(want, "; "))
	}
	for _, a := range actions {
		if a.Status.Name == "Plan Review" && a.Status.Position != -25 {
			t.Errorf("Plan Review planned at position %v, want -25 (between In Planning's -40 and Todo's 1)", a.Status.Position)
		}
		if a.Status.Name == "In Planning" && a.Status.Position != -40 {
			t.Errorf("In Planning planned at position %v, want -40 (between To Plan's -50 and Plan Review's -25)", a.Status.Position)
		}
	}
}

func TestPlanIsIdempotentOnceToPlanInPlanningAndPlanReviewExist(t *testing.T) {
	current := existingSchema1Team()
	current.States = append(current.States,
		linear.WorkflowState{ID: "st-ToPlan", Name: "To Plan", Type: "unstarted", Position: -50},
		linear.WorkflowState{ID: "st-InPlanning", Name: "In Planning", Type: "started", Position: -40},
		linear.WorkflowState{ID: "st-PlanReview", Name: "Plan Review", Type: "unstarted", Position: -25})

	if actions := Plan(covenant.Default(), current); len(actions) != 0 {
		t.Errorf("team already carrying To Plan/In Planning/Plan Review should plan nothing, planned: %s", ops(actions))
	}
}

func ops(actions []Action) string {
	var parts []string
	for _, a := range actions {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, "; ")
}

func TestPlanFreshTeam(t *testing.T) {
	actions := Plan(covenant.Default(), freshTeam())

	want := []string{
		`create status "To Plan" (unstarted)`,
		`create status "In Planning" (started)`,
		`create status "Plan Review" (unstarted)`,
		`create status "Needs Input" (unstarted)`,
		`create label "human-only"`,
		`create label "agent-filed"`,
		`create label "ready-for-human"`,
		`create label "re-review"`,
		`create label "parked"`,
		`create PR automation: draft → In Progress`,
	}
	if got := ops(actions); got != strings.Join(want, "; ") {
		t.Errorf("plan for a fresh team:\n got %s\nwant %s", got, strings.Join(want, "; "))
	}
}

func TestPlanIsIdempotentOnceSatisfied(t *testing.T) {
	// A team that already satisfies the covenant plans zero actions.
	current := freshTeam()
	current.States = append(current.States,
		linear.WorkflowState{ID: "st-ToPlan", Name: "To Plan", Type: "unstarted"},
		linear.WorkflowState{ID: "st-InPlanning", Name: "In Planning", Type: "started"},
		linear.WorkflowState{ID: "st-PlanReview", Name: "Plan Review", Type: "unstarted"},
		linear.WorkflowState{ID: "st-NI", Name: "Needs Input", Type: "unstarted"},
	)
	for _, name := range []string{"human-only", "agent-filed", "ready-for-human", "re-review", "parked"} {
		current.Labels = append(current.Labels, linear.Label{ID: "l-" + name, Name: name})
	}
	draft := linear.GitAutomation{ID: "ga-draft", Event: "draft"}
	draft.State = &struct {
		Name string `json:"name"`
	}{Name: "In Progress"}
	current.Automations = append(current.Automations, draft)

	if actions := Plan(covenant.Default(), current); len(actions) != 0 {
		t.Errorf("satisfied team should plan nothing, planned: %s", ops(actions))
	}
}

func TestPlanTreatsWorkspaceLabelsAsSatisfying(t *testing.T) {
	// Label names are unique workspace-wide: a workspace-scoped "human-only"
	// must satisfy the covenant, because creating a team label of the same
	// name is an API error ("duplicate label name" — observed live).
	current := freshTeam()
	current.Labels = append(current.Labels, linear.Label{ID: "ws-1", Name: "human-only"})

	for _, a := range Plan(covenant.Default(), current) {
		if a.Op == CreateLabel && a.Label.Name == "human-only" {
			t.Errorf("planned creating %q, which exists workspace-wide", a.Label.Name)
		}
	}
}

func TestPlanRepointsExistingAutomationInsteadOfCreating(t *testing.T) {
	// Creating over an existing event is an API conflict ("conflict on insert
	// of GitAutomationState" — observed live). It must be an update, carrying
	// the existing rule's id.
	current := freshTeam()
	for i := range current.Automations {
		if current.Automations[i].Event == "merge" {
			current.Automations[i].State.Name = "Canceled" // drifted
		}
	}

	var repoint *Action
	for _, a := range Plan(covenant.Default(), current) {
		if a.Event == "merge" {
			a := a
			repoint = &a
		}
	}
	if repoint == nil {
		t.Fatal("no action planned for the drifted merge automation")
	}
	if repoint.Op != UpdateAutomation {
		t.Errorf("drifted automation planned as %s, want %s", repoint.Op, UpdateAutomation)
	}
	if repoint.AutomationID != "ga-merge" {
		t.Errorf("update carries id %q, want the existing rule's id %q", repoint.AutomationID, "ga-merge")
	}
	if repoint.StatusName != "Done" {
		t.Errorf("update targets %q, want %q", repoint.StatusName, "Done")
	}
}

func TestPlanMatchesNamesCaseInsensitively(t *testing.T) {
	current := freshTeam()
	current.States = append(current.States,
		linear.WorkflowState{ID: "st-x", Name: "needs input", Type: "unstarted"},
		linear.WorkflowState{ID: "st-y", Name: "TO PLAN", Type: "unstarted"},
		linear.WorkflowState{ID: "st-w", Name: "in planning", Type: "started"},
		linear.WorkflowState{ID: "st-z", Name: "plan review", Type: "unstarted"},
	)
	for _, a := range Plan(covenant.Default(), current) {
		if a.Op == CreateStatus {
			t.Errorf("planned %s despite a case-variant existing", a)
		}
	}
}

// fakeClient records Apply's calls and serves canned reads.
type fakeClient struct {
	states  []linear.WorkflowState
	calls   []string
	nextID  int
	created map[string]string // status name -> id
}

func (f *fakeClient) TeamStates(_ context.Context, _ string) ([]linear.WorkflowState, error) {
	f.calls = append(f.calls, "TeamStates")
	return f.states, nil
}

func (f *fakeClient) CreateWorkflowState(_ context.Context, _ string, s linear.WorkflowState, _ string) (linear.WorkflowState, error) {
	f.nextID++
	s.ID = "new-" + s.Name
	f.states = append(f.states, s)
	f.calls = append(f.calls, "CreateWorkflowState:"+s.Name)
	return s, nil
}

func (f *fakeClient) Labels(_ context.Context) ([]linear.Label, error) {
	f.calls = append(f.calls, "Labels")
	return nil, nil
}

func (f *fakeClient) CreateLabel(_ context.Context, _ string, name, _ string) (linear.Label, error) {
	f.calls = append(f.calls, "CreateLabel:"+name)
	return linear.Label{ID: "l-" + name, Name: name}, nil
}

func (f *fakeClient) GitAutomations(_ context.Context, _ string) ([]linear.GitAutomation, error) {
	f.calls = append(f.calls, "GitAutomations")
	return nil, nil
}

func (f *fakeClient) CreateGitAutomation(_ context.Context, _ string, event, stateID string) error {
	f.calls = append(f.calls, "CreateGitAutomation:"+event+"→"+stateID)
	return nil
}

func (f *fakeClient) UpdateGitAutomation(_ context.Context, automationID, stateID string) error {
	f.calls = append(f.calls, "UpdateGitAutomation:"+automationID+"→"+stateID)
	return nil
}

func TestApplyResolvesStatusCreatedBySamePlan(t *testing.T) {
	// The stock plan on a fresh team creates "Needs Input" and later actions
	// may target statuses that only exist because an earlier action in the
	// same plan created them. Apply must resolve those without a re-read
	// failing the run.
	fake := &fakeClient{states: freshTeam().States}
	actions := []Action{
		{Op: CreateStatus, Status: covenant.Status{Name: "To Plan", Type: "unstarted"}},
		{Op: CreateAutomation, Event: "draft", StatusName: "To Plan"},
	}
	if err := Apply(context.Background(), fake, "team-1", actions); err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "CreateGitAutomation:draft→new-To Plan"
	found := false
	for _, c := range fake.calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("calls %v missing %q", fake.calls, want)
	}
}

func TestApplyFailsClearlyOnMissingAutomationTarget(t *testing.T) {
	fake := &fakeClient{states: freshTeam().States}
	err := Apply(context.Background(), fake, "team-1", []Action{
		{Op: CreateAutomation, Event: "draft", StatusName: "No Such Status"},
	})
	if err == nil || !strings.Contains(err.Error(), "No Such Status") {
		t.Errorf("want a clear missing-status error, got %v", err)
	}
}
