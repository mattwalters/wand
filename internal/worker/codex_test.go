package worker_test

import (
	"slices"
	"testing"

	"github.com/mattwalters/wand/internal/worker"
	"github.com/mattwalters/wand/internal/workertest"
)

func TestCodexStructuralConformance(t *testing.T) {
	workertest.Structural(t, worker.Codex{})
}

func TestCodexInvocation(t *testing.T) {
	spec := specFor(t)
	spec.Model = "gpt-5.3-codex"
	spec.Effort = "low"
	prompt := worker.Compose(spec)

	inv, err := worker.Codex{}.Invocation(spec, prompt, []string{"PATH=/usr/bin"})
	if err != nil {
		t.Fatal(err)
	}

	if inv.Argv[0] != "codex" || !slices.Contains(inv.Argv, "exec") {
		t.Errorf("Argv = %v, want a codex exec invocation", inv.Argv)
	}
	if !slices.Contains(inv.Argv, "--json") {
		t.Errorf("not structured output: %v", inv.Argv)
	}
	for _, flag := range []string{"--ignore-user-config", "--ignore-rules", "--ephemeral", "--skip-git-repo-check"} {
		if !slices.Contains(inv.Argv, flag) {
			t.Errorf("cold worker isolation flag %q missing from %v", flag, inv.Argv)
		}
	}
	if !hasFlag(inv.Argv, "--disable", "hooks") {
		t.Errorf("repository hooks are not disabled: %v", inv.Argv)
	}
	if !hasFlag(inv.Argv, "--sandbox", "workspace-write") ||
		!hasFlag(inv.Argv, "--ask-for-approval", "never") ||
		!hasFlag(inv.Argv, "--cd", spec.Dir) ||
		!hasFlag(inv.Argv, "--add-dir", spec.ScratchDir) {
		t.Errorf("sandbox/worktree/scratch contract missing from %v", inv.Argv)
	}
	if !hasFlag(inv.Argv, "--model", "gpt-5.3-codex") ||
		!hasFlag(inv.Argv, "--config", "model_reasoning_effort=low") {
		t.Errorf("model/effort not passed through: %v", inv.Argv)
	}
	if !slices.Contains(inv.Env, "PATH=/usr/bin") {
		t.Errorf("the handed-down environ did not pass through: %v", inv.Env)
	}
	if inv.Dir != spec.Dir || inv.Stdin != prompt {
		t.Errorf("invocation did not preserve the runner's dir/prompt: %+v", inv)
	}
}

func TestCodexDefaultsOmitSelection(t *testing.T) {
	spec := specFor(t)
	inv, err := worker.Codex{}.Invocation(spec, worker.Compose(spec), nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(inv.Argv, "--model") || slices.Contains(inv.Argv, "--config") {
		t.Errorf("empty Model/Effort must fall through to the harness default: %v", inv.Argv)
	}
}

// codexSample is a real `codex exec --json` event stream, captured live
// against a trivial prompt (2026-08-19, codex-cli 0.148.0) — not hand-built,
// so a future CLI upgrade that changes the shape fails this test instead of
// silently going absent in production. The leading plain-text banner line
// is what a real invocation actually prints before the JSONL starts.
const codexSample = `Reading prompt from stdin...
{"type":"thread.started","thread_id":"01a01dd6-142a-76a3-b133-c49e0eac61e4"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"pong"}}
{"type":"turn.completed","usage":{"input_tokens":14101,"cached_input_tokens":9984,"cache_write_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0}}`

func TestCodexParseUsage(t *testing.T) {
	got := worker.Codex{}.ParseUsage(codexSample)
	if got == nil || got.InputTokens == nil || got.OutputTokens == nil {
		t.Fatalf("ParseUsage = %+v, want both fields populated from the sample", got)
	}
	if *got.InputTokens != 14101 {
		t.Errorf("InputTokens = %d, want 14101 (already the turn total, cached tokens included)", *got.InputTokens)
	}
	if *got.OutputTokens != 5 {
		t.Errorf("OutputTokens = %d, want 5", *got.OutputTokens)
	}
}

func TestCodexParseUsageSumsMultipleTurns(t *testing.T) {
	stream := `{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":10}}
{"type":"turn.completed","usage":{"input_tokens":50,"output_tokens":5}}`
	got := worker.Codex{}.ParseUsage(stream)
	if got == nil || *got.InputTokens != 150 || *got.OutputTokens != 15 {
		t.Errorf("ParseUsage = %+v, want summed across every turn.completed event", got)
	}
}

func TestCodexParseUsageAbsentOnGarbage(t *testing.T) {
	for name, output := range map[string]string{
		"Empty":        "",
		"PlainText":    "Reading prompt from stdin...",
		"NoTurnEvent":  `{"type":"thread.started","thread_id":"x"}`,
		"NoUsageField": `{"type":"turn.completed"}`,
		"NotJSON":      "{not json at all",
	} {
		t.Run(name, func(t *testing.T) {
			if got := (worker.Codex{}).ParseUsage(output); got != nil {
				t.Errorf("ParseUsage(%q) = %+v, want nil — absent, never faked", output, got)
			}
		})
	}
}
