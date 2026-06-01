package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// applyDocsHelpStyles overwrites the palette-driven fields on a bubbles/v2
// help.Styles using the project palette accessors. Same approach as
// internal/core/ui/cmdbrowser so the docs TUI footer reads with the same
// vocabulary as the commands TUI.
func applyDocsHelpStyles(s *help.Styles) {
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorAccent()))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorMuted()))
	s.ShortKey = keyStyle
	s.FullKey = keyStyle
	s.ShortDesc = descStyle
	s.FullDesc = descStyle
	s.ShortSeparator = descStyle
	s.FullSeparator = descStyle
	s.Ellipsis = descStyle
}

// viewportInnerWidth returns the width passed to the markdown renderer / the
// viewport widget — the right panel's inner area minus its border cells.
func viewportInnerWidth(termWidth int) int {
	return max(rightWidth(termWidth)-2, 1)
}

// viewportInnerHeight returns the height passed to the viewport widget —
// body height minus the right panel's border rows.
func viewportInnerHeight(termHeight int) int {
	return max(bodyHeight(termHeight, footerRows)-2, 1)
}

// footerRows is the fixed height reserved for the help footer. Two rows is
// enough to fit every binding on a 100+ col terminal and avoids body-height
// jitter on resize. Narrower terminals wrap into the second row; if even two
// rows aren't enough, trailing bindings are dropped (the keys still work,
// they just aren't displayed).
const footerRows = 2

func (m *Model) renderTwoPanel() tea.View {
	lw := leftWidth(m.TermWidth)
	rw := rightWidth(m.TermWidth)
	bh := bodyHeight(m.TermHeight, m.helpHeight())

	border := lipgloss.NormalBorder()
	borderColor := lipgloss.Color(styles.ColorBorder())
	accent := lipgloss.Color(styles.ColorAccent())

	treeStyle := lipgloss.NewStyle().Border(border).Width(lw).Height(bh).BorderForeground(borderColor)
	viewportStyle := lipgloss.NewStyle().Border(border).Width(rw).Height(bh).BorderForeground(borderColor)

	switch m.FocusZone {
	case FocusTree:
		treeStyle = treeStyle.BorderForeground(accent)
	case FocusViewport:
		viewportStyle = viewportStyle.BorderForeground(accent)
	}

	treePanel := treeStyle.Render(m.renderTree())
	viewportPanel := viewportStyle.Render(m.Viewport.View())

	body := lipgloss.JoinHorizontal(lipgloss.Top, treePanel, viewportPanel)
	totalWidth := lw + rw

	title := m.renderTitleBar(totalWidth)
	status := m.renderStatusLine(totalWidth)
	footer := m.renderHelpFooter(totalWidth)

	content := strings.Join([]string{title, body, status, footer}, "\n")
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// renderTitleBar renders the brand title — `{▪} DWE · <project> ·
// Documentation` in accent + bold — padded to totalWidth so it lines up
// with the panels below. Same shape as cmdbrowser.renderTitleBar so the
// two TUIs read consistently.
func (m *Model) renderTitleBar(totalWidth int) string {
	text := render.LogoMarkPlain() + " " + m.Title
	return lipgloss.NewStyle().
		Width(totalWidth).
		Padding(0, 1).
		Foreground(lipgloss.Color(styles.ColorAccent())).
		Bold(true).
		Render(text)
}

// renderStatusLine renders the path / language / progress strip pulled from
// the StatusBar widget, padded to totalWidth so it lines up with the panels.
func (m *Model) renderStatusLine(totalWidth int) string {
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorMuted()))
	return lipgloss.NewStyle().
		Width(totalWidth).
		Padding(0, 1).
		Render(muted.Render(m.StatusBar.View()))
}

// renderHelpFooter lays out every binding as a flat list with " · "
// separators, wrapping to the next row when the current row would exceed
// the inner width. Same approach as cmdbrowser so the two TUIs read
// consistently. The output is padded to footerRows so body height is
// stable across terminal sizes.
func (m *Model) renderHelpFooter(totalWidth int) string {
	inner := totalWidth - 2 // Padding(0, 1) below
	sep := m.Help.Styles.ShortSeparator.Render(" · ")
	sepW := lipgloss.Width(sep)

	var rows []string
	var cur strings.Builder
	var curW int

	for _, group := range m.Keys.FullHelp() {
		for _, b := range group {
			help := b.Help()
			item := m.Help.Styles.ShortKey.Inline(true).Render(help.Key) + " " +
				m.Help.Styles.ShortDesc.Inline(true).Render(help.Desc)
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
				if len(rows) >= footerRows {
					break
				}
			}
			cur.WriteString(prefix)
			cur.WriteString(item)
			curW += prefixW + itemW
		}
		if len(rows) >= footerRows {
			break
		}
	}
	if cur.Len() > 0 && len(rows) < footerRows {
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

// helpHeight returns the rendered height of the help footer so bodyHeight
// can reserve space for it. Fixed at footerRows so the body doesn't jitter.
func (m *Model) helpHeight() int { return footerRows }

func (m *Model) renderTree() string {
	if m.Tree == nil {
		return ""
	}

	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorAccent()))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorMuted()))

	inner := leftPanelInnerWidth(m.TermWidth)

	// The filter header MUST render even when the current query has zero
	// matches — otherwise typing a non-matching second character makes the
	// entire left panel blank and the user has no feedback on what they
	// typed. The header itself shows the query and match count.
	var header string
	if m.Filter != nil && m.Filter.Active {
		header = m.renderFilterHeader(inner, accent, muted)
	}

	rows := m.renderTreeRows(inner, accent, muted)

	// Reserve one row for the filter header when present so the
	// scrolling window matches the available row area inside the
	// bordered left panel.
	rowsAvail := m.treeViewportHeight()
	if header != "" {
		rowsAvail = max(rowsAvail-1, 0)
	}

	m.ensureTreeFocusVisible(len(rows), rowsAvail)
	clipped := clipTreeRows(rows, m.treeTopIdx, rowsAvail)

	switch {
	case header == "" && len(clipped) == 0:
		return ""
	case header == "":
		return strings.Join(clipped, "\n")
	case len(clipped) == 0:
		return header
	default:
		return header + "\n" + strings.Join(clipped, "\n")
	}
}

// renderTreeRows emits one styled string per visible tree node. Each
// row is independent (no shared trailing newline) so clipTreeRows can
// slice them by index when the rendered tree would otherwise overflow
// the left panel and push the body off-screen.
func (m *Model) renderTreeRows(inner int, accent, muted lipgloss.Style) []string {
	visible := m.Tree.VisibleNodes()
	if len(visible) == 0 {
		return nil
	}
	rows := make([]string, 0, len(visible))
	for _, node := range visible {
		depth := m.getNodeDepth(node)
		indent := strings.Repeat("  ", depth)

		cursor := "  "
		if node == m.Tree.Cursor() {
			cursor = "> "
		}

		var glyph, label string
		switch {
		case node.Heading != nil:
			// Heading rows: a small bullet, deeper indent for H3 so it reads
			// nested under H2 even when they are siblings in the tree visit
			// order.
			if node.Heading.Level >= 3 {
				glyph = "  · "
			} else {
				glyph = "· "
			}
			label = node.Heading.Text
		case node.Node.IsDir && node.Expanded:
			glyph = "▼ "
			label = node.Node.Name
		case node.Node.IsDir:
			glyph = "▶ "
			label = node.Node.Name
		default:
			if len(node.Children) > 0 {
				if node.Expanded {
					glyph = "▾ "
				} else {
					glyph = "▸ "
				}
			} else {
				glyph = "  "
			}
			label = docs.TitleOrFallback(node.Node.Title, node.Node.Name)
		}

		prefix := indent + cursor + glyph
		label = truncateLabel(label, max(inner-lipgloss.Width(prefix), 1))

		styledGlyph := glyph
		if glyph != "  " {
			styledGlyph = muted.Render(glyph)
		}
		styledCursor := cursor
		if cursor == "> " {
			styledCursor = accent.Render(cursor)
		}

		line := indent + styledCursor + styledGlyph + label
		if node == m.Tree.Cursor() {
			line = accent.Render(line)
		}
		rows = append(rows, line)
	}
	return rows
}

// treeViewportHeight is the number of rows that fit inside the left
// panel's bordered frame — bodyHeight minus the two border rows.
// Mirrors cmdbrowser.treeViewportHeight so both TUIs handle oversized
// trees the same way.
func (m *Model) treeViewportHeight() int {
	return max(bodyHeight(m.TermHeight, m.helpHeight())-2, 1)
}

// ensureTreeFocusVisible adjusts treeTopIdx so the cursor row stays
// inside the visible window after navigation, expansion, or a tree
// rebuild (locale switch). Mirrors cmdbrowser.ensureTreeFocusVisible.
// Called at render time so we don't need to instrument every mutator.
func (m *Model) ensureTreeFocusVisible(total, rowsAvail int) {
	if total == 0 || rowsAvail <= 0 {
		m.treeTopIdx = 0
		return
	}
	idx := -1
	if m.Tree != nil {
		cursor := m.Tree.Cursor()
		for i, n := range m.Tree.VisibleNodes() {
			if n == cursor {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		m.treeTopIdx = 0
		return
	}
	if idx < m.treeTopIdx {
		m.treeTopIdx = idx
	} else if idx >= m.treeTopIdx+rowsAvail {
		m.treeTopIdx = idx - rowsAvail + 1
	}
	maxTop := max(total-rowsAvail, 0)
	if m.treeTopIdx > maxTop {
		m.treeTopIdx = maxTop
	}
	if m.treeTopIdx < 0 {
		m.treeTopIdx = 0
	}
}

// clipTreeRows returns the slice [top:top+h] of rows, clamped to the
// available range. The tree renderer emits exactly one line per
// visible node (labels are truncated, never wrapped), so row indices
// align with Tree.VisibleNodes() and a plain slice is safe.
func clipTreeRows(rows []string, top, h int) []string {
	if h <= 0 || len(rows) == 0 {
		return nil
	}
	if len(rows) <= h {
		return rows
	}
	top = min(max(top, 0), len(rows)-h)
	return rows[top : top+h]
}

// renderFilterHeader renders the filter prompt + match count at the top of
// the left panel while a tree filter is active.
func (m *Model) renderFilterHeader(inner int, accent, muted lipgloss.Style) string {
	query := m.Filter.Query
	prompt := accent.Bold(true).Render("/")
	tail := muted.Render("  " + filterCountLabel(len(m.Tree.VisibleNodes())))
	body := prompt + " " + query + tail
	// Pad / truncate to fit so the surrounding panel doesn't shift width.
	if lipgloss.Width(body) > inner {
		body = truncateLabel(body, inner)
	}
	return body
}

func filterCountLabel(n int) string {
	switch n {
	case 1:
		return "1 match"
	default:
		return fmt.Sprintf("%d matches", n)
	}
}

// leftPanelInnerWidth returns the cell width available for tree row content
// inside the bordered left panel.
func leftPanelInnerWidth(termWidth int) int {
	return max(leftWidth(termWidth)-2, 1)
}

// truncateLabel clips s to fit within width cells, appending "…" when it
// actually clipped. ANSI sequences in s are honoured via lipgloss.Width.
func truncateLabel(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(s)
	for i := len(runes) - 1; i > 0; i-- {
		cand := string(runes[:i]) + "…"
		if lipgloss.Width(cand) <= width {
			return cand
		}
	}
	return "…"
}

func (m *Model) getNodeDepth(node *TreeNode) int {
	depth := 0
	current := node
	for current != nil && current.Parent != nil {
		if current.Parent.Node.Name != "root" {
			depth++
		}
		current = current.Parent
	}
	return depth
}

// leftWidth returns the frame width of the left tree panel. Bumped from the
// previous fixed 30 to roughly one-third of the terminal (~40 at 120 cols),
// with a lower clamp for narrow terminals.
func leftWidth(w int) int {
	return max(w/6, 20)
}

// rightWidth returns the frame width of the right viewport panel so that the
// two panels joined by JoinHorizontal fill exactly w cells.
func rightWidth(w int) int {
	return max(w-leftWidth(w), 20)
}

// bodyHeight returns the panel frame height: terminal height minus the
// title bar (1 row), status line (1 row), and help footer (helpRows rows).
func bodyHeight(h, helpRows int) int {
	return max(h-2-helpRows, 5)
}
