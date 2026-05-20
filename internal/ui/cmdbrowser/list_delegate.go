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
type listItem struct {
	origIdx int
	id      string
	desc    string
	typ     string
}

// FilterValue is the haystack used by list.DefaultFilter. Concatenating id and
// description so the fuzzy match covers both — matches the §5 spec.
func (li listItem) FilterValue() string {
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

	idCellWidth := max(avail-len(cursor)-badgeWidth, 4)
	id := truncate(li.id, idCellWidth)
	idStyled := id
	if isSelected {
		idStyled = lipgloss.NewStyle().Bold(true).Render(id)
	}
	// Pad to the available width so the badge right-aligns. Pad on the
	// rendered-id width to keep alignment stable with ANSI escapes.
	padCount := max(idCellWidth-lipgloss.Width(idStyled), 0)
	pad := strings.Repeat(" ", padCount)

	line1 := cursor + idStyled + pad + badge

	desc := li.desc
	if desc != "" {
		descAvail := max(avail-len(cursor), 8)
		desc = truncate(desc, descAvail)
		desc = lipgloss.NewStyle().Faint(true).Render(desc)
		line2 := strings.Repeat(" ", len(cursor)) + desc
		_, _ = fmt.Fprintf(w, "%s\n%s", line1, line2)
		return
	}
	_, _ = fmt.Fprintf(w, "%s\n%s", line1, strings.Repeat(" ", len(cursor)))
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
