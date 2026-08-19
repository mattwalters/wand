package worker

// Codex spawns workers through the Codex CLI ("codex exec").
//
// Codex normally reads MCP servers from $CODEX_HOME/config.toml. That is a
// credential boundary: the parent's Linear connector would otherwise become a
// tool available to the worker. --ignore-user-config makes the invocation
// start without that configuration (while still retaining the CLI's own login
// material), so the worker has no inherited MCP servers. The flag is
// deliberately paired with --ignore-rules and --ephemeral: a cold worker must
// not inherit policy files or leave a resumable session behind.
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
		"--ephemeral",
		"--skip-git-repo-check",
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
