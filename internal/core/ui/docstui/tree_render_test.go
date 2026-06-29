package docstui

import (
	"context"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// buildFlatTree returns a TreeWidget with N simple file nodes visible at
// depth-0 so scroll/clip tests don't depend on filesystem I/O.
func buildFlatTree(t *testing.T, n int) *TreeWidget {
	t.Helper()
	// Build a minimal multi-file in-memory FS.
	files := make(map[string]string, n)
	for i := range n {
		name := string(rune('a'+i)) + ".md"
		files[name] = "content"
	}
	fsys := &testFS{files: files}
	roots := []docs.DocRoot{{Name: "dwe", FS: fsys}}
	tw, err := NewTreeWidget(roots, "en")
	if err != nil {
		t.Fatalf("NewTreeWidget: %v", err)
	}
	return tw
}

// TestEnsureFocusVisible_FocusBelowWindow verifies that scrolling down past
// the viewport bottom slides topIdx forward.
func TestEnsureFocusVisible_FocusBelowWindow(t *testing.T) {
	tw := buildFlatTree(t, 5)
	// Move cursor to the last row.
	tw.MoveEnd()
	height := 3

	tw.ensureFocusVisible(height)

	idx := tw.indexOfNode(tw.cursor)
	if idx < tw.topIdx || idx >= tw.topIdx+height {
		t.Errorf("cursor idx %d not in window [%d, %d)", idx, tw.topIdx, tw.topIdx+height)
	}
}

// TestEnsureFocusVisible_FocusAboveWindow verifies that scrolling up past the
// viewport top slides topIdx backward.
func TestEnsureFocusVisible_FocusAboveWindow(t *testing.T) {
	tw := buildFlatTree(t, 5)
	// Push topIdx down, then move cursor to the top.
	tw.topIdx = 3
	tw.MoveStart()
	height := 3

	tw.ensureFocusVisible(height)

	if tw.topIdx != 0 {
		t.Errorf("topIdx = %d after scrolling cursor to top, want 0", tw.topIdx)
	}
}

// TestEnsureFocusVisible_ShortTree verifies that when all nodes fit in height
// topIdx stays 0.
func TestEnsureFocusVisible_ShortTree(t *testing.T) {
	tw := buildFlatTree(t, 3)
	tw.MoveEnd()
	tw.ensureFocusVisible(10)
	if tw.topIdx != 0 {
		t.Errorf("topIdx = %d for short tree with tall panel, want 0", tw.topIdx)
	}
}

// TestEnsureFocusVisible_ZeroHeight verifies that a zero height resets topIdx.
func TestEnsureFocusVisible_ZeroHeight(t *testing.T) {
	tw := buildFlatTree(t, 3)
	tw.topIdx = 2
	tw.ensureFocusVisible(0)
	if tw.topIdx != 0 {
		t.Errorf("topIdx = %d after zero height, want 0", tw.topIdx)
	}
}

// TestEnsureFocusVisible_MaxTopClamp verifies that topIdx never pushes the
// last page beyond the total visible count (no scrolling past bottom).
func TestEnsureFocusVisible_MaxTopClamp(t *testing.T) {
	tw := buildFlatTree(t, 3)
	tw.MoveEnd()
	height := 2

	tw.ensureFocusVisible(height)

	n := len(tw.visible)
	maxTop := max(n-height, 0)
	if tw.topIdx > maxTop {
		t.Errorf("topIdx %d exceeds maxTop %d", tw.topIdx, maxTop)
	}
}

// TestFocusRow_Basic verifies that clicking panel row 0 selects the first
// visible node (topIdx=0).
func TestFocusRow_Basic(t *testing.T) {
	tw := buildFlatTree(t, 3)
	tw.MoveEnd() // move cursor away from row 0

	tw.focusRow(0)

	if tw.cursor != tw.visible[0] {
		t.Errorf("focusRow(0) cursor = %v, want visible[0]", tw.cursor)
	}
}

// TestFocusRow_WithTopIdx verifies that panel-local row N maps to
// visible[topIdx+N].
func TestFocusRow_WithTopIdx(t *testing.T) {
	tw := buildFlatTree(t, 5)
	tw.topIdx = 2

	tw.focusRow(1) // panel row 1 → visible[3]

	if tw.cursor != tw.visible[3] {
		t.Errorf("focusRow(1) with topIdx=2 cursor = %v, want visible[3]", tw.cursor)
	}
}

// TestFocusRow_OutOfRange verifies that a click past the last visible node is
// a no-op (cursor unchanged).
func TestFocusRow_OutOfRange(t *testing.T) {
	tw := buildFlatTree(t, 3)
	before := tw.cursor

	tw.focusRow(100) // well past the last row

	if tw.cursor != before {
		t.Errorf("focusRow(out-of-range) changed cursor: got %v, want %v", tw.cursor, before)
	}
}

// TestFocusRow_NegativeIsNoOp verifies negative row is a no-op.
func TestFocusRow_NegativeIsNoOp(t *testing.T) {
	tw := buildFlatTree(t, 3)
	before := tw.cursor

	tw.focusRow(-1)

	if tw.cursor != before {
		t.Errorf("focusRow(-1) changed cursor unexpectedly")
	}
}

// TestTreeViewPanel_RendersRows verifies that ViewPanel for the tree panel
// returns a non-empty string with one line per visible node (at a fixed inner
// size that fits all rows).
func TestTreeViewPanel_RendersRows(t *testing.T) {
	b := newTestBrowser(t)
	// The test fixture has a single "index.md" node.
	inner := tui.Region{Width: 30, Height: 20}
	out := b.ViewPanel(panelTree, inner)
	if out == "" {
		t.Fatal("ViewPanel(tree) returned empty string, want at least one row")
	}
}

// TestTreeViewPanel_ClipsToHeight verifies that when the tree has more nodes
// than the panel height, the output contains at most Height lines.
func TestTreeViewPanel_ClipsToHeight(t *testing.T) {
	// Build a browser backed by a tree with many nodes.
	files := map[string]string{
		"a.md": "content", "b.md": "content", "c.md": "content",
		"d.md": "content", "e.md": "content",
	}
	fsys := &testFS{files: files}
	roots := []docs.DocRoot{{Name: "dwe", FS: fsys}}

	b := newTestBrowserWithRoots(t, roots)
	height := 3
	inner := tui.Region{Width: 30, Height: height}
	out := b.ViewPanel(panelTree, inner)

	lines := strings.Split(out, "\n")
	if len(lines) > height {
		t.Errorf("ViewPanel(tree) returned %d lines, want <= %d", len(lines), height)
	}
}

// TestTreeViewPanel_CachesInnerRegion verifies that ViewPanel still caches the
// inner region (regression guard for the combined cache+render path).
func TestTreeViewPanel_CachesInnerRegion(t *testing.T) {
	b := newTestBrowser(t)
	inner := tui.Region{X: 0, Y: 0, Width: 20, Height: 10}
	b.ViewPanel(panelTree, inner)
	if b.treeInner != inner {
		t.Errorf("treeInner = %+v, want %+v", b.treeInner, inner)
	}
}

// newTestBrowserWithRoots is like newTestBrowser but accepts custom roots.
func newTestBrowserWithRoots(t *testing.T, roots []docs.DocRoot) *browser {
	t.Helper()
	m, err := NewModel(
		context.Background(),
		roots,
		"en",
		nil,
		nil,
		80, 24,
		"",
		"Test",
		"auto",
	)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	return newBrowser(m)
}
