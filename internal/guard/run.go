package guard

import (
	"encoding/json"
	"fmt"
	"io"
)

// Run implements the PreToolUse hook protocol: read the pending tool call as
// JSON on stdin, exit 2 with an explanation on stderr to block it, any other
// exit code allows it. Run returns the exit code rather than exiting so the
// whole protocol, block path included, is testable in-process; the cobra
// layer turns the return into the real exit.
//
// Failing open is deliberate — a broken guard must never wedge a session. Any
// input that does not decode (garbage, empty stdin, a tool_input that is not
// an object) allows.
func Run(stdin io.Reader, stderr io.Writer) int {
	var payload struct {
		ToolName  string         `json:"tool_name"`
		ToolInput map[string]any `json:"tool_input"`
	}
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		return 0
	}

	reason, blocked := Evaluate(payload.ToolName, payload.ToolInput)
	if !blocked {
		return 0
	}
	fmt.Fprintf(stderr, "Blocked by wand guard\n\n%s\n", reason)
	return 2
}
