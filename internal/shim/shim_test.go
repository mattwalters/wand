package shim

import (
	"bytes"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/mattwalters/wand/internal/guard"
)

// decode pulls the parts of the settings a test asserts on.
func decode(t *testing.T, data []byte) (top map[string]json.RawMessage, pre []hookEntry) {
	t.Helper()
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("output is not a JSON object: %v\n%s", err, data)
	}
	var hooks map[string]json.RawMessage
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			t.Fatalf("hooks is not an object: %v", err)
		}
	}
	if raw, ok := hooks["PreToolUse"]; ok {
		if err := json.Unmarshal(raw, &pre); err != nil {
			t.Fatalf("PreToolUse is not an array: %v", err)
		}
	}
	return top, pre
}

func findShim(pre []hookEntry) *hookEntry {
	for i := range pre {
		for _, h := range pre[i].Hooks {
			if h.Command == Command {
				return &pre[i]
			}
		}
	}
	return nil
}

func TestEnsureFromNothing(t *testing.T) {
	out, changed, err := Ensure(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("a fresh settings file is a change")
	}
	_, pre := decode(t, out)
	entry := findShim(pre)
	if entry == nil {
		t.Fatalf("no shim entry in output:\n%s", out)
	}
	if entry.Matcher != Matcher {
		t.Errorf("matcher = %q, want %q", entry.Matcher, Matcher)
	}
	if len(entry.Hooks) != 1 || entry.Hooks[0].Type != "command" {
		t.Errorf("hooks = %+v, want one command hook", entry.Hooks)
	}
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Error("output should end with a newline")
	}
}

// The merge must never cost a user their settings: unrelated keys, other
// PreToolUse entries and other hook events all survive.
func TestEnsurePreservesExistingContent(t *testing.T) {
	existing := []byte(`{
  "$schema": "https://json.schemastore.org/claude-code-settings.json",
  "permissions": {"allow": ["Bash(go test:*)"]},
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "scripts/guard-bash.sh"}]}
    ],
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "scripts/hello.sh"}]}
    ]
  }
}`)
	out, changed, err := Ensure(existing)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("the shim was absent; Ensure should report a change")
	}

	top, pre := decode(t, out)
	for _, key := range []string{"$schema", "permissions"} {
		if _, ok := top[key]; !ok {
			t.Errorf("unrelated top-level key %q was lost", key)
		}
	}
	var hooks map[string]json.RawMessage
	if err := json.Unmarshal(top["hooks"], &hooks); err != nil {
		t.Fatal(err)
	}
	if _, ok := hooks["SessionStart"]; !ok {
		t.Error("unrelated hook event SessionStart was lost")
	}
	if len(pre) != 2 {
		t.Fatalf("PreToolUse has %d entries, want the Bash guard plus the shim", len(pre))
	}
	if pre[0].Matcher != "Bash" {
		t.Errorf("existing Bash entry was displaced; first entry matches %q", pre[0].Matcher)
	}
	if findShim(pre) == nil {
		t.Error("shim entry missing after merge")
	}
}

// Ensure on its own output reports no change, byte-identically — this is
// what init's readback verification leans on.
func TestEnsureIsIdempotent(t *testing.T) {
	first, _, err := Ensure(nil)
	if err != nil {
		t.Fatal(err)
	}
	second, changed, err := Ensure(first)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second Ensure reports a change")
	}
	if !bytes.Equal(first, second) {
		t.Errorf("second Ensure altered bytes:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// The shim is a build artifact: a stale variant is regenerated in place, not
// left alone and not accumulated alongside a second copy.
func TestEnsureRegeneratesStaleShim(t *testing.T) {
	stale := []byte(`{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "scripts/guard-bash.sh"}]},
      {"matcher": "mcp__old-id__save_issue", "hooks": [{"type": "command", "command": "wand guard --legacy"}]}
    ]
  }
}`)
	out, changed, err := Ensure(stale)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("a stale shim should be regenerated")
	}
	_, pre := decode(t, out)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse has %d entries, want 2 (no duplicate shim)", len(pre))
	}
	if pre[0].Matcher != "Bash" {
		t.Errorf("unrelated entry displaced; first entry matches %q", pre[0].Matcher)
	}
	if pre[1].Matcher != Matcher || pre[1].Hooks[0].Command != Command {
		t.Errorf("stale shim not regenerated in place: %+v", pre[1])
	}
}

// Malformed settings are a human's problem to fix; silently replacing the
// file would trade a parse error for data loss.
func TestEnsureRefusesMalformedSettings(t *testing.T) {
	if _, _, err := Ensure([]byte(`{"hooks": `)); err == nil {
		t.Fatal("malformed settings should error, not be clobbered")
	}
}

// Everything above proves the entry is written; none of it proves the entry
// routes the right calls. The matcher is a regex over tool names whose
// connector id changes on reconnect — ported from the reference suite's
// settings-wiring cases.
func TestMatcherRoutesTheGuardedTool(t *testing.T) {
	matcher, err := regexp.Compile(Matcher)
	if err != nil {
		t.Fatalf("Matcher does not compile: %v", err)
	}

	const (
		linearTool      = "mcp__79e4e202-24ab-40dc-b0d9-91f6f3b6201f__save_issue"
		reconnectedTool = "mcp__00000000-1111-2222-3333-444444444444__save_issue"
	)
	if !matcher.MatchString(linearTool) {
		t.Error("matcher misses this install's save_issue")
	}
	if !matcher.MatchString(reconnectedTool) {
		t.Error("matcher does not survive a reconnected connector id")
	}
	if matcher.MatchString("mcp__79e4e202-24ab-40dc-b0d9-91f6f3b6201f__save_comment") {
		t.Error("matcher swallows save_comment")
	}

	// The guard fires only where the harness routes to it: every tool name
	// the guard would block on must be one the matcher sends its way. (The
	// reverse need not hold — the matcher may be looser; the guard then
	// allows, which fails open.)
	for _, tool := range []string{linearTool, reconnectedTool, "save_issue", "mcp__x__save_comment", "Bash"} {
		_, blocked := guard.Evaluate(tool, map[string]any{"state": "Todo"})
		if blocked && !matcher.MatchString(tool) {
			t.Errorf("guard blocks %q but the matcher never routes it there", tool)
		}
	}
}
