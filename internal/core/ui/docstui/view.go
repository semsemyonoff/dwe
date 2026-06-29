package docstui

import (
	"charm.land/lipgloss/v2"
)

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

// footerRows is the fixed height reserved for the help footer in the legacy
// standalone model path (kept so NewModel's initial geometry is stable).
const footerRows = 2

// Scrollbar runes used by applyInnerScrollbar in plugin.go.
const (
	scrollbarThumbGlyph = "█"
	scrollbarTrackGlyph = "░"
)

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

// leftWidth returns the frame width of the left tree panel.
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
