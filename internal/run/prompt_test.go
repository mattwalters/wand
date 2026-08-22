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

// The docs-change-with-code rule (AGENTS.md) graduates from prose into a
// standing reviewer dimension here. This test guards against the instruction
// being silently deleted or reworded away in a future prompt edit — the only
// way this check can regress, since the check itself runs inside an LLM
// reviewer's judgment, not in Go code.
func TestReviewPromptCarriesDocsDimension(t *testing.T) {
	got := reviewPrompt("a ticket", "main")
	for _, want := range []string{
		"Docs change with the code",
		"README.md",
		"docs/",
		"whether the docs moved with it",
		"not a semantic audit",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("reviewPrompt does not carry %q:\n%s", want, got)
		}
	}
}
