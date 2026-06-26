package tui

import (
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"

	"charm.land/lipgloss/v2"
)

// overlay.go owns the modal overlay layer: a LIFO stack of [Overlay] values and
// the ANSI-aware centred compositing of the visible modal over the body region.
//
// Compositing substrate decision (Task 5 discovery):
//
// We adopt lipgloss v2's built-in layer compositor —
// lipgloss.NewLayer(content).X().Y().Z() + lipgloss.NewCompositor(layers...).
// Render() (charm.land/lipgloss/v2@v2.0.4/layer.go) — rather than hand-rolling
// ANSI cell math. The compositor flattens layers, draws them onto an
// ultraviolet screen cell-by-cell (so styled base content is preserved with
// correct cell widths), and already exposes Compositor.Hit(x,y) LayerHit /
// LayerHit.Bounds() for the Stage 2 mouse layer. Hand-rolling would re-derive
// exactly the ANSI width-semantics risk the spec (§ 4 / § 7) flags. No concrete
// gap forces a hand-rolled path: dimming (below) is applied to the base string
// BEFORE it becomes a layer, so the compositor itself needs no extension.

// Overlay layer IDs. They tag the composited layers so the Stage 2 mouse layer
// can distinguish a click on the modal from a click on the dimmed body via
// Compositor.Hit (see overlayClicksOutsideSwallowed).
const (
	overlayBaseLayerID  = "tui.overlay.base"
	overlayModalLayerID = "tui.overlay.modal"
)

// overlayClicksOutsideSwallowed documents the Stage 0 click policy: a mouse
// click that lands outside the visible modal is SWALLOWED (consumed, the body
// does not act behind the modal) but does NOT dismiss the modal. Stage 0 only
// records the policy; Stage 2's mouse layer enforces it, choosing the hit-test
// mechanism (expected: Compositor.Hit / LayerHit.Bounds() over the two layer
// IDs above). No bespoke zone classifier is built here — this constant plus the
// layer IDs are the only seam.
const overlayClicksOutsideSwallowed = true

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
// position); Composite does not clip, so callers must keep overlays within the
// body (the help builder is width-aware for this reason).
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
// The returned string has the same cell dimensions as base provided ov fits
// within body (centerOffset clamps the offset but does not clip).
func Composite(base string, ov Overlay, body Region) string {
	dimmed := dimStyle().Render(base)
	x, y := centerOffset(body, ov)

	baseLayer := lipgloss.NewLayer(dimmed).ID(overlayBaseLayerID)
	modalLayer := lipgloss.NewLayer(ov.Content).X(x).Y(y).Z(1).ID(overlayModalLayerID)

	return lipgloss.NewCompositor(baseLayer, modalLayer).Render()
}
