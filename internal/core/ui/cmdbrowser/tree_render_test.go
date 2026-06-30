package cmdbrowser

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// TestTreeRenderRegion_CountsByWidth asserts the inner-width threshold gates the
// per-group counts: at >= treeCountsMinWidth the "(N)" suffix shows; below it is
// dropped so deep rows do not overflow the narrow panel.
func TestTreeRenderRegion_CountsByWidth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		width      int
		wantCounts bool
	}{
		{"wide shows counts", treeCountsMinWidth, true},
		{"above threshold shows counts", treeCountsMinWidth + 10, true},
		{"narrow hides counts", treeCountsMinWidth - 1, false},
		{"very narrow hides counts", 8, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tm := newTreeModel(sampleItems(), false, 3)
			tm.eng.SetCursorByKey("db")
			out := stripANSI(tm.renderRegion(tui.Region{Width: tc.width, Height: 20}, true, nil))
			hasCounts := strings.Contains(out, "(1)") || strings.Contains(out, "(4)")
			if hasCounts != tc.wantCounts {
				t.Errorf("width=%d hasCounts=%v, want %v\n%s", tc.width, hasCounts, tc.wantCounts, out)
			}
		})
	}
}

// TestTreeRenderRegion_FocusedGlyph asserts the focus marker appears only when
// the panel is focused, and the chosen node carries it.
func TestTreeRenderRegion_FocusedGlyph(t *testing.T) {
	t.Parallel()
	tm := newTreeModel(sampleItems(), false, 3)
	tm.eng.SetCursorByKey("services")

	focused := stripANSI(tm.renderRegion(tui.Region{Width: 30, Height: 20}, true, nil))
	if !strings.Contains(focused, "❯") {
		t.Errorf("focused render missing marker:\n%s", focused)
	}
	// The marker must sit on the focused row (services), not elsewhere.
	for line := range strings.SplitSeq(focused, "\n") {
		if strings.Contains(line, "❯") && !strings.Contains(line, "services") {
			t.Errorf("focus marker on wrong row: %q", line)
		}
	}

	unfocused := stripANSI(tm.renderRegion(tui.Region{Width: 30, Height: 20}, false, nil))
	if strings.Contains(unfocused, "❯") {
		t.Errorf("unfocused render should not contain marker:\n%s", unfocused)
	}
}

// TestTreeRenderRegion_NoOverflow asserts the rendered tree never exceeds the
// inner region: at most height rows, each no wider than inner.Width.
func TestTreeRenderRegion_NoOverflow(t *testing.T) {
	t.Parallel()
	tm := newTreeModel(sampleItems(), false, 3) // 6 visible nodes
	cases := []struct {
		name   string
		region tui.Region
	}{
		{"taller than tree", tui.Region{Width: 30, Height: 20}},
		{"exact height", tui.Region{Width: 30, Height: 6}},
		{"clipped height", tui.Region{Width: 30, Height: 3}},
		{"single row", tui.Region{Width: 30, Height: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := stripANSI(tm.renderRegion(tc.region, true, nil))
			lines := strings.Split(out, "\n")
			if len(lines) > tc.region.Height {
				t.Errorf("rows=%d exceed height=%d:\n%s", len(lines), tc.region.Height, out)
			}
			for _, line := range lines {
				if w := lipgloss.Width(line); w > tc.region.Width {
					t.Errorf("line %q width=%d exceeds inner width=%d", line, w, tc.region.Width)
				}
			}
		})
	}
}

// The viewport scroll window (topIdx), clip, EnsureFocusVisible, and FocusRow
// behaviors now live in the shared tui/tree engine and are covered by
// internal/core/ui/tui/tree/tree_test.go (TestClip, TestEnsureFocusVisibleWindow,
// TestFocusRowAndClickPastLastNoop). The cmdbrowser-level no-overflow guarantee
// is exercised by TestTreeRenderRegion_NoOverflow above.

// TestTreeRenderRegion_FilterPath asserts the filter-aware renderer is selected
// when a filter session is passed, surfacing "M/N" counts.
func TestTreeRenderRegion_FilterPath(t *testing.T) {
	t.Parallel()
	tm := newTreeModel(sampleItems(), false, 3)
	f := newFilterState(tm.eng.ExpandedSnapshot(), tm.focusedID())
	f.query = "migrate"
	f.recompute(tm.items, false)

	out := stripANSI(tm.renderRegion(tui.Region{Width: 30, Height: 20}, true, f))
	// db has 1 match of 1 public leaf → "(1/1)".
	if !strings.Contains(out, "(1/1)") {
		t.Errorf("filter render missing M/N count:\n%s", out)
	}
}

// TestBrowserViewPanel_Tree asserts the plugin renders the tree into the inner
// region and caches it; the list panel is exercised by plugin_test.go (Task 5).
func TestBrowserViewPanel_Tree(t *testing.T) {
	t.Parallel()
	b := newBrowser("title", sampleItems(), Options{Mode: ModeRun})
	inner := tui.Region{X: 2, Y: 1, Width: 20, Height: 8}

	out := b.ViewPanel(panelTree, inner)
	if !strings.Contains(stripANSI(out), "db") {
		t.Errorf("tree panel did not render groups:\n%s", out)
	}
	if b.treeInner != inner {
		t.Errorf("treeInner=%+v, want %+v", b.treeInner, inner)
	}
	for line := range strings.SplitSeq(stripANSI(out), "\n") {
		if w := lipgloss.Width(line); w > inner.Width {
			t.Errorf("tree line %q width=%d exceeds %d", line, w, inner.Width)
		}
	}

	// The list panel now renders (Task 5); it must still cache its inner region.
	b.ViewPanel(panelList, inner)
	if b.listInner != inner {
		t.Errorf("listInner not cached: %+v", b.listInner)
	}
}
