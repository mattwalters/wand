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
	// scale here — a scope whose estimate is "none" has not estimated.
	for _, v := range Default().EstimateValues() {
		if v == 0 {
			t.Error("the fibonacci scale offers 0, which is the absence of an estimate")
		}
	}
}
