package cmdbrowser

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// focus identifies which panel currently receives input. Filter and Inspect
// are modal — they pause the underlying panels while active.
type focus int

const (
	focusLeft focus = iota
	focusRight
	focusFilter
	focusInspect
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

	filter       *filterState
	inspect      *inspectState
	skipConfirm  bool
	showFullHelp bool
	help         help.Model
	priorFocus   focus
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
		help:     help.New(),
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
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey routes keypresses based on the active focus mode. Ctrl-C and the
// global help toggle apply in every mode; everything else is mode-specific.
func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.CtrlC) {
		m.cancelled = true
		return m, tea.Quit
	}
	// Inspect captures all keys (modal overlay).
	if m.focus == focusInspect {
		return m.updateInspect(msg)
	}
	if m.focus == focusFilter {
		return m.updateFilter(msg)
	}
	// Global toggles available in left/right modes.
	if key.Matches(msg, m.keys.Help) {
		m.showFullHelp = !m.showFullHelp
		return m, nil
	}
	if key.Matches(msg, m.keys.Filter) {
		m.enterFilter()
		return m, nil
	}
	if key.Matches(msg, m.keys.Inspect) {
		m.openInspect()
		return m, nil
	}
	if key.Matches(msg, m.keys.SkipConfirm) && m.opts.Mode == ModeRun {
		m.skipConfirm = !m.skipConfirm
		return m, nil
	}
	if key.Matches(msg, m.keys.Cancel) {
		m.cancelled = true
		return m, tea.Quit
	}
	if key.Matches(msg, m.keys.Tab) {
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
			m.result = Result{Idx: it.origIdx, Action: actionForMode(m.opts.Mode), SkipConfirm: m.skipConfirm}
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

	focusBorder := lipgloss.Color("12")
	switch m.focus {
	case focusLeft:
		leftStyle = leftStyle.BorderForeground(focusBorder)
	case focusRight, focusFilter, focusInspect:
		rightStyle = rightStyle.BorderForeground(focusBorder)
	}

	leftBody := "groups\n" + m.renderTree()
	rightBody := m.renderRight()

	if m.focus == focusInspect && m.inspect != nil {
		rightBody = "inspect: " + m.items[m.inspect.inspectIdx].ID + "\n" + m.inspect.vp.View()
	}

	leftPanel := leftStyle.Render(leftBody)
	rightPanel := rightStyle.Render(rightBody)

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
	titleBar := lipgloss.NewStyle().Bold(true).Render(m.title)
	if m.skipConfirm && m.opts.Mode == ModeRun {
		titleBar += "  " + lipgloss.NewStyle().Bold(true).Render("[--yes ON]")
	}
	footer := m.renderHelpFooter()

	content := strings.Join([]string{titleBar, body, footer}, "\n")

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// renderTree renders the left-panel tree, switching to filter-aware mode (M/N
// counts, zero-match dimming) when a filter session is active.
func (m *Model) renderTree() string {
	if m.filter != nil {
		return m.tree.renderFilter(m.focus == focusLeft, showCounts(m.width), m.filter)
	}
	return m.tree.renderOpt(m.focus == focusLeft, showCounts(m.width))
}

// renderRight returns the right-panel body. While filter is active the header
// becomes the query prompt and a match count; otherwise it is the breadcrumb.
func (m *Model) renderRight() string {
	if m.filter != nil {
		count := len(m.filter.matched)
		noun := "matches"
		if count == 1 {
			noun = "match"
		}
		header := lipgloss.NewStyle().Bold(true).Render(m.filter.renderQueryLine())
		tail := lipgloss.NewStyle().Faint(true).Render(" · " + intStr(count) + " " + noun)
		return header + tail + "\n" + m.list.View()
	}
	return m.breadcrumb() + "\n" + m.list.View()
}

// renderHelpFooter renders the short or long help line based on showFullHelp,
// driven by the dynamic bindings for the current focus.
func (m *Model) renderHelpFooter() string {
	if m.showFullHelp {
		return m.help.FullHelpView(m.fullBindings())
	}
	return m.help.ShortHelpView(m.shortBindings())
}

// shortBindings returns the bindings to show in the short-help footer. The
// set depends on the active focus mode.
func (m *Model) shortBindings() []key.Binding {
	switch m.focus {
	case focusFilter:
		return []key.Binding{m.keys.Enter, m.keys.Cancel, m.keys.Help}
	case focusInspect:
		return []key.Binding{m.keys.Up, m.keys.Down, m.keys.Enter, m.keys.Cancel}
	case focusRight:
		base := []key.Binding{m.keys.Tab, m.keys.Enter, m.keys.Filter, m.keys.Inspect, m.keys.Cancel, m.keys.Help}
		if m.opts.Mode == ModeRun {
			base = append(base, m.keys.SkipConfirm)
		}
		return base
	default: // focusLeft
		return []key.Binding{m.keys.Up, m.keys.Down, m.keys.Tab, m.keys.Filter, m.keys.Cancel, m.keys.Help}
	}
}

// fullBindings returns the grouped bindings for the long-help footer.
func (m *Model) fullBindings() [][]key.Binding {
	nav := []key.Binding{m.keys.Up, m.keys.Down, m.keys.Left, m.keys.Right, m.keys.Home, m.keys.End}
	act := []key.Binding{m.keys.Enter, m.keys.Tab, m.keys.Filter, m.keys.Inspect, m.keys.Cancel, m.keys.Help}
	if m.opts.Mode == ModeRun {
		act = append(act, m.keys.SkipConfirm)
	}
	return [][]key.Binding{nav, act}
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
