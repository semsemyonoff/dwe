package ui

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
)

// ErrCancelled is returned by RunSelector when the user presses Esc or q.
var ErrCancelled = errors.New("cancelled")

// SelectorItem represents one option in the interactive list.
type SelectorItem struct {
	Label       string // display name, e.g. "main", "services.main.migrate"
	Description string // secondary text, e.g. "app-main", "Run migrations"
	Status      string // state indicator: "enabled", "disabled", or ""
	Disabled    bool   // if true, item is shown but not selectable
}

var (
	styleSelectorAccent  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	styleSelectorMuted   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleSelectorEnabled = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleSelectorHint    = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
)

type selectorModel struct {
	title    string
	items    []SelectorItem
	cursor   int
	selected int // -1 = cancelled, >=0 = chosen index
	done     bool
}

// Init satisfies tea.Model; no initial command needed.
func (m selectorModel) Init() tea.Cmd { return nil }

// Update handles key input for navigation, selection, and cancellation.
func (m selectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.String() {
		case "up", "k":
			m.cursor = m.prevSelectable(m.cursor)
		case "down", "j":
			m.cursor = m.nextSelectable(m.cursor)
		case "enter", " ":
			if len(m.items) > 0 && m.cursor >= 0 && m.cursor < len(m.items) &&
				!m.items[m.cursor].Disabled {
				m.selected = m.cursor
				m.done = true
				return m, tea.Quit
			}
		case "esc", "q", "ctrl+c":
			m.selected = -1
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// View renders the selector list.
func (m selectorModel) View() tea.View {
	var b strings.Builder

	if m.title != "" {
		b.WriteString(styleSectionTitle.Render(m.title))
		b.WriteString("\n\n")
	}

	for i, item := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = styleSelectorAccent.Render("> ")
		}

		label := item.Label
		if item.Disabled {
			label = styleSelectorMuted.Render(label)
		} else if i == m.cursor {
			label = styleSelectorAccent.Render(label)
		}

		line := cursor + label

		if item.Description != "" {
			desc := styleSelectorMuted.Render(" " + item.Description)
			line += desc
		}

		if item.Status != "" {
			var statusStr string
			switch item.Status {
			case "enabled":
				statusStr = styleSelectorEnabled.Render(" ✓")
			case "disabled":
				statusStr = styleSelectorMuted.Render(" ○")
			default:
				statusStr = styleSelectorMuted.Render(" " + item.Status)
			}
			line += statusStr
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(styleSelectorHint.Render("↑/↓ navigate  enter select  esc cancel"))
	b.WriteString("\n")

	return tea.NewView(b.String())
}

// prevSelectable returns the nearest selectable index before current (wraps around).
func (m selectorModel) prevSelectable(current int) int {
	n := len(m.items)
	if n == 0 {
		return current
	}
	for i := 1; i <= n; i++ {
		idx := (current - i + n) % n
		if !m.items[idx].Disabled {
			return idx
		}
	}
	return current
}

// nextSelectable returns the nearest selectable index after current (wraps around).
func (m selectorModel) nextSelectable(current int) int {
	n := len(m.items)
	if n == 0 {
		return current
	}
	for i := 1; i <= n; i++ {
		idx := (current + i) % n
		if !m.items[idx].Disabled {
			return idx
		}
	}
	return current
}

// initialCursor returns the first selectable index, or 0 if none.
func initialCursor(items []SelectorItem) int {
	for i, item := range items {
		if !item.Disabled {
			return i
		}
	}
	return 0
}

// RunSelector displays an interactive list selector and returns the index of
// the chosen item. Returns ErrCancelled if the user presses Esc or q.
func RunSelector(title string, items []SelectorItem) (int, error) {
	if len(items) == 0 {
		return -1, fmt.Errorf("selector: no items to display")
	}

	m := selectorModel{
		title:    title,
		items:    items,
		cursor:   initialCursor(items),
		selected: -1,
	}

	p := tea.NewProgram(m)
	final, err := p.Run()
	if err != nil {
		if errors.Is(err, tea.ErrInterrupted) {
			return -1, ErrCancelled
		}
		return -1, err
	}

	result, ok := final.(selectorModel)
	if !ok {
		return -1, fmt.Errorf("selector: unexpected model type %T", final)
	}
	if result.selected < 0 {
		return -1, ErrCancelled
	}
	return result.selected, nil
}
