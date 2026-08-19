package shim

// EnsureCodex returns hooks.json bytes carrying the same PreToolUse guard as
// Ensure, in Codex's hooks-file format. Codex discovers
// <repo>/.codex/hooks.json alongside its project config, so no config.toml
// edit is needed.
func EnsureCodex(existing []byte) ([]byte, bool, error) {
	return ensure(existing, "hooks file")
}
