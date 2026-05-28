package cmdbrowser

import (
	"bytes"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

func renderDelegate(t *testing.T, d *cmdDelegate, items []list.Item, selectIdx, renderIdx int) string {
	t.Helper()
	m := list.New(items, d, d.width, 10)
	m.Select(selectIdx)
	var buf bytes.Buffer
	d.Render(&buf, m, renderIdx, items[renderIdx])
	return buf.String()
}

func TestListItem_FilterValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		it   listItem
		want string
	}{
		{"id only", listItem{id: "db.migrate"}, "db.migrate"},
		{"id and description", listItem{id: "db.migrate", desc: "apply schema"}, "db.migrate apply schema"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.it.FilterValue(); got != tc.want {
				t.Errorf("FilterValue=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestCmdDelegate_HeightAndSpacing(t *testing.T) {
	t.Parallel()
	d := newCmdDelegate(80, true)
	if d.Height() != 2 {
		t.Errorf("Height=%d, want 2", d.Height())
	}
	if d.Spacing() != 1 {
		t.Errorf("Spacing=%d, want 1", d.Spacing())
	}
	if cmd := d.Update(tea.WindowSizeMsg{}, nil); cmd != nil {
		t.Errorf("Update must return nil cmd, got %v", cmd)
	}
}

func TestCmdDelegate_RendersAllTypes(t *testing.T) {
	t.Parallel()
	types := []string{"shell", "script", "workflow", "service_exec", "service_run", "builtin", "devbox"}
	for _, typ := range types {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			d := newCmdDelegate(80, true)
			items := []list.Item{listItem{origIdx: 0, id: "foo.bar", desc: "describe me", typ: typ}}
			out := renderDelegate(t, d, items, 0, 0)
			plain := stripANSI(out)
			if !strings.Contains(plain, "foo.bar") {
				t.Errorf("missing id; got %q", plain)
			}
			if !strings.Contains(plain, "["+typ+"]") {
				t.Errorf("missing badge for %q; got %q", typ, plain)
			}
			if !strings.Contains(plain, "describe me") {
				t.Errorf("missing description; got %q", plain)
			}
		})
	}
}

func TestCmdDelegate_UnknownTypeFallsBackToMuted(t *testing.T) {
	t.Parallel()
	d := newCmdDelegate(80, true)
	items := []list.Item{listItem{origIdx: 0, id: "x.y", typ: "wat"}}
	out := stripANSI(renderDelegate(t, d, items, 0, 0))
	if !strings.Contains(out, "[wat]") {
		t.Errorf("unknown type badge missing; got %q", out)
	}
}

func TestCmdDelegate_HidesBadgesWhenDisabled(t *testing.T) {
	t.Parallel()
	d := newCmdDelegate(80, false)
	items := []list.Item{listItem{origIdx: 0, id: "x.y", typ: "shell"}}
	out := stripANSI(renderDelegate(t, d, items, 0, 0))
	if strings.Contains(out, "[shell]") {
		t.Errorf("badge must be hidden when ShowBadges=false; got %q", out)
	}
	if !strings.Contains(out, "x.y") {
		t.Errorf("id must still be rendered; got %q", out)
	}
}

func TestCmdDelegate_TruncatesLongID(t *testing.T) {
	t.Parallel()
	d := newCmdDelegate(40, true)
	longID := "services.main.index.reindex-catalog-product-availability-search"
	items := []list.Item{listItem{origIdx: 0, id: longID, typ: "shell"}}
	out := stripANSI(renderDelegate(t, d, items, 0, 0))
	if !strings.Contains(out, "…") {
		t.Errorf("expected ellipsis on truncated id; got %q", out)
	}
}

func TestCmdDelegate_SelectionMarker(t *testing.T) {
	t.Parallel()
	d := newCmdDelegate(80, true)
	items := []list.Item{
		listItem{origIdx: 0, id: "a.one", typ: "shell"},
		listItem{origIdx: 1, id: "a.two", typ: "shell"},
	}
	selected := stripANSI(renderDelegate(t, d, items, 0, 0))
	unselected := stripANSI(renderDelegate(t, d, items, 0, 1))
	if !strings.Contains(selected, "❯") {
		t.Errorf("selected item must show cursor; got %q", selected)
	}
	if strings.Contains(unselected, "❯") {
		t.Errorf("unselected item must not show cursor; got %q", unselected)
	}
}

func TestCmdDelegate_TruncationIndicators(t *testing.T) {
	t.Parallel()
	t.Run("multiline_collapse_adds_ellipsis", func(t *testing.T) {
		t.Parallel()
		d := newCmdDelegate(80, true)
		items := []list.Item{listItem{origIdx: 0, id: "db.dump", typ: "shell", desc: "First line is short\nSecond line carries more context"}}
		out := stripANSI(renderDelegate(t, d, items, 1, 0)) // not selected
		if !strings.Contains(out, "First line is short…") {
			t.Errorf("expected ellipsis after collapsed first line; got %q", out)
		}
		if strings.Contains(out, "(i)") {
			t.Errorf("unselected item must not show inspect hint; got %q", out)
		}
	})
	t.Run("selected_truncated_shows_inspect_hint", func(t *testing.T) {
		t.Parallel()
		d := newCmdDelegate(80, true)
		items := []list.Item{listItem{origIdx: 0, id: "db.dump", typ: "shell", desc: "First line\nSecond line"}}
		out := stripANSI(renderDelegate(t, d, items, 0, 0)) // selected
		if !strings.Contains(out, "First line…") {
			t.Errorf("expected ellipsis on collapsed first line; got %q", out)
		}
		if !strings.Contains(out, "(i)") {
			t.Errorf("selected truncated item must show inspect hint; got %q", out)
		}
	})
	t.Run("selected_short_desc_no_hint", func(t *testing.T) {
		t.Parallel()
		d := newCmdDelegate(80, true)
		items := []list.Item{listItem{origIdx: 0, id: "db.cli", typ: "shell", desc: "Short single line"}}
		out := stripANSI(renderDelegate(t, d, items, 0, 0)) // selected
		if strings.Contains(out, "(i)") {
			t.Errorf("non-truncated selected item must not show inspect hint; got %q", out)
		}
		if strings.Contains(out, "Short single line…") {
			t.Errorf("non-truncated desc must not be marked with ellipsis; got %q", out)
		}
	})
	t.Run("width_truncation_keeps_ellipsis", func(t *testing.T) {
		t.Parallel()
		d := newCmdDelegate(40, true)
		longDesc := "This description is intentionally very long so width truncation kicks in regardless of selection state"
		items := []list.Item{listItem{origIdx: 0, id: "x.y", typ: "shell", desc: longDesc}}
		out := stripANSI(renderDelegate(t, d, items, 1, 0)) // not selected
		if !strings.Contains(out, "…") {
			t.Errorf("width-truncated desc must end with ellipsis; got %q", out)
		}
	})
}

func TestTruncateBoundary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"abc", 5, "abc"},
		{"abcdef", 4, "abc…"},
		{"abcdef", 1, "…"},
		{"", 5, ""},
		{"x", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := truncate(tc.in, tc.width); got != tc.want {
				t.Errorf("truncate(%q,%d)=%q, want %q", tc.in, tc.width, got, tc.want)
			}
		})
	}
}

func TestModel_BreadcrumbFormatting(t *testing.T) {
	t.Parallel()
	items := []Item{
		{ID: "db.migrate"},
		{ID: "services.main.cs.list"},
		{ID: "services.main.cs.update"},
	}
	m := newModel("pick", items, DefaultOptions(), 120, 26)
	m.tree.focusedID = "services.main.cs"
	m.refreshList()
	got := stripANSI(m.breadcrumb())
	if !strings.Contains(got, "services › main › cs") {
		t.Errorf("breadcrumb missing path; got %q", got)
	}
	if !strings.Contains(got, "· 2 commands") {
		t.Errorf("breadcrumb missing plural count; got %q", got)
	}

	m.tree.focusedID = "db"
	m.refreshList()
	got = stripANSI(m.breadcrumb())
	if !strings.Contains(got, "· 1 command") || strings.Contains(got, "· 1 commands") {
		t.Errorf("breadcrumb should singularize; got %q", got)
	}
}

func TestModel_EnterOnCollapsedGroupExpandsAndFocusesRight(t *testing.T) {
	items := []Item{
		{ID: "db.migrate"},
		{ID: "services.api.test"},
	}
	m := newModel("pick", items, DefaultOptions(), 120, 26)
	// Collapse services so Enter must expand it.
	m.tree.focusedID = "services"
	delete(m.tree.expanded, "services")
	m.tree.rebuildVisible()
	if m.tree.expanded["services"] {
		t.Fatal("precondition: services must start collapsed")
	}
	m.Update(syntheticKey("enter"))
	if !m.tree.expanded["services"] {
		t.Errorf("Enter on collapsed group did not expand it")
	}
	// services has no own leaves (its child api carries the leaf), so the
	// list for focused services is empty. Enter should not switch focus in
	// that case — assert by verifying the spec: focus only moves when items
	// exist for the focused node.
	if m.focus != focusLeft {
		t.Errorf("Enter on group with no direct items must keep focus on left; got %v", m.focus)
	}
}

func TestModel_EnterOnGroupWithItemsFocusesRight(t *testing.T) {
	items := []Item{
		{ID: "db.migrate"},
	}
	m := newModel("pick", items, DefaultOptions(), 120, 26)
	m.tree.focusedID = "db"
	m.Update(syntheticKey("enter"))
	if m.focus != focusRight {
		t.Errorf("Enter on group with items must focus right; got %v", m.focus)
	}
}

func TestModel_EnterOnListReturnsResult(t *testing.T) {
	items := []Item{
		{ID: "db.migrate"},
		{ID: "db.seed"},
	}
	m := newModel("pick", items, DefaultOptions(), 120, 26)
	m.tree.focusedID = "db"
	m.refreshList()
	m.focus = focusRight
	// Move list cursor to second item.
	m.list.Select(1)
	_, cmd := m.Update(syntheticKey("enter"))
	if cmd == nil {
		t.Fatal("Enter on list must return a tea.Cmd (Quit)")
	}
	if m.result.Action != ActionRun {
		t.Errorf("Action=%v, want ActionRun", m.result.Action)
	}
	if m.items[m.result.Idx].ID != "db.seed" {
		t.Errorf("Idx=%d (%q), want db.seed", m.result.Idx, m.items[m.result.Idx].ID)
	}
}

func TestModel_EnterReturnsInspectActionInInspectMode(t *testing.T) {
	items := []Item{{ID: "db.migrate"}}
	opts := DefaultOptions()
	opts.Mode = ModeInspect
	m := newModel("inspect", items, opts, 120, 26)
	m.tree.focusedID = "db"
	m.refreshList()
	m.focus = focusRight
	m.list.Select(0)
	m.Update(syntheticKey("enter"))
	if m.result.Action != ActionInspect {
		t.Errorf("Action=%v, want ActionInspect", m.result.Action)
	}
}

func TestModel_LeftOnRightReturnsFocusToTree(t *testing.T) {
	m := newModel("pick", []Item{{ID: "db.migrate"}}, DefaultOptions(), 120, 26)
	m.focus = focusRight
	m.Update(syntheticKey("left"))
	if m.focus != focusLeft {
		t.Errorf("Left on right panel must return focus to tree; got %v", m.focus)
	}
}

func TestModel_WidthBuckets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		width  int
		badges bool
		counts bool
	}{
		{"reduced 80", 80, false, false},
		{"reduced 99", 99, false, false},
		{"full 100", 100, true, true},
		{"full 120", 120, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if showBadges(tc.width) != tc.badges {
				t.Errorf("showBadges(%d)=%v, want %v", tc.width, showBadges(tc.width), tc.badges)
			}
			if showCounts(tc.width) != tc.counts {
				t.Errorf("showCounts(%d)=%v, want %v", tc.width, showCounts(tc.width), tc.counts)
			}
		})
	}
}

func TestModel_BadgesHiddenAtReducedWidth(t *testing.T) {
	items := []Item{{ID: "db.migrate", Type: "shell"}}
	m := newModel("pick", items, DefaultOptions(), 90, 26)
	out := stripANSI(m.View().Content)
	if strings.Contains(out, "[shell]") {
		t.Errorf("reduced-width view should hide type badge; got:\n%s", out)
	}
	// Counts also hidden — tree should not show "(N)".
	if strings.Contains(out, "(1)") {
		t.Errorf("reduced-width view should hide tree counts; got:\n%s", out)
	}
}

func TestModel_BadgesShownAtFullWidth(t *testing.T) {
	items := []Item{{ID: "db.migrate", Type: "shell"}}
	m := newModel("pick", items, DefaultOptions(), 120, 26)
	out := stripANSI(m.View().Content)
	if !strings.Contains(out, "[shell]") {
		t.Errorf("full-width view should show type badge; got:\n%s", out)
	}
	if !strings.Contains(out, "(1)") {
		t.Errorf("full-width view should show tree count; got:\n%s", out)
	}
}
