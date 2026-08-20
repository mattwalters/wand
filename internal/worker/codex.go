package worker

import (
	"encoding/json"
	"strings"
)

// Codex spawns workers through the Codex CLI ("codex exec").
//
// Codex normally reads MCP servers from $CODEX_HOME/config.toml. That is a
// credential boundary: the parent's Linear connector would otherwise become a
// tool available to the worker. --ignore-user-config excludes those
// user-configured servers while retaining the CLI's login material. Plugins
// may still contribute their own MCP servers, so the adapter does not claim a
// blanket "no MCP servers" guarantee. --disable hooks, --ignore-rules, and
// --ephemeral keep a cold worker from inheriting repository hooks, policy
// files, or a resumable session.
//
// The shared ChildEnviron closure has already removed Linear/GitHub/SSH
// credentials and redirected gh's on-disk credentials by the time this
// adapter is called. The sandbox grants writes only to the worktree and the
// per-run scratch directory; --add-dir is therefore the explicit grant for
// the handoff path outside the worktree.
type Codex struct {
	// Bin is the command to run; empty means "codex" from PATH.
	Bin string
}

func (c Codex) Name() string { return "codex" }

// ConformanceSpec uses Codex's configured default model. Claude's haiku
// selection is not a Codex model, so sharing it would make the live proof
// fail before a worker starts. The isolation task and its verdict remain
// identical across adapters.
func (c Codex) ConformanceSpec(spec Spec) Spec {
	spec.Model = ""
	spec.Effort = ""
	return spec
}

func (c Codex) Invocation(spec Spec, prompt string, environ []string) (Invocation, error) {
	bin := c.Bin
	if bin == "" {
		bin = "codex"
	}

	argv := []string{
		bin,
		// These are global Codex flags, and therefore must appear before
		// the exec subcommand (Codex rejects them after it).
		"--sandbox", "workspace-write",
		"--ask-for-approval", "never",
		"exec",
		"--ignore-user-config",
		"--ignore-rules",
		"--disable", "hooks",
		"--ephemeral",
		"--skip-git-repo-check",
		"--json",
		"--cd", spec.Dir,
		"--add-dir", spec.ScratchDir,
	}
	if spec.Model != "" {
		argv = append(argv, "--model", spec.Model)
	}
	if spec.Effort != "" {
		// Codex config uses this name for the per-invocation reasoning
		// selection. -c is used rather than an ambient config so the
		// selection remains part of this Invocation's pure contract.
		argv = append(argv, "--config", "model_reasoning_effort="+spec.Effort)
	}

	return Invocation{Argv: argv, Env: environ, Dir: spec.Dir, Stdin: prompt}, nil
}

// codexUsage is the shape of the "usage" object on a codex exec --json
// turn.completed event. Field names recorded from a live probe rather than
// guessed: run `codex exec --json` against a trivial prompt to see it.
// input_tokens and output_tokens are already the totals for the turn — the
// cached/reasoning breakdowns are subsets of them, not additional tokens —
// matching the convention Claude's own usage object uses once its own
// cache fields are summed in (see ClaudeCode.ParseUsage).
type codexUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// ParseUsage sums the usage carried on every turn.completed event in the
// --json event stream codex exec writes to stdout, one JSON object per
// line. A plain-text banner line ("Reading prompt from stdin...") and any
// stderr sharing the captured Tail are skipped by construction: only lines
// that parse as a turn.completed event with a usage object count. No such
// line — a parse miss, a harness upgrade that changed the shape, a run
// that never got that far — yields nil: absent, never estimated.
func (c Codex) ParseUsage(output string) *Usage {
	var in, out int64
	var found bool
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var ev struct {
			Type  string      `json:"type"`
			Usage *codexUsage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type != "turn.completed" || ev.Usage == nil {
			continue
		}
		in += ev.Usage.InputTokens
		out += ev.Usage.OutputTokens
		found = true
	}
	if !found {
		return nil
	}
	return &Usage{InputTokens: &in, OutputTokens: &out}
}
