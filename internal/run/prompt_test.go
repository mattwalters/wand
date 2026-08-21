package run

import (
	"strings"
	"testing"
)

// The live failure mode this guards against: a ticket's comments routinely
// carry approaches that were proposed and then rejected, and a cold worker
// that treats the whole ticket as equally authoritative has no way to tell
// the rejected approach from the accepted one except by being told which
// half governs.
func TestImplementPromptCarriesTheBodyIsSpecContract(t *testing.T) {
	got := implementPrompt("a ticket")
	for _, want := range []string{
		"description is",
		"the specification",
		"comments are the record of how it",
		"never implement an approach the comments show was rejected",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("implementPrompt does not carry %q:\n%s", want, got)
		}
	}
}
