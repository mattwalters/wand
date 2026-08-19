package shim

import (
	"encoding/json"
	"fmt"
)

// EnsureCodex returns hooks.json bytes carrying the same PreToolUse guard as
// Ensure, in Codex's hooks-file format. Existing hook events and matcher
// groups are preserved. Codex discovers <repo>/.codex/hooks.json alongside
// its project config, so no config.toml edit is needed.
func EnsureCodex(existing []byte) ([]byte, bool, error) {
	top := map[string]json.RawMessage{}
	if len(existing) != 0 {
		if err := json.Unmarshal(existing, &top); err != nil {
			return nil, false, fmt.Errorf("invalid hooks JSON: %w", err)
		}
	}

	hooks := map[string]json.RawMessage{}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, false, fmt.Errorf("hooks is not an object: %w", err)
		}
	}

	var pre []hookEntry
	if raw, ok := hooks["PreToolUse"]; ok {
		if err := json.Unmarshal(raw, &pre); err != nil {
			return nil, false, fmt.Errorf("hooks.PreToolUse is not an array: %w", err)
		}
	}
	for _, entry := range pre {
		if entry.Matcher != Matcher {
			continue
		}
		for _, hook := range entry.Hooks {
			if hook.Type == "command" && hook.Command == Command {
				return existing, false, nil
			}
		}
	}

	pre = append(pre, hookEntry{Matcher: Matcher, Hooks: []settingsHook{{Type: "command", Command: Command}}})
	preRaw, err := json.Marshal(pre)
	if err != nil {
		return nil, false, err
	}
	hooks["PreToolUse"] = preRaw
	hooksRaw, err := json.Marshal(hooks)
	if err != nil {
		return nil, false, err
	}
	top["hooks"] = hooksRaw
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}
