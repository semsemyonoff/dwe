package docstui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// errorState owns the scrollable modal that shows the full mmdc render error for
// a diagram (the "render failed" placeholder only hints; this surfaces the real
// cause, e.g. "Could not find Chrome …"). It mirrors the cmdbrowser inspect
// overlay: a viewport in a rounded-border box flagged CapturesInput, with a
// proportional scrollbar overdrawn on the right border. The Model holds a
// *errorState; nil means the overlay is closed.
type errorState struct {
	vp viewport.Model
	// errText is the raw render error (without the "Diagram N/M" header) so the
	// copy-all key can put exactly the error on the clipboard.
	errText string
	// num / total identify the diagram (for the "Diagram N/M" header) and are kept
	// so the box dimensions can be recomputed when leaving selection mode.
	num, total int
	// w / h are the current viewport content dimensions (inside the box in normal
	// mode; the full body in selection mode). Tracked so overlay() can pad the
	// selection-mode block to a fully opaque body-sized rectangle.
	w, h int
	// selecting is the "selection mode" toggle: when true the overlay asks the
	// Frame to release the mouse (ReleaseMouse) AND take over the full terminal
	// (FullScreen), rendering as a border-free, opaque block so the ONLY selectable
	// cells are the error text — no frame chrome (panel borders, status line) or
	// dimmed panels remain on screen to bleed into a native selection (the terminal
	// owns native selection and cannot be clipped to a sub-region, so we remove
	// everything else from the screen instead). Toggled by the `s` key.
	selecting bool
}

// errorOverlay chrome sizing. Mirrors cmdbrowser's inspect overlay: the box adds
// a 1-cell rounded border + 1-cell horizontal padding per side (4 horizontal,
// 2 vertical). The box widens to fit the widest error line (so stack traces stay
// on one line when the terminal is wide enough) but never below errorBoxMinWidth
// and never past the available body width.
const (
	errorBoxMinWidth = 50
	errorBoxHChrome  = 4
	errorBoxVChrome  = 2
)

// formatErrorContent renders the modal body for a diagram render failure. Kept
// separate from newErrorState so the sizing pass can measure the exact text the
// viewport will show (including the empty-error fallback).
func formatErrorContent(num, total int, errText string) string {
	if strings.TrimSpace(errText) == "" {
		errText = "(no error detail available)"
	}
	return fmt.Sprintf("Diagram %d/%d — render failed\n\n%s", num, total, errText)
}

// errorContentWidth returns the display width of the widest line in content —
// the inner width the box would need to show every line without wrapping.
func errorContentWidth(content string) int {
	w := 0
	for line := range strings.SplitSeq(content, "\n") {
		if lw := lipgloss.Width(line); lw > w {
			w = lw
		}
	}
	return w
}

// newErrorState builds the error overlay's viewport at (width, height) showing
// the render error for diagram num/total. An empty error falls back to a
// placeholder so the box is never blank.
func newErrorState(width, height, num, total int, errText string) *errorState {
	w := max(width, 10)
	h := max(height, 3)
	vp := viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))
	// Soft-wrap long lines (stack traces, file paths) to the box width instead of
	// requiring fiddly horizontal scrolling to read them — the box is already
	// sized to the content where the terminal allows, so wrapping only kicks in
	// when a line genuinely exceeds the available width.
	vp.SoftWrap = true
	vp.SetContent(formatErrorContent(num, total, errText))
	return &errorState{vp: vp, errText: errText, num: num, total: total, w: w, h: h}
}

// resize updates the viewport content dimensions (and records them so the
// selection-mode block can pad to the same rectangle). SoftWrap re-flows the
// content to the new width.
func (s *errorState) resize(w, h int) {
	s.w = max(w, 10)
	s.h = max(h, 3)
	s.vp.SetWidth(s.w)
	s.vp.SetHeight(s.h)
}

// overlay renders the error viewport as a centred CapturesInput modal so the
// Frame routes scroll/esc to it. A hint footer beneath the box advertises the
// copy / selection / close keys. When selection mode is on the overlay asks the
// Frame to release the mouse (ReleaseMouse) so the terminal's native selection
// works. Width/Height are measured from the rendered box so centring uses the
// real post-border dimensions.
func (s *errorState) overlay() tui.Overlay {
	if s.selecting {
		return s.selectionOverlay()
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(styles.ColorBorder())).
		Padding(0, 1).
		Render(s.vp.View())
	box = tui.OverlayScrollbar(box, s.vp.YOffset(), s.vp.TotalLineCount())
	content := lipgloss.JoinVertical(lipgloss.Left, box, s.hintLine())
	return tui.Overlay{
		Content:       content,
		Width:         lipgloss.Width(content),
		Height:        lipgloss.Height(content),
		CapturesInput: true,
		ReleaseMouse:  s.selecting,
	}
}

// selectionOverlay renders the full-body, border-free, opaque block used in
// selection mode. Every cell of the body is an error-text or blank-padding cell
// (no border, no scrollbar, no dimmed panels showing through), so a native
// terminal drag-select can only grab the error — the closest we can get to
// clipping selection to the overlay when the terminal owns the selection. The
// viewport was already resized to the full body (minus the hint row) by
// applyErrorOverlayDims, so the padded block + hint exactly fills the body.
func (s *errorState) selectionOverlay() tui.Overlay {
	block := lipgloss.NewStyle().Width(s.w).Height(s.h).Render(s.vp.View())
	// Bound the hint to the block width: on a narrow terminal the hint text is
	// wider than the full-screen block, which would push the overlay past the
	// terminal edge (fullScreenOverlayView would then clamp and truncate it).
	hint := lipgloss.NewStyle().MaxWidth(s.w).Render(s.hintLine())
	content := lipgloss.JoinVertical(lipgloss.Left, block, hint)
	return tui.Overlay{
		Content:       content,
		Width:         lipgloss.Width(content),
		Height:        lipgloss.Height(content),
		CapturesInput: true,
		ReleaseMouse:  true,
		FullScreen:    true,
	}
}

// hintLine renders the footer key hints beneath the error box. In selection mode
// it also flags that the mouse is released for native drag-select.
func (s *errorState) hintLine() string {
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorMuted()))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorAccent()))
	if s.selecting {
		return accent.Render(" select mode — drag to select · ") + muted.Render("s exit · c copy all · esc close")
	}
	return muted.Render(" c copy all · s select · esc close")
}
