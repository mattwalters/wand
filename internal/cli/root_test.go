package cli

import (
	"bytes"
	"strings"
	"testing"
)

// runRoot executes the root command with args and returns its combined
// output and error.
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := Root()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// A bare `wand` cannot open the cockpit without a terminal to draw into, so
// outside one it falls back to the same help a bare `wand` printed before
// this command had a RunE at all — a pipe, a script, or CI reads that as
// "what is this", not as a hang.
func TestBareRootWithoutATTYPrintsHelp(t *testing.T) {
	out, err := runRoot(t)
	if err != nil {
		t.Fatalf("wand (no tty): %v", err)
	}
	if !strings.Contains(out, "wand sets up and maintains the agent machinery") {
		t.Errorf("output does not look like help; got:\n%s", out)
	}
}

// A typo must still fail as an unknown command rather than silently opening
// the cockpit — root gaining a RunE must not loosen its Args.
func TestUnknownCommandStillErrors(t *testing.T) {
	_, err := runRoot(t, "bogus")
	if err == nil {
		t.Fatal("wand bogus: want an error, got none")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error = %q, want it to mention \"unknown command\"", err)
	}
}

// `wand help` is cobra's built-in help command; giving root a RunE must not
// shadow it.
func TestHelpCommandPrintsHelp(t *testing.T) {
	out, err := runRoot(t, "help")
	if err != nil {
		t.Fatalf("wand help: %v", err)
	}
	if !strings.Contains(out, "wand sets up and maintains the agent machinery") {
		t.Errorf("output does not look like help; got:\n%s", out)
	}
}

// `wand --help` / `-h` are cobra's built-in help flags, unaffected by root
// gaining a RunE.
func TestHelpFlagPrintsHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			out, err := runRoot(t, flag)
			if err != nil {
				t.Fatalf("wand %s: %v", flag, err)
			}
			if !strings.Contains(out, "wand sets up and maintains the agent machinery") {
				t.Errorf("output does not look like help; got:\n%s", out)
			}
		})
	}
}

func TestIsTTY(t *testing.T) {
	if isTTY(&bytes.Buffer{}) {
		t.Error("a bytes.Buffer is not a terminal")
	}
}
