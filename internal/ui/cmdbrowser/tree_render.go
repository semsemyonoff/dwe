package cmdbrowser

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// renderTree returns the left-panel body (no border) for the given focus.
// Lines use existing styles.yml semantics in spirit: focused row is bold,
// muted faint is used for the (N) counts. No new style keys are required
// at this stage — Task 4 swaps in the badge palette for the right panel.
func (tm *treeModel) render(focused bool) string {
	if len(tm.visible) == 0 {
		return lipgloss.NewStyle().Faint(true).Render("(no groups)")
	}
	var b strings.Builder
	for i, n := range tm.visible {
		isFocused := focused && n.id == tm.focusedID
		marker := " "
		if isFocused {
			marker = "❯"
		}
		glyph := "  "
		if len(n.children) > 0 {
			if tm.expanded[n.id] {
				glyph = "▾ "
			} else {
				glyph = "▸ "
			}
		}
		indent := strings.Repeat("  ", n.depth)
		count := n.countPublic
		if tm.includePrivate {
			count = n.countAll
		}
		countStr := lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf(" (%d)", count))
		line := fmt.Sprintf("%s %s%s%s%s", marker, indent, glyph, n.name, countStr)
		if isFocused {
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
		b.WriteString(line)
		if i < len(tm.visible)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// renderRightForFocus is the Task-3 placeholder: a plain bullet list of the
// commands directly attached to the focused group. Task 4 swaps in
// list.Model with a real delegate and breadcrumb header.
func (tm *treeModel) renderRightForFocus() string {
	idxs := tm.itemsForFocus()
	if len(idxs) == 0 {
		return lipgloss.NewStyle().Faint(true).Render("(no commands)")
	}
	var b strings.Builder
	for i, idx := range idxs {
		b.WriteString("• ")
		b.WriteString(tm.items[idx].ID)
		if i < len(idxs)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
