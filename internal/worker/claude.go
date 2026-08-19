package worker

import "path/filepath"

// ClaudeCode spawns workers through the Claude Code CLI (`claude -p`).
//
// The harness-specific isolation work happens here, because every harness
// leaks differently. Claude Code's leak paths, and how each is closed:
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
//   - gh keeps its own token in its config directory, outside the
//     environment, so stripping GH_TOKEN is not enough. GH_CONFIG_DIR is
//     pointed into the run's scratch directory, where no token lives:
//     gh runs unauthenticated.
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

	env := make([]string, 0, len(environ)+1)
	env = append(env, environ...)
	env = append(env, "GH_CONFIG_DIR="+filepath.Join(spec.ScratchDir, "gh-config"))

	return Invocation{Argv: argv, Env: env, Dir: spec.Dir, Stdin: prompt}, nil
}
