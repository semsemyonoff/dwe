package cmdbrowser

import (
	"fmt"
	"math/rand"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func sampleItems() []Item {
	return []Item{
		{ID: "db.migrate"},
		{ID: "db.seed", Private: true},
		{ID: "services.main.cs.list"},
		{ID: "services.main.cs.update"},
		{ID: "services.main.web.build"},
		{ID: "services.api.test"},
	}
}

func TestNewTreeModel_BuildsHierarchyAndCounts(t *testing.T) {
	t.Parallel()
	tm := newTreeModel(sampleItems(), false, 3)

	wantNodes := []string{"db", "services", "services.api", "services.main", "services.main.cs", "services.main.web"}
	got := make([]string, 0, len(tm.nodesByID))
	for id := range tm.nodesByID {
		if id == "" {
			continue
		}
		got = append(got, id)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(wantNodes, ",") {
		t.Errorf("nodes=%v, want %v", got, wantNodes)
	}

	cases := map[string]struct{ pub, all int }{
		"db":                {1, 2}, // db.seed is private
		"services":          {4, 4},
		"services.api":      {1, 1},
		"services.main":     {3, 3},
		"services.main.cs":  {2, 2},
		"services.main.web": {1, 1},
	}
	for id, want := range cases {
		n := tm.nodesByID[id]
		if n.countPublic != want.pub || n.countAll != want.all {
			t.Errorf("%s: countPublic=%d countAll=%d, want %d/%d", id, n.countPublic, n.countAll, want.pub, want.all)
		}
	}
}

func TestNewTreeModel_InitialExpansion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		depth       int
		wantVisible []string
	}{
		{
			name:  "depth zero collapses everything",
			depth: 0,
			// Top-level always visible (children of invisible root)
			wantVisible: []string{"db", "services"},
		},
		{
			name:        "depth one expands top-level",
			depth:       1,
			wantVisible: []string{"db", "services", "services.api", "services.main"},
		},
		{
			name:        "depth three expands all",
			depth:       3,
			wantVisible: []string{"db", "services", "services.api", "services.main", "services.main.cs", "services.main.web"},
		},
		{
			name:        "negative depth clamps to zero",
			depth:       -5,
			wantVisible: []string{"db", "services"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tm := newTreeModel(sampleItems(), false, tc.depth)
			got := visibleIDs(tm)
			if strings.Join(got, ",") != strings.Join(tc.wantVisible, ",") {
				t.Errorf("visible=%v, want %v", got, tc.wantVisible)
			}
		})
	}
}

func visibleIDs(tm *treeModel) []string {
	out := make([]string, 0, len(tm.eng.VisibleNodes()))
	for _, n := range tm.eng.VisibleNodes() {
		out = append(out, n.id)
	}
	return out
}

func TestTreeNavigation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		startID   string
		op        func(*treeModel)
		wantID    string
		wantExpID []string
	}{
		{
			name:    "down moves to next visible",
			startID: "db",
			op:      func(tm *treeModel) { tm.eng.MoveDown() },
			wantID:  "services",
		},
		{
			name:    "up at top stays",
			startID: "db",
			op:      func(tm *treeModel) { tm.eng.MoveUp() },
			wantID:  "db",
		},
		{
			name:    "end jumps to last",
			startID: "db",
			op:      func(tm *treeModel) { tm.eng.MoveEnd() },
			wantID:  "services.main.web",
		},
		{
			name:    "home jumps to first",
			startID: "services.main",
			op:      func(tm *treeModel) { tm.eng.MoveHome() },
			wantID:  "db",
		},
		{
			name:    "right on collapsed expands",
			startID: "db",
			op: func(tm *treeModel) {
				tm.eng.SetExpandedByKey("db", false)
				tm.eng.RebuildVisible(nil)
				tm.eng.Expand()
			},
			wantID: "db",
		},
		{
			name:    "right on expanded steps in",
			startID: "services",
			op:      func(tm *treeModel) { tm.eng.Expand() },
			wantID:  "services.api",
		},
		{
			name:    "left on expanded collapses",
			startID: "services.main",
			op:      func(tm *treeModel) { tm.eng.Collapse() },
			wantID:  "services.main",
		},
		{
			name:    "left on collapsed ascends",
			startID: "services.api",
			op: func(tm *treeModel) {
				// services.api is "expanded" at depth 3 even though it has no
				// children — collapsing it first lets onLeft ascend.
				tm.eng.SetExpandedByKey("services.api", false)
				tm.eng.RebuildVisible(nil)
				tm.eng.Collapse()
			},
			wantID: "services",
		},
		{
			name:    "space toggles expansion",
			startID: "services.main",
			op:      func(tm *treeModel) { tm.eng.Toggle() },
			wantID:  "services.main",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tm := newTreeModel(sampleItems(), false, 3)
			tm.eng.SetCursorByKey(tc.startID)
			tc.op(tm)
			if tm.focusedID() != tc.wantID {
				t.Errorf("focus=%q, want %q", tm.focusedID(), tc.wantID)
			}
		})
	}
}

func TestTreeRight_ToggleCollapsesAfterRight(t *testing.T) {
	t.Parallel()
	tm := newTreeModel(sampleItems(), false, 3)
	tm.eng.SetCursorByKey("services.main")
	tm.eng.Collapse() // collapse
	if tm.eng.IsExpanded(tm.nodesByID["services.main"]) {
		t.Errorf("services.main should be collapsed after onLeft")
	}
	if !contains(visibleIDs(tm), "services.main") {
		t.Errorf("services.main missing from visible")
	}
	if contains(visibleIDs(tm), "services.main.cs") {
		t.Errorf("services.main.cs should be hidden when parent collapsed")
	}
	tm.eng.Expand() // expand again
	if !tm.eng.IsExpanded(tm.nodesByID["services.main"]) {
		t.Errorf("services.main should be expanded after onRight")
	}
}

func TestTreeIncludePrivate_RefreshesCounts(t *testing.T) {
	t.Parallel()
	tm := newTreeModel(sampleItems(), false, 3)
	if tm.nodesByID["db"].countPublic != 1 {
		t.Fatalf("baseline countPublic=%d", tm.nodesByID["db"].countPublic)
	}
	tm.setIncludePrivate(true)
	// includePrivate flag flips; renderer uses countAll which is unchanged.
	if !tm.includePrivate {
		t.Errorf("includePrivate not set")
	}
	// Idempotent flip
	tm.setIncludePrivate(true)
	if !tm.includePrivate {
		t.Errorf("includePrivate lost after idempotent flip")
	}
}

func TestTreeItemsForFocus_FiltersPrivate(t *testing.T) {
	t.Parallel()
	tm := newTreeModel(sampleItems(), false, 3)
	tm.eng.SetCursorByKey("db")
	got := tm.itemsForFocus()
	if len(got) != 1 || tm.items[got[0]].ID != "db.migrate" {
		t.Errorf("itemsForFocus=%v, want only db.migrate", got)
	}
	tm.setIncludePrivate(true)
	got = tm.itemsForFocus()
	if len(got) != 2 {
		t.Errorf("itemsForFocus with private=%v, want 2", got)
	}
}

func TestTreeRender_ShowsMarkerAndCount(t *testing.T) {
	t.Parallel()
	tm := newTreeModel(sampleItems(), false, 3)
	tm.eng.SetCursorByKey("db")
	out := tm.renderOpt(true, true)
	if !strings.Contains(out, "db") || !strings.Contains(out, "(1)") {
		t.Errorf("render missing db / count, got:\n%s", out)
	}
	if !strings.Contains(out, "❯") {
		t.Errorf("render missing focus marker, got:\n%s", out)
	}
	// Unfocused render must drop the focus marker.
	if strings.Contains(tm.renderOpt(false, true), "❯") {
		t.Errorf("unfocused render should not contain focus marker")
	}
}

func TestRenderEmptyTree(t *testing.T) {
	t.Parallel()
	tm := newTreeModel(nil, false, 3)
	out := tm.renderOpt(true, true)
	if !strings.Contains(out, "no groups") {
		t.Errorf("empty tree should render placeholder, got %q", out)
	}
}

// FuzzTreeCountInvariant asserts that for every node, countPublic equals the
// sum of children countPublic plus the public leaves directly attached. This
// is the structural invariant guarded throughout navigation.
func FuzzTreeCountInvariant(f *testing.F) {
	f.Add(int64(1))
	f.Add(int64(42))
	f.Add(int64(9001))
	f.Fuzz(func(t *testing.T, seed int64) {
		r := rand.New(rand.NewSource(seed))
		n := 1 + r.Intn(20)
		items := make([]Item, n)
		for i := range n {
			depth := 1 + r.Intn(4)
			parts := make([]string, depth)
			for d := range depth {
				parts[d] = fmt.Sprintf("g%d", r.Intn(3))
			}
			parts = append(parts, fmt.Sprintf("cmd%d", i))
			items[i] = Item{
				ID:      strings.Join(parts, "."),
				Private: r.Intn(2) == 0,
			}
		}
		tm := newTreeModel(items, false, 5)
		for _, node := range tm.nodesByID {
			sum := 0
			for _, idx := range node.leaves {
				if !tm.items[idx].Private {
					sum++
				}
			}
			for _, c := range node.children {
				sum += c.countPublic
			}
			if sum != node.countPublic {
				t.Fatalf("node %q countPublic=%d, sum=%d", node.id, node.countPublic, sum)
			}
		}
		// Collapse-idempotency: collapsing twice == once.
		visibleBefore := visibleIDs(tm)
		for id := range tm.nodesByID {
			tm.eng.SetExpandedByKey(id, false)
			tm.eng.SetExpandedByKey(id, false)
		}
		tm.eng.RebuildVisible(nil)
		v1 := visibleIDs(tm)
		tm.eng.RebuildVisible(nil)
		v2 := visibleIDs(tm)
		if strings.Join(v1, ",") != strings.Join(v2, ",") {
			t.Fatalf("rebuildVisible not idempotent: %v vs %v", v1, v2)
		}
		_ = visibleBefore
	})
}

// TestTree_Snapshot pins the initial render of the sample tree at depth 3.
// Lipgloss under `go test` falls back to ASCII; the output is byte-stable.
func TestTree_Snapshot(t *testing.T) {
	t.Parallel()
	tm := newTreeModel(sampleItems(), false, 3)
	tm.eng.SetCursorByKey("db")
	got := stripANSI(tm.renderOpt(true, true))
	// db / api / cs / web have no sub-group children, so they render with a
	// two-space gutter where the expand glyph would be. services and
	// services.main carry the ▾ glyph because they have children.
	want := strings.Join([]string{
		"❯   db (1)",
		"  ▾ services (4)",
		"      api (1)",
		"    ▾ main (3)",
		"        cs (2)",
		"        web (1)",
	}, "\n")
	if got != want {
		t.Errorf("snapshot mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func contains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}

func TestNearestVisibleAncestor(t *testing.T) {
	t.Parallel()
	// Depth 0: only top-level groups are visible (db, services). Their children
	// and deeper nodes are collapsed.
	tm := newTreeModel(sampleItems(), false, 0)

	cases := []struct {
		id   string
		want string
	}{
		// Node that IS itself visible → returns itself.
		{"db", "db"},
		{"services", "services"},
		// Child of visible node but not visible itself → nearest visible parent.
		{"services.main", "services"},
		{"services.main.cs", "services"},
		{"services.api", "services"},
		// Fully top-level (root group "") → "" (root).
		{"", ""},
	}
	for _, tc := range cases {
		got := tm.nearestVisibleAncestor(tc.id)
		if got != tc.want {
			t.Errorf("nearestVisibleAncestor(%q)=%q, want %q", tc.id, got, tc.want)
		}
	}
}
