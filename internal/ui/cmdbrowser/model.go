package cmdbrowser

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// focus identifies which panel currently receives input. Future tasks add
// focusFilter and focusInspect; the skeleton ships left/right only.
type focus int

const (
	focusLeft focus = iota
	focusRight
)

// Model is the bubbletea program backing cmdbrowser.Run. The skeleton owns
// two empty bordered panels; Tasks 3-6 fill in the tree, list, filter, and
// inspect overlay.
type Model struct {
	title  string
	items  []Item
	opts   Options
	keys   keymap
	width  int
	height int
	focus  focus

	cancelled bool
	result    Result
}

// Compile-time guarantee that Model satisfies tea.Model. bubbletea/v2 is
// still maturing — lock the contract.
var _ tea.Model = (*Model)(nil)

func newModel(title string, items []Item, opts Options, w, h int) *Model {
	return &Model{
		title:  title,
		items:  items,
		opts:   opts,
		keys:   defaultKeymap(),
		width:  w,
		height: h,
		focus:  focusLeft,
	}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyPressMsg:
		s := msg.String()
		switch {
		case key.Matches(msg, m.keys.Cancel), key.Matches(msg, m.keys.CtrlC), s == "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		case key.Matches(msg, m.keys.Tab):
			if m.focus == focusLeft {
				m.focus = focusRight
			} else {
				m.focus = focusLeft
			}
			return m, nil
		}
	}
	return m, nil
}

// View implements tea.Model. The skeleton renders two bordered panels with
// a title strip on top. AltScreen is set on the View so bubbletea hides the
// caller's previous output for the duration of the program.
func (m *Model) View() tea.View {
	leftWidth := max(m.width/3, 20)
	rightWidth := max(m.width-leftWidth-2, 10)
	bodyHeight := max(m.height-3, 3)

	border := lipgloss.NormalBorder()
	leftStyle := lipgloss.NewStyle().Border(border).Width(leftWidth).Height(bodyHeight)
	rightStyle := lipgloss.NewStyle().Border(border).Width(rightWidth).Height(bodyHeight)

	if m.focus == focusLeft {
		leftStyle = leftStyle.BorderForeground(lipgloss.Color("12"))
	} else {
		rightStyle = rightStyle.BorderForeground(lipgloss.Color("12"))
	}

	leftPanel := leftStyle.Render(" groups ")
	rightPanel := rightStyle.Render(" commands ")

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
	titleBar := lipgloss.NewStyle().Bold(true).Render(m.title)
	footer := lipgloss.NewStyle().Faint(true).Render("tab: switch · enter: select · esc/q: quit")

	content := strings.Join([]string{titleBar, body, footer}, "\n")

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}
