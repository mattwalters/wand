package worker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// schemaFileName is where SchemaInvocation writes the shape schema inside
// spec.ScratchDir, so --output-schema (which takes a file path, unlike
// Claude Code's inline --json-schema — see ClaudeCode.SchemaInvocation) can
// read it. Overwritten on every invocation, exactly like the handoff file
// itself, so a stale schema from an earlier phase can never be read by
// mistake.
const schemaFileName = "handoff-schema.json"

// SchemaInvocation is Invocation plus --output-schema and
// --output-last-message. This is nearly drop-in next to Claude Code's: the
// handoff stays file-based, at exactly spec.HandoffPath, so collection does
// not change — CollectHandoff below is the same file read collect does
// without a schema. Only the schema itself needs a place to live, since
// codex exec takes it by path rather than inline.
func (c Codex) SchemaInvocation(spec Spec, prompt string, environ []string, schema json.RawMessage) (Invocation, error) {
	inv, err := c.Invocation(spec, prompt, environ)
	if err != nil {
		return Invocation{}, err
	}
	// Run creates spec.ScratchDir before building the Invocation, but that
	// ordering is Run's own, not a guarantee this method can lean on from
	// elsewhere it might be called — so it makes the directory itself.
	if err := os.MkdirAll(spec.ScratchDir, 0o755); err != nil {
		return Invocation{}, fmt.Errorf("codex: writing schema file: %w", err)
	}
	schemaPath := filepath.Join(spec.ScratchDir, schemaFileName)
	if err := os.WriteFile(schemaPath, schema, 0o644); err != nil {
		return Invocation{}, fmt.Errorf("codex: writing schema file: %w", err)
	}
	// Exec-subcommand flags, like --json above; appending after Invocation's
	// own argv keeps them after "exec" in argv order.
	inv.Argv = append(inv.Argv, "--output-schema", schemaPath, "--output-last-message", spec.HandoffPath)
	return inv, nil
}

// CollectHandoff is the same file-based read collect does without a schema:
// codex exec's --output-last-message keeps writing to spec.HandoffPath
// regardless of --output-schema, so nothing about collection changes.
func (c Codex) CollectHandoff(spec Spec, res Result) (json.RawMessage, error) {
	return collect(spec.HandoffPath)
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
