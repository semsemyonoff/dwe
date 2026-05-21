package cmdbrowser

import (
	"fmt"
	"strings"
)

// renderOpt renders the tree with control over count visibility — Task 4 hides
// the (N) counts at 80–99 cols per §4.1.
func (tm *treeModel) renderOpt(focused, showCounts bool) string {
	if len(tm.visible) == 0 {
		return paletteDescription().Render("(no groups)")
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
			countStr = paletteTreeCount().Render(fmt.Sprintf(" (%d)", count))
		}
		line := fmt.Sprintf("%s %s%s%s%s", marker, indent, glyph, n.name, countStr)
		if isFocused {
			line = paletteFocusBorder().Bold(true).Render(line)
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
		return paletteDescription().Render("(no groups)")
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
			countStr = paletteTreeCount().Render(fmt.Sprintf(" (%d/%d)", m, total))
		}
		line := fmt.Sprintf("%s %s%s%s%s", marker, indent, glyph, n.name, countStr)
		switch {
		case !f.hasMatch(n.id):
			line = paletteDescription().Render(line)
		case isFocused:
			line = paletteFocusBorder().Bold(true).Render(line)
		}
		b.WriteString(line)
		if i < len(tm.visible)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
