package shim

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEnsureCodexInstallsAndIsIdempotent(t *testing.T) {
	got, changed, err := EnsureCodex(nil)
	if err != nil || !changed {
		t.Fatalf("EnsureCodex(nil) = changed %v, err %v", changed, err)
	}
	var top struct {
		Hooks struct {
			PreToolUse []hookEntry `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(got, &top); err != nil {
		t.Fatal(err)
	}
	entry := findShim(top.Hooks.PreToolUse)
	if entry == nil {
		t.Fatalf("guard hook missing from %s", got)
	}
	if entry.Matcher != Matcher || len(entry.Hooks) != 1 || entry.Hooks[0].Command != Command {
		t.Errorf("entry = %+v, want guard matcher and command", entry)
	}
	again, changed, err := EnsureCodex(got)
	if err != nil || changed || string(again) != string(got) {
		t.Errorf("second EnsureCodex = changed %v, err %v, bytes changed %v", changed, err, string(again) != string(got))
	}
}

func TestEnsureCodexPreservesOtherHooks(t *testing.T) {
	input := []byte(`{"description":"existing", "hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"scripts/start","timeout":60}]}],"PreToolUse":[{"matcher":"Bash","enabled":true,"hooks":[{"type":"command","command":"scripts/bash","timeout":60,"execution_mode":"async"}]}]}}`)
	got, changed, err := EnsureCodex(input)
	if err != nil || !changed {
		t.Fatalf("EnsureCodex = changed %v, err %v", changed, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(got, &top); err != nil {
		t.Fatal(err)
	}
	var hooks map[string]json.RawMessage
	if err := json.Unmarshal(top["hooks"], &hooks); err != nil {
		t.Fatal(err)
	}
	if _, ok := hooks["SessionStart"]; !ok {
		t.Error("unrelated SessionStart hook was lost")
	}
	var pre []hookEntry
	if err := json.Unmarshal(hooks["PreToolUse"], &pre); err != nil {
		t.Fatal(err)
	}
	if len(pre) != 2 || pre[0].Matcher != "Bash" || findShim(pre) == nil {
		t.Errorf("PreToolUse = %+v, want original group plus guard", pre)
	}
	var original, merged []json.RawMessage
	var inputTop map[string]json.RawMessage
	if err := json.Unmarshal(input, &inputTop); err != nil {
		t.Fatal(err)
	}
	var inputHooks map[string]json.RawMessage
	if err := json.Unmarshal(inputTop["hooks"], &inputHooks); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(inputHooks["PreToolUse"], &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(hooks["PreToolUse"], &merged); err != nil {
		t.Fatal(err)
	}
	var want, compactMerged bytes.Buffer
	if err := json.Compact(&want, original[0]); err != nil {
		t.Fatal(err)
	}
	if err := json.Compact(&compactMerged, merged[0]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want.Bytes(), compactMerged.Bytes()) {
		t.Errorf("unrelated Codex hook changed:\nwant %s\n got %s", original[0], merged[0])
	}
}

func TestEnsureCodexRegeneratesStaleShim(t *testing.T) {
	stale := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"scripts/bash"}]},{"matcher":"old__save_issue","hooks":[{"type":"command","command":"wand guard --legacy"}]}]}}`)
	out, changed, err := EnsureCodex(stale)
	if err != nil || !changed {
		t.Fatalf("EnsureCodex = changed %v, err %v", changed, err)
	}
	_, pre := decode(t, out)
	if len(pre) != 2 || pre[0].Matcher != "Bash" {
		t.Fatalf("PreToolUse = %+v, want the Bash hook plus one regenerated shim", pre)
	}
	if entry := findShim(pre); entry == nil || entry.Matcher != Matcher || entry.Hooks[0].Command != Command {
		t.Errorf("stale shim was not regenerated: %+v", entry)
	}
}

func TestEnsureCodexAcceptsWhitespaceOnlyFile(t *testing.T) {
	out, changed, err := EnsureCodex([]byte(" \n\t"))
	if err != nil || !changed {
		t.Fatalf("EnsureCodex(whitespace) = changed %v, err %v", changed, err)
	}
	if _, pre := decode(t, out); findShim(pre) == nil {
		t.Fatalf("no shim in output: %s", out)
	}
}
