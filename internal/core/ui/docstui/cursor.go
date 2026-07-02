package docstui

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// cursorGlyph is the left-margin marker drawn on the viewport's cursor row. It
// overwrites glamour's leading document-margin space (width-neutral) rather than
// using a full-line background, which glamour's inline ANSI resets would break.
const cursorGlyph = "▎"

// clampCursor returns line clamped into the valid rendered-row range
// [0, TotalLines-1]; 0 when the document is empty.
func (m *Model) clampCursor(line int) int {
	total := m.Viewport.TotalLines()
	if total <= 0 {
		return 0
	}
	return max(0, min(line, total-1))
}

// setViewportCursor moves the reading cursor to an absolute rendered-row index,
// clamped to the document bounds.
func (m *Model) setViewportCursor(line int) {
	m.viewportCursor = m.clampCursor(line)
}

// moveViewportCursor advances the reading cursor by delta rows (clamped).
func (m *Model) moveViewportCursor(delta int) {
	m.setViewportCursor(m.viewportCursor + delta)
}

// syncViewportToCursor scrolls the viewport the minimum amount needed to keep
// the cursor row on screen (revdiff diffnav.go pattern). It never re-renders —
// the cursor glyph is overdrawn at view time — so it is O(1).
func (m *Model) syncViewportToCursor() {
	h := m.Viewport.VisibleHeight()
	if h <= 0 {
		return
	}
	top := m.Viewport.YOffset()
	switch {
	case m.viewportCursor < top:
		m.Viewport.ScrollToLine(m.viewportCursor)
	case m.viewportCursor >= top+h:
		m.Viewport.ScrollToLine(m.viewportCursor - h + 1)
	}
}

// pinCursorToWindow nudges the cursor back into the visible window after a
// free scroll (mouse wheel) moved the viewport out from under it. Unlike
// syncViewportToCursor it moves the cursor, not the viewport — the user is
// driving the scroll, so the cursor follows to the nearest visible edge.
func (m *Model) pinCursorToWindow() {
	h := m.Viewport.VisibleHeight()
	if h <= 0 {
		return
	}
	top := m.Viewport.YOffset()
	switch {
	case m.viewportCursor < top:
		m.setViewportCursor(top)
	case m.viewportCursor >= top+h:
		m.setViewportCursor(top + h - 1)
	}
}

// cursorGlyphStyled renders the cursor marker in the accent color. Rebuilt per
// call (cheap) so a runtime theme change is picked up.
func cursorGlyphStyled() string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(styles.ColorAccent())).
		Bold(true).
		Render(cursorGlyph)
}

// overwriteFirstCell replaces the first visible cell of line with glyph,
// preserving all leading ANSI escapes and the styling of the remainder. When
// that first cell is glamour's document-margin space the result is
// width-neutral (the common case). When it is not a space (a rare no-margin
// line) the glyph is prepended instead, shifting the row one cell — preferable
// to deleting a content glyph. ANSI handling is delegated to charmbracelet/x/ansi
// so OSC-8 / SGR sequences are never miscounted.
func overwriteFirstCell(line, glyph string) string {
	first := ansi.Strip(ansi.Truncate(line, 1, ""))
	if first == " " {
		// Drop the leading space cell (TruncateLeft preserves the active style
		// for the remainder) and prefix the styled glyph in its place.
		return glyph + ansi.TruncateLeft(line, 1, "")
	}
	return glyph + line
}
