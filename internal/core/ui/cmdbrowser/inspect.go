package cmdbrowser

import (
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
	box = tui.OverlayScrollbar(box, s.vp.YOffset(), s.vp.TotalLineCount())
	return tui.Overlay{
		Content:       box,
		Width:         lipgloss.Width(box),
		Height:        lipgloss.Height(box),
		CapturesInput: true,
	}
}
