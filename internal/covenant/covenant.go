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
//
// What the number does and does not mean is worth stating exactly, because
// the two halves pull in opposite directions:
//
//   - It gates reading. A file declaring a schema newer than this constant
//     is refused, because wand cannot know what its keys mean. An older one
//     is read unchanged: schema bumps only add optional keys, so a file
//     that sets none of them says the same thing under either number.
//   - It does not gate topology. The state graph is wand's opinion, shipped
//     centrally, so every file gets the current states whatever number it
//     declares. That is the point: it is what makes a team bootstrapped
//     under an older topology report as drift under `wand doctor` and get
//     repaired by `wand init`.
//
// So the number in a file describes the file, not a pin on the lifecycle.
// It stays 1 until wand is distributed: an increment is a promise to other
// people's covenant files, and there are no other people yet. Scoped landed
// as plain schema-1 topology for exactly that reason.
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
	// Lanes is how many `wand run` loops this repo runs at once. `wand
	// dispatch` reads it to decide whether a Todo winner may start; a
	// Scoping winner never needs one (see internal/dispatch), so research
	// is never starved by full lane occupancy.
	Lanes int
	// WorkerRetries is how many extra times one phase may respawn its
	// worker after a failure the harness itself reported as
	// infrastructure — see worker.TransienceAdapter. It counts retries,
	// not attempts: every phase gets one spawn regardless, and zero is the
	// coherent, safe answer meaning "never retry", which is what wand did
	// before this existed. Nothing else is ever retried; a failure that
	// might be about the work still parks on the first attempt.
	WorkerRetries int
}

// Toggles switch the lifecycle's optional stages.
type Toggles struct {
	// ScopeInterview allows `wand scope --interactive` to grill a human
	// over its draft before writing anything.
	ScopeInterview bool
	// ScopeCritic runs a cold critic over the draft scope before the
	// interview. Off by default: it costs a whole extra model call per
	// scope, and unlike the rest of this covenant it is not a rule the
	// reference system has finished paying for.
	ScopeCritic bool
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
// Scoping blesses research; Scoped is the plan that research produced, done
// and awaiting judgment; Todo blesses building; Needs Input parks a question
// on a human. Scoped is to Scoping what In Review is to In Progress — the
// finished-work end of a phase, held apart from the question-parking state
// because "read this plan and decide" and "answer this question" are
// different jobs for the human doing them.
//
// The automations mirror Prosewell's observed
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
			// Scoped sits midway between Scoping (-50) and Todo (1) rather
			// than adjacent to either, so a later state on the research side
			// has somewhere to go without renumbering the ones around it.
			// Green is the ready-for-human green: Scoped is a finished plan
			// waiting on a person, the same signal the label carries, and
			// deliberately not Needs Input's orange — orange is an agent
			// stuck on you, green is work ready for you.
			{Key: "scoped", Name: "Scoped", Type: "unstarted", Position: -25, Color: "#4CB782"},
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
			{Name: "re-review", Color: "#EB5757"},
			{Name: "parked", Color: "#BEC2C8"},
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
			Lanes:         1,
			WorkerRetries: 1,
		},
		Toggles: Toggles{
			ScopeInterview: true,
			ScopeCritic:    false,
		},
	}
}

// estimateScales are the values Linear's issueEstimationType accepts, mapped
// to the points each offers — zero excluded, because Linear's zero is "no
// estimate" rather than a size, and a scope whose estimate is "none" has not
// estimated. "notUsed" is a known scale with no points: a team that does not
// estimate is configured, not broken.
//
// These are Linear's base scales. Its extended-estimates setting adds larger
// values to each, and the covenant does not carry that flag yet — a team
// running extended estimates will see `wand scope` refuse the extra values
// until it does.
var estimateScales = map[string][]int{
	"notUsed":     nil,
	"exponential": {1, 2, 4, 8, 16},
	"fibonacci":   {1, 2, 3, 5, 8},
	"linear":      {1, 2, 3, 4, 5},
	"tShirt":      {1, 2, 3, 4, 5},
}

// EstimateValues returns the point values this covenant's estimate scale
// offers, in order, or nil when the team does not estimate. Code that writes
// an estimate validates against this rather than trusting the number it was
// handed: Linear silently adjusts an off-scale estimate to fit, so an
// unchecked write lands a number nobody chose.
func (c Covenant) EstimateValues() []int {
	return estimateScales[c.IssueEstimationType]
}
