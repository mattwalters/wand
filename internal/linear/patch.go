package linear

import (
	"fmt"
	"strings"
)

// WithReplacement returns a description with one anchored replacement
// applied, as a pure string operation.
//
// This is the one-off-write half of the body-edit rule (section.go carries
// the other): what a machine writes repeatedly goes in a fenced region it
// owns; what it writes once — a description correction — anchors on the
// exact text it replaces and fails rather than clobbers. The anchor must
// match exactly once:
//
//   - Zero matches means the premise being corrected is not in the body —
//     most likely someone already corrected it, and writing anyway would
//     paste a correction of text that is no longer there.
//   - More than one match means the anchor does not pin a location, and
//     picking an occurrence would be a guess. Guessing in someone else's
//     prose is the failure both halves of this rule exist to prevent.
//
// An empty replacement deletes the anchored text; an empty anchor is
// refused, since it matches everywhere and pins nothing.
func WithReplacement(description, old, new string) (string, error) {
	if old == "" {
		return "", fmt.Errorf("linear: an empty anchor matches everywhere and pins nothing; quote the exact wording to replace")
	}
	// Index-based, not strings.Count: Count sees only non-overlapping
	// occurrences, so a self-overlapping anchor ("very very" in "very very
	// very") counts as one match while pinning two locations.
	i := strings.Index(description, old)
	if i < 0 {
		return "", fmt.Errorf(
			"linear: the description does not contain %q; the wording may already have been corrected — re-read the ticket rather than write a correction of text that is not there", old)
	}
	if strings.Contains(description[i+1:], old) {
		return "", fmt.Errorf(
			"linear: %q appears more than once in the description; an anchor that matches more than once does not pin a location, so quote enough surrounding text to make it unique", old)
	}
	return description[:i] + new + description[i+len(old):], nil
}
