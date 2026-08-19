// Package covenant defines the process contract wand maintains in Linear.
//
// The covenant's topology — which states exist and what they mean — is wand's
// opinion, not the user's configuration. What a repo customizes are the
// parameters of the machine (names, caps, commands), never its shape. Today
// only the stock covenant exists; a covenant file will layer parameterization
// over these types later.
package covenant

// SchemaVersion is the covenant file schema this binary speaks. A repo's
// covenant file declares the schema version it was written against, and
// `wand version` reports this constant — that pairing is how you learn
// whether a given binary can read a given covenant. Topology upgrades ship
// by incrementing it.
const SchemaVersion = 1

// Status is one workflow state the covenant requires on the team.
type Status struct {
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

// Covenant is the desired state of a Linear team.
type Covenant struct {
	TriageEnabled       bool
	IssueEstimationType string
	Statuses            []Status
	Labels              []Label
	Automations         []Automation
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
			{Name: "Triage", Type: "triage", Position: 0, Color: "#8A6FDF"},
			{Name: "Backlog", Type: "backlog", Position: 0, Color: "#8A6FDF"},
			{Name: "Scoping", Type: "unstarted", Position: -50, Color: "#8A6FDF"},
			{Name: "Todo", Type: "unstarted", Position: 1, Color: "#8A6FDF"},
			{Name: "Needs Input", Type: "unstarted", Position: 50, Color: "#F2994A"},
			{Name: "In Progress", Type: "started", Position: 2, Color: "#8A6FDF"},
			{Name: "In Review", Type: "started", Position: 3, Color: "#8A6FDF"},
			{Name: "Done", Type: "completed", Position: 4, Color: "#8A6FDF"},
			{Name: "Canceled", Type: "canceled", Position: 5, Color: "#8A6FDF"},
			{Name: "Duplicate", Type: "duplicate", Position: 6, Color: "#8A6FDF"},
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
	}
}
