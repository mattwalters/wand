package shim

import (
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
	input := []byte(`{"description":"existing", "hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"scripts/start"}]}],"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"scripts/bash"}]}]}}`)
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
}
