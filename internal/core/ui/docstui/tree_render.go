package docstui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// nodeDepth computes the tree depth of node by walking the parent chain. A
// child of the invisible root (whose Name is "root") is depth 0. Package-level
// so both the legacy Model.renderTreeRows (view.go) and the new plugin
// ViewPanel path share the same logic.
func nodeDepth(node *TreeNode) int {
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

// renderRegion is the Framework entry point: it renders the visible tree rows
// into the inner Region the Frame computed, clipping to inner.Height starting
// at topIdx. Call ensureFocusVisible(inner.Height) before renderRegion so the
// focused row stays on screen across resizes. Mirrors cmdbrowser
// treeModel.renderRegion.
//
// panelFocused is true when the tree panel holds the Frame focus; the cursor
// row is always rendered (for spatial orientation) but styled with accent when
// focused and muted when not, matching the Frame's border-colour signal.
func (tw *TreeWidget) renderRegion(inner tui.Region, panelFocused bool) string {
	full := tw.renderAllRows(inner.Width, panelFocused)
	return tw.clipToViewport(full, inner.Height)
}

// clipToViewport slices the rendered tree to height rows starting at topIdx.
// The tree renderer emits exactly one line per visible node (labels are
// truncated, never wrapped), so line indices align with visible indices —
// strings.Split is safe to use as a window. Mirrors cmdbrowser
// treeModel.clipToViewport.
func (tw *TreeWidget) clipToViewport(full string, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(full, "\n")
	if len(lines) <= height {
		return full
	}
	top := min(max(tw.topIdx, 0), len(lines)-height)
	return strings.Join(lines[top:top+height], "\n")
}

// renderAllRows emits one styled line per visible node (no clipping). Labels
// are truncated to fit innerWidth. Mirrors the logic in Model.renderTreeRows
// (view.go) but operates on the TreeWidget's own state so ViewPanel can call
// it without going through the old Model's geometry fields.
func (tw *TreeWidget) renderAllRows(innerWidth int, panelFocused bool) string {
	visible := tw.visible
	if len(visible) == 0 {
		return ""
	}

	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorAccent()))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorMuted()))

	var sb strings.Builder
	for i, node := range visible {
		depth := nodeDepth(node)
		indent := strings.Repeat("  ", depth)

		cursor := "  "
		if node == tw.cursor {
			cursor = "> "
		}

		var glyph, label string
		switch {
		case node.Heading != nil:
			if node.Heading.Level >= 3 {
				glyph = "  · "
			} else {
				glyph = "· "
			}
			label = node.Heading.Text
		case node.Node.IsDir && node.Expanded:
			glyph = "▼ "
			label = nodeLabel(node)
		case node.Node.IsDir:
			glyph = "▶ "
			label = nodeLabel(node)
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
		label = truncateLabel(label, max(innerWidth-lipgloss.Width(prefix), 1))

		styledGlyph := glyph
		if glyph != "  " {
			styledGlyph = muted.Render(glyph)
		}

		styledCursor := cursor
		if cursor == "> " {
			if panelFocused {
				styledCursor = accent.Render(cursor)
			} else {
				styledCursor = muted.Render(cursor)
			}
		}

		line := indent + styledCursor + styledGlyph + label
		if node == tw.cursor && panelFocused {
			line = accent.Render(line)
		}

		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(line)
	}
	return sb.String()
}
