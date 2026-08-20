package scope

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mattwalters/wand/internal/covenant"
)

// The handoff, and the validation that decides whether anything gets
// written at all.
//
// Everything the scout produces is judged here, before the first Linear
// call, because a scope is read as a decision: a human blesses the plan in
// the ticket body on the strength of the argument in the comment, and by
// then nobody re-derives whether the argument held together. A draft
// missing its trade-offs, recommending an approach it never described, or
// citing files without lines, is a draft that reads finished and is not.
// Refusing it costs one model call; letting it through costs the decision
// it goes on to inform.
//
// So validation is deliberately strict, and every rule below is one of the
// deliverables the ticket names, checked rather than hoped for.

// Draft is the scout's report, and the same shape a reviser returns: a
// revision replaces the draft whole and is validated identically, so a
// second pass cannot smuggle in a weaker scope than the first.
type Draft struct {
	// Premise is "sound" or "wrong". A scout that finds the ticket asks
	// for something already done, impossible, or built on a mistake says
	// so instead of scoping around it — the alternative is a beautifully
	// argued plan for the wrong work.
	Premise string `json:"premise"`
	// Reason is the scout's own account of why the premise is wrong,
	// required when it is. It is quoted verbatim to the human, never
	// paraphrased.
	Reason string `json:"reason,omitempty"`

	// Understanding is what the scout takes the ticket to be asking. It
	// leads the interview: the most expensive thing to get wrong is the
	// problem.
	Understanding string `json:"understanding,omitempty"`

	// Approaches are the ways the work could be done — one to three, each
	// with what it costs. More than three is a scout that has not chosen;
	// none is not a scope.
	Approaches []Approach `json:"approaches,omitempty"`

	// Recommendation names one of the approaches and argues for it.
	Recommendation Recommendation `json:"recommendation"`

	// Files are the code the plan touches, cited file:line. A cold reader
	// picking this ticket up should not have to repeat the search the
	// scout already did.
	Files []FileRef `json:"files,omitempty"`

	// Estimate is the size on the covenant's scale. A pointer because a
	// missing estimate and an estimate of zero are different answers, and
	// only one of them is allowed.
	Estimate *int `json:"estimate,omitempty"`

	// Plan is what goes into the ticket body: ordered steps and the story
	// of how the work is proven.
	Plan Plan `json:"plan"`

	// Assumptions are what the plan rests on that the scout could not
	// verify, each with what breaks if it is wrong. They are the middle
	// of the interview, costliest first.
	Assumptions []Assumption `json:"assumptions,omitempty"`

	// OpenQuestions are what the scout could not answer from the code.
	OpenQuestions []string `json:"open_questions,omitempty"`
}

// Approach is one way the work could be done.
type Approach struct {
	Name string `json:"name"`
	// Sketch is what the approach actually is, in a few sentences.
	Sketch string `json:"sketch"`
	// Tradeoff is what it costs — what it gives up, forecloses or risks.
	// Required: an approach listed without one is an option presented as
	// free, and a menu of free options is not a choice.
	Tradeoff string `json:"tradeoff"`
}

// Recommendation is the scout's pick, by name, with the argument for it.
type Recommendation struct {
	Approach string `json:"approach"`
	Why      string `json:"why"`
}

// FileRef is one citation into the code.
type FileRef struct {
	// Location is path:line, or path:line-line for a range.
	Location string `json:"location"`
	// Note is why this location matters to the plan.
	Note string `json:"note"`
}

// Plan is the implementation plan: what to do, in order, and how it is
// proven.
type Plan struct {
	Steps []string `json:"steps"`
	// Tests is the test story — what proves the work, which is the half
	// of a plan that gets left out when nobody asks for it explicitly.
	Tests string `json:"tests"`
}

// Assumption is something the plan rests on that the scout could not check.
type Assumption struct {
	Statement string `json:"statement"`
	// IfWrong is what has to change if the assumption does not hold.
	IfWrong string `json:"if_wrong"`
	// Cost is the blast radius: "high", "medium" or "low". It orders the
	// interview, so it is a stated field rather than the order the scout
	// happened to list them in — remembered ordering is sometimes
	// ordering.
	Cost string `json:"cost"`
}

// Costs are the blast-radius levels, worst first. The interview asks in
// this order.
const (
	CostHigh   = "high"
	CostMedium = "medium"
	CostLow    = "low"
)

// costRank orders assumptions costliest-first; unknown costs never reach
// here, because validation refuses them.
func costRank(cost string) int {
	switch cost {
	case CostHigh:
		return 0
	case CostMedium:
		return 1
	default:
		return 2
	}
}

// PremiseSound and PremiseWrong are the two verdicts a draft may carry.
const (
	PremiseSound = "sound"
	PremiseWrong = "wrong"
)

// maxApproaches is the ceiling the ticket sets. Above it, the scout has
// surveyed rather than scoped, and a human is handed the choosing it was
// sent to do.
const maxApproaches = 3

// fileLine matches a path with a line number, or a line range. The line is
// the whole point of the citation: a bare filename sends the next reader
// back to the search the scout already ran.
var fileLine = regexp.MustCompile(`^[^\s:]+:\d+(-\d+)?$`)

// ParseDraft validates a scout's or reviser's handoff against the covenant
// it must satisfy. An error means nothing is written: the caller has no
// scope, and half a scope on a ticket is worse than none, because it reads
// like a whole one.
func ParseDraft(raw json.RawMessage, cov covenant.Covenant) (Draft, error) {
	if len(raw) == 0 {
		return Draft{}, errors.New("the worker wrote no handoff")
	}
	var d Draft
	if err := strictUnmarshal(raw, &d); err != nil {
		return Draft{}, err
	}
	if err := d.validate(cov); err != nil {
		return Draft{}, err
	}
	return d, nil
}

func (d Draft) validate(cov covenant.Covenant) error {
	switch d.Premise {
	case PremiseWrong:
		if strings.TrimSpace(d.Reason) == "" {
			return errors.New(`the handoff says the ticket's premise is wrong but gives no reason — that verdict is quoted to a human verbatim, and there is nothing to quote`)
		}
		return nil
	case PremiseSound:
	default:
		return fmt.Errorf("the handoff's premise is %q, not %q or %q", d.Premise, PremiseSound, PremiseWrong)
	}

	if strings.TrimSpace(d.Understanding) == "" {
		return errors.New("the handoff states no understanding of the ticket; the first thing a human checks is whether the scout read the right problem")
	}
	if err := d.validateApproaches(); err != nil {
		return err
	}
	if err := d.validateFiles(); err != nil {
		return err
	}
	if err := d.validateEstimate(cov); err != nil {
		return err
	}
	if err := d.validatePlan(); err != nil {
		return err
	}
	return d.validateAssumptions()
}

func (d Draft) validateApproaches() error {
	switch {
	case len(d.Approaches) == 0:
		return errors.New("the handoff offers no approaches; a scope that names no way to do the work is not a scope")
	case len(d.Approaches) > maxApproaches:
		return fmt.Errorf("the handoff offers %d approaches, more than the %d a scope may carry — narrowing the field is the work, and handing a human every option is handing the work back",
			len(d.Approaches), maxApproaches)
	}
	seen := map[string]bool{}
	for i, a := range d.Approaches {
		name := strings.TrimSpace(a.Name)
		switch {
		case name == "":
			return fmt.Errorf("approach %d has no name; the recommendation points at an approach by name", i+1)
		case strings.TrimSpace(a.Sketch) == "":
			return fmt.Errorf("approach %q says nothing about what it is", name)
		case strings.TrimSpace(a.Tradeoff) == "":
			return fmt.Errorf("approach %q states no trade-off; an option presented as free is not one a human can weigh", name)
		case seen[strings.ToLower(name)]:
			return fmt.Errorf("two approaches are both named %q; the recommendation would point at either", name)
		}
		seen[strings.ToLower(name)] = true
	}

	rec := strings.TrimSpace(d.Recommendation.Approach)
	if rec == "" {
		return errors.New("the handoff recommends nothing; a scout that surveys without choosing hands the judgement back to the human it was sent to spare")
	}
	if !seen[strings.ToLower(rec)] {
		return fmt.Errorf("the recommendation names %q, which is not one of the approaches offered", rec)
	}
	if strings.TrimSpace(d.Recommendation.Why) == "" {
		return fmt.Errorf("the recommendation of %q carries no argument for it", rec)
	}
	return nil
}

// isRecommended reports whether an approach is the one the draft
// recommends.
//
// It exists so the match is made in exactly one place. validateApproaches
// pairs the recommendation with an approach on the trimmed, case-folded
// name, so every later reader of that pair must match the same way: a
// comparison that trims one side and not the other accepts a draft as
// valid and then renders it as recommending nothing, and lists the
// recommended approach to the human as one the draft passed over.
func isRecommended(d Draft, a Approach) bool {
	return strings.EqualFold(strings.TrimSpace(a.Name), strings.TrimSpace(d.Recommendation.Approach))
}

func (d Draft) validateFiles() error {
	if len(d.Files) == 0 {
		return errors.New("the handoff cites no files; a scope whose reader has to find the code again did half the job")
	}
	for _, f := range d.Files {
		loc := strings.TrimSpace(f.Location)
		if !fileLine.MatchString(loc) {
			return fmt.Errorf("file citation %q is not path:line (or path:line-line) — the line is what makes it a citation rather than a filename", f.Location)
		}
		if strings.TrimSpace(f.Note) == "" {
			return fmt.Errorf("file citation %q says nothing about why it matters", loc)
		}
	}
	return nil
}

func (d Draft) validateEstimate(cov covenant.Covenant) error {
	values := cov.EstimateValues()
	if len(values) == 0 {
		if d.Estimate != nil {
			return fmt.Errorf("the handoff estimates %d, but this team does not use estimates (the covenant's scale is %q)", *d.Estimate, cov.IssueEstimationType)
		}
		return nil
	}
	if d.Estimate == nil {
		return fmt.Errorf("the handoff carries no estimate; the covenant's scale is %s (%s)", cov.IssueEstimationType, joinInts(values))
	}
	for _, v := range values {
		if v == *d.Estimate {
			return nil
		}
	}
	return fmt.Errorf("the estimate %d is not on the covenant's %s scale (%s); Linear would adjust it to fit, landing a number nobody chose",
		*d.Estimate, cov.IssueEstimationType, joinInts(values))
}

func (d Draft) validatePlan() error {
	if len(d.Plan.Steps) == 0 {
		return errors.New("the plan has no steps")
	}
	for i, s := range d.Plan.Steps {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("plan step %d is empty", i+1)
		}
	}
	if strings.TrimSpace(d.Plan.Tests) == "" {
		return errors.New("the plan carries no test story; how the work is proven is the half of a plan that goes missing when nobody asks for it")
	}
	return nil
}

func (d Draft) validateAssumptions() error {
	for i, a := range d.Assumptions {
		switch {
		case strings.TrimSpace(a.Statement) == "":
			return fmt.Errorf("assumption %d states nothing", i+1)
		case strings.TrimSpace(a.IfWrong) == "":
			return fmt.Errorf("assumption %q does not say what changes if it is wrong, which is the whole reason to surface it", a.Statement)
		case a.Cost != CostHigh && a.Cost != CostMedium && a.Cost != CostLow:
			return fmt.Errorf("assumption %q has cost %q, not %q, %q or %q — the cost is what orders the questions a human is asked",
				a.Statement, a.Cost, CostHigh, CostMedium, CostLow)
		}
	}
	for _, q := range d.OpenQuestions {
		if strings.TrimSpace(q) == "" {
			return errors.New("an open question is empty")
		}
	}
	return nil
}

// Critique is the cold critic's handoff: an attack on the draft, validated
// exactly as strictly, because an objection nobody can act on costs the
// reviser a round and the human nothing but noise.
type Critique struct {
	// Verdict is "sound" or "flawed".
	Verdict string `json:"verdict"`
	// Objections are what the critic found. Required when flawed.
	Objections []Objection `json:"objections,omitempty"`
}

// Objection is one attack on the draft.
type Objection struct {
	// Target names the part of the draft under attack — the premise, an
	// approach, the recommendation, the plan, the estimate, an assumption.
	Target string `json:"target"`
	// Summary is what is wrong.
	Summary string `json:"summary"`
	// Consequence is what it costs if the draft ships as written. An
	// objection without one is a preference, and the reviser is not to
	// spend a round on preferences.
	Consequence string `json:"consequence"`
}

// Verdicts a critique may carry.
const (
	VerdictSound  = "sound"
	VerdictFlawed = "flawed"
)

// ParseCritique validates a critic's handoff.
func ParseCritique(raw json.RawMessage) (Critique, error) {
	if len(raw) == 0 {
		return Critique{}, errors.New("the critic wrote no handoff")
	}
	var c Critique
	if err := strictUnmarshal(raw, &c); err != nil {
		return Critique{}, err
	}
	switch c.Verdict {
	case VerdictSound:
	case VerdictFlawed:
		if len(c.Objections) == 0 {
			return Critique{}, errors.New(`the critic called the draft flawed but raised no objections`)
		}
	default:
		return Critique{}, fmt.Errorf("the critic's verdict is %q, not %q or %q", c.Verdict, VerdictSound, VerdictFlawed)
	}
	for i, o := range c.Objections {
		switch {
		case strings.TrimSpace(o.Target) == "":
			return Critique{}, fmt.Errorf("objection %d names no target in the draft", i+1)
		case strings.TrimSpace(o.Summary) == "":
			return Critique{}, fmt.Errorf("objection %d says nothing", i+1)
		case strings.TrimSpace(o.Consequence) == "":
			return Critique{}, fmt.Errorf("objection %q states no consequence; an objection with no cost is a preference, and a revision round is too expensive to spend on one", o.Summary)
		}
	}
	return c, nil
}

// strictUnmarshal decodes one JSON object, refusing unknown fields. A
// worker that misspells a field name should fail loudly here rather than
// have that half of its work silently read as absent — a scope missing its
// trade-offs because of a typo is refused, which costs a model call, and
// one written without them costs the decision it informs.
func strictUnmarshal(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("the handoff does not match the schema: %w", err)
	}
	return nil
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ", ")
}
