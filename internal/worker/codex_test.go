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
