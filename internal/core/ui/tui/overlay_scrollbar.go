package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// overlay_scrollbar.go holds the shared chrome for the plugins' CapturesInput
// viewport overlays (the cmdbrowser inspect modal and the docstui render-error
// modal): a proportional right-border scrollbar and the coalesced-wheel scroll.
// Both overlays are rounded-border boxes wrapping a bubbles viewport, so the
// behaviour is identical — this is the single home for it.

// Overlay scrollbar runes. The box's rounded right border (`│`) is the overdraw
// target; thumb rows become a solid accent block (`█`) and the rest a muted
// shaded track (`░`), so the right column reads as a scrollbar with a visible
// thumb. Thumb/track are exported so consumers' tests can assert the scrollbar
// drew without duplicating the literal glyphs.
const (
	overlayScrollbarBorderRune = "│"
	// OverlayScrollbarThumbGlyph / OverlayScrollbarTrackGlyph are the thumb and
	// track runes OverlayScrollbar overdraws onto the box's right border.
	OverlayScrollbarThumbGlyph = "█"
	OverlayScrollbarTrackGlyph = "░"
)

// OverlayScrollbar overdraws a proportional scrollbar onto the right rounded
// border of an already-rendered overlay box: a muted shaded track down the full
// content height with a solid accent thumb at the current scroll position. It
// returns box unchanged when everything fits (nothing to scroll). yOffset and
// totalLines come from the overlay's embedded viewport (YOffset /
// TotalLineCount), so the bar tracks 1:1 with scrolling. Kept viewport-free (it
// takes the two ints rather than a viewport.Model) so the geometry stays
// decoupled from the widget type.
func OverlayScrollbar(box string, yOffset, totalLines int) string {
	lines := strings.Split(box, "\n")
	if len(lines) < 3 {
		return box // no content rows between the top/bottom border rows
	}
	vh := len(lines) - 2 // rows between the border rows
	if vh <= 0 || totalLines <= vh {
		return box // everything is visible — no scrollbar needed
	}

	thumbSize := min(max(vh*vh/totalLines, 1), vh)
	maxStart := vh - thumbSize
	thumbStart := 0
	if denom := totalLines - vh; denom > 0 {
		thumbStart = min(yOffset*maxStart/denom, maxStart)
	}

	thumb := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorAccent())).Bold(true).Render(OverlayScrollbarThumbGlyph)
	track := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorMuted())).Render(OverlayScrollbarTrackGlyph)
	for i := range vh {
		glyph := track
		if i >= thumbStart && i < thumbStart+thumbSize {
			glyph = thumb
		}
		lines[1+i] = replaceLastOverlayRune(lines[1+i], overlayScrollbarBorderRune, glyph) // 1+i skips the top border row
	}
	return strings.Join(lines, "\n")
}

// replaceLastOverlayRune swaps the last occurrence of old in line for repl,
// leaving any surrounding ANSI styling intact (used to overwrite the box's
// rightmost border rune with the scrollbar thumb).
func replaceLastOverlayRune(line, old, repl string) string {
	idx := strings.LastIndex(line, old)
	if idx < 0 {
		return line
	}
	return line[:idx] + repl + line[idx+len(old):]
}

// OverlayWheelStep is how many lines one coalesced wheel notch scrolls an
// overlay's embedded viewport — matches the viewport's own MouseWheelDelta
// default so a notch feels the same as before wheel coalescing.
const OverlayWheelStep = 3

// ScrollOverlayViewport scrolls vp by delta coalesced wheel notches (delta<0 up,
// delta>0 down), OverlayWheelStep lines each. No-op when delta==0. Consumed by
// plugins that scroll a CapturesInput overlay's viewport in response to a
// coalesced WheelMsg{Panel: OverlayWheelPanel}.
func ScrollOverlayViewport(vp *viewport.Model, delta int) {
	switch {
	case delta < 0:
		vp.ScrollUp(-delta * OverlayWheelStep)
	case delta > 0:
		vp.ScrollDown(delta * OverlayWheelStep)
	}
}
