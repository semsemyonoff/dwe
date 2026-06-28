package cmdbrowser

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// inspectState owns the viewport overlay shown while the user is inspecting a
// single command. The Model holds a *inspectState; nil means the overlay is
// closed. inspectIdx remembers which item is being shown so Enter can return
// the right Result.
type inspectState struct {
	vp         viewport.Model
	inspectIdx int // index into Model.items
}

// newInspectState builds a viewport at the given (width, height). The render
// closure is called with the final content width so the builder word-wraps to
// the actual viewport — passing a pre-rendered string wrapped to the terminal
// would clip on the right edge. A nil render (or one that returns "") falls
// back to a placeholder. The caller is responsible for sizing — see
// Model.inspectViewportSize.
func newInspectState(width, height int, render func(width int) string, idx int) *inspectState {
	w := max(width, 10)
	h := max(height, 3)
	vp := viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))
	applyViewportStyles(&vp)
	content := ""
	if render != nil {
		content = render(w)
	}
	if content == "" {
		content = "(no inspect content available)"
	}
	vp.SetContent(content)
	return &inspectState{vp: vp, inspectIdx: idx}
}

// overlay renders the inspect viewport as a centred modal [tui.Overlay]: the
// viewport content wrapped in a rounded-border box (mirroring the framework
// help modal) and flagged CapturesInput so the Frame routes navigation/enter/esc
// while it is the top overlay (see routeWhileCapturing). The Frame dims the body
// beneath and centres this box over it. Width/Height are measured from the
// rendered box so centring uses the real post-border dimensions; the viewport is
// pre-sized (browser.inspectViewportSize) to leave room for the border + padding
// so the box never overflows the body region.
func (s *inspectState) overlay() tui.Overlay {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(styles.ColorBorder())).
		Padding(0, 1).
		Render(s.vp.View())
	box = s.applyScrollbar(box)
	return tui.Overlay{
		Content:       box,
		Width:         lipgloss.Width(box),
		Height:        lipgloss.Height(box),
		CapturesInput: true,
	}
}

// Scrollbar runes. The box's rounded right border (`scrollbarBorderRune`, `│`)
// is the overdraw target; on the thumb rows it becomes a solid accent block
// (`█`) and on the remaining rows a muted shaded track (`░`), so the right
// column reads as a scrollbar with a clearly visible thumb. Mirrors the
// docs-browser scrollbar (internal/core/docs/tui/view.go).
const (
	scrollbarBorderRune = "│"
	scrollbarThumbGlyph = "█"
	scrollbarTrackGlyph = "░"
)

// applyScrollbar overdraws a proportional scrollbar onto the right border of
// the already-rendered inspect box: a muted shaded track down the full content
// height with a solid accent thumb at the current scroll position. It returns
// the box unchanged when the whole description fits (nothing to scroll). Thumb
// size and position mirror the viewport's own offset/total so the bar tracks
// 1:1 with scrolling.
func (s *inspectState) applyScrollbar(box string) string {
	lines := strings.Split(box, "\n")
	if len(lines) < 3 {
		return box // no content rows between the top/bottom border rows
	}
	vh := len(lines) - 2 // rows between the border rows
	total := s.vp.TotalLineCount()
	if vh <= 0 || total <= vh {
		return box // everything is visible — no scrollbar needed
	}

	thumbSize := vh * vh / total
	thumbSize = min(max(thumbSize, 1), vh)
	maxStart := vh - thumbSize
	thumbStart := 0
	if denom := total - vh; denom > 0 {
		thumbStart = min(s.vp.YOffset()*maxStart/denom, maxStart)
	}

	thumb := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorAccent())).Bold(true).Render(scrollbarThumbGlyph)
	track := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorMuted())).Render(scrollbarTrackGlyph)
	for i := range vh {
		glyph := track
		if i >= thumbStart && i < thumbStart+thumbSize {
			glyph = thumb
		}
		lines[1+i] = replaceLastRune(lines[1+i], scrollbarBorderRune, glyph) // 1+i skips the top border row
	}
	return strings.Join(lines, "\n")
}

// replaceLastRune swaps the last occurrence of old in line for repl, leaving
// any surrounding ANSI styling intact. Used to overwrite the box's rightmost
// border rune with the scrollbar thumb.
func replaceLastRune(line, old, repl string) string {
	idx := strings.LastIndex(line, old)
	if idx < 0 {
		return line
	}
	return line[:idx] + repl + line[idx+len(old):]
}
