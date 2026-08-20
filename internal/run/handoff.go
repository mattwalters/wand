package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// WorkHandoff is what an implement, fix-CI or revise worker reports back.
//
// Reason exists because of the PW-190 lesson: a hand-back must carry the
// worker's own account of why it stopped, quoted verbatim — never a canned
// guess inferred from the phase. So a blocked handoff without a reason is
// invalid rather than defaulted.
type WorkHandoff struct {
	// Status is "done" or "blocked". Blocked means the worker stopped on
	// something a human owns; the run hands back with Reason quoted.
	Status string `json:"status"`
	// Summary is what was done and why, for the PR body and the ticket.
	Summary string `json:"summary"`
	// Reason is the worker's own words for why it stopped. Required when
	// blocked; quoted verbatim in the Needs Input comment.
	Reason string `json:"reason,omitempty"`
	// Title is the worker's suggested PR title, without the identifier
	// prefix — the orchestrator adds and repairs that (the PW-189 rule:
	// conventions the orchestrator can make true are made true in code).
	Title string `json:"title,omitempty"`
	// DescriptionCorrections are anchored edits to the ticket body for
	// wording the work disproved. The orchestrator applies them — one
	// writer per ticket — quoting the old wording in a comment first.
	DescriptionCorrections []Correction `json:"description_corrections,omitempty"`
	// PlanDeviations are the places the work departed from the ticket's
	// plan. They reach the ticket and the PR body instead of dying in a
	// worker's transcript (the PW-191 lesson).
	PlanDeviations []string `json:"plan_deviations,omitempty"`
}

// Correction is one anchored description edit: the exact wording the work
// disproved, and what should stand in its place (empty deletes).
type Correction struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// ParseWork validates a work handoff. An error means the worker left no
// usable report, which the loop treats as a park — it cannot distinguish a
// crash from a success without one.
func ParseWork(raw json.RawMessage) (WorkHandoff, error) {
	if len(raw) == 0 {
		return WorkHandoff{}, errors.New("the worker wrote no handoff")
	}
	var h WorkHandoff
	if err := strictUnmarshal(raw, &h); err != nil {
		return WorkHandoff{}, err
	}
	switch h.Status {
	case "done":
		if strings.TrimSpace(h.Summary) == "" {
			return WorkHandoff{}, errors.New(`the handoff says "done" but carries no summary of what was done`)
		}
	case "blocked":
		if strings.TrimSpace(h.Reason) == "" {
			return WorkHandoff{}, errors.New(`the handoff says "blocked" but gives no reason — the hand-back must quote the worker's own account, and there is none`)
		}
	default:
		return WorkHandoff{}, fmt.Errorf("the handoff status is %q, not \"done\" or \"blocked\"", h.Status)
	}
	return h, nil
}

// ReviewHandoff is what a review worker reports back.
type ReviewHandoff struct {
	// Verdict is "approve" or "revise".
	Verdict string `json:"verdict"`
	// Summary is the positive evidence behind an approval: what was
	// verified and why it is enough. Convergence happens only on this —
	// never on the absence of complaints — so an approval without it is
	// not parseable as an approval.
	Summary string `json:"summary,omitempty"`
	// Findings are the reviewer's objections.
	Findings []Finding `json:"findings,omitempty"`
}

// Finding is one review objection.
type Finding struct {
	Summary string `json:"summary"`
	// FailureScenario is the concrete way the code misbehaves: the inputs
	// or state, and what goes wrong. A finding without one is dropped in
	// code before anything downstream sees it.
	FailureScenario string `json:"failure_scenario"`
	Location        string `json:"location,omitempty"`
}

// ParseReview validates a review handoff. An error here parks the run: a
// reviewer that produces no parseable handoff must never read as a clean
// pass, or every reviewer crash converges the run it was judging.
func ParseReview(raw json.RawMessage) (ReviewHandoff, error) {
	if len(raw) == 0 {
		return ReviewHandoff{}, errors.New("the reviewer wrote no handoff")
	}
	var h ReviewHandoff
	if err := strictUnmarshal(raw, &h); err != nil {
		return ReviewHandoff{}, err
	}
	switch h.Verdict {
	case "approve":
		if strings.TrimSpace(h.Summary) == "" {
			return ReviewHandoff{}, errors.New("the reviewer approved without stating what it verified; convergence needs positive evidence, and there is none")
		}
	case "revise":
		if len(h.Findings) == 0 {
			return ReviewHandoff{}, errors.New(`the reviewer said "revise" but raised no findings`)
		}
	default:
		return ReviewHandoff{}, fmt.Errorf("the review verdict is %q, not \"approve\" or \"revise\"", h.Verdict)
	}
	return h, nil
}

// Concrete filters findings to those carrying a failure scenario, and
// reports how many were dropped. The filter runs in code, before posting or
// prompting — a rule enforced where it cannot be forgotten.
func Concrete(findings []Finding) (kept []Finding, dropped int) {
	for _, f := range findings {
		if strings.TrimSpace(f.FailureScenario) == "" || strings.TrimSpace(f.Summary) == "" {
			dropped++
			continue
		}
		kept = append(kept, f)
	}
	return kept, dropped
}

// strictUnmarshal decodes one JSON object, refusing unknown fields. A worker
// that misspells "failure_scenario" should fail loudly here, not have its
// findings silently dropped as concrete-less.
func strictUnmarshal(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("the handoff does not match the schema: %w", err)
	}
	return nil
}
