// Package tui holds wand's Bubble Tea models.
//
// Everything here obeys the determinism rules in CLAUDE.md: Update is a pure
// function of (model, msg), View is a pure function of model state, and
// neither reads the clock, the filesystem, or the network. That is what lets
// the golden-screen harness produce byte-stable output.
package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mattwalters/wand/internal/theme"
)

// state is which screen the app is currently showing.
type state int

const (
	stateMenu state = iota
	stateDetail
)

// Command is one of wand's top-level verbs.
type Command struct {
	Name string
	Desc string
}

// Title, Description and FilterValue implement list.DefaultItem.
func (c Command) Title() string       { return c.Name }
func (c Command) Description() string { return c.Desc }
func (c Command) FilterValue() string { return c.Name }

// commands is the set of verbs wand exposes. All three are stubs for now.
var commands = []Command{
	{Name: "init", Desc: "Set up wand in this repository"},
	{Name: "covenant", Desc: "Read and edit the repository covenant"},
	{Name: "bless", Desc: "Promote work along the blessing path"},
}

// Model is the root model. It routes between the menu and the detail screen.
type Model struct {
	state    state
	list     list.Model
	keys     keyMap
	theme    theme.Theme
	selected Command
	width    int
	height   int
}

// New returns the root model sized for the given terminal dimensions.
func New(width, height int) Model {
	items := make([]list.Item, 0, len(commands))
	for _, c := range commands {
		items = append(items, c)
	}

	delegate := list.NewDefaultDelegate()
	l := list.New(items, delegate, width, listHeight(height))
	l.Title = "wand"

	// Every one of these is chrome that would add noise to golden screens
	// without exercising anything we care about. The help line below is
	// rendered by us instead, so it stays in one place.
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowPagination(false)
	l.SetShowHelp(false)

	return Model{
		state:  stateMenu,
		list:   l,
		keys:   defaultKeyMap(),
		theme:  theme.New(),
		width:  width,
		height: height,
	}
}

// listHeight reserves room for the help line beneath the list.
func listHeight(total int) int {
	h := total - 2
	if h < 1 {
		h = 1
	}
	return h
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

// Update implements tea.Model. It is pure: no I/O, no clock, no randomness.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, listHeight(msg.Height))
		return m, nil

	case tea.KeyPressMsg:
		// Quit works from anywhere.
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}

		switch m.state {
		case stateDetail:
			if key.Matches(msg, m.keys.Back) {
				m.state = stateMenu
			}
			return m, nil

		case stateMenu:
			if key.Matches(msg, m.keys.Select) {
				if c, ok := m.list.SelectedItem().(Command); ok {
					m.selected = c
					m.state = stateDetail
				}
				return m, nil
			}
		}
	}

	// Anything else in the menu belongs to the list (navigation, mostly).
	if m.state == stateMenu {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	return m, nil
}

// View implements tea.Model. It is a pure function of model state.
func (m Model) View() tea.View {
	var body string
	switch m.state {
	case stateDetail:
		body = m.detailView()
	default:
		body = m.menuView()
	}

	v := tea.NewView(body)
	v.AltScreen = true
	return v
}

func (m Model) menuView() string {
	return strings.Join([]string{
		m.list.View(),
		m.helpView("↑/k up", "↓/j down", "enter select", "q quit"),
	}, "\n")
}

func (m Model) detailView() string {
	title := m.theme.Title.Render("wand " + m.selected.Name)
	desc := m.theme.Body.Render(m.selected.Desc)
	stub := m.theme.Muted.Render(
		fmt.Sprintf("%q is not implemented yet.", m.selected.Name),
	)

	return strings.Join([]string{
		title,
		"",
		lipgloss.NewStyle().PaddingLeft(2).Render(desc),
		"",
		lipgloss.NewStyle().PaddingLeft(2).Render(stub),
		"",
		m.helpView("esc back", "q quit"),
	}, "\n")
}

func (m Model) helpView(items ...string) string {
	return m.theme.Muted.Render("  " + strings.Join(items, " • "))
}
