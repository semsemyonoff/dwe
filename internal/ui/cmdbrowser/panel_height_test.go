package cmdbrowser

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestModel_PanelHeightsMatch_WithLongDescriptions guards against two
// regressions that surface together as the "torn right border" symptom:
//
//   - Multi-line descriptions (YAML literal blocks carry `\n`) used to render
//     beyond the delegate's Height()=2 contract, pushing the right panel taller
//     than the left under JoinHorizontal.
//   - listHeight previously reserved only one row for the breadcrumb instead of
//     also accounting for both panel borders, so even single-line descriptions
//     could exceed inner panel space by two rows.
//
// The two together stretched the right panel below where the left panel ended,
// leaving the right border without a closing corner in the visible area. This
// test asserts both panel frames close on the same row.
func TestModel_PanelHeightsMatch_WithLongDescriptions(t *testing.T) {
	t.Parallel()
	items := []Item{
		{ID: "db.change-admin-config", Type: "service_exec", Description: "Upsert one row in the `configs` table (INSERT … ON DUPLICATE KEY UPDATE). Generic primitive consumed by services.main.admin.* / services.main.config.*"},
		{ID: "db.cli", Type: "service_exec", Description: "Connect to the database in the db container"},
		{ID: "db.dump", Type: "shell", Description: "Dump `${param.database}` from the db container into `${param.out}`\n(gzipped). Generic primitive — snapshot workflows pass an explicit\n`out` path under `${snapshot.path}/db/`."},
		{ID: "db.sync-tables", Type: "shell", Description: "Sync tables from a remote dev MariaDB into the local DB. Pipeline:\nssh remote → mariadb-dump → xz → scp back → import via `docker exec db mariadb`.\n`tables` may be a comma-separated list."},
		{ID: "db.truncate", Type: "service_exec", Description: "Truncate a table (FKs temporarily disabled)."},
	}
	for _, dim := range []struct{ w, h int }{{100, 26}, {120, 26}, {150, 30}, {180, 40}} {
		t.Run("w_"+itoa(dim.w)+"_h_"+itoa(dim.h), func(t *testing.T) {
			t.Parallel()
			m := newModel("pick", items, DefaultOptions(), dim.w, dim.h)
			m.tree.focusedID = "db"
			m.refreshList()
			out := m.View().Content
			lines := strings.Split(out, "\n")
			lw := leftWidth(dim.w)
			leftClose, rightClose := -1, -1
			for i, line := range lines {
				plain := stripANSI(line)
				if lipgloss.Width(plain) < lw {
					continue
				}
				if r := []rune(plain)[lw-1]; r == '┘' {
					leftClose = i
				}
				runes := []rune(plain)
				if len(runes) >= dim.w && runes[dim.w-1] == '┘' {
					rightClose = i
				}
			}
			if leftClose < 0 {
				t.Fatalf("left panel never closed; output:\n%s", out)
			}
			if rightClose < 0 {
				t.Fatalf("right panel never closed; output:\n%s", out)
			}
			if leftClose != rightClose {
				t.Errorf("panel bottoms misaligned: left closes at row %d, right at row %d", leftClose, rightClose)
			}
		})
	}
}

// TestModel_TallTree_DoesNotPushFooterOffScreen guards the bug where a tree
// with more visible group nodes than panel rows overflowed the bordered frame,
// stretched the JoinHorizontal body, and pushed the help footer off the alt
// screen. With viewport clipping the joined body stays at bodyHeight rows so
// the footer always fits and the "enter" key label remains in View().Content.
func TestModel_TallTree_DoesNotPushFooterOffScreen(t *testing.T) {
	t.Parallel()
	// Build 60 distinct top-level groups, each with one leaf. With any
	// DefaultExpandedDepth ≥ 0 the 60 top-level group rows are visible in
	// the tree — far more than the left panel can hold at any reasonable
	// terminal size.
	items := make([]Item, 0, 60)
	for i := 0; i < 60; i++ {
		id := "g" + itoa(i) + ".cmd"
		items = append(items, Item{ID: id, Description: "leaf " + itoa(i), Type: "shell"})
	}
	for _, dim := range []struct{ w, h int }{{120, 26}, {120, 30}, {140, 40}, {90, 26}} {
		t.Run("w_"+itoa(dim.w)+"_h_"+itoa(dim.h), func(t *testing.T) {
			t.Parallel()
			m := newModel("pick", items, DefaultOptions(), dim.w, dim.h)
			out := m.View().Content
			content := strings.TrimRight(out, "\n")
			lines := strings.Count(content, "\n") + 1
			if lines > dim.h {
				t.Errorf("View().Content has %d lines, exceeds terminal height %d (footer will be clipped):\n%s",
					lines, dim.h, out)
			}
			if !strings.Contains(out, "enter") {
				t.Errorf("footer 'enter' label missing from View().Content — footer was clipped:\n%s", out)
			}
		})
	}
}

// TestModel_TreeScrollsFocusIntoView asserts that pressing Down past the
// bottom of the left-panel viewport scrolls the tree so the focused node
// remains visible. Without this the cursor would silently move onto nodes
// hidden below the panel.
func TestModel_TreeScrollsFocusIntoView(t *testing.T) {
	t.Parallel()
	items := make([]Item, 0, 50)
	for i := 0; i < 50; i++ {
		items = append(items, Item{ID: "g" + itoa(i) + ".cmd", Type: "shell"})
	}
	m := newModel("pick", items, DefaultOptions(), 120, 20)
	// Press Down enough times to land near the end. The viewport height
	// at h=20 is bodyHeight(20)-2 = 20-3-2-2 = 13 rows; pressing Down 30
	// times forces the focus past the initial window.
	for i := 0; i < 30; i++ {
		m.Update(syntheticKey("down"))
	}
	idx := m.tree.indexOfFocused()
	if idx < m.treeTopIdx || idx >= m.treeTopIdx+m.treeViewportHeight() {
		t.Errorf("focus idx %d outside viewport [%d, %d)",
			idx, m.treeTopIdx, m.treeTopIdx+m.treeViewportHeight())
	}
	// And the focused node must appear in the rendered output.
	out := stripANSI(m.View().Content)
	wantNode := m.tree.visible[idx].name
	if !strings.Contains(out, wantNode) {
		t.Errorf("focused node %q not visible in rendered output:\n%s", wantNode, out)
	}
}
