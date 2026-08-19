package worker_test

import (
	"testing"

	"github.com/mattwalters/wand/internal/worker"
)

func TestConformanceSelection(t *testing.T) {
	spec := worker.Spec{Model: "haiku", Effort: "low"}
	if got := (worker.ClaudeCode{}).ConformanceSpec(spec); got.Model != "haiku" || got.Effort != "low" {
		t.Errorf("ClaudeCode ConformanceSpec = %+v, want haiku/low", got)
	}
	if got := (worker.Codex{}).ConformanceSpec(spec); got.Model != "" || got.Effort != "" {
		t.Errorf("Codex ConformanceSpec = %+v, want the Codex default selection", got)
	}
}
