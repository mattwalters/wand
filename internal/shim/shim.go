// Package shim generates the harness hook entry that routes Linear writes
// through wand guard.
//
// The shim is a build artifact of the covenant: wand init regenerates it,
// nobody hand-edits it. The guard's verdict logic lives once, in the wand
// binary; this package only ensures the harness actually calls it — a guard
// that decides correctly but is never invoked protects nothing.
package shim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// Matcher routes tool calls to the guard. Deliberately loose in the
	// middle: the harness addresses the Linear MCP server as
	// mcp__<connector-id>__save_issue, and the id changes when Linear is
	// reconnected. A matcher pinned to today's id would go dead silently,
	// which for a guard means allowing everything with no sign of it.
	Matcher = "mcp__.*__save_issue"

	// Command assumes wand is on PATH, which is what make install and
	// go install arrange. Not an absolute path — settings.json is committed
	// and shared across machines.
	Command = "wand guard"
)

type settingsHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type hookEntry struct {
	Matcher string         `json:"matcher"`
	Hooks   []settingsHook `json:"hooks"`
}

// Ensure returns Claude settings bytes carrying the guard hook entry.
func Ensure(existing []byte) (out []byte, changed bool, err error) {
	return ensure(existing, "settings file")
}

// ensure merges the guard into either harness's JSON hook document. It holds
// every layer as raw JSON until the one PreToolUse entry it owns is replaced,
// so fields added by a harness (or another tool) cannot be silently dropped.
// existing may be nil or whitespace-only. changed is false when the entry is
// already exactly present, in which case out is the input, byte-identical.
func ensure(existing []byte, name string) (out []byte, changed bool, err error) {
	desired, err := json.Marshal(hookEntry{
		Matcher: Matcher,
		Hooks:   []settingsHook{{Type: "command", Command: Command}},
	})
	if err != nil {
		return nil, false, err
	}

	// Each layer is unmarshalled only as deep as the edit needs, with
	// everything else held as raw bytes, so unrelated settings survive.
	top := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &top); err != nil {
			return nil, false, fmt.Errorf("%s is not a JSON object: %w", name, err)
		}
	}

	hooks := map[string]json.RawMessage{}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, false, fmt.Errorf("%s \"hooks\" is not an object: %w", name, err)
		}
	}

	var pre []json.RawMessage
	if raw, ok := hooks["PreToolUse"]; ok {
		if err := json.Unmarshal(raw, &pre); err != nil {
			return nil, false, fmt.Errorf("%s \"hooks.PreToolUse\" is not an array: %w", name, err)
		}
	}

	// The shim is whichever entry runs wand guard, however stale its shape;
	// it is regenerated in place rather than accumulating variants.
	at := -1
	for i, raw := range pre {
		var entry hookEntry
		if json.Unmarshal(raw, &entry) != nil {
			continue
		}
		for _, h := range entry.Hooks {
			if strings.Contains(h.Command, Command) {
				at = i
				break
			}
		}
		if at >= 0 {
			break
		}
	}

	if at >= 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, pre[at]); err == nil && bytes.Equal(compact.Bytes(), desired) {
			return existing, false, nil
		}
		pre[at] = desired
	} else {
		pre = append(pre, desired)
	}

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

	out, err = json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}
