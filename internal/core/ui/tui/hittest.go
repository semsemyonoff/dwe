package tui

// hitZone classifies where a mouse event landed relative to the frame's
// visible regions.
type hitZone int

const (
	// zoneNone is blank space (status bar gaps, body edges) — swallow.
	zoneNone hitZone = iota
	// zonePanel is inside a body panel's outer region (border + content).
	zonePanel
	// zoneHelpHint is the right-hand help-key hint on the status line.
	zoneHelpHint
	// zoneModal is inside the visible centred overlay.
	zoneModal
	// zoneOutsideModal is outside the visible overlay — a click here dismisses
	// the overlay (the click-away-to-close affordance; see handleClick).
	zoneOutsideModal
)

// panelRect pairs a panel's identity with its outer region for hit-testing.
type panelRect struct {
	ID     PanelID
	Region Region
}

// contains reports whether the terminal cell (x, y) lies within r. The right
// and bottom edges are exclusive (x < X+Width, y < Y+Height).
func (r Region) contains(x, y int) bool {
	return x >= r.X && x < r.X+r.Width &&
		y >= r.Y && y < r.Y+r.Height
}

// classifyHit classifies the terminal coordinates (x, y) into a hitZone.
//
// When ov is non-nil (an overlay is visible) the overlay's centred bounds are
// computed via centerOffset(geo.Overlay, *ov). Points inside the bounds →
// zoneModal; all other points → zoneOutsideModal. Panel and help-hint
// classification are suppressed while a modal is open.
//
// When ov is nil: a point within helpHint → zoneHelpHint; a point within any
// panel's outer region → zonePanel + that panel's PanelID; anything else →
// zoneNone.
//
// Region math is used rather than lipgloss Compositor.Hit because the
// compositor only knows the base and modal layers — it cannot classify panels
// or the help-hint zone. Region math is needed regardless, and the
// inside/outside modal test is trivially the centred overlay bounds.
func classifyHit(geo Geometry, panels []panelRect, helpHint Region, ov *Overlay, x, y int) (hitZone, PanelID) {
	if ov != nil {
		mx, my := centerOffset(geo.Overlay, *ov)
		modal := Region{X: mx, Y: my, Width: ov.Width, Height: ov.Height}
		if modal.contains(x, y) {
			return zoneModal, ""
		}
		return zoneOutsideModal, ""
	}

	if helpHint.contains(x, y) {
		return zoneHelpHint, ""
	}
	for _, p := range panels {
		if p.Region.contains(x, y) {
			return zonePanel, p.ID
		}
	}
	return zoneNone, ""
}
