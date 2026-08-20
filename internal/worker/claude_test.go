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
	if !hasFlag(inv.Argv, "--output-format", "json") {
		t.Errorf("not structured output: %v", inv.Argv)
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

// claudeSample is a real `claude -p --output-format json` result, captured
// live against a trivial prompt (2026-08-19, Claude Code 2.1.234) — not
// hand-built, so a future CLI upgrade that changes the shape fails this
// test instead of silently going absent in production.
const claudeSample = `{"is_error":false,"duration_api_ms":2922,"num_turns":1,"stop_reason":"end_turn","session_id":"9080ca82-851b-4501-a773-abae3cdc645a","total_cost_usd":0.0578496,"usage":{"input_tokens":2,"cache_creation_input_tokens":8638,"cache_read_input_tokens":16652,"output_tokens":4,"output_tokens_details":{"thinking_tokens":0},"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard"},"permission_denials":[],"terminal_reason":"completed","subtype":"success","result":"pong","type":"result","duration_ms":1946,"uuid":"15b29593-e7a8-43dd-a0e9-2d558dad0fda"}`

func TestClaudeCodeParseUsage(t *testing.T) {
	got := worker.ClaudeCode{}.ParseUsage(claudeSample)
	if got == nil || got.InputTokens == nil || got.OutputTokens == nil {
		t.Fatalf("ParseUsage = %+v, want both fields populated from the sample", got)
	}
	// Input is the sum of fresh, cache-write and cache-read tokens: the
	// three ways Anthropic's own accounting reports input was processed.
	if want := int64(2 + 8638 + 16652); *got.InputTokens != want {
		t.Errorf("InputTokens = %d, want %d", *got.InputTokens, want)
	}
	if *got.OutputTokens != 4 {
		t.Errorf("OutputTokens = %d, want 4", *got.OutputTokens)
	}
}

func TestClaudeCodeParseUsageWithNoiseAroundTheLine(t *testing.T) {
	// stderr shares the captured Tail with stdout; a warning line before or
	// after the JSON result must not stop the parser finding it.
	noisy := "warning: something on stderr\n" + claudeSample + "\n"
	got := worker.ClaudeCode{}.ParseUsage(noisy)
	if got == nil || got.OutputTokens == nil || *got.OutputTokens != 4 {
		t.Errorf("ParseUsage = %+v, want the sample's usage found around the noise", got)
	}
}

func TestClaudeCodeParseUsageAbsentOnGarbage(t *testing.T) {
	for name, output := range map[string]string{
		"Empty":     "",
		"PlainText": "the CLI crashed before writing anything structured",
		"NoUsage":   `{"result": "pong", "type": "result"}`,
		"NotJSON":   "{not json at all",
	} {
		t.Run(name, func(t *testing.T) {
			if got := (worker.ClaudeCode{}).ParseUsage(output); got != nil {
				t.Errorf("ParseUsage(%q) = %+v, want nil — absent, never faked", output, got)
			}
		})
	}
}
