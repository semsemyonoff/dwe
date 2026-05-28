package cmdbrowser

import (
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"devbox-cli/internal/core/ui"
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

	filter      *filterState
	inspect     *inspectState
	skipConfirm bool
	help        help.Model
	priorFocus  focus

	// lastSinglePanel tracks the most recent layout bucket so applyLayout can
	// repopulate the list when the user resizes across the 80-column boundary.
	lastSinglePanel bool

	// treeTopIdx is the index into tree.visible of the first row rendered in
	// the left panel. Without this, an oversized tree (more group nodes than
	// the panel can hold) overflows the bordered frame, stretches the joined
	// body via JoinHorizontal, and pushes the help footer off the alt screen.
	treeTopIdx int
}

// Compile-time guarantee that Model satisfies tea.Model. bubbletea/v2 is
// still maturing — lock the contract.
var _ tea.Model = (*Model)(nil)

func newModel(title string, items []Item, opts Options, w, h int) *Model {
	tm := newTreeModel(items, opts.IncludePrivate, opts.DefaultExpandedDepth)
	// listW is the **inner content width** the bubbles list / delegate renders
	// into — frame minus the 2 border cells. See [leftWidth] for the v2
	// frame-semantics rationale.
	listW := rightWidth(w) - 2
	if singlePanel(w) {
		listW = singlePanelWidth(w) - 2
	}
	dlg := newCmdDelegate(listW, !singlePanel(w) && showBadges(w) && opts.ShowTypeBadges)
	l := list.New(nil, dlg, listW, listHeight(h))
	l.SetShowTitle(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	applyListStyles(&l)
	hm := help.New()
	applyHelpStyles(&hm.Styles)
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
		help:     hm,
	}
	m.lastSinglePanel = singlePanel(w)
	if m.lastSinglePanel {
		m.focus = focusRight
	}
	m.populateList()
	return m
}

// populateList rebuilds the list contents for the current focus / filter /
// layout state. Single-panel mode bypasses the tree and emits all items with
// pseudo-header rows between groups; two-panel mode delegates to refreshList.
func (m *Model) populateList() {
	switch {
	case m.filter != nil:
		m.refreshFilterMatches()
	case singlePanel(m.width):
		m.refreshSingleList()
	default:
		m.refreshList()
	}
}

// refreshSingleList builds the flat single-panel list with "── group ──"
// pseudo-header rows interleaved between groups. Honours IncludePrivate so
// private commands stay hidden in the run path.
func (m *Model) refreshSingleList() {
	type indexed struct {
		idx   int
		group string
		id    string
	}
	rows := make([]indexed, 0, len(m.items))
	for i, it := range m.items {
		if !m.opts.IncludePrivate && it.Private {
			continue
		}
		rows = append(rows, indexed{idx: i, group: groupOf(it.ID), id: it.ID})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].group != rows[j].group {
			return rows[i].group < rows[j].group
		}
		return rows[i].id < rows[j].id
	})
	out := make([]list.Item, 0, len(rows)+8)
	seen := "\x00" // sentinel guaranteed distinct from any group label
	for _, r := range rows {
		if r.group != seen {
			label := r.group
			if label == "" {
				label = "(root)"
			}
			out = append(out, listItem{header: true, id: label})
			seen = r.group
		}
		it := m.items[r.idx]
		out = append(out, listItem{origIdx: r.idx, id: it.ID, desc: it.Description, typ: it.Type, paramCount: it.ParamCount})
	}
	m.list.SetItems(out)
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
		if singlePanel(m.width) {
			return m, nil
		}
		if m.focus == focusLeft {
			m.focus = focusRight
		} else {
			m.focus = focusLeft
		}
		return m, nil
	}
	// Single-panel mode has no tree: all navigation flows through the list.
	if singlePanel(m.width) {
		return m.updateRight(msg)
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
	m.ensureTreeFocusVisible()
	return m, nil
}

// updateRight handles right-panel input: Enter selects, Left returns focus to
// the tree (two-panel only), all other keys are forwarded to the embedded
// list model. Single-panel mode reuses this path with header-row skipping.
func (m *Model) updateRight(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Left) && !singlePanel(m.width) {
		m.focus = focusLeft
		return m, nil
	}
	if key.Matches(msg, m.keys.Enter) {
		if it, ok := m.list.SelectedItem().(listItem); ok {
			if it.header {
				return m, nil
			}
			m.result = Result{Idx: it.origIdx, Action: actionForMode(m.opts.Mode), SkipConfirm: m.skipConfirm}
			return m, tea.Quit
		}
		return m, nil
	}
	// EditParams is an alt-Enter: same selection, but signals to the orchestrator
	// that the param form must open even when all defaults are already
	// satisfied. Inspect mode ignores it (no params to edit during inspect).
	if key.Matches(msg, m.keys.EditParams) && m.opts.Mode == ModeRun {
		if it, ok := m.list.SelectedItem().(listItem); ok {
			if it.header {
				return m, nil
			}
			m.result = Result{
				Idx:            it.origIdx,
				Action:         actionForMode(m.opts.Mode),
				SkipConfirm:    m.skipConfirm,
				ForceParamForm: true,
			}
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
		out = append(out, listItem{origIdx: idx, id: it.ID, desc: it.Description, typ: it.Type, paramCount: it.ParamCount})
	}
	m.list.SetItems(out)
}

// visibleListItems returns the current list contents — used to decide whether
// pressing Enter on a group is worth focus-switching to.
func (m *Model) visibleListItems() []list.Item { return m.list.Items() }

// applyLayout pushes the current width/height to the list delegate and the
// list model so resize is reflected in the next render. When a resize crosses
// the single-panel boundary, the list contents are rebuilt so the
// pseudo-header rows appear (or disappear) accordingly.
func (m *Model) applyLayout() {
	nowSingle := singlePanel(m.width)
	// Inner content width = frame - 2 (one border cell on each side).
	listW := rightWidth(m.width) - 2
	if nowSingle {
		listW = singlePanelWidth(m.width) - 2
	}
	m.delegate.width = listW
	m.delegate.showBadges = !nowSingle && showBadges(m.width) && m.opts.ShowTypeBadges
	m.list.SetSize(listW, listHeight(m.height))
	// Let bubbles/v2 help adapt its grouped-bindings layout to the panel width
	// so FullHelpView wraps inside totalWidth rather than overflowing on the
	// 60-col single-panel bucket.
	m.help.SetWidth(m.width)
	if m.inspect != nil {
		// Resize the viewport in-place to preserve scroll position. Re-render
		// content at the new width so word-wrap matches the visible area
		// after a horizontal resize.
		w, ih := m.inspectViewportSize()
		m.inspect.vp.SetWidth(w)
		m.inspect.vp.SetHeight(ih)
		if render := m.items[m.inspect.inspectIdx].Inspect; render != nil {
			if content := render(w); content != "" {
				m.inspect.vp.SetContent(content)
			}
		}
	}
	if nowSingle != m.lastSinglePanel {
		m.lastSinglePanel = nowSingle
		if nowSingle && m.focus == focusLeft {
			m.focus = focusRight
		}
		m.populateList()
	}
	m.ensureTreeFocusVisible()
}

// View implements tea.Model. AltScreen is set on the View so bubbletea hides
// the caller's previous output for the duration of the program.
func (m *Model) View() tea.View {
	if singlePanel(m.width) {
		return m.viewSinglePanel()
	}
	lw := leftWidth(m.width)
	rw := rightWidth(m.width)
	bh := bodyHeight(m.height)

	border := lipgloss.NormalBorder()
	leftStyle := lipgloss.NewStyle().Border(border).Width(lw).Height(bh).BorderForeground(lipgloss.Color(ui.ColorBorder()))
	rightStyle := lipgloss.NewStyle().Border(border).Width(rw).Height(bh).BorderForeground(lipgloss.Color(ui.ColorBorder()))

	focusBorder := lipgloss.Color(ui.ColorAccent())
	switch m.focus {
	case focusLeft:
		leftStyle = leftStyle.BorderForeground(focusBorder)
	case focusRight, focusFilter, focusInspect:
		rightStyle = rightStyle.BorderForeground(focusBorder)
	}

	leftBody := m.renderTree()
	rightBody := m.renderRight()

	if m.focus == focusInspect && m.inspect != nil {
		rightBody = m.inspect.vp.View()
	}

	leftPanel := leftStyle.Render(leftBody)
	rightPanel := rightStyle.Render(rightBody)

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
	// leftWidth/rightWidth already return frame widths under v2 lipgloss
	// (Task 10), so the joined body spans exactly lw+rw cells. Title bar and
	// help footer must fill that same totalWidth so the brand line and key
	// hints align with the panel edges.
	totalWidth := lw + rw
	titleBar := m.renderTitleBar(totalWidth)
	footer := m.renderHelpFooter(totalWidth)

	content := strings.Join([]string{titleBar, body, footer}, "\n")

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// viewSinglePanel is the §4.1 60–79 col layout: title bar, full-width list
// with "── group ──" pseudo-headers, footer. No tree, no badges. Inspect
// overlay reuses the right-panel viewport contents inline.
func (m *Model) viewSinglePanel() tea.View {
	bh := bodyHeight(m.height)
	border := lipgloss.NormalBorder()
	style := lipgloss.NewStyle().Border(border).Width(singlePanelWidth(m.width)).Height(bh).BorderForeground(lipgloss.Color(ui.ColorBorder()))
	if m.focus == focusFilter || m.focus == focusInspect || m.focus == focusRight {
		style = style.BorderForeground(lipgloss.Color(ui.ColorAccent()))
	}

	var body string
	switch {
	case m.focus == focusInspect && m.inspect != nil:
		body = m.inspect.vp.View()
	case m.filter != nil:
		count := len(m.filter.matched)
		noun := "matches"
		if count == 1 {
			noun = "match"
		}
		header := paletteKey().Bold(true).Render(m.filter.renderQueryLine())
		tail := paletteDescription().Render(" · " + strconv.Itoa(count) + " " + noun)
		body = header + tail + "\n" + m.list.View()
	default:
		body = m.list.View()
	}

	panel := style.Render(body)
	totalWidth := singlePanelWidth(m.width)
	titleBar := m.renderTitleBar(totalWidth)
	footer := m.renderHelpFooter(totalWidth)
	content := strings.Join([]string{titleBar, panel, footer}, "\n")
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// renderTree renders the left-panel tree, switching to filter-aware mode (M/N
// counts, zero-match dimming) when a filter session is active. The output is
// clipped to the left panel's inner height so an oversized tree (more visible
// group nodes than rows) does not stretch the joined body past the bordered
// frame and push the help footer off the alt screen.
func (m *Model) renderTree() string {
	var full string
	if m.filter != nil {
		full = m.tree.renderFilter(m.focus == focusLeft, showCounts(m.width), m.filter)
	} else {
		full = m.tree.renderOpt(m.focus == focusLeft, showCounts(m.width))
	}
	return m.clipTreeToViewport(full)
}

// clipTreeToViewport slices the rendered tree to fit treeViewportHeight() rows
// starting at treeTopIdx. The tree renderer emits exactly one line per visible
// node (no wrapping), so line indices align with tree.visible indices —
// strings.Split is safe to use as a window.
func (m *Model) clipTreeToViewport(full string) string {
	h := m.treeViewportHeight()
	if h <= 0 {
		return full
	}
	lines := strings.Split(full, "\n")
	if len(lines) <= h {
		return full
	}
	top := min(max(m.treeTopIdx, 0), len(lines)-h)
	return strings.Join(lines[top:top+h], "\n")
}

// treeViewportHeight is the number of tree rows that fit inside the left
// panel. The panel frame is bodyHeight(m.height) rows tall, minus the two
// border rows of the lipgloss frame — there is no in-panel header anymore.
func (m *Model) treeViewportHeight() int {
	return max(bodyHeight(m.height)-2, 1)
}

// inspectMaxWidth caps the inspect viewport on wide terminals so the content
// lines up with the section divider rendered by `ui.RenderSectionTitle`
// (which uses the same min(TermWidth, 100) cap). Without this, the inspect
// area would stretch to the full right-panel width and the section dividers
// would float in a sea of dead space at the right edge.
const inspectMaxWidth = 100

// inspectViewportSize returns the (width, height) for the inspect viewport
// so it fills the right panel's content area. Width is the lesser of the
// panel's inner content area and `inspectMaxWidth`: narrow terminals get the
// full panel; wide ones are capped to match the section-title width so the
// layout stays balanced. Single-panel mode reuses the same body area.
// Defensive lower clamps protect against degenerate sizes from transient
// WindowSizeMsg events.
func (m *Model) inspectViewportSize() (int, int) {
	var panelW int
	if singlePanel(m.width) {
		panelW = singlePanelWidth(m.width) - 2
	} else {
		panelW = rightWidth(m.width) - 2
	}
	w := max(min(panelW, inspectMaxWidth), 10)
	h := max(bodyHeight(m.height)-2, 3) // -2 borders
	return w, h
}

// ensureTreeFocusVisible adjusts treeTopIdx so the focused node is within
// the current viewport. Called after every tree mutation (move / expand /
// collapse) and on resize.
func (m *Model) ensureTreeFocusVisible() {
	h := m.treeViewportHeight()
	n := len(m.tree.visible)
	if n == 0 || h <= 0 {
		m.treeTopIdx = 0
		return
	}
	idx := m.tree.indexOfFocused()
	if idx < 0 {
		m.treeTopIdx = 0
		return
	}
	if idx < m.treeTopIdx {
		m.treeTopIdx = idx
	} else if idx >= m.treeTopIdx+h {
		m.treeTopIdx = idx - h + 1
	}
	maxTop := max(n-h, 0)
	if m.treeTopIdx > maxTop {
		m.treeTopIdx = maxTop
	}
	if m.treeTopIdx < 0 {
		m.treeTopIdx = 0
	}
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
		header := paletteKey().Bold(true).Render(m.filter.renderQueryLine())
		tail := paletteDescription().Render(" · " + strconv.Itoa(count) + " " + noun)
		return header + tail + "\n" + m.list.View()
	}
	return m.breadcrumb() + "\n" + m.list.View()
}

// renderTitleBar renders the branded title bar — `{▪} <title>` in accent+bold,
// optionally suffixed by a success-coloured `[--yes ON]` toggle — wrapped in a
// v2 lipgloss envelope that pads the line out to totalWidth so it lines up
// with the joined panel(s) below. The plain logomark is used because the
// outer accent foreground colours the entire string uniformly; v1's
// LogoMark() carries a reset escape that would clip the accent partway
// through the title.
func (m *Model) renderTitleBar(totalWidth int) string {
	text := ui.LogoMarkPlain() + " " + m.title
	if m.skipConfirm && m.opts.Mode == ModeRun {
		// Pre-render the toggle with its own success-coloured envelope; the
		// outer accent style below threads around the existing SGR escapes so
		// the suffix keeps its distinct colour.
		text += "  " + paletteSuccess().Bold(true).Render("[--yes ON]")
	}
	return lipgloss.NewStyle().
		Width(totalWidth).
		Padding(0, 1).
		Foreground(lipgloss.Color(ui.ColorAccent())).
		Bold(true).
		Render(text)
}

// helpEntry is one rendered cell in the footer: a key label and a short
// description. Entries are kept separate from `keymap` so the footer can fuse
// related bindings (Up+Down → ↑↓ nav) and drop conventional keys (home/end)
// without affecting the actual key handling.
type helpEntry struct {
	key, desc string
}

// helpEntries returns the compact bindings shown in the footer. The full
// keymap stays active — this only controls what the footer surfaces.
//
// Design choices:
//   - Up/Down → "↑↓ nav" and Left/Right → "←→ tree" so footer fits in two
//     rows even at the minimum two-panel width (60 cols).
//   - Home/End/PgUp/PgDn are omitted as conventional keys most users assume.
//   - "tab switch" drops "panel" — context makes the target obvious.
func (m *Model) helpEntries() []helpEntry {
	out := []helpEntry{
		{"↑↓", "nav"},
		{"←→", "tree"},
		{"enter", "select"},
		{"tab", "switch"},
		{"/", "filter"},
		{"i", "inspect"},
		{"esc", "quit"},
	}
	if m.opts.Mode == ModeRun {
		out = append(out, helpEntry{"e", "edit params"}, helpEntry{"y", "skip confirm"})
	}
	return out
}

// renderHelpFooter lays out compact help entries as a wrapped left-to-right
// flow: items are joined with " · " separators and wrap to the next row when
// the current row would exceed the inner width. The result is padded to
// `footerRows` rows so the body height stays stable across terminal widths and
// mode changes — without the reservation, narrower widths would push the body
// up and wider widths would leave it stretched, jittering on every redraw.
func (m *Model) renderHelpFooter(totalWidth int) string {
	inner := totalWidth - 2 // Padding(0, 1) below
	sep := paletteDescription().Render(" · ")
	sepW := lipgloss.Width(sep)

	var rows []string
	var cur strings.Builder
	var curW int

	for _, e := range m.helpEntries() {
		item := m.help.Styles.ShortKey.Inline(true).Render(e.key) + " " +
			m.help.Styles.ShortDesc.Inline(true).Render(e.desc)
		itemW := lipgloss.Width(item)

		var prefix string
		var prefixW int
		if cur.Len() > 0 {
			prefix = sep
			prefixW = sepW
		}
		if curW+prefixW+itemW > inner && cur.Len() > 0 {
			rows = append(rows, cur.String())
			cur.Reset()
			curW = 0
			prefix = ""
			prefixW = 0
		}
		cur.WriteString(prefix)
		cur.WriteString(item)
		curW += prefixW + itemW
	}
	if cur.Len() > 0 {
		rows = append(rows, cur.String())
	}
	for len(rows) < footerRows {
		rows = append(rows, "")
	}
	if len(rows) > footerRows {
		rows = rows[:footerRows]
	}
	return lipgloss.NewStyle().
		Width(totalWidth).
		Padding(0, 1).
		Render(strings.Join(rows, "\n"))
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
	header := paletteKey().Bold(true).Render(path)
	tail := paletteDescription().Render(" · " + strconv.Itoa(count) + " " + noun)
	return header + tail
}

// Width bucket helpers — keep the layout rules in one place. The fallback
// path ensures width is always at least minTwoPanelWidth.

// footerRows is the fixed height reserved for the flow-wrapped help footer.
// At ≥ 100 cols all compact entries fit on a single row; at narrower widths
// the trailing entries that would overflow are dropped by the truncate step
// in `renderHelpFooter` so the row stays exactly one tall. Trading the second
// row buys an extra body row for the tree / list. `renderHelpFooter` still
// pads when no entries are present so the body height stays stable across
// resizes and mode switches.
const footerRows = 1

// bodyHeight returns the bordered panel frame height. Under v2 lipgloss
// `Style.Height(bh)` sets the frame (border-inclusive) height directly — the
// `+2` for borders that v1 added is already baked into bh. The View composes
// title(1) + panel-frame(bh) + footer(footerRows), so bh = h - 1 - footerRows
// fills the terminal exactly. The earlier `h - 3 - footerRows` was a v1-era
// formula and left two unused rows below the footer.
func bodyHeight(h int) int { return max(h-1-footerRows, 3) }

// listHeight returns the inner height for the bubbles list. Under v2 lipgloss
// frame semantics `Style.Height(bh)` makes the panel frame bh rows tall, so the
// inner content area is `bh-2` (two border rows). The right panel renders
// breadcrumb(1) + list, so the list must fit in `bh-3` rows or the right panel
// frame stretches under JoinHorizontal and tears below the left panel.
// Single-panel non-filter mode renders just the list, leaving one unused inner
// row — an acceptable trade-off vs computing two list heights.
func listHeight(h int) int { return max(bodyHeight(h)-3, 3) }

// leftWidth returns the **total frame width** of the left panel, including the
// 2 cells consumed by its left/right borders. In charm.land/lipgloss/v2,
// `Style.Width(n)` sets the frame width (border-inclusive), not the inner
// content width as it did in v1 — so callers can pass this value straight to
// `Width(...)` and the rendered panel will span exactly this many cells.
func leftWidth(w int) int {
	return max(2*w/9, 18)
}

// rightWidth returns the **total frame width** of the right panel so that the
// two panels joined by `JoinHorizontal` fill exactly w cells. The previous
// implementation subtracted 4 (both panels' borders) under v1 semantics, which
// left a 4-cell gap on the right edge — the "torn right border" bug.
func rightWidth(w int) int {
	return max(w-leftWidth(w), 12)
}

// showBadges returns true for the full two-panel bucket (≥ 100 cols).
func showBadges(w int) bool { return w >= fullTwoPanelWidth }

// showCounts mirrors showBadges — the (N) per-group counts hide at 80–99 cols
// alongside the type badges (per §4.1).
func showCounts(w int) bool { return w >= fullTwoPanelWidth }

// singlePanel reports whether the layout should collapse to a single panel.
// Width 60–79 falls into this bucket per §4.1; widths < 60 are handled by the
// huh fallback before the Model is ever constructed.
func singlePanel(w int) bool { return w < reducedTwoPanelWidth }

// singlePanelWidth returns the **total frame width** of the single panel so it
// fills the full terminal width. Inner content width is `singlePanelWidth(w) - 2`
// (border on each side). See [leftWidth] for the v2 lipgloss frame-semantics
// rationale.
func singlePanelWidth(w int) int { return max(w, 12) }
