package worker_test

import (
	"encoding/json"
	"slices"
	"strings"
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
	if !hasFlag(inv.Argv, "--output-format", "stream-json") {
		t.Errorf("not the full event stream: %v", inv.Argv)
	}
	// --output-format stream-json requires --verbose on --print, per the
	// CLI's own error when it is missing.
	if !slices.Contains(inv.Argv, "--verbose") {
		t.Errorf("stream-json without --verbose, which claude -p refuses: %v", inv.Argv)
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

func TestClaudeCodeSchemaInvocationAddsTheFlagInline(t *testing.T) {
	spec := specFor(t)
	schema := json.RawMessage(`{"type":"object","properties":{"verdict":{"type":"string"}},"required":["verdict"],"additionalProperties":false}`)
	inv, err := worker.ClaudeCode{}.SchemaInvocation(spec, worker.Compose(spec), []string{"PATH=/usr/bin"}, schema)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFlag(inv.Argv, "--json-schema", string(schema)) {
		t.Errorf("--json-schema not passed inline: %v", inv.Argv)
	}
}

// claudeStructuredResultSample is a stream-json final "result" event
// carrying structured_output, the shape a --json-schema run succeeds with.
const claudeStructuredResultSample = `{"type":"result","subtype":"success","is_error":false,"terminal_reason":"completed","structured_output":{"verdict":"sound"}}`

func TestClaudeCodeCollectHandoffReadsStructuredOutput(t *testing.T) {
	got, err := worker.ClaudeCode{}.CollectHandoff(worker.Spec{}, worker.Result{Output: claudeStructuredResultSample})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"verdict":"sound"}` {
		t.Errorf("CollectHandoff = %s, want the structured_output object", got)
	}
}

func TestClaudeCodeCollectHandoffWithNoiseAroundTheLine(t *testing.T) {
	noisy := "warning: something on stderr\n" + claudeStructuredResultSample + "\n"
	got, err := worker.ClaudeCode{}.CollectHandoff(worker.Spec{}, worker.Result{Output: noisy})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"verdict":"sound"}` {
		t.Errorf("CollectHandoff = %s, want the structured_output object found around the noise", got)
	}
}

// This is the load-bearing case the ticket's live probe found: an
// unsatisfiable schema still exits 0 with is_error false and subtype
// "success" — every signal but structured_output's presence reads this as
// a clean run.
func TestClaudeCodeCollectHandoffFailsWhenStructuredOutputIsAbsent(t *testing.T) {
	for name, output := range map[string]string{
		"NoStructuredOutputField": `{"type":"result","subtype":"success","is_error":false,"result":"the model gave up and wrote prose instead"}`,
		"StructuredOutputNull":    `{"type":"result","subtype":"success","structured_output":null}`,
		"Empty":                   "",
		"NoResultEvent":           `{"type":"assistant","message":"hi"}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := worker.ClaudeCode{}.CollectHandoff(worker.Spec{}, worker.Result{Output: output})
			if err == nil {
				t.Fatalf("CollectHandoff = %s, nil error — want a failure, not a silent misread of a schema failure as a handoff", got)
			}
			if got != nil {
				t.Errorf("CollectHandoff returned a handoff alongside an error: %s", got)
			}
		})
	}
}

func TestClaudeCodeCollectHandoffRejectsNonObjectStructuredOutput(t *testing.T) {
	_, err := worker.ClaudeCode{}.CollectHandoff(worker.Spec{}, worker.Result{
		Output: `{"type":"result","structured_output":"just a string"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "not a single JSON object") {
		t.Errorf("err = %v, want a not-a-JSON-object error", err)
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

// claudeAPIErrorSample is the real `claude -p --output-format json` result
// that ended run WND-68-20260820T101039Z, lifted verbatim out of that run's
// journal. The laptop had suspended mid-response; the phase had already
// spent 1.19M input tokens; the run parked. Captured rather than
// hand-built, so a CLI upgrade that renames terminal_reason fails this test
// instead of quietly making every transient failure look permanent again.
const claudeAPIErrorSample = `{"is_error":true,"duration_api_ms":163608,"num_turns":19,"stop_reason":"stop_sequence","session_id":"4f4e2fec-c5bc-44d6-9f4f-ad3aa7cf544b","total_cost_usd":0.9739736999999998,"usage":{"input_tokens":34,"cache_creation_input_tokens":64876,"cache_read_input_tokens":1125041,"output_tokens":14490,"output_tokens_details":{"thinking_tokens":7697},"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard","cache_creation":{"ephemeral_1h_input_tokens":64876,"ephemeral_5m_input_tokens":0},"inference_geo":"not_available","iterations":[],"speed":"standard"},"modelUsage":{"claude-haiku-4-5-20251001":{"inputTokens":5154,"outputTokens":18,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"webSearchRequests":0,"costUSD":0.0052439999999999995,"contextWindow":200000,"maxOutputTokens":32000,"canonicalModel":"claude-haiku-4-5","provider":"firstParty"},"claude-sonnet-5":{"inputTokens":36,"outputTokens":14493,"cacheReadInputTokens":1206569,"cacheCreationInputTokens":64876,"webSearchRequests":0,"costUSD":0.9687296999999998,"contextWindow":1000000,"maxOutputTokens":64000,"canonicalModel":"claude-sonnet-5","provider":"firstParty"}},"permission_denials":[],"terminal_reason":"api_error","fast_mode_state":"off","fast_mode_disabled_reason":"sdk_opt_in_required","subtype":"success","api_error_status":null,"result":"API Error: Your computer went to sleep mid-response. The response above may be incomplete.","type":"result","duration_ms":163362,"uuid":"ff7af09c-5185-4c51-a700-d6b4a40996fc"}`

func TestClaudeCodeTransient(t *testing.T) {
	if !(worker.ClaudeCode{}).Transient(worker.Result{Output: claudeAPIErrorSample}) {
		t.Error("Transient = false on a captured api_error result; this is the failure the retry exists for")
	}
}

func TestClaudeCodeTransientWithNoiseAroundTheLine(t *testing.T) {
	// stderr shares the captured Tail, exactly as in the ParseUsage case.
	noisy := "warning: something on stderr\n" + claudeAPIErrorSample + "\ntrailing chatter\n"
	if !(worker.ClaudeCode{}).Transient(worker.Result{Output: noisy}) {
		t.Error("Transient = false with noise around the result line; the scan is line by line for exactly this reason")
	}
}

// Every one of these must read as "not transient", because that is the
// answer that parks — the behavior wand already had. A wrong false costs one
// park; a wrong true respawns a worker over a real failure.
func TestClaudeCodeTransientFailsSoft(t *testing.T) {
	for name, output := range map[string]string{
		"Empty":               "",
		"PlainText":           "the CLI crashed before writing anything structured",
		"NotJSON":             "{not json at all",
		"CompletedNormally":   claudeSample,
		"OtherTerminalReason": `{"type":"result","terminal_reason":"refusal"}`,
		"NoTerminalReason":    `{"type":"result","is_error":true}`,
		// The reason on something that is not the result record. A
		// transcript event echoing the string must not be mistaken for the
		// harness's own verdict on the turn.
		"ReasonOnAnotherRecord": `{"type":"assistant","terminal_reason":"api_error"}`,
		// The prose is deliberately not consulted: it is a human-facing
		// sentence, free to change between releases.
		"ProseOnly": `{"type":"result","result":"API Error: Your computer went to sleep mid-response."}`,
	} {
		t.Run(name, func(t *testing.T) {
			if (worker.ClaudeCode{}).Transient(worker.Result{Output: output}) {
				t.Errorf("Transient(%q) = true, want false — a parse miss must park, never respawn", output)
			}
		})
	}
}
