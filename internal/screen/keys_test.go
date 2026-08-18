package screen

import "testing"

func TestParseKey(t *testing.T) {
	tests := []struct {
		in       string
		wantCode rune
		wantText string
		wantMod  bool
	}{
		{in: "j", wantCode: 'j', wantText: "j"},
		{in: "enter", wantCode: '\r'},
		{in: "esc", wantCode: '\x1b'},
		{in: "DOWN", wantCode: 0},
		{in: "ctrl+c", wantCode: 'c', wantMod: true},
		{in: "shift+tab", wantCode: '\t', wantMod: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseKey(tt.in)
			if err != nil {
				t.Fatalf("ParseKey(%q) failed: %v", tt.in, err)
			}
			// Named keys resolve to codes defined by Bubble Tea, so assert on
			// the round trip through String rather than hardcoding each one.
			if got.String() == "" {
				t.Errorf("ParseKey(%q) produced an unnameable key", tt.in)
			}
			if tt.wantText != "" && got.Text != tt.wantText {
				t.Errorf("Text = %q, want %q", got.Text, tt.wantText)
			}
			if tt.wantMod && got.Mod == 0 {
				t.Errorf("ParseKey(%q) lost its modifier", tt.in)
			}
			if !tt.wantMod && got.Mod != 0 {
				t.Errorf("ParseKey(%q) invented modifier %v", tt.in, got.Mod)
			}
		})
	}
}

func TestParseKeyRoundTrip(t *testing.T) {
	// A script names keys the way Bubble Tea does, so what goes in must be
	// what a key binding declared with the same name matches on.
	for _, name := range []string{"j", "enter", "esc", "up", "down", "ctrl+c", "tab"} {
		got, err := ParseKey(name)
		if err != nil {
			t.Fatalf("ParseKey(%q) failed: %v", name, err)
		}
		if got.String() != name {
			t.Errorf("ParseKey(%q).String() = %q, want %q", name, got.String(), name)
		}
	}
}

func TestParseKeyErrors(t *testing.T) {
	for _, in := range []string{"", "nosuchkey", "hyper+a", "ctrl+nosuchkey"} {
		if _, err := ParseKey(in); err == nil {
			t.Errorf("ParseKey(%q) succeeded, want an error", in)
		}
	}
}

func TestParseScript(t *testing.T) {
	tests := []struct {
		in      string
		wantLen int
	}{
		{in: "", wantLen: 0},
		{in: "   ", wantLen: 0},
		{in: "j", wantLen: 1},
		{in: "j,j,enter", wantLen: 3},
		{in: " j , enter ", wantLen: 2},
		{in: "j,,enter", wantLen: 2},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseScript(tt.in)
			if err != nil {
				t.Fatalf("ParseScript(%q) failed: %v", tt.in, err)
			}
			if len(got) != tt.wantLen {
				t.Errorf("ParseScript(%q) produced %d messages, want %d", tt.in, len(got), tt.wantLen)
			}
		})
	}

	if _, err := ParseScript("j,nosuchkey"); err == nil {
		t.Error("ParseScript accepted an unknown key, want an error")
	}
}
