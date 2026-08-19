package worker

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

func (c ClaudeCode) Invocation(spec Spec, prompt string, environ []string) (Invocation, error) {
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	argv := []string{
		bin, "-p",
		"--output-format", "text",
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
