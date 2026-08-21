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

// Scoped is the research-side analog of In Review: the plan is written and a
// human has to judge it. It is `unstarted` — blessing it re-authorizes the
// work rather than resuming it — and it sits between Scoping and Todo with
// room on both sides, because a board that orders it anywhere else stops
// reading as a pipeline.
func TestScopedSitsBetweenScopingAndTodo(t *testing.T) {
	var scoping, scoped, todo Status
	for _, s := range Default().Statuses {
		switch s.Key {
		case "scoping":
			scoping = s
		case "scoped":
			scoped = s
		case "todo":
			todo = s
		}
	}
	if scoped.Key == "" {
		t.Fatal("the covenant has no scoped status")
	}
	if scoped.Name != "Scoped" {
		t.Errorf("scoped name = %q, want %q", scoped.Name, "Scoped")
	}
	if scoped.Type != "unstarted" {
		t.Errorf("scoped type = %q, want unstarted: blessing a plan re-authorizes the work, it does not resume it", scoped.Type)
	}
	if !(scoping.Position < scoped.Position && scoped.Position < todo.Position) {
		t.Errorf("positions scoping=%v scoped=%v todo=%v, want Scoped strictly between", scoping.Position, scoped.Position, todo.Position)
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
	if got := f.Covenant().StatusName("scoped"); got != "Scoped" {
		t.Errorf(`minimal file StatusName("scoped") = %q, want "Scoped"`, got)
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

// Scoped is a status like any other: the file may rename it, and code that
// reads it by key follows the rename.
func TestScopedIsRenameable(t *testing.T) {
	f, err := Parse([]byte("schema = 1\n[statuses]\nscoped = \"Planned\"\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := f.Covenant().StatusName("scoped"); got != "Planned" {
		t.Errorf(`renamed StatusName("scoped") = %q, want "Planned"`, got)
	}
}
