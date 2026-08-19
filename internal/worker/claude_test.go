package worker_test

import (
	"slices"
	"testing"

	"github.com/mattwalters/wand/internal/worker"
	"github.com/mattwalters/wand/internal/workertest"
)

// hasFlag reports whether argv carries flag with exactly value beside it.
func hasFlag(argv []string, flag, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}

func TestClaudeCodeStructuralConformance(t *testing.T) {
	// The fast half of the isolation conformance suite; the live half is
	// TestClaudeCodeIsolation behind the `conformance` build tag.
	workertest.Structural(t, worker.ClaudeCode{})
}

func TestClaudeCodeInvocation(t *testing.T) {
	spec := specFor(t)
	spec.Model = "haiku"
	spec.Effort = "low"
	prompt := worker.Compose(spec)

	inv, err := worker.ClaudeCode{}.Invocation(spec, prompt, []string{"PATH=/usr/bin"})
	if err != nil {
		t.Fatal(err)
	}

	if inv.Argv[0] != "claude" {
		t.Errorf("Argv[0] = %q, want claude", inv.Argv[0])
	}
	if !slices.Contains(inv.Argv, "-p") {
		t.Errorf("not a headless run: no -p in %v", inv.Argv)
	}
	// The isolation flags: no settings files, no MCP servers from anywhere.
	if !hasFlag(inv.Argv, "--setting-sources", "") {
		t.Errorf("settings files are not disabled: %v", inv.Argv)
	}
	if !slices.Contains(inv.Argv, "--strict-mcp-config") || !hasFlag(inv.Argv, "--mcp-config", `{"mcpServers":{}}`) {
		t.Errorf("MCP inheritance is not closed off: %v", inv.Argv)
	}
	if !slices.Contains(inv.Argv, "--no-session-persistence") {
		t.Errorf("worker sessions would be resumable: %v", inv.Argv)
	}
	// Per-phase selection reaches the harness.
	if !hasFlag(inv.Argv, "--model", "haiku") || !hasFlag(inv.Argv, "--effort", "low") {
		t.Errorf("model/effort not passed through: %v", inv.Argv)
	}

	// The environment Run hands down (already stripped and redirected by
	// ChildEnviron) passes through unchanged; the shared closures are
	// asserted by workertest.Structural.
	if !slices.Contains(inv.Env, "PATH=/usr/bin") {
		t.Errorf("the handed-down environ did not pass through: %v", inv.Env)
	}

	if inv.Dir != spec.Dir {
		t.Errorf("Dir = %q, want %q", inv.Dir, spec.Dir)
	}
	if inv.Stdin != prompt {
		t.Errorf("the composed prompt is not what gets delivered")
	}
}

func TestClaudeCodeDefaultsOmitSelection(t *testing.T) {
	spec := specFor(t)
	inv, err := worker.ClaudeCode{}.Invocation(spec, worker.Compose(spec), nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(inv.Argv, "--model") || slices.Contains(inv.Argv, "--effort") {
		t.Errorf("empty Model/Effort must fall through to the harness default: %v", inv.Argv)
	}
}
