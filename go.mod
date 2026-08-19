module github.com/mattwalters/wand

go 1.25.3

// Note: Charm's v2 packages import from charm.land/..., but fang's module is
// still declared as github.com/charmbracelet/fang.
//
// x/vt, x/exp/teatest/v2 and x/exp/golden are untagged pseudo-versions from a
// single charmbracelet/x monorepo commit (68d539dca504). Keep them on the same
// commit — mixing commits across that monorepo breaks the build.
require (
	charm.land/bubbles/v2 v2.1.1
	charm.land/bubbletea/v2 v2.0.8
	charm.land/lipgloss/v2 v2.0.6
	github.com/aymanbagabas/go-udiff v0.4.1
	github.com/charmbracelet/colorprofile v0.4.3
	github.com/charmbracelet/fang v1.0.0
	github.com/charmbracelet/x/exp/teatest/v2 v2.0.0-20260816001655-68d539dca504
	github.com/charmbracelet/x/vt v0.0.0-20260816001655-68d539dca504
	github.com/charmbracelet/x/xpty v0.1.4
	github.com/spf13/cobra v1.10.2
)

require github.com/BurntSushi/toml v1.6.0

require (
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260811164956-006e29f97886 // indirect
	github.com/charmbracelet/x/ansi v0.11.8 // indirect
	github.com/charmbracelet/x/conpty v0.2.0 // indirect
	github.com/charmbracelet/x/exp/charmtone v0.0.0-20250603201427-c31516f43444 // indirect
	github.com/charmbracelet/x/exp/golden v0.0.0-20260816001655-68d539dca504 // indirect
	github.com/charmbracelet/x/exp/ordered v0.1.0 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.24 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/mango v0.1.0 // indirect
	github.com/muesli/mango-cobra v1.2.0 // indirect
	github.com/muesli/mango-pflag v0.1.0 // indirect
	github.com/muesli/roff v0.1.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sahilm/fuzzy v0.1.3 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)
