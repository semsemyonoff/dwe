package tui

import (
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"

	"charm.land/lipgloss/v2"
)

// overlay.go owns the modal overlay layer: a LIFO stack of [Overlay] values and
// the ANSI-aware centred compositing of the visible modal over the body region.
//
// Compositing substrate: lipgloss v2's built-in layer compositor —
// lipgloss.NewLayer(content).X().Y().Z() + lipgloss.NewCompositor(layers...).
// Render() (charm.land/lipgloss/v2@v2.0.4/layer.go) — rather than hand-rolled
// ANSI cell math. The compositor flattens layers and draws them onto an
// ultraviolet screen cell-by-cell, so styled base content survives with correct
// cell widths; hand-rolling would re-derive exactly that ANSI width-semantics
// risk. Dimming (below) is applied to the base string BEFORE it becomes a
// layer, so the compositor itself needs no extension.

// Overlay layer IDs. They name the two composited lipgloss layers (see
// [Composite]); hit testing does NOT use them — classifyHit does the
// modal/body split with region math (see hittest.go).
const (
	overlayBaseLayerID  = "tui.overlay.base"
	overlayModalLayerID = "tui.overlay.modal"
)

// overlayClickOutsideDismisses documents the click-outside-modal policy: a
// mouse click that lands outside the visible modal DISMISSES it (the
// click-away-to-close affordance, mirroring esc), while a click inside the modal
// is swallowed so the body never acts behind it. The mouse layer enforces this
// in Frame.handleClick (zoneOutsideModal → dismissTopOverlay); the body is still
// never acted on behind the modal. This constant plus the layer IDs are the only
// seam.
const overlayClickOutsideDismisses = true

// overlayStack is a LIFO stack of modal overlays. Mutual exclusivity (one
// visible modal) is structural: the framework only ever composites Top(), so no
// matter how deep the stack is, at most one modal is on screen. Push layers a
// new modal over the current one; Pop reveals the one beneath.
type overlayStack struct {
	layers []Overlay
}

// Push adds ov as the new top (visible) modal.
func (s *overlayStack) Push(ov Overlay) {
	s.layers = append(s.layers, ov)
}

// Pop removes and returns the top modal. The bool is false when the stack is
// empty (nothing to pop).
func (s *overlayStack) Pop() (Overlay, bool) {
	if len(s.layers) == 0 {
		return Overlay{}, false
	}
	top := s.layers[len(s.layers)-1]
	s.layers = s.layers[:len(s.layers)-1]
	return top, true
}

// ReplaceTop swaps the visible top modal with ov in place, leaving the stack
// depth unchanged. It is used to refresh a capturing overlay's pre-rendered
// snapshot after the plugin mutates its internal state (e.g. a viewport scroll),
// so the refresh does not grow the stack one layer per key. When the stack is
// empty it behaves as Push.
func (s *overlayStack) ReplaceTop(ov Overlay) {
	if len(s.layers) == 0 {
		s.layers = append(s.layers, ov)
		return
	}
	s.layers[len(s.layers)-1] = ov
}

// Top returns the visible modal without removing it. The bool is false when the
// stack is empty (no modal visible).
func (s *overlayStack) Top() (Overlay, bool) {
	if len(s.layers) == 0 {
		return Overlay{}, false
	}
	return s.layers[len(s.layers)-1], true
}

// Empty reports whether no modal is visible.
func (s *overlayStack) Empty() bool {
	return len(s.layers) == 0
}

// centerOffset computes the top-left cell where ov should be drawn to centre it
// within body. The result is offset by body's origin so the modal centres inside
// the body region (typically the inner content region — see Geometry.Overlay —
// so the modal never overlaps the frame border). When the overlay is larger than
// the body span on an axis the offset clamps to the body origin (no negative
// position); Composite clamps the overlay itself to the body as a safety net
// (clampOverlay), but builders should still keep overlays within the body for
// good output (the help builder is width-aware for this reason).
func centerOffset(body Region, ov Overlay) (x, y int) {
	x = body.X + max(0, (body.Width-ov.Width)/2)
	y = body.Y + max(0, (body.Height-ov.Height)/2)
	return x, y
}

// dimStyle is the muted style the body is re-rendered through before the modal
// is placed over it, so the inactive body reads as dimmed beneath the overlay.
// It is built from the styles.ColorMuted() accessor (no v1 lipgloss values).
func dimStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Faint(true).
		Foreground(lipgloss.Color(styles.ColorMuted()))
}

// Composite centres ov over base within the body region and returns the merged
// string. base is the BODY string only (the joined bordered panels) — never the
// full frame — so Composite structurally cannot touch the status line, which the
// caller composes afterwards (and therefore never dims).
//
// Dimming applies to the entire base (body) region: base is re-rendered through
// dimStyle before it becomes the bottom layer; the centred modal is drawn over
// it at full brightness. The compositor preserves the modal's and the body's
// cell styling (ANSI-aware), so a styled base survives with correct widths.
//
// Composite is the single geometry chokepoint, so it clamps ov to the body
// region (clampOverlay) as a final safety net: an overlay larger than the body
// — e.g. a framework help modal built for a previous, larger geometry that has
// not yet been rebuilt after a shrink, or an oversized plugin-supplied overlay —
// would otherwise grow the composited frame past the body (and thus the
// terminal) bounds, since the underlying compositor expands its canvas to fit
// the larger layer. Builders should still size their overlays correctly for
// good-looking output (the help builder is width/height-aware); this clamp only
// guarantees the never-overflow invariant when they do not. The returned string
// therefore always has the same cell dimensions as base.
func Composite(base string, ov Overlay, body Region) string {
	dimmed := dimStyle().Render(base)
	// A post-launch resize can drive either body span to zero (clampOverlay only
	// trims an axis whose body span is positive, so a zero-area body would leave a
	// stale full-size modal and grow the output past base). Return the dimmed base
	// untouched in that degenerate case, preserving the same-dimensions invariant.
	if body.Width <= 0 || body.Height <= 0 {
		return dimmed
	}
	ov = clampOverlay(ov, body)
	x, y := centerOffset(body, ov)

	baseLayer := lipgloss.NewLayer(dimmed).ID(overlayBaseLayerID)
	modalLayer := lipgloss.NewLayer(ov.Content).X(x).Y(y).Z(1).ID(overlayModalLayerID)

	return lipgloss.NewCompositor(baseLayer, modalLayer).Render()
}

// clampOverlay truncates ov to fit within the body region on both axes so a
// centred modal can never extend past the body bounds. It is a no-op when ov
// already fits (the common case — builders size overlays to the body); only an
// oversized overlay (a stale, pre-resize help modal or an oversized plugin
// overlay) is trimmed, with MaxWidth/MaxHeight cutting the overflowing edge.
// Width/Height are recomputed from the truncated content so centerOffset uses
// the real post-clamp dimensions.
func clampOverlay(ov Overlay, body Region) Overlay {
	if ov.Width <= body.Width && ov.Height <= body.Height {
		return ov
	}
	content := ov.Content
	if body.Width > 0 {
		content = lipgloss.NewStyle().MaxWidth(body.Width).Render(content)
	}
	if body.Height > 0 {
		content = lipgloss.NewStyle().MaxHeight(body.Height).Render(content)
	}
	return Overlay{
		Content: content,
		Width:   lipgloss.Width(content),
		Height:  lipgloss.Height(content),
	}
}
