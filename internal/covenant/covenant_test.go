package covenant

import "testing"

// StatusName is how code refers to a status by meaning; a covenant file's
// rename must travel through it, or a renamed board breaks every read.
func TestStatusNameFollowsFileRenames(t *testing.T) {
	if got := Default().StatusName("todo"); got != "Todo" {
		t.Errorf(`stock StatusName("todo") = %q, want "Todo"`, got)
	}

	f, err := Parse([]byte("schema = 1\n[statuses]\ntodo = \"Ready\"\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := f.Covenant().StatusName("todo"); got != "Ready" {
		t.Errorf(`renamed StatusName("todo") = %q, want "Ready"`, got)
	}

	if got := Default().StatusName("no-such-key"); got != "" {
		t.Errorf("unknown key should return empty, got %q", got)
	}
}

// The estimate scale is the covenant's, and an orchestrator writing an
// estimate checks the number against it first: Linear adjusts an off-scale
// estimate to fit rather than refusing it, so an unchecked write lands a
// number nobody chose.
func TestEstimateValuesFollowTheScale(t *testing.T) {
	if got := Default().EstimateValues(); len(got) == 0 || got[len(got)-1] != 8 {
		t.Errorf("stock (fibonacci) EstimateValues = %v, want the fibonacci points", got)
	}

	f, err := Parse([]byte("schema = 1\n[estimates]\nscale = \"exponential\"\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := f.Covenant().EstimateValues(); len(got) == 0 || got[len(got)-1] != 16 {
		t.Errorf("exponential EstimateValues = %v, want the exponential points", got)
	}

	// "notUsed" is a configured team, not a broken one: no points, and the
	// verbs that would write an estimate skip it.
	f, err = Parse([]byte("schema = 1\n[estimates]\nscale = \"notUsed\"\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := f.Covenant().EstimateValues(); got != nil {
		t.Errorf("notUsed EstimateValues = %v, want none", got)
	}

	// Zero is Linear's "no estimate" rather than a size, so it is on no
	// scale here — a plan whose estimate is "none" has not estimated.
	for _, v := range Default().EstimateValues() {
		if v == 0 {
			t.Error("the fibonacci scale offers 0, which is the absence of an estimate")
		}
	}
}

// Plan Review is the research-side analog of In Review: the plan is written
// and a human has to judge it. It is `unstarted` — blessing it
// re-authorizes the work rather than resuming it — and it sits between
// In Planning and Todo with room on both sides, because a board that orders
// it anywhere else stops reading as a pipeline. In Planning itself sits
// between To Plan and Plan Review, and is `started` — the research-side
// analog of In Progress — because a live plan run is work in flight, not a
// blessing waiting to be spent.
func TestPlanReviewSitsBetweenInPlanningAndTodo(t *testing.T) {
	var toPlan, inPlanning, planReview, todo Status
	for _, s := range Default().Statuses {
		switch s.Key {
		case "to_plan":
			toPlan = s
		case "in_planning":
			inPlanning = s
		case "plan_review":
			planReview = s
		case "todo":
			todo = s
		}
	}
	if planReview.Key == "" {
		t.Fatal("the covenant has no plan_review status")
	}
	if planReview.Name != "Plan Review" {
		t.Errorf("plan_review name = %q, want %q", planReview.Name, "Plan Review")
	}
	if planReview.Type != "unstarted" {
		t.Errorf("plan_review type = %q, want unstarted: blessing a plan re-authorizes the work, it does not resume it", planReview.Type)
	}
	if inPlanning.Type != "started" {
		t.Errorf("in_planning type = %q, want started: a live plan run is work in flight", inPlanning.Type)
	}
	if !(toPlan.Position < inPlanning.Position && inPlanning.Position < planReview.Position && planReview.Position < todo.Position) {
		t.Errorf("positions to_plan=%v in_planning=%v plan_review=%v todo=%v, want strictly ordered",
			toPlan.Position, inPlanning.Position, planReview.Position, todo.Position)
	}
}

// The lesson the schema field encodes, pinned: its number gates whether wand
// can *read* a file, not which topology the file gets. A file that mentions
// no statuses at all still gets every current state, which is exactly what
// makes an unmigrated team show up as drift instead of silently keeping the
// old board.
func TestMinimalFileGetsTheCurrentTopology(t *testing.T) {
	f, err := Parse([]byte("schema = 1\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := f.Covenant().StatusName("plan_review"); got != "Plan Review" {
		t.Errorf(`minimal file StatusName("plan_review") = %q, want "Plan Review"`, got)
	}
}

// The other half of the same lesson: a file from a future wand is refused,
// not guessed at — wand cannot know what its keys mean.
func TestNewerSchemaIsRefused(t *testing.T) {
	_, err := Parse([]byte("schema = 2\n"))
	if err == nil {
		t.Fatal("schema = 2 parsed; want refusal — this wand speaks schema 1")
	}
}

// Plan Review is a status like any other: the file may rename it, and code
// that reads it by key follows the rename.
func TestPlanReviewIsRenameable(t *testing.T) {
	f, err := Parse([]byte("schema = 1\n[statuses]\nplan_review = \"Planned\"\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := f.Covenant().StatusName("plan_review"); got != "Planned" {
		t.Errorf(`renamed StatusName("plan_review") = %q, want "Planned"`, got)
	}
}
