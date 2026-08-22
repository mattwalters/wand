package worker

import (
	"bytes"
	"encoding/json"
	"errors"
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
		// stream-json (with --verbose, which it requires) is every event of
		// the turn — assistant text, tool_use, tool_result — not just the
		// final answer, so what Run captures to Spec.TranscriptPath carries
		// the worker's reasoning, not only its conclusion. The final line is
		// still a "result" event carrying the same usage/terminal_reason
		// shape --output-format json's single object did, which is what
		// keeps ParseUsage and Transient below working unchanged.
		"--output-format", "stream-json",
		"--verbose",
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

// SchemaInvocation is Invocation plus --json-schema: claude -p takes the
// schema inline (unlike Codex, which takes a file path — see
// Codex.SchemaInvocation), forcing a StructuredOutput tool call validated
// locally before the model is even spawned. The plain file-write half of
// the contract (Compose's "write your result to <path>" line) is left
// untouched: a worker under a schema still may write the file, but nothing
// here depends on it — CollectHandoff below reads the schema-enforced
// result off stdout instead.
func (c ClaudeCode) SchemaInvocation(spec Spec, prompt string, environ []string, schema json.RawMessage) (Invocation, error) {
	inv, err := c.Invocation(spec, prompt, environ)
	if err != nil {
		return Invocation{}, err
	}
	inv.Argv = append(inv.Argv, "--json-schema", string(schema))
	return inv, nil
}

// CollectHandoff reads the handoff back out of the "structured_output"
// field on the stream-json event stream's final "result" event, rather than
// the file collect reads elsewhere.
//
// It gates on structured_output's presence, not on exit code, is_error,
// subtype or terminal_reason: a live probe (2026-08-21, recorded in
// WND-97) found an unsatisfiable schema still exits 0 with is_error false
// and subtype "success" — Claude Code retries internally, gives up, and
// falls back to prose in "result" instead. Every one of those signals reads
// that run as a clean success; only structured_output's absence tells the
// truth. Falling back to the prose in "result" would feed it straight into
// a package's strict handoff parser, which is worse than refusing: a
// schema failure must park, not be misread as a handoff.
func (c ClaudeCode) CollectHandoff(spec Spec, res Result) (json.RawMessage, error) {
	for _, line := range strings.Split(res.Output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var r struct {
			Type             string          `json:"type"`
			StructuredOutput json.RawMessage `json:"structured_output"`
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil || r.Type != "result" {
			continue
		}
		so := bytes.TrimSpace(r.StructuredOutput)
		if len(so) == 0 || string(so) == "null" {
			return nil, errors.New("claude-code: the result carried no structured_output — the schema could not be satisfied, so the worker fell back to prose")
		}
		return validHandoff(so)
	}
	return nil, errors.New("claude-code: no result event found in the output")
}

// claudeUsage is the shape of the "usage" object on the final "result"
// event of claude -p's --output-format stream-json event stream — the same
// shape --output-format json's single result object carried, which is why
// this parser did not need to change when the invocation moved to
// stream-json for WND-45. Field names and presence are the CLI's own,
// recorded from a live probe rather than guessed: run `claude -p
// --output-format stream-json --verbose` against a trivial prompt to see it.
type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// ParseUsage reads the CLI's own usage object out of the final "result"
// line of the stream-json event stream printed to stdout. Anthropic's
// accounting counts freshly-processed, cache-written and cache-read input
// tokens separately; InputTokens is their sum, so it reads as "total input
// tokens processed" the same way Codex's already-total input_tokens does.
//
// stderr shares the captured Tail and could in principle land on the same
// or an adjacent line, and stream-json itself is many JSON lines rather
// than one, so this scans line by line for one that parses and carries a
// top-level usage object — only the result line does; the per-turn
// "assistant" events carry their own usage nested under "message", which
// this intentionally does not match — rather than parsing the whole tail as
// one document. A tail with no such line — a parse miss, a harness upgrade
// that changed the shape, a run that never got that far — yields nil:
// absent, never estimated.
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

// claudeAPIErrorReason is the value claude -p puts in terminal_reason when
// its own turn ended on a provider or transport failure rather than on the
// work. Recorded from a live park, not guessed: run
// WND-68-20260820T101039Z ended an implement phase with
//
//	"terminal_reason":"api_error","subtype":"success","api_error_status":null,
//	"result":"API Error: Your computer went to sleep mid-response. …"
//
// after 1.19M input tokens. The laptop had suspended. Nothing about that
// says anything about the ticket.
const claudeAPIErrorReason = "api_error"

// Transient reports whether claude -p's own JSON result blamed the failure
// on infrastructure. It reads the same result line ParseUsage does, with
// the same discipline — scan line by line, skip anything that does not
// parse, never treat the whole tail as one document — because stderr
// shares the captured Tail and can land beside the JSON.
//
// Only terminal_reason is consulted. The prose in "result" is a
// human-facing sentence that changes freely between releases; matching on
// it would be a string-compare against a changelog. A tail with no
// recognizable result line yields false: not transient, so the caller
// parks. That is the pre-existing behavior, which is what makes a parse
// miss safe.
func (c ClaudeCode) Transient(res Result) bool {
	for _, line := range strings.Split(res.Output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var r struct {
			Type           string `json:"type"`
			TerminalReason string `json:"terminal_reason"`
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		if r.Type == "result" && r.TerminalReason == claudeAPIErrorReason {
			return true
		}
	}
	return false
}
