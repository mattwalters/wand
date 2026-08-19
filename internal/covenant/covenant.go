// Package covenant defines the process contract wand maintains in Linear.
//
// The covenant's topology — which states exist and what they mean — is wand's
// opinion, not the user's configuration. What a repo customizes are the
// parameters of the machine (names, caps, commands), never its shape. The
// parameterization arrives through a checked-in covenant file, wand.toml
// (file.go); absent one, the stock covenant applies unchanged.
package covenant

import "time"

// SchemaVersion is the covenant file schema this binary speaks. A repo's
// covenant file declares the schema version it was written against, and
// `wand version` reports this constant — that pairing is how you learn
// whether a given binary can read a given covenant. Topology upgrades ship
// by incrementing it.
const SchemaVersion = 1

// Status is one workflow state the covenant requires on the team.
//
// Key is the status's fixed semantic identity ("todo", "needs_input", …) —
// what the state means in the lifecycle, which no covenant file can change.
// Name is the display name a team sees, the one parameter a file may remap.
type Status struct {
	Key      string
	Name     string
	Type     string // Linear state type: triage|backlog|unstarted|started|completed|canceled|duplicate
	Position float64
	Color    string
}

// Label is one issue label the covenant requires to exist.
// Linear label names are unique across the whole workspace (team- and
// workspace-scoped together), so "exists anywhere" satisfies the requirement.
type Label struct {
	Name  string
	Color string
}

// Automation is one "On PR <event> → set status" rule.
type Automation struct {
	Event  string // draft|start|review|mergeable|merge
	Status string // name of a covenant Status
}

// Caps are the hard limits on the run loop. A cap running out is never
// silent convergence: exhaustion is a hand-back that says so.
type Caps struct {
	ReviewRounds  int
	CIAttempts    int
	WorkerTimeout time.Duration
}

// Toggles switch the lifecycle's optional stages.
type Toggles struct {
	ScopeInterview bool
}

// Commands are the three pluggable commands the lifecycle shells out to.
// Empty means not configured; the verbs that need one demand it then.
type Commands struct {
	Verify    string // proves a change works, e.g. "make check"
	Provision string // prepares a worker's workspace
	RunAgent  string // spawns a worker on a prompt
}

// Covenant is the process contract: the desired state of a Linear team plus
// the parameters of the lifecycle wand runs through it.
type Covenant struct {
	TriageEnabled       bool
	IssueEstimationType string
	Statuses            []Status
	Labels              []Label
	Automations         []Automation
	Caps                Caps
	Toggles             Toggles
	Commands            Commands
	Templates           map[string]string
}

// StatusName returns the display name of the status with the given semantic
// key ("todo", "needs_input", …), or "" when the covenant has no such key.
// This is how code refers to a status: by what it means, letting the
// covenant file own what it is called.
func (c Covenant) StatusName(key string) string {
	for _, s := range c.Statuses {
		if s.Key == key {
			return s.Name
		}
	}
	return ""
}

// Default is the stock covenant: the lifecycle proven in Prosewell.
//
// Triage is the inbox agents file into; Backlog is the undifferentiated pool;
// Scoping blesses research; Todo blesses building; Needs Input parks a
// question on a human. The automations mirror Prosewell's observed
// configuration: open and draft target In Progress (a claimed ticket is
// already there, so the write changes nothing), review targets In Review,
// and merge closes the ticket.
func Default() Covenant {
	return Covenant{
		TriageEnabled:       true,
		IssueEstimationType: "fibonacci",
		Statuses: []Status{
			// A fresh Linear team already has Triage, Backlog, Todo,
			// In Progress, In Review, Done, Canceled and Duplicate; they are
			// listed so the planner can verify rather than assume.
			{Key: "triage", Name: "Triage", Type: "triage", Position: 0, Color: "#8A6FDF"},
			{Key: "backlog", Name: "Backlog", Type: "backlog", Position: 0, Color: "#8A6FDF"},
			{Key: "scoping", Name: "Scoping", Type: "unstarted", Position: -50, Color: "#8A6FDF"},
			{Key: "todo", Name: "Todo", Type: "unstarted", Position: 1, Color: "#8A6FDF"},
			{Key: "needs_input", Name: "Needs Input", Type: "unstarted", Position: 50, Color: "#F2994A"},
			{Key: "in_progress", Name: "In Progress", Type: "started", Position: 2, Color: "#8A6FDF"},
			{Key: "in_review", Name: "In Review", Type: "started", Position: 3, Color: "#8A6FDF"},
			{Key: "done", Name: "Done", Type: "completed", Position: 4, Color: "#8A6FDF"},
			{Key: "canceled", Name: "Canceled", Type: "canceled", Position: 5, Color: "#8A6FDF"},
			{Key: "duplicate", Name: "Duplicate", Type: "duplicate", Position: 6, Color: "#8A6FDF"},
		},
		Labels: []Label{
			{Name: "human-only", Color: "#F2994A"},
			{Name: "agent-filed", Color: "#5E6AD2"},
			{Name: "ready-for-human", Color: "#4CB782"},
		},
		Automations: []Automation{
			{Event: "draft", Status: "In Progress"},
			{Event: "start", Status: "In Progress"},
			{Event: "review", Status: "In Review"},
			{Event: "merge", Status: "Done"},
		},
		Caps: Caps{
			ReviewRounds:  3,
			CIAttempts:    3,
			WorkerTimeout: 30 * time.Minute,
		},
		Toggles: Toggles{
			ScopeInterview: true,
		},
	}
}
