package screen

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// namedKeys maps the names accepted in a key script to their key codes.
// Names match what Bubble Tea's Key.String() produces, so a binding declared
// as key.WithKeys("esc") is driven by writing "esc" in a script.
var namedKeys = map[string]rune{
	"enter":     tea.KeyEnter,
	"return":    tea.KeyReturn,
	"esc":       tea.KeyEsc,
	"escape":    tea.KeyEscape,
	"tab":       tea.KeyTab,
	"space":     tea.KeySpace,
	"backspace": tea.KeyBackspace,
	"delete":    tea.KeyDelete,
	"up":        tea.KeyUp,
	"down":      tea.KeyDown,
	"left":      tea.KeyLeft,
	"right":     tea.KeyRight,
	"home":      tea.KeyHome,
	"end":       tea.KeyEnd,
	"pgup":      tea.KeyPgUp,
	"pgdown":    tea.KeyPgDown,
}

// ParseKey turns a single key name into a key press message.
//
// It accepts a named key ("enter", "esc", "down"), a single character ("j"),
// or either with modifier prefixes ("ctrl+c", "shift+tab").
func ParseKey(name string) (tea.KeyPressMsg, error) {
	if name == "" {
		return tea.KeyPressMsg{}, fmt.Errorf("empty key name")
	}

	var mod tea.KeyMod
	for {
		prefix, rest, found := strings.Cut(name, "+")
		if !found || rest == "" {
			break
		}
		switch strings.ToLower(prefix) {
		case "ctrl":
			mod |= tea.ModCtrl
		case "alt":
			mod |= tea.ModAlt
		case "shift":
			mod |= tea.ModShift
		default:
			return tea.KeyPressMsg{}, fmt.Errorf("unknown modifier %q in key %q", prefix, name)
		}
		name = rest
	}

	if code, ok := namedKeys[strings.ToLower(name)]; ok {
		key := tea.KeyPressMsg{Code: code, Mod: mod}
		// Space is the one named key that also inserts a character, and a
		// script driving a text field needs it to: without Text set, a
		// scripted "space" moves nothing and types nothing, which reads
		// as the script being wrong rather than the parser.
		if code == tea.KeySpace && mod == 0 {
			key.Text = " "
		}
		return key, nil
	}

	runes := []rune(name)
	if len(runes) != 1 {
		return tea.KeyPressMsg{}, fmt.Errorf("unknown key %q", name)
	}

	key := tea.KeyPressMsg{Code: runes[0], Mod: mod}
	// Text is what a printable keypress actually inserts. Modified keys are
	// not printable, so they carry no text.
	if mod == 0 {
		key.Text = string(runes)
	}
	return key, nil
}

// ParseScript turns a comma-separated key script such as "j,j,enter" into
// messages ready to feed to Render.
//
// The same parser backs `wand ui --script` and the test harness, so a
// reproduction typed at a shell is the same input the test replays.
func ParseScript(script string) ([]tea.Msg, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return nil, nil
	}

	parts := strings.Split(script, ",")
	msgs := make([]tea.Msg, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, err := ParseKey(part)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, key)
	}
	return msgs, nil
}
