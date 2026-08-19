package linear

import (
	"strings"
	"testing"
)

func TestWithReplacementReplacesAUniqueAnchor(t *testing.T) {
	got, err := WithReplacement("the test fails on every third run, always", "fails on every third run", "no longer fails")
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if got != "the test no longer fails, always" {
		t.Errorf("got %q", got)
	}
}

func TestWithReplacementEmptyNewDeletes(t *testing.T) {
	got, err := WithReplacement("keep this. drop this. keep that.", " drop this.", "")
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if got != "keep this. keep that." {
		t.Errorf("got %q", got)
	}
}

// The refusals are the design: a one-off body edit anchors on exact text and
// fails rather than clobbers (section.go carries the repeated-write half).
func TestWithReplacementRefusesAnchorsThatDoNotPin(t *testing.T) {
	cases := map[string]struct {
		description, old string
	}{
		// Zero matches usually means someone already corrected the wording;
		// writing anyway pastes a correction of text that is not there.
		"absent": {"the corrected wording", "the old wording"},
		// Two matches cannot pin a location; picking one is a guess in
		// someone else's prose.
		"ambiguous": {"it fails. it fails.", "it fails."},
		// Self-overlapping matches fool strings.Count (it sees one), but the
		// anchor still pins two locations; the refusal must see both.
		"overlapping": {"very very very", "very very"},
		"empty":       {"anything", ""},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := WithReplacement(tt.description, tt.old, "new")
			if err == nil {
				t.Fatal("want a refusal")
			}
			if !strings.Contains(err.Error(), "linear:") {
				t.Errorf("error %q should be package-prefixed like its neighbours", err)
			}
		})
	}
}
