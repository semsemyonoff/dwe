package tui

import "testing"

// TestRegion_Contains verifies the contains helper covers all four edge cases
// (inclusive corners, exclusive right/bottom edges, interior, far outside).
func TestRegion_Contains(t *testing.T) {
	r := Region{X: 5, Y: 10, Width: 10, Height: 5}
	tests := []struct {
		x, y int
		want bool
	}{
		{5, 10, true},   // top-left corner (inclusive)
		{14, 14, true},  // bottom-right corner (x+w-1, y+h-1, inclusive)
		{15, 10, false}, // one past right edge (exclusive)
		{5, 15, false},  // one past bottom edge (exclusive)
		{4, 10, false},  // one before left edge
		{5, 9, false},   // one above top edge
		{10, 12, true},  // interior point
		{0, 0, false},   // far outside
	}
	for _, tc := range tests {
		got := r.contains(tc.x, tc.y)
		if got != tc.want {
			t.Errorf("contains(%d,%d) = %v; want %v", tc.x, tc.y, got, tc.want)
		}
	}
}

// TestClassifyHit_NoOverlay_Panels verifies that a point inside each panel's
// outer region classifies as zonePanel with the correct PanelID, across all
// canonical frame width buckets (odd + even so remainder policies are stressed).
func TestClassifyHit_NoOverlay_Panels(t *testing.T) {
	for _, w := range widthBuckets {
		t.Run("width_"+itoa(w), func(t *testing.T) {
			f, _ := newTestFrame(t, w, frameGoldenHeight)
			panels := f.panelRects()
			hint := f.helpHintRegion()

			for _, p := range panels {
				// Use the centre of the outer region so the point is
				// unambiguously inside (not on a border shared with another panel).
				cx := p.Region.X + p.Region.Width/2
				cy := p.Region.Y + p.Region.Height/2
				zone, id := classifyHit(f.geo, panels, hint, nil, cx, cy)
				if zone != zonePanel {
					t.Errorf("panel %q centre (%d,%d): got zone %v; want zonePanel", p.ID, cx, cy, zone)
				}
				if id != p.ID {
					t.Errorf("panel %q centre (%d,%d): got PanelID %q; want %q", p.ID, cx, cy, id, p.ID)
				}
			}
		})
	}
}

// TestClassifyHit_NoOverlay_HelpHint verifies that a point within the
// right-hand help-key hint on the status line classifies as zoneHelpHint.
func TestClassifyHit_NoOverlay_HelpHint(t *testing.T) {
	for _, w := range widthBuckets {
		t.Run("width_"+itoa(w), func(t *testing.T) {
			f, _ := newTestFrame(t, w, frameGoldenHeight)
			panels := f.panelRects()
			hint := f.helpHintRegion()

			// Click in the centre of the hint region.
			cx := hint.X + hint.Width/2
			cy := hint.Y
			zone, id := classifyHit(f.geo, panels, hint, nil, cx, cy)
			if zone != zoneHelpHint {
				t.Errorf("width=%d: hint centre (%d,%d): got zone %v; want zoneHelpHint", w, cx, cy, zone)
			}
			if id != "" {
				t.Errorf("width=%d: help-hint hit returned non-empty PanelID %q", w, id)
			}
		})
	}
}

// TestClassifyHit_NoOverlay_None verifies that a point in the status bar
// outside the help-hint zone classifies as zoneNone.
func TestClassifyHit_NoOverlay_None(t *testing.T) {
	f, _ := newTestFrame(t, 80, frameGoldenHeight)
	panels := f.panelRects()
	hint := f.helpHintRegion()

	// Status line at X=0 is not in any panel (body spans Y=0..outer.Height-1)
	// and not in the help-hint (which is at the right edge).
	zone, id := classifyHit(f.geo, panels, hint, nil, 0, f.geo.Status.Y)
	if zone != zoneNone {
		t.Errorf("status line X=0,Y=%d: got zone %v; want zoneNone", f.geo.Status.Y, zone)
	}
	if id != "" {
		t.Errorf("zoneNone hit returned non-empty PanelID %q", id)
	}
}

// TestClassifyHit_WithOverlay_Modal verifies that when an overlay is present
// the entire classification is modal/outside-modal: inside the centred bounds
// → zoneModal; outside → zoneOutsideModal; and that panel and help-hint zones
// are NOT returned while a modal is open.
func TestClassifyHit_WithOverlay_Modal(t *testing.T) {
	f, _ := newTestFrame(t, 80, frameGoldenHeight)
	panels := f.panelRects()
	hint := f.helpHintRegion()

	ov := &Overlay{Content: "modal content here!!", Width: 20, Height: 5}
	mx, my := centerOffset(f.geo.Overlay, *ov)

	// A point inside the modal bounds → zoneModal.
	zone, id := classifyHit(f.geo, panels, hint, ov, mx+1, my+1)
	if zone != zoneModal {
		t.Errorf("inside modal (%d,%d): got zone %v; want zoneModal", mx+1, my+1, zone)
	}
	if id != "" {
		t.Errorf("inside modal: got non-empty PanelID %q", id)
	}

	// A point outside the modal (far top-left, in the border zone) →
	// zoneOutsideModal, not the panel or body zones.
	zone, id = classifyHit(f.geo, panels, hint, ov, 0, 0)
	if zone != zoneOutsideModal {
		t.Errorf("outside modal (0,0): got zone %v; want zoneOutsideModal", zone)
	}
	if id != "" {
		t.Errorf("outside modal: got non-empty PanelID %q", id)
	}

	// A point that would classify as zonePanel without a modal → still
	// zoneOutsideModal when the overlay is present.
	panelCX := panels[0].Region.X + panels[0].Region.Width/2
	panelCY := panels[0].Region.Y + panels[0].Region.Height/2
	zone, _ = classifyHit(f.geo, panels, hint, ov, panelCX, panelCY)
	if zone == zonePanel {
		t.Errorf("point in panel body while overlay open: got zonePanel; want zoneModal or zoneOutsideModal")
	}

	// A point that would classify as zoneHelpHint → still overlay zone, not
	// zoneHelpHint.
	hintCX := hint.X + hint.Width/2
	hintCY := hint.Y
	zone, _ = classifyHit(f.geo, panels, hint, ov, hintCX, hintCY)
	if zone == zoneHelpHint {
		t.Errorf("help-hint point while overlay open: got zoneHelpHint; want zoneModal or zoneOutsideModal")
	}
}

// TestClassifyHit_WithOverlay_InsideModalCorners verifies the inclusive
// top-left and exclusive bottom-right boundary of the centred modal region.
func TestClassifyHit_WithOverlay_InsideModalCorners(t *testing.T) {
	f, _ := newTestFrame(t, 80, frameGoldenHeight)
	panels := f.panelRects()
	hint := f.helpHintRegion()

	ov := &Overlay{Content: "box", Width: 10, Height: 4}
	mx, my := centerOffset(f.geo.Overlay, *ov)

	// Top-left corner (inclusive).
	zone, _ := classifyHit(f.geo, panels, hint, ov, mx, my)
	if zone != zoneModal {
		t.Errorf("modal top-left (%d,%d): got zone %v; want zoneModal", mx, my, zone)
	}

	// Bottom-right corner (inclusive: x=mx+w-1, y=my+h-1).
	zone, _ = classifyHit(f.geo, panels, hint, ov, mx+ov.Width-1, my+ov.Height-1)
	if zone != zoneModal {
		t.Errorf("modal bottom-right (%d,%d): got zone %v; want zoneModal", mx+ov.Width-1, my+ov.Height-1, zone)
	}

	// One past the right edge (exclusive).
	zone, _ = classifyHit(f.geo, panels, hint, ov, mx+ov.Width, my)
	if zone != zoneOutsideModal {
		t.Errorf("one past modal right (%d,%d): got zone %v; want zoneOutsideModal", mx+ov.Width, my, zone)
	}

	// One past the bottom edge (exclusive).
	zone, _ = classifyHit(f.geo, panels, hint, ov, mx, my+ov.Height)
	if zone != zoneOutsideModal {
		t.Errorf("one past modal bottom (%d,%d): got zone %v; want zoneOutsideModal", mx, my+ov.Height, zone)
	}
}

// TestPanelRects_MatchesLayoutPanels verifies that panelRects() returns outer
// regions that sum to the body width and share the correct panel IDs.
func TestPanelRects_MatchesLayoutPanels(t *testing.T) {
	for _, w := range widthBuckets {
		t.Run("width_"+itoa(w), func(t *testing.T) {
			f, _ := newTestFrame(t, w, frameGoldenHeight)
			rects := f.panelRects()

			if len(rects) == 0 {
				t.Fatal("panelRects() returned empty slice")
			}

			sum := 0
			for _, r := range rects {
				if r.Region.Width <= 0 {
					t.Errorf("panel %q has non-positive width %d", r.ID, r.Region.Width)
				}
				sum += r.Region.Width
			}
			if sum != w {
				t.Errorf("panel widths sum = %d; want frame width %d", sum, w)
			}

			// IDs must match the plugin's panel declarations in order.
			panels := f.plugin.Panels()
			for i, r := range rects {
				if r.ID != panels[i].ID {
					t.Errorf("rects[%d].ID = %q; want %q", i, r.ID, panels[i].ID)
				}
			}
		})
	}
}

// TestHelpHintRegion_RightAligned verifies that helpHintRegion() places the
// hint at the right edge of the status line and that Width is positive.
func TestHelpHintRegion_RightAligned(t *testing.T) {
	for _, w := range widthBuckets {
		t.Run("width_"+itoa(w), func(t *testing.T) {
			f, _ := newTestFrame(t, w, frameGoldenHeight)
			hint := f.helpHintRegion()

			if hint.Width <= 0 {
				t.Errorf("width=%d: hint width %d; want positive", w, hint.Width)
			}
			if hint.X+hint.Width != w {
				t.Errorf("width=%d: hint right edge %d; want %d (frame width)", w, hint.X+hint.Width, w)
			}
			if hint.Y != f.geo.Status.Y {
				t.Errorf("width=%d: hint Y %d; want status Y %d", w, hint.Y, f.geo.Status.Y)
			}
			if hint.Height != 1 {
				t.Errorf("width=%d: hint height %d; want 1", w, hint.Height)
			}
		})
	}
}
