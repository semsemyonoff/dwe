package cmdbrowser

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// focus identifies which panel currently receives input. Future tasks add
// focusFilter and focusInspect; Task 4 ships left/right only.
type focus int

const (
	focusLeft focus = iota
	focusRight
)

// Width buckets per §4.1 of the spec. width < 60 is handled by the fallback
// before the model is ever built, so it does not appear here.
const (
	minTwoPanelWidth     = 60
	reducedTwoPanelWidth = 80
	fullTwoPanelWidth    = 100
)

// Model is the bubbletea program backing cmdbrowser.Run. Owns the tree (left
// panel) and a bubbles/v2/list (right panel). Filter / inspect overlays land
// in Task 5.
type Model struct {
	title    string
	items    []Item
	opts     Options
	keys     keymap
	width    int
	height   int
	focus    focus
	tree     *treeModel
	list     list.Model
	delegate *cmdDelegate

	cancelled bool
	result    Result
}

// Compile-time guarantee that Model satisfies tea.Model. bubbletea/v2 is
// still maturing — lock the contract.
var _ tea.Model = (*Model)(nil)

func newModel(title string, items []Item, opts Options, w, h int) *Model {
	tm := newTreeModel(items, opts.IncludePrivate, opts.DefaultExpandedDepth)
	dlg := newCmdDelegate(rightWidth(w), showBadges(w) && opts.ShowTypeBadges)
	l := list.New(nil, dlg, rightWidth(w), max(h-3, 3))
	l.SetShowTitle(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	m := &Model{
		title:    title,
		items:    items,
		opts:     opts,
		keys:     defaultKeymap(),
		width:    w,
		height:   h,
		focus:    focusLeft,
		tree:     tm,
		list:     l,
		delegate: dlg,
	}
	m.refreshList()
	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.applyLayout()
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
		if m.focus == focusLeft {
			return m.updateLeft(msg)
		}
		return m.updateRight(msg)
	}
	return m, nil
}

// updateLeft routes keypresses to the tree when the left panel is focused.
func (m *Model) updateLeft(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		m.tree.moveUp()
		m.refreshList()
	case key.Matches(msg, m.keys.Down):
		m.tree.moveDown()
		m.refreshList()
	case key.Matches(msg, m.keys.Left):
		m.tree.onLeft()
		m.refreshList()
	case key.Matches(msg, m.keys.Right):
		m.tree.onRight()
		m.refreshList()
	case key.Matches(msg, m.keys.Home):
		m.tree.moveHome()
		m.refreshList()
	case key.Matches(msg, m.keys.End):
		m.tree.moveEnd()
		m.refreshList()
	case key.Matches(msg, m.keys.Space):
		m.tree.toggleFocused()
	case key.Matches(msg, m.keys.Enter):
		// §7.1: collapsed group with children → expand AND focus right;
		// expanded or leaf group → just focus right.
		n := m.tree.focusedNode()
		if n != nil && n != m.tree.root && len(n.children) > 0 && !m.tree.expanded[n.id] {
			m.tree.expanded[n.id] = true
			m.tree.rebuildVisible()
		}
		m.refreshList()
		if len(m.visibleListItems()) > 0 {
			m.focus = focusRight
		}
	}
	return m, nil
}

// updateRight handles right-panel input: Enter selects, Left returns focus to
// the tree, all other keys are forwarded to the embedded list model.
func (m *Model) updateRight(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Left) {
		m.focus = focusLeft
		return m, nil
	}
	if key.Matches(msg, m.keys.Enter) {
		if it, ok := m.list.SelectedItem().(listItem); ok {
			m.result = Result{Idx: it.origIdx, Action: actionForMode(m.opts.Mode)}
			return m, tea.Quit
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// actionForMode maps Mode → Action so Result carries the right intent for the
// caller. Mode is normalised by Options.applyDefaults before reaching here.
func actionForMode(mode Mode) Action {
	if mode == ModeInspect {
		return ActionInspect
	}
	return ActionRun
}

// refreshList rebuilds the list items from the currently focused tree group.
// Called after any tree mutation that changes the focused group.
func (m *Model) refreshList() {
	idxs := m.tree.itemsForFocus()
	out := make([]list.Item, 0, len(idxs))
	for _, idx := range idxs {
		it := m.items[idx]
		out = append(out, listItem{origIdx: idx, id: it.ID, desc: it.Description, typ: it.Type})
	}
	m.list.SetItems(out)
}

// visibleListItems returns the current list contents — used to decide whether
// pressing Enter on a group is worth focus-switching to.
func (m *Model) visibleListItems() []list.Item { return m.list.Items() }

// applyLayout pushes the current width/height to the list delegate and the
// list model so resize is reflected in the next render.
func (m *Model) applyLayout() {
	rw := rightWidth(m.width)
	bh := max(m.height-3, 3)
	m.delegate.width = rw
	m.delegate.showBadges = showBadges(m.width) && m.opts.ShowTypeBadges
	m.list.SetSize(rw, bh)
}

// View implements tea.Model. AltScreen is set on the View so bubbletea hides
// the caller's previous output for the duration of the program.
func (m *Model) View() tea.View {
	lw := leftWidth(m.width)
	rw := rightWidth(m.width)
	bodyHeight := max(m.height-3, 3)

	border := lipgloss.NormalBorder()
	leftStyle := lipgloss.NewStyle().Border(border).Width(lw).Height(bodyHeight)
	rightStyle := lipgloss.NewStyle().Border(border).Width(rw).Height(bodyHeight)

	if m.focus == focusLeft {
		leftStyle = leftStyle.BorderForeground(lipgloss.Color("12"))
	} else {
		rightStyle = rightStyle.BorderForeground(lipgloss.Color("12"))
	}

	leftBody := "groups\n" + m.tree.renderOpt(m.focus == focusLeft, showCounts(m.width))
	rightBody := m.breadcrumb() + "\n" + m.list.View()
	leftPanel := leftStyle.Render(leftBody)
	rightPanel := rightStyle.Render(rightBody)

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
	titleBar := lipgloss.NewStyle().Bold(true).Render(m.title)
	footer := lipgloss.NewStyle().Faint(true).Render("tab: switch · enter: select · esc/q: quit")

	content := strings.Join([]string{titleBar, body, footer}, "\n")

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// breadcrumb formats the focused group's full path and item count for the
// right-panel header. Root group shows as "(root)"; nested groups use " › "
// separators so the path is unambiguous regardless of width.
func (m *Model) breadcrumb() string {
	n := m.tree.focusedNode()
	path := "(root)"
	if n != nil && n.id != "" {
		path = strings.ReplaceAll(n.id, ".", " › ")
	}
	count := len(m.list.Items())
	noun := "commands"
	if count == 1 {
		noun = "command"
	}
	header := lipgloss.NewStyle().Bold(true).Render(path)
	tail := lipgloss.NewStyle().Faint(true).Render(" · " + intStr(count) + " " + noun)
	return header + tail
}

// intStr is a tiny strconv.Itoa replacement that keeps imports light.
func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Width bucket helpers — keep the layout rules in one place. The fallback
// path ensures width is always at least minTwoPanelWidth.

func leftWidth(w int) int {
	return max(w/3, 20)
}

func rightWidth(w int) int {
	return max(w-leftWidth(w)-2, 10)
}

// showBadges returns true for the full two-panel bucket (≥ 100 cols).
func showBadges(w int) bool { return w >= fullTwoPanelWidth }

// showCounts mirrors showBadges — the (N) per-group counts hide at 80–99 cols
// alongside the type badges (per §4.1).
func showCounts(w int) bool { return w >= fullTwoPanelWidth }
