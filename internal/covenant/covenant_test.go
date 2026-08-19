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
