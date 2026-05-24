package cmdbrowser

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// listItem adapts an Item for bubbles/v2/list. The original-items index is
// preserved so Result.Idx survives filtering and reordering inside the list.
// In single-panel mode (width 60–79) the list is populated with pseudo-header
// rows interleaved between groups; those rows set header=true and are not
// selectable.
type listItem struct {
	origIdx    int
	id         string
	desc       string
	typ        string
	paramCount int
	header     bool
}

// FilterValue is the haystack used by list.DefaultFilter. Concatenating id and
// description so the fuzzy match covers both — matches the §5 spec. Header
// rows return an empty value so they never appear in filtered results.
func (li listItem) FilterValue() string {
	if li.header {
		return ""
	}
	if li.desc == "" {
		return li.id
	}
	return li.id + " " + li.desc
}

// cmdDelegate is a two-line item renderer (line 1 = id + right-aligned badge,
// line 2 = description). Height 2, Spacing 1 — matches the §4 spec. The
// delegate carries the width and badge visibility so Render can right-align
// the badge without relying on the (un-exported) m.width on list.Model.
type cmdDelegate struct {
	width      int
	showBadges bool
}

func newCmdDelegate(width int, showBadges bool) *cmdDelegate {
	return &cmdDelegate{width: width, showBadges: showBadges}
}

// Height implements list.ItemDelegate.
func (d *cmdDelegate) Height() int { return 2 }

// Spacing implements list.ItemDelegate.
func (d *cmdDelegate) Spacing() int { return 1 }

// Update implements list.ItemDelegate. The cmdbrowser model handles all
// item-level updates externally; the delegate is stateless w.r.t. key events.
func (d *cmdDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

// Render implements list.ItemDelegate. Line 1 is the command id with the type
// badge right-aligned; line 2 is the description, truncated to fit. Selection
// is conveyed by bolding the ID and showing a cursor arrow.
func (d *cmdDelegate) Render(w io.Writer, m list.Model, index int, it list.Item) {
	li, ok := it.(listItem)
	if !ok {
		return
	}
	width := d.width
	if width <= 0 {
		width = m.Width()
	}
	if li.header {
		d.renderHeader(w, width, li.id)
		return
	}
	// Reserve two cols of padding on each side so the badge doesn't kiss the border.
	avail := max(width-4, 10)

	isSelected := index == m.Index()
	cursor := "  "
	if isSelected {
		cursor = "❯ "
	}

	badge := ""
	if d.showBadges && li.typ != "" {
		badge = badgeRender(li.typ)("[" + li.typ + "]")
	}
	badgeWidth := lipgloss.Width(badge)

	// Param-count indicator: rendered as `[N]` in the muted description colour
	// so users can spot input-taking commands without opening the param form.
	// Placed left of the type badge with a one-space gap. Hidden in
	// single-panel and reduced (80–99) buckets via the same showBadges gate
	// the type badge uses — these widths already drop secondary chrome to keep
	// the list scannable.
	paramBadge := ""
	if d.showBadges && li.paramCount > 0 {
		paramBadge = paletteDescription().Render(fmt.Sprintf("[%d]", li.paramCount))
	}
	gap := ""
	if paramBadge != "" && badge != "" {
		gap = " "
	}
	gapWidth := lipgloss.Width(gap)
	paramBadgeWidth := lipgloss.Width(paramBadge)

	cursorW := lipgloss.Width(cursor)
	idCellWidth := max(avail-cursorW-badgeWidth-paramBadgeWidth-gapWidth, 4)
	id := truncate(li.id, idCellWidth)
	idStyled := id
	if isSelected {
		idStyled = paletteFocusBorder().Bold(true).Render(id)
	}
	// Pad to the available width so the badge right-aligns. Pad on the
	// rendered-id width to keep alignment stable with ANSI escapes.
	padCount := max(idCellWidth-lipgloss.Width(idStyled), 0)
	pad := strings.Repeat(" ", padCount)

	line1 := cursor + idStyled + pad + paramBadge + gap + badge

	// Collapse multi-line descriptions (YAML literal blocks carry `\n`) to
	// their first line. Without this the item overflows Height()=2 and the
	// right panel's frame stretches under JoinHorizontal — the "torn right
	// border" symptom users see when long-description commands are listed.
	desc := li.desc
	hadMore := false
	if idx := strings.IndexByte(desc, '\n'); idx >= 0 {
		desc = desc[:idx]
		hadMore = true
	}
	if desc == "" {
		_, _ = fmt.Fprintf(w, "%s\n%s", line1, strings.Repeat(" ", cursorW))
		return
	}

	// Pre-compute whether truncation would happen at the full descAvail. If so,
	// the selected item earns an accent "(i)" affordance pointing at the
	// inspect keybinding — the user otherwise has no way to discover that the
	// full description is reachable.
	descAvail := max(avail-cursorW, 8)
	wouldTruncate := hadMore || lipgloss.Width(desc) > descAvail
	inspectHint := ""
	hintW := 0
	if isSelected && wouldTruncate {
		inspectHint = "  " + paletteKey().Bold(true).Render("(i)")
		hintW = 4 // "  " + "(i)"
	}

	descAvail = max(descAvail-hintW, 8)
	if wouldTruncate {
		desc = truncate(desc, descAvail)
		// truncate appends "…" when it actually clipped runes. When the first
		// line happens to fit but content continued on subsequent lines, force
		// the ellipsis so the user can still see that more is hidden.
		if hadMore && !strings.HasSuffix(desc, "…") {
			if lipgloss.Width(desc)+1 <= descAvail {
				desc += "…"
			} else {
				desc = truncate(desc, descAvail-1) + "…"
			}
		}
	}

	desc = paletteDescription().Render(desc)
	line2 := strings.Repeat(" ", cursorW) + desc + inspectHint
	_, _ = fmt.Fprintf(w, "%s\n%s", line1, line2)
}

// renderHeader emits the "── group ──" pseudo-header used in single-panel
// mode (width 60–79). The header consumes both lines of the delegate height
// so spacing stays consistent with item rows.
func (d *cmdDelegate) renderHeader(w io.Writer, width int, label string) {
	if label == "" {
		label = "(root)"
	}
	avail := max(width-2, 4)
	inner := " " + label + " "
	pad := max(avail-lipgloss.Width(inner)-4, 0)
	leftPad := 2 + pad/2
	rightPad := 2 + (pad - pad/2)
	bar := strings.Repeat("─", leftPad) + inner + strings.Repeat("─", rightPad)
	bar = paletteDescription().Render(bar)
	_, _ = fmt.Fprintf(w, "%s\n%s", bar, "")
}

// truncate clips s to fit within width cells (rune width approximated by
// ANSI-aware lipgloss.Width). When truncation occurs, it appends a single
// horizontal-ellipsis rune.
func truncate(s string, width int) string {
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
