package worker

import (
	"encoding/json"
	"strings"
)

// ClaudeCode spawns workers through the Claude Code CLI (`claude -p`).
//
// The harness-specific isolation work happens here, because every harness
// leaks differently. (The harness-agnostic closures — the credential strip,
// the GH_CONFIG_DIR redirect — are ChildEnviron's, applied before this
// adapter runs.) Claude Code's own leak paths, and how each is closed:
//
//   - MCP servers inherited from user or project config would hand the
//     worker the same Linear connector the orchestrator uses.
//     --strict-mcp-config with an empty --mcp-config leaves the worker
//     with no MCP servers at all: nothing to guard, and no dependence on
//     the wand guard shim being installed in the child. (The shim
//     protects interactive sessions, which do carry the connector.)
//
//   - Settings files (user, project, local) can add MCP servers, hooks
//     and permission grants underneath the flags. --setting-sources ""
//     loads none of them; the worker runs on exactly the flags below.
//
// Permission prompts are bypassed. A headless run has no human to answer
// them, and the isolation model here is structural — the credentials and
// connectors are gone before the process starts — not interactive.
// Sessions are not persisted: workers are cold by design, and a resumable
// worker session is a second way for state to leak between phases.
type ClaudeCode struct {
	// Bin is the command to run; empty means "claude" from PATH.
	Bin string
}

func (c ClaudeCode) Name() string { return "claude-code" }

// ConformanceSpec keeps the deliberate live probe inexpensive on Claude.
func (c ClaudeCode) ConformanceSpec(spec Spec) Spec {
	spec.Model = "haiku"
	spec.Effort = "low"
	return spec
}

func (c ClaudeCode) Invocation(spec Spec, prompt string, environ []string) (Invocation, error) {
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	argv := []string{
		bin, "-p",
		"--output-format", "json",
		"--setting-sources", "",
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--no-session-persistence",
		"--permission-mode", "bypassPermissions",
		// The scratch directory sits outside the worktree, and the handoff
		// is written into it.
		"--add-dir", spec.ScratchDir,
	}
	if spec.Model != "" {
		argv = append(argv, "--model", spec.Model)
	}
	if spec.Effort != "" {
		argv = append(argv, "--effort", spec.Effort)
	}

	return Invocation{Argv: argv, Env: environ, Dir: spec.Dir, Stdin: prompt}, nil
}

// claudeUsage is the shape of the "usage" object in claude -p's
// --output-format json result. Field names and presence are the CLI's own,
// recorded from a live probe rather than guessed: run `claude -p
// --output-format json` against a trivial prompt to see it.
type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// ParseUsage reads the CLI's own usage object out of the JSON result
// --output-format json prints to stdout. Anthropic's accounting counts
// freshly-processed, cache-written and cache-read input tokens separately;
// InputTokens is their sum, so it reads as "total input tokens processed"
// the same way Codex's already-total input_tokens does.
//
// The result is one compact JSON object, but stderr shares the captured
// Tail and could in principle land on the same or an adjacent line, so
// this scans line by line for one that parses and carries a usage object,
// rather than parsing the whole tail as one document. A tail with no such
// line — a parse miss, a harness upgrade that changed the shape, a run
// that never got that far — yields nil: absent, never estimated.
func (c ClaudeCode) ParseUsage(output string) *Usage {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var res struct {
			Usage *claudeUsage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(line), &res); err != nil || res.Usage == nil {
			continue
		}
		in := res.Usage.InputTokens + res.Usage.CacheCreationInputTokens + res.Usage.CacheReadInputTokens
		out := res.Usage.OutputTokens
		return &Usage{InputTokens: &in, OutputTokens: &out}
	}
	return nil
}
