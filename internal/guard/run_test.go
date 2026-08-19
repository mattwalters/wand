package guard

import (
	"strings"
	"testing"
)

func TestRunBlocksWithReasonOnStderr(t *testing.T) {
	var stderr strings.Builder
	in := `{"tool_name":"` + linearTool + `","tool_input":{"id":"WND-1","state":"Todo"},"cwd":"/tmp"}`

	if code := Run(strings.NewReader(in), &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if got := stderr.String(); !strings.Contains(got, "Blocked by wand guard") {
		t.Errorf("stderr does not say who blocked it:\n%s", got)
	} else if !strings.Contains(got, "In Progress") {
		t.Errorf("stderr does not carry the reason:\n%s", got)
	}
}

func TestRunAllowsWithSilence(t *testing.T) {
	var stderr strings.Builder
	in := `{"tool_name":"` + linearTool + `","tool_input":{"id":"WND-1","state":"In Review"}}`

	if code := Run(strings.NewReader(in), &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("an allow should write nothing to stderr, got:\n%s", stderr.String())
	}
}

// Failing open is deliberate: a broken guard must never wedge a session.
func TestRunFailsOpen(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"unparseable stdin", "not json"},
		{"empty stdin", ""},
		{"tool_input is a string", `{"tool_name":"` + linearTool + `","tool_input":"state: Todo"}`},
		{"missing tool_input", `{"tool_name":"` + linearTool + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr strings.Builder
			if code := Run(strings.NewReader(tt.in), &stderr); code != 0 {
				t.Fatalf("exit code = %d, want 0 (fail open); stderr:\n%s", code, stderr.String())
			}
		})
	}
}
