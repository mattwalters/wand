// Package pm is the stage-zero orchestrator: a decided product brief in,
// a hard-validated set of proposed tickets out.
//
// It is the sibling of internal/plan one stage earlier in the lifecycle —
// idea -> pm -> (bless) -> plan -> (bless) -> run — and it borrows plan's
// central discipline whole: an invalid handoff writes nothing, and every
// deliverable a human blesses is checked rather than hoped for.
//
// pm splits into two commands rather than one, and that split is the whole
// design:
//
//   - **propose** (Execute) spawns one cold scout over a brief and this
//     team's board, validates what it hands back, and writes the validated
//     proposal to a file. It makes no Linear writes at all — not even a
//     read failure half-way through leaves a partial write, because there
//     is no write to be partial.
//   - **bless** (Bless) re-parses and re-validates that file — possibly
//     hand-edited by the human who reviewed it — re-checks it against
//     whatever the board looks like now, and performs the batched write as
//     the single writer.
//
// Neither command promotes anything. Every ticket pm creates lands in
// Triage, the same place `wand file` lands an agent's own finding: blessing
// a proposal onward to Todo stays a human's act, exactly as promoting a
// plan run's Plan Review to Todo is.
package pm

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// SchemaVersion is the schema a proposal *file* on disk declares itself
// against — mirrors covenant.SchemaVersion for the same reason: a proposal
// is written by one wand binary and may be read back by another, later, once
// a human has edited it. `wand pm bless` refuses a file declaring a newer
// schema than it speaks; an older one is read unchanged, because a schema
// bump only ever adds optional keys.
//
// The scout's own handoff carries no such field — a fresh scout always
// talks to the schema the running binary speaks, so there is nothing to
// declare. Execute stamps it onto the Draft once validation of the scout's
// handoff has passed, which is what makes it present in every file bless
// reads.
const SchemaVersion = 1

// PremiseSound and PremiseWrong are the two verdicts a draft may carry, the
// same vocabulary plan.Draft uses for the same reason: a brief built on
// something untrue is reported, not proposed around.
const (
	PremiseSound = "sound"
	PremiseWrong = "wrong"
)

// Draft is the scout's report, and — once Execute has validated it and
// stamped a SchemaVersion onto it — the schema of a proposal file on disk.
// ParseProposal is what a bless run reads back; ParseDraft is what a
// propose run validates a scout's handoff against, board-agnostic in both
// directions, so a test can hold every validation rule without a Linear
// client in reach.
type Draft struct {
	// SchemaVersion is unset (0) on a scout's handoff and always present on
	// a written proposal file; see the constant above.
	SchemaVersion int `json:"schema_version,omitempty"`

	// Premise is "sound" or "wrong". A scout that finds the brief asks for
	// something already done, already tracked, or built on a mistake says
	// so instead of proposing tickets around it.
	Premise string `json:"premise"`
	// Reason is the scout's own account of why the premise is wrong,
	// required when it is, quoted verbatim to the human.
	Reason string `json:"reason,omitempty"`

	// Projects are new Linear projects the scout proposes creating. A
	// ProposedTicket may reference one of these by name, or an existing
	// project already on the board — ParseDraft cannot tell the two apart,
	// because it has no board in reach; that resolution happens at write
	// time, in Bless.
	Projects []ProposedProject `json:"projects,omitempty"`

	// Tickets is the proposed set, required and non-empty for a sound
	// premise.
	Tickets []ProposedTicket `json:"tickets,omitempty"`
}

// ProposedProject is one new Linear project the draft asks the bless step
// to create.
type ProposedProject struct {
	Name string `json:"name"`
	// Description argues for the project the way an Approach's Tradeoff
	// argues for itself: a project proposed with nothing said about it is a
	// grouping presented as self-evident, and none is.
	Description string `json:"description"`
}

// ProposedTicket is one ticket the draft asks the bless step to file.
type ProposedTicket struct {
	Title string `json:"title"`
	// Body is the goal and open questions, in Markdown — written into the
	// issue's description inside the same kind of marker-fenced region
	// plan.PlanSectionID owns, so a later pm proposal for the same ticket
	// replaces only what pm wrote.
	Body string `json:"body"`
	// Project names an existing project on the board or one of this
	// draft's own Projects, exactly. Empty leaves the ticket unassigned to
	// any project.
	Project string `json:"project,omitempty"`
	// BlockedBy names other tickets in this same proposed set, by title,
	// exactly. Every reference must resolve inside the set — pm proposes a
	// closed graph, never a relation to a ticket that already exists on the
	// board, because a title match against live Linear data would make
	// ParseDraft need a board it does not have.
	BlockedBy []string `json:"blocked_by,omitempty"`
}

// ParseDraft validates a scout's handoff: the structural and cross-reference
// rules that need no board at all. An error means nothing is written: the
// caller has no proposal, and half a proposed set is worse than none,
// because it reads like a whole one.
func ParseDraft(raw json.RawMessage) (Draft, error) {
	if len(raw) == 0 {
		return Draft{}, errors.New("the worker wrote no handoff")
	}
	var d Draft
	if err := strictUnmarshal(raw, &d); err != nil {
		return Draft{}, err
	}
	if err := d.validate(); err != nil {
		return Draft{}, err
	}
	return d, nil
}

// ParseProposal validates a proposal file read back off disk: ParseDraft's
// rules, plus the schema gate a round-tripped file needs and a scout's raw
// handoff does not. Bless calls this rather than ParseDraft, because a file
// on disk may have been written by an older or newer wand than the one
// reading it now.
func ParseProposal(raw []byte) (Draft, error) {
	d, err := ParseDraft(raw)
	if err != nil {
		return Draft{}, err
	}
	if d.SchemaVersion > SchemaVersion {
		return Draft{}, fmt.Errorf(
			"this proposal declares schema %d, but this wand speaks schema %d; it was written by a newer wand — rebuild before blessing it",
			d.SchemaVersion, SchemaVersion)
	}
	if d.SchemaVersion == 0 {
		return Draft{}, errors.New("this file carries no schema_version; it does not look like a proposal wand pm wrote")
	}
	return d, nil
}

func (d Draft) validate() error {
	switch d.Premise {
	case PremiseWrong:
		if strings.TrimSpace(d.Reason) == "" {
			return errors.New(`the handoff says the brief's premise is wrong but gives no reason — that verdict is quoted to a human verbatim, and there is nothing to quote`)
		}
		return nil
	case PremiseSound:
	default:
		return fmt.Errorf("the handoff's premise is %q, not %q or %q", d.Premise, PremiseSound, PremiseWrong)
	}

	if len(d.Tickets) == 0 {
		return errors.New("the handoff proposes no tickets; a sound premise with nothing to file is not a proposal")
	}

	// Project references are resolved against the live board at write time,
	// in Bless — ParseDraft has no board in reach, so it validates only that
	// the proposed projects themselves are well-formed.
	if _, err := validateProjects(d.Projects); err != nil {
		return err
	}
	ticketTitles, err := validateTicketTitlesAndBodies(d.Tickets)
	if err != nil {
		return err
	}
	if err := validateBlockedBy(d.Tickets, ticketTitles); err != nil {
		return err
	}
	return detectCycle(d.Tickets)
}

func validateProjects(projects []ProposedProject) (map[string]bool, error) {
	names := map[string]bool{}
	for i, p := range projects {
		name := strings.TrimSpace(p.Name)
		switch {
		case name == "":
			return nil, fmt.Errorf("proposed project %d has no name", i+1)
		case strings.TrimSpace(p.Description) == "":
			return nil, fmt.Errorf("proposed project %q says nothing about what it is for", name)
		case names[strings.ToLower(name)]:
			return nil, fmt.Errorf("two proposed projects are both named %q", name)
		}
		names[strings.ToLower(name)] = true
	}
	return names, nil
}

func validateTicketTitlesAndBodies(tickets []ProposedTicket) (map[string]bool, error) {
	titles := map[string]bool{}
	for i, t := range tickets {
		title := strings.TrimSpace(t.Title)
		switch {
		case title == "":
			return nil, fmt.Errorf("proposed ticket %d has no title", i+1)
		case strings.TrimSpace(t.Body) == "":
			return nil, fmt.Errorf("proposed ticket %q has no body; the goal and open questions are the whole point of proposing it", title)
		case titles[strings.ToLower(title)]:
			return nil, fmt.Errorf("two proposed tickets are both titled %q; a human blessing the set could not tell them apart, and neither could blocked_by", title)
		}
		titles[strings.ToLower(title)] = true
	}
	return titles, nil
}

// validateBlockedBy rejects a dangling or self reference: every name in
// BlockedBy must be another ticket's title in this same set, and never the
// ticket's own.
func validateBlockedBy(tickets []ProposedTicket, titles map[string]bool) error {
	for _, t := range tickets {
		title := strings.TrimSpace(t.Title)
		for _, dep := range t.BlockedBy {
			depTitle := strings.TrimSpace(dep)
			switch {
			case depTitle == "":
				return fmt.Errorf("proposed ticket %q has an empty blocked_by entry", title)
			case strings.EqualFold(depTitle, title):
				return fmt.Errorf("proposed ticket %q lists itself in blocked_by; a ticket cannot block itself", title)
			case !titles[strings.ToLower(depTitle)]:
				return fmt.Errorf("proposed ticket %q is blocked_by %q, which is not one of the proposed titles — pm proposes relations only inside its own set, never to a ticket already on the board", title, depTitle)
			}
		}
	}
	return nil
}

// detectCycle refuses a proposed set whose blocked_by graph has a cycle.
// Bless creates tickets in dependency order and CreateBlocker needs both
// ends to exist; a cycle has no valid order, and letting one through would
// only surface as a write-time failure a human cannot fix by editing the
// file that produced it — clearer to refuse before anything is written.
func detectCycle(tickets []ProposedTicket) error {
	byTitle := map[string]ProposedTicket{}
	for _, t := range tickets {
		byTitle[strings.ToLower(strings.TrimSpace(t.Title))] = t
	}
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := map[string]int{}
	var path []string
	var visit func(key string) error
	visit = func(key string) error {
		switch state[key] {
		case done:
			return nil
		case visiting:
			path = append(path, byTitle[key].Title)
			return fmt.Errorf("blocked_by has a cycle: %s", strings.Join(path, " -> "))
		}
		state[key] = visiting
		path = append(path, byTitle[key].Title)
		for _, dep := range byTitle[key].BlockedBy {
			if err := visit(strings.ToLower(strings.TrimSpace(dep))); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[key] = done
		return nil
	}
	// Sorted for a deterministic error across otherwise-equal cycles; map
	// iteration order would make the same bad file report a different path
	// on different runs.
	keys := make([]string, 0, len(byTitle))
	for k := range byTitle {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := visit(k); err != nil {
			return err
		}
	}
	return nil
}

// TopoOrder returns the tickets in dependency order — every ticket after
// every ticket it is blocked_by — using the proposed order as the tiebreak
// so an acyclic set with no shared dependencies files in the order the
// scout wrote it. The graph is assumed acyclic; ParseDraft/ParseProposal
// refuse a cyclic one before this is ever called.
func TopoOrder(tickets []ProposedTicket) []ProposedTicket {
	index := map[string]int{}
	for i, t := range tickets {
		index[strings.ToLower(strings.TrimSpace(t.Title))] = i
	}
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make([]int, len(tickets))
	out := make([]ProposedTicket, 0, len(tickets))
	var visit func(i int)
	visit = func(i int) {
		if state[i] == done {
			return
		}
		state[i] = visiting
		for _, dep := range tickets[i].BlockedBy {
			if j, ok := index[strings.ToLower(strings.TrimSpace(dep))]; ok {
				visit(j)
			}
		}
		state[i] = done
		out = append(out, tickets[i])
	}
	for i := range tickets {
		visit(i)
	}
	return out
}

// strictUnmarshal decodes one JSON object, refusing unknown fields — the
// same discipline plan.strictUnmarshal enforces, and for the same reason:
// a misspelled field should fail loudly rather than read as silently
// absent.
func strictUnmarshal(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("the handoff does not match the schema: %w", err)
	}
	return nil
}

// firstLine trims a verbatim account down to something a journal reason or
// a CLI error can carry.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 200
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
