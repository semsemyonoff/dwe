package tui

// Geometry is the single source of truth for outer-vs-inner sizing, border
// ownership, and the overlay coordinate space. It is computed once per resize
// (from a terminal width/height) and handed to the rest of the framework so the
// per-TUI border/width fixes that lipgloss v2 forced in cmdbrowser are not
// re-derived in every surface.
//
// Border ownership rule: the FRAME draws every border. Plugins only ever receive
// INNER dimensions (the content area inside the border + padding) — they must
// never draw the frame border themselves.

// Chrome constants. These describe the cells the frame's chrome consumes around
// plugin content. They are provisional (Stage 0) and may be tuned once a real
// surface is migrated.
const (
	// statusLineRows is the height of the bottom status line, which lives below
	// the body frame and is never part of the overlay coordinate space.
	statusLineRows = 1

	// borderSize is the width of one panel border edge (one cell per side).
	borderSize = 1

	// hPadding / vPadding are the inner padding cells per side, matching the
	// lipgloss Padding(0, 1) convention used elsewhere in core/ui. Horizontal
	// padding gives body content breathing room; vertical padding is zero so the
	// frame fills the terminal height exactly.
	hPadding = 1
	vPadding = 0

	// minWidth / minHeight are the minimum usable terminal dimensions. Below
	// either, tooNarrow reports true and the launch helper drops to a fallback
	// (a later stage). Provisional thresholds.
	minWidth  = 40
	minHeight = 10
)

// Region is an axis-aligned rectangle in terminal cell coordinates. X/Y are the
// top-left corner (0-based); Width/Height are cell counts.
type Region struct {
	X, Y, Width, Height int
}

// Geometry holds every region the frame needs for one render pass.
//
//   - Term is the full terminal.
//   - Outer is the body frame region — the terminal minus the bottom status
//     line. Body panels are laid out (via [layoutPanels]) inside Outer; the
//     frame draws each panel's border here.
//   - Inner is the content region of a single full-width body panel: Outer minus
//     the border and padding on every side. Plugins render into inner regions
//     only.
//   - Status is the bottom status line (one row, full width).
//   - Overlay is the overlay coordinate space. It equals the inner body region
//     and therefore NEVER covers the status line — modals composite over body
//     content only.
type Geometry struct {
	Term    Region
	Outer   Region
	Inner   Region
	Status  Region
	Overlay Region
}

// newGeometry computes the frame geometry from a terminal size. Negative results
// are clamped to zero so a degenerate terminal cannot produce nonsense regions
// (the launch path additionally refuses to start when [tooNarrow] is true).
func newGeometry(w, h int) Geometry {
	outer := Region{
		X:      0,
		Y:      0,
		Width:  max(w, 0),
		Height: max(h-statusLineRows, 0),
	}
	inner := contentRegion(outer)
	status := Region{
		X:      0,
		Y:      outer.Height,
		Width:  max(w, 0),
		Height: statusLineRows,
	}
	return Geometry{
		Term:    Region{X: 0, Y: 0, Width: max(w, 0), Height: max(h, 0)},
		Outer:   outer,
		Inner:   inner,
		Status:  status,
		Overlay: inner,
	}
}

// contentRegion subtracts the frame chrome (border + padding on every side) from
// a panel's outer region, yielding the inner region the plugin renders into.
// This is the single outer→inner subtraction — callers must not double-count the
// border.
func contentRegion(outer Region) Region {
	return Region{
		X:      outer.X + borderSize + hPadding,
		Y:      outer.Y + borderSize + vPadding,
		Width:  max(outer.Width-2*(borderSize+hPadding), 0),
		Height: max(outer.Height-2*(borderSize+vPadding), 0),
	}
}

// tooNarrow reports whether the terminal is below the minimum usable size. The
// launch helper uses it to drop to a plain-selector fallback instead of
// rendering a torn frame.
func tooNarrow(w, h int) bool {
	return w < minWidth || h < minHeight
}

// layoutPanels splits body horizontally into len(weights) outer regions by
// weight. The widths sum EXACTLY to body.Width: each non-final panel takes its
// proportional floor (body.Width*weight/total) and the LAST panel absorbs the
// remainder. This mirrors cmdbrowser's `right = total − left` math and avoids the
// column-leak that naive per-panel `w*weight/sum` rounding produces at odd widths
// (79, 99). Every region shares body's Y and Height; only X and Width differ.
//
// Precondition: weights is non-empty and every weight is positive. The caller
// (newFrame, Task 7) validates this before launch, so layoutPanels has no error
// path and stays pure for the View hot path — a violated precondition is a
// programmer error, not a runtime condition.
func layoutPanels(body Region, weights []int) []Region {
	total := 0
	for _, wt := range weights {
		total += wt
	}

	regions := make([]Region, len(weights))
	x := body.X
	consumed := 0
	last := len(weights) - 1
	for i, wt := range weights {
		width := body.Width * wt / total
		if i == last {
			// The last panel absorbs whatever the proportional floors left
			// behind, so the outer widths sum exactly to body.Width.
			width = body.Width - consumed
		}
		regions[i] = Region{X: x, Y: body.Y, Width: width, Height: body.Height}
		x += width
		consumed += width
	}
	return regions
}
