package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

func TestOverlayScrollbar(t *testing.T) {
	// A rounded box around 3 content rows: 5 lines total, so vh = 3.
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Render("a\nb\nc")

	// Everything fits (totalLines <= vh) → box returned unchanged.
	if got := OverlayScrollbar(box, 0, 3); got != box {
		t.Error("OverlayScrollbar drew a scrollbar when content fits")
	}

	// Overflow → thumb + track overdrawn on the right border.
	out := OverlayScrollbar(box, 0, 100)
	if !strings.Contains(out, OverlayScrollbarThumbGlyph) {
		t.Errorf("missing thumb glyph:\n%s", out)
	}
	if !strings.Contains(out, OverlayScrollbarTrackGlyph) {
		t.Errorf("missing track glyph:\n%s", out)
	}

	// A box with too few lines to have content rows is returned unchanged.
	if got := OverlayScrollbar("only\nborder", 0, 100); got != "only\nborder" {
		t.Error("OverlayScrollbar mutated a box with no content rows")
	}
}

func TestScrollOverlayViewport(t *testing.T) {
	vp := viewport.New(viewport.WithWidth(10), viewport.WithHeight(3))
	vp.SetContent(strings.Repeat("line\n", 50))

	ScrollOverlayViewport(&vp, 2) // down 2 notches
	if got := vp.YOffset(); got != 2*OverlayWheelStep {
		t.Errorf("after down 2: YOffset = %d; want %d", got, 2*OverlayWheelStep)
	}

	ScrollOverlayViewport(&vp, -1) // up 1 notch
	if got := vp.YOffset(); got != OverlayWheelStep {
		t.Errorf("after up 1: YOffset = %d; want %d", got, OverlayWheelStep)
	}

	before := vp.YOffset()
	ScrollOverlayViewport(&vp, 0) // no-op
	if got := vp.YOffset(); got != before {
		t.Errorf("delta 0 moved the viewport: %d → %d", before, got)
	}
}
