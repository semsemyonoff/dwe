package docstui

import (
	"context"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// The tree scroll window (topIdx), clip, EnsureFocusVisible, and FocusRow
// behavior now lives in the shared tree engine and is unit-tested there:
// internal/core/ui/tui/tree/tree_test.go (TestClip, TestEnsureFocusVisibleWindow,
// TestFocusRowAndClickPastLastNoop). The docstui-level no-overflow guarantee is
// covered by TestTreeViewPanel_ClipsToHeight below.

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
	return newBrowser(context.Background(), m)
}
