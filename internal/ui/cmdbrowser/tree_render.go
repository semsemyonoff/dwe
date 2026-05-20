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
func (tm *treeModel) render(focused bool) string { return tm.renderOpt(focused, true) }

// renderOpt renders the tree with control over count visibility — Task 4 hides
// the (N) counts at 80–99 cols per §4.1.
func (tm *treeModel) renderOpt(focused, showCounts bool) string {
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
		countStr := ""
		if showCounts {
			count := n.countPublic
			if tm.includePrivate {
				count = n.countAll
			}
			countStr = lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf(" (%d)", count))
		}
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

// renderFilter is the filter-active variant of the tree renderer. Each visible
// group line shows "M/N" counts (M = matches in subtree, N = total) and
// zero-match groups are dimmed so the user can still see the surrounding
// structure.
func (tm *treeModel) renderFilter(focused, showCounts bool, f *filterState) string {
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
		countStr := ""
		if showCounts {
			total := n.countPublic
			if tm.includePrivate {
				total = n.countAll
			}
			m := f.matchCount[n.id]
			countStr = lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf(" (%d/%d)", m, total))
		}
		line := fmt.Sprintf("%s %s%s%s%s", marker, indent, glyph, n.name, countStr)
		switch {
		case !f.hasMatch(n.id):
			line = lipgloss.NewStyle().Faint(true).Render(line)
		case isFocused:
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
		b.WriteString(line)
		if i < len(tm.visible)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
