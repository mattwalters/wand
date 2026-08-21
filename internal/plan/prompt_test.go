package plan

import (
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
)

// The fence is absolute doctrine has no code path that enforces it on the
// scout — UpsertSection enforces it structurally regardless of what a
// worker says — but the prompt is the thing that keeps a worker from
// treating the plan region as its whole ticket to rewrite. Pinning the
// wording here means a later edit that quietly drops it fails a test
// rather than a review nobody happened to catch.
func TestScoutRulesCarryTheFenceIsAbsoluteContract(t *testing.T) {
	rules := strings.Join(scoutRules(), "\n")
	for _, want := range []string{
		"marker-fenced plan region",
		"never rewritten by a plan",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("scoutRules does not carry %q:\n%s", want, rules)
		}
	}
}

// The critic and the reviser see the same fence rule the scout does — both
// build on scoutRules rather than restating it, and this pins that they
// still inherit it if that ever changes.
func TestCriticRulesInheritTheFenceContract(t *testing.T) {
	rules := strings.Join(criticRules(), "\n")
	if !strings.Contains(rules, "marker-fenced plan region") {
		t.Errorf("criticRules does not inherit the fence contract:\n%s", rules)
	}
}

// A re-plan is the same scout prompt run again over a ticket that already
// carries comments — a prior plan's argument, a human's answers. Those
// comments are the whole reason the run exists, and the prompt says so.
func TestScoutPromptTellsAReplanToReadCommentsAsInput(t *testing.T) {
	got := scoutPrompt("a ticket", covenant.Default())
	if !strings.Contains(got, "re-plan") {
		t.Errorf("scoutPrompt does not tell a re-plan to read comments as input:\n%s", got)
	}
}
