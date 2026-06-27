package cmdbrowser

import (
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// treeCountsMinWidth is the minimum inner panel width (cells) at which the tree
// renders the per-group "(N)" / "M/N" counts. Below it the suffix is dropped so
// a deep row does not overflow the narrow tree panel.
//
// Keyed on the framework INNER width (outer − border − padding), NOT raw
// terminal width: under the Frame the tree panel takes weight 2 of {2,7}, so at
// terminal widths 99–100 the tree inner width is 18 and at 80 it is 13. The
// deepest sample row ("        cs (2)" plus the focus-marker gutter) is exactly
// 18 cells wide, so 18 is the width at which counts first fit cleanly. The
// legacy model keyed counts on terminal width ≥ 100; this recomputes the
// threshold against the inner width the Frame now supplies.
const treeCountsMinWidth = 18

// renderRegion is the framework entry point: it renders the tree into the inner
// Region the Frame computed, choosing count visibility from inner.Width and
// clipping to inner.Height. The filter-aware path is selected when f != nil so
// callers route both plain and filtered renders through one entry.
func (tm *treeModel) renderRegion(inner tui.Region, focused bool, f *filterState) string {
	showCounts := inner.Width >= treeCountsMinWidth
	var full string
	if f != nil {
		full = tm.renderFilter(focused, showCounts, f)
	} else {
		full = tm.renderOpt(focused, showCounts)
	}
	return tm.clipToViewport(full, inner.Height)
}

// clipToViewport slices the rendered tree to fit height rows starting at
// topIdx. The tree renderer emits exactly one line per visible node (no
// wrapping), so line indices align with tree.visible indices — strings.Split is
// safe to use as a window. Ported from *Model.clipTreeToViewport, driven off the
// passed height instead of a *Model's layout.
func (tm *treeModel) clipToViewport(full string, height int) string {
	if height <= 0 {
		return full
	}
	lines := strings.Split(full, "\n")
	if len(lines) <= height {
		return full
	}
	top := min(max(tm.topIdx, 0), len(lines)-height)
	return strings.Join(lines[top:top+height], "\n")
}

// renderOpt renders the tree with control over count visibility — hides
// the (N) counts at narrow inner widths per §4.1.
func (tm *treeModel) renderOpt(focused, showCounts bool) string {
	return tm.renderTree(focused, showCounts, nil)
}

// renderFilter is the filter-active variant of the tree renderer. Each visible
// group line shows "M/N" counts (M = matches in subtree, N = total) and
// zero-match groups are dimmed so the user can still see the surrounding
// structure.
func (tm *treeModel) renderFilter(focused, showCounts bool, f *filterState) string {
	return tm.renderTree(focused, showCounts, f)
}

// renderTree is the shared body behind renderOpt (f == nil) and renderFilter
// (f != nil). The filter variant shows "M/N" counts and dims zero-match groups;
// the plain variant shows "(N)" counts and only styles the focused line. The
// nil check is the single point of divergence between the two callers.
func (tm *treeModel) renderTree(focused, showCounts bool, f *filterState) string {
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
			if f != nil {
				m := f.matchCount[n.id]
				countStr = paletteTreeCount().Render(fmt.Sprintf(" (%d/%d)", m, total))
			} else {
				countStr = paletteTreeCount().Render(fmt.Sprintf(" (%d)", total))
			}
		}
		line := fmt.Sprintf("%s %s%s%s%s", marker, indent, glyph, n.name, countStr)
		switch {
		case f != nil && !f.hasMatch(n.id):
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
