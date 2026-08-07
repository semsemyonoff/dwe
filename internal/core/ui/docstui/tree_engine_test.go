package docstui

import (
	"io/fs"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/docs"
)

// newLocaleFixtureFS is a nested in-memory docs root with locale-variant
// content. It exercises the engine-backed tree: an index-only `solo/` dir, a
// plain `sub/` dir with a child page, and a top-level `guide.md` whose Russian
// translation drops the second heading (so a Rebuild("ru") makes a heading
// vanish). Directories sort before files, so the first visible row is `solo`.
func newLocaleFixtureFS() indexFixtureFS {
	return indexFixtureFS{
		dirs: map[string][]fs.DirEntry{
			".":    {dirEnt{name: "solo", dir: true}, dirEnt{name: "sub", dir: true}, dirEnt{name: "guide.md"}},
			"solo": {dirEnt{name: "index.md"}},
			"sub":  {dirEnt{name: "page.md"}},
		},
		files: map[string]string{
			"solo/index.md":    "# Solo\n",
			"sub/page.md":      "# Page\n",
			"guide.md":         "# Guide\n\n## One\n\nA.\n\n## Two\n\nB.\n",
			"i18n/ru/guide.md": "# Гид\n\n## Один\n\nA.\n",
		},
	}
}

func findVisible(vis []*TreeNode, pred func(*TreeNode) bool) *TreeNode {
	for _, n := range vis {
		if pred(n) {
			return n
		}
	}
	return nil
}

func isFileNamed(name string) func(*TreeNode) bool {
	return func(n *TreeNode) bool {
		return n.Node != nil && n.Heading == nil && n.Node.Name == name
	}
}

// TestRebuildPreservesExpansionAndCursor locks the intended bugfix: expansion
// state of nodes that are NOT cursor ancestors survives a locale Rebuild,
// because the engine keys expansion by stable Key. The old findByPath transfer
// only re-expanded the restored cursor's ancestors, dropping a sibling dir.
func TestRebuildPreservesExpansionAndCursor(t *testing.T) {
	roots := []docs.DocRoot{{Name: "dwe", FS: newLocaleFixtureFS()}}
	tw, err := NewTreeWidget(roots, "en")
	if err != nil {
		t.Fatalf("NewTreeWidget: %v", err)
	}

	sub := findVisible(tw.VisibleNodes(), func(n *TreeNode) bool {
		return n.Node != nil && n.Node.IsDir && n.Node.Name == "sub"
	})
	guide := findVisible(tw.VisibleNodes(), isFileNamed("guide.md"))
	if sub == nil || guide == nil {
		t.Fatalf("fixture rows missing: sub=%v guide=%v", sub, guide)
	}

	// Expand the sibling dir AND the cursor's file, then put the cursor on the
	// file (sub is NOT an ancestor of guide).
	tw.SetExpanded(sub, true)
	tw.SetExpanded(guide, true)
	tw.SetCursor(guide)
	tw.recomputeVisible()

	if err := tw.Rebuild("en"); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	sub2 := findVisible(tw.VisibleNodes(), func(n *TreeNode) bool {
		return n.Node != nil && n.Node.IsDir && n.Node.Name == "sub"
	})
	guide2 := findVisible(tw.VisibleNodes(), isFileNamed("guide.md"))
	if sub2 == nil || guide2 == nil {
		t.Fatalf("rows missing after Rebuild: sub=%v guide=%v", sub2, guide2)
	}
	if !tw.IsExpanded(sub2) {
		t.Error("sibling dir 'sub' lost its expansion across Rebuild (bugfix regressed)")
	}
	if !tw.IsExpanded(guide2) {
		t.Error("'guide.md' lost its expansion across Rebuild")
	}
	if cur := tw.Cursor(); cur == nil || cur.Node == nil || cur.Node.Name != "guide.md" || cur.Heading != nil {
		t.Errorf("cursor after Rebuild = %v, want the guide.md file node", cur)
	}
}

// TestRebuildHeadingVanishFallsBackToParentFile locks the two-tier cursor
// restore: when the cursor sits on a heading that disappears after a locale
// Rebuild, it falls back to the heading's PARENT FILE, not the first visible row.
func TestRebuildHeadingVanishFallsBackToParentFile(t *testing.T) {
	roots := []docs.DocRoot{{Name: "dwe", FS: newLocaleFixtureFS()}}
	tw, err := NewTreeWidget(roots, "en")
	if err != nil {
		t.Fatalf("NewTreeWidget: %v", err)
	}

	guide := findVisible(tw.VisibleNodes(), isFileNamed("guide.md"))
	if guide == nil || len(guide.Children) < 2 {
		t.Fatalf("guide.md must have 2 heading children in English, got %v", guide)
	}
	// Cursor on the SECOND heading ("Two"), which the Russian variant drops.
	tw.SetExpanded(guide, true)
	tw.SetCursor(guide.Children[1])
	tw.recomputeVisible()

	if err := tw.Rebuild("ru"); err != nil {
		t.Fatalf("Rebuild(ru): %v", err)
	}

	cur := tw.Cursor()
	if cur == nil {
		t.Fatal("cursor is nil after heading vanished")
	}
	if cur.Heading != nil {
		t.Errorf("cursor is still a heading after vanish: %q", cur.Heading.Text)
	}
	if cur.Node == nil || cur.Node.Name != "guide.md" {
		t.Errorf("cursor = %v, want fallback to parent file guide.md", cur)
	}
	// Must NOT have fallen back to the first visible row (the 'solo' dir).
	if cur.Node != nil && cur.Node.Name == "solo" {
		t.Error("cursor fell back to first-visible row, want parent file")
	}
}

// TestMultiRootGroupsExpandedByDefault verifies the multi-root group seeding:
// group header nodes render expanded on the initial build so their children
// are visible without a manual expand.
func TestMultiRootGroupsExpandedByDefault(t *testing.T) {
	roots := []docs.DocRoot{
		{Name: "alpha", FS: &testFS{files: map[string]string{"a.md": "a"}}},
		{Name: "beta", FS: &testFS{files: map[string]string{"b.md": "b"}}},
	}
	tw, err := NewTreeWidget(roots, "en")
	if err != nil {
		t.Fatalf("NewTreeWidget: %v", err)
	}

	var groups int
	for _, n := range tw.VisibleNodes() {
		if n.IsGroup {
			groups++
			if !tw.IsExpanded(n) {
				t.Errorf("group node %q not expanded by default", nodeLabel(n))
			}
		}
	}
	if groups != 2 {
		t.Errorf("visible group nodes = %d, want 2", groups)
	}
	// Each group's child file must be visible (groups expanded).
	if findVisible(tw.VisibleNodes(), isFileNamed("a.md")) == nil {
		t.Error("alpha group child a.md not visible")
	}
	if findVisible(tw.VisibleNodes(), isFileNamed("b.md")) == nil {
		t.Error("beta group child b.md not visible")
	}
}

// TestEmptyQueryFilterRespectsExpansion pins that opening the filter with an
// empty query must NOT fully expand the tree — it keeps the
// expansion-respecting visible set (a node inside a collapsed dir stays hidden).
func TestEmptyQueryFilterRespectsExpansion(t *testing.T) {
	roots := []docs.DocRoot{{Name: "dwe", FS: newLocaleFixtureFS()}}
	tw, err := NewTreeWidget(roots, "en")
	if err != nil {
		t.Fatalf("NewTreeWidget: %v", err)
	}

	// 'sub' is collapsed by default → its child page is hidden.
	if findVisible(tw.VisibleNodes(), isFileNamed("page.md")) != nil {
		t.Fatal("precondition: page.md should be hidden under collapsed 'sub'")
	}

	f := NewTreeFilter()
	f.Open() // active, empty query
	tw.ApplyFilter(f)

	if findVisible(tw.VisibleNodes(), isFileNamed("page.md")) != nil {
		t.Error("empty-query filter fully expanded the tree (page.md became visible)")
	}

	// Sanity: a real query reduces the set without exposing unrelated hidden rows.
	f.Append('p')
	f.Append('a')
	f.Append('g')
	f.Append('e')
	tw.ApplyFilter(f)
	if findVisible(tw.VisibleNodes(), isFileNamed("page.md")) == nil {
		t.Error("query 'page' did not surface the matching page.md")
	}
}

// TestIndexOnlyDirStaysExpandableAndEnterable pins that an index-only
// directory (no child rows after folding index.md) remains expandable and
// resolves its index content on Enter — it must not degrade to an inert leaf.
func TestIndexOnlyDirStaysExpandableAndEnterable(t *testing.T) {
	roots := []docs.DocRoot{{Name: "dwe", FS: newLocaleFixtureFS()}}
	tw, err := NewTreeWidget(roots, "en")
	if err != nil {
		t.Fatalf("NewTreeWidget: %v", err)
	}

	solo := findVisible(tw.VisibleNodes(), func(n *TreeNode) bool {
		return n.Node != nil && n.Node.IsDir && n.Node.Name == "solo"
	})
	if solo == nil {
		t.Fatal("solo dir not found")
	}
	if len(solo.Children) != 0 {
		t.Fatalf("solo should be index-only (no child rows), got %d children", len(solo.Children))
	}
	if solo.IndexNode == nil {
		t.Fatal("solo.IndexNode not folded")
	}
	if !(docsTreeAdapter{}).Expandable(solo) {
		t.Error("index-only dir reported non-expandable; Enter/Toggle would break")
	}
	if cn := contentNodeFor(solo); cn == nil || cn.Path != "solo/index.md" {
		t.Errorf("contentNodeFor(solo) = %v, want solo/index.md", cn)
	}

	// Toggle on the index-only dir flips its expansion glyph (▶ ↔ ▼) even though
	// it has no child rows to reveal. Expand (→/l) stays a no-op (nothing to step
	// into); neither moves the cursor.
	tw.SetCursor(solo)
	if tw.IsExpanded(solo) {
		t.Fatal("index-only dir should start collapsed")
	}
	tw.Toggle()
	if !tw.IsExpanded(solo) {
		t.Error("Toggle on index-only dir did not flip expansion (glyph would never become ▼)")
	}
	tw.Toggle()
	if tw.IsExpanded(solo) {
		t.Error("second Toggle on index-only dir did not collapse it")
	}
	tw.Expand() // no children → no-op
	if tw.IsExpanded(solo) {
		t.Error("Expand on childless index-only dir expanded it; expected no-op")
	}
	if tw.Cursor() != solo {
		t.Errorf("cursor moved off index-only dir after Expand/Toggle: %v", tw.Cursor())
	}
	tw.Collapse() // top-level → no parent → no-op
	if tw.Cursor() != solo {
		t.Errorf("Collapse on top-level index-only dir moved cursor: %v", tw.Cursor())
	}
}
