package cli

import (
	"bytes"
	"strings"
	"testing"
)

// runUI executes the ui command with args and returns its stdout and error.
func runUI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newUICmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// The dump path must render without a key, without a team, and without a
// network: it is how an agent looks at a screen it cannot see.
func TestDumpScreenNeedsNothing(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")

	out, err := runUI(t, "--dump-screen")
	if err != nil {
		t.Fatalf("ui --dump-screen: %v", err)
	}
	for _, want := range []string{"wand cockpit", "Triage", "Needs Input", "Ready for human", "Lanes", sampleNotice} {
		if !strings.Contains(out, want) {
			t.Errorf("dump is missing %q; screen was:\n%s", want, out)
		}
	}
}

// The blessing screen is the one most worth being able to inspect, and the
// dump has to be able to reach it — while refusing, on that screen, to
// write.
func TestDumpScreenReachesTheBlessingScreenAndRefuses(t *testing.T) {
	out, err := runUI(t, "--dump-screen", "--script", "t")
	if err != nil {
		t.Fatalf("ui --dump-screen --script t: %v", err)
	}
	if !strings.Contains(out, "Blessing") {
		t.Errorf("script did not reach the blessing screen; screen was:\n%s", out)
	}
	if !strings.Contains(out, "read-only") {
		t.Errorf("the blessing screen does not say it cannot write; screen was:\n%s", out)
	}
	if strings.Contains(out, "enter bless") {
		t.Errorf("the dump offers a working confirm key; screen was:\n%s", out)
	}
}

func TestUIFlagContract(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "script without dump",
			args: []string{"--script", "j"},
			want: "only applies with --dump-screen",
		},
		{
			name: "team key with dump",
			args: []string{"--dump-screen", "--team-key", "WND"},
			want: "does not apply",
		},
		{
			name: "team key with sample",
			args: []string{"--sample", "--team-key", "WND"},
			want: "does not apply",
		},
		{
			name: "unparseable script",
			args: []string{"--dump-screen", "--script", "ctrl+nope"},
			want: "could not parse --script",
		},
		{
			name: "live run without a team key",
			args: nil,
			want: "--team-key is required",
		},
		// An interactive program is sized by its terminal: Bubble Tea's
		// first message is the real window size and overwrites anything
		// passed here. Accepting the flag and then ignoring it is the same
		// silent-ignore --team-key is refused for.
		{
			name: "width without dump",
			args: []string{"--sample", "--width", "60"},
			want: "the --width flag only applies with --dump-screen",
		},
		{
			name: "height without dump",
			args: []string{"--team-key", "WND", "--height", "10"},
			want: "the --height flag only applies with --dump-screen",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runUI(t, tt.args...)
			if err == nil {
				t.Fatal("command succeeded, want a refusal")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

// The size flags still have to work where they do apply, or the refusal
// above would have taken the feature away rather than made it honest.
func TestDumpScreenHonorsTheSizeFlags(t *testing.T) {
	out, err := runUI(t, "--dump-screen", "--width", "60")
	if err != nil {
		t.Fatalf("ui --dump-screen --width 60: %v", err)
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len([]rune(line)) > 60 {
			t.Errorf("line is %d wide, want no more than 60: %q", len([]rune(line)), line)
		}
	}
}
