package cmdbrowser

import (
	"maps"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func filterTestItems() []Item {
	return []Item{
		{ID: "db.migrate", Description: "apply schema", Type: "shell"},
		{ID: "db.seed", Description: "load fixtures", Type: "shell"},
		{ID: "services.api.test", Description: "run api tests", Type: "shell"},
		{ID: "services.api.lint", Description: "lint api", Type: "shell"},
	}
}

func TestFilter_EnterAndExitRestoresExpansion(t *testing.T) {
	m := newModel("pick", filterTestItems(), DefaultOptions(), 120, 26)
	// Capture original expanded state.
	want := make(map[string]bool, len(m.tree.expanded))
	maps.Copy(want, m.tree.expanded)
	// Enter filter, type a query that prunes most groups, then exit.
	m.Update(syntheticKey("/"))
	if m.focus != focusFilter || m.filter == nil {
		t.Fatalf("entering filter failed; focus=%v filter=%v", m.focus, m.filter)
	}
	m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if m.filter.query != "d" {
		t.Errorf("query=%q, want %q", m.filter.query, "d")
	}
	m.Update(syntheticKey("esc"))
	if m.focus == focusFilter || m.filter != nil {
		t.Fatalf("exit filter failed; focus=%v filter=%v", m.focus, m.filter)
	}
	got := m.tree.expanded
	if len(got) != len(want) {
		t.Errorf("expanded set not restored; want %d entries, got %d", len(want), len(got))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("expanded[%q]=%v, want %v", k, got[k], v)
		}
	}
}

func TestFilter_AutoCollapseHidesZeroMatchSubtrees(t *testing.T) {
	opts := DefaultOptions()
	opts.AutoCollapseEmpty = true
	m := newModel("pick", filterTestItems(), opts, 120, 26)
	m.Update(syntheticKey("/"))
	m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	// "db" matches; "services.*" should not match.
	if m.filter.matchCount["db"] == 0 {
		t.Errorf("expected db matches, got 0")
	}
	if m.filter.matchCount["services"] != 0 {
		t.Errorf("expected zero matches for services, got %d", m.filter.matchCount["services"])
	}
	// services should be collapsed.
	if m.tree.expanded["services"] {
		t.Errorf("services should be collapsed under AutoCollapseEmpty")
	}
}

func TestFilter_BackspaceTrims(t *testing.T) {
	m := newModel("pick", filterTestItems(), DefaultOptions(), 120, 26)
	m.Update(syntheticKey("/"))
	for _, r := range "db" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if m.filter.query != "db" {
		t.Fatalf("query=%q", m.filter.query)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.filter.query != "d" {
		t.Errorf("after backspace query=%q, want %q", m.filter.query, "d")
	}
}

// TestFilter_LetterShortcutsTypeIntoQuery verifies that letter keys bound to
// global shortcuts (Inspect=i, SkipConfirm=y, EditParams=e, Cancel=q) extend
// the filter query instead of firing the shortcut while the cursor is on the
// search line. Non-printable keys (Esc, Backspace, arrows) keep their
// shortcut semantics — verified by TestEsc_InFilterExitsFilterOnly and
// TestFilter_BackspaceTrims.
func TestFilter_LetterShortcutsTypeIntoQuery(t *testing.T) {
	items := filterTestItems()
	items[0].Inspect = func(int) string { return "inspect details" }
	m := newModel("pick", items, DefaultOptions(), 120, 26)
	m.Update(syntheticKey("/"))
	if m.focus != focusFilter {
		t.Fatalf("entering filter failed; focus=%v", m.focus)
	}
	for _, r := range "iyeq" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if m.filter == nil {
		t.Fatalf("filter exited unexpectedly; focus=%v", m.focus)
	}
	if m.focus != focusFilter {
		t.Errorf("focus=%v, want focusFilter — letter shortcuts must not exit the search line", m.focus)
	}
	if m.filter.query != "iyeq" {
		t.Errorf("query=%q, want %q — letter shortcuts must extend the query", m.filter.query, "iyeq")
	}
	if m.inspect != nil {
		t.Errorf("'i' inside filter must not open inspect")
	}
	if m.skipConfirm {
		t.Errorf("'y' inside filter must not toggle skip-confirm")
	}
}

func TestFilter_EnterSelectsMatchedItem(t *testing.T) {
	m := newModel("pick", filterTestItems(), DefaultOptions(), 120, 26)
	m.Update(syntheticKey("/"))
	m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	// First match should be db.migrate.
	_, cmd := m.Update(syntheticKey("enter"))
	if cmd == nil {
		t.Fatal("Enter on filter must Quit")
	}
	if m.items[m.result.Idx].ID != "db.migrate" {
		t.Errorf("idx=%d (%q), want db.migrate", m.result.Idx, m.items[m.result.Idx].ID)
	}
}

func TestFilter_MNCountsInView(t *testing.T) {
	m := newModel("pick", filterTestItems(), DefaultOptions(), 120, 26)
	m.Update(syntheticKey("/"))
	m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	out := stripANSI(m.View().Content)
	// db subtree has 2/2 matches.
	if !strings.Contains(out, "(2/2)") {
		t.Errorf("missing M/N count for db; got:\n%s", out)
	}
}

func TestSkipConfirm_TogglesAndAppearsInResult(t *testing.T) {
	m := newModel("pick", filterTestItems(), DefaultOptions(), 120, 26)
	m.focus = focusRight
	m.Update(syntheticKey("y"))
	if !m.skipConfirm {
		t.Errorf("y should turn skipConfirm on")
	}
	out := stripANSI(m.View().Content)
	if !strings.Contains(out, "[--yes ON]") {
		t.Errorf("title bar missing yes indicator; got:\n%s", out)
	}
	m.Update(syntheticKey("y"))
	if m.skipConfirm {
		t.Errorf("second y should turn it off")
	}
	// Toggle on, then Enter — Result.SkipConfirm must propagate.
	m.Update(syntheticKey("y"))
	m.list.Select(0)
	m.Update(syntheticKey("enter"))
	if !m.result.SkipConfirm {
		t.Errorf("Result.SkipConfirm not propagated; got %+v", m.result)
	}
}

func TestSkipConfirm_InspectModeIgnoresY(t *testing.T) {
	opts := DefaultOptions()
	opts.Mode = ModeInspect
	m := newModel("pick", filterTestItems(), opts, 120, 26)
	m.focus = focusRight
	m.Update(syntheticKey("y"))
	if m.skipConfirm {
		t.Errorf("y must not toggle in inspect mode")
	}
}

func TestInspect_RendersAtViewportWidth(t *testing.T) {
	// Inspect closure receives the viewport's *content width* — the right
	// panel's inner content area, capped at inspectMaxWidth so the section
	// divider rendered by ui.RenderSectionTitle (also capped at 100) lines up
	// with the surrounding content. Narrow terminals get the full panel;
	// wide ones are capped.
	cases := []struct {
		name       string
		w, h       int
		wantMin    int // viewport must be at least this wide
		wantAtMost int // and at most this wide (e.g. inspectMaxWidth on wide screens)
	}{
		{"two_panel_120", 120, 26, 60, inspectMaxWidth},
		{"two_panel_200", 200, 30, inspectMaxWidth, inspectMaxWidth}, // wide terminal caps at inspectMaxWidth
		{"single_panel_70", 70, 20, 50, 70},                          // narrow terminal uses panel width, well under the cap
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotWidth int
			items := []Item{{ID: "db.x", Type: "shell", Description: "short",
				Inspect: func(w int) string { gotWidth = w; return "content" }}}
			m := newModel("inspect", items, DefaultOptions(), tc.w, tc.h)
			m.tree.focusedID = "db"
			m.refreshList()
			m.focus = focusRight
			m.list.Select(0)
			m.Update(syntheticKey("i"))
			if gotWidth == 0 {
				t.Fatal("Inspect closure was not called")
			}
			if gotWidth >= tc.w {
				t.Errorf("Inspect width %d must be < terminal width %d", gotWidth, tc.w)
			}
			if gotWidth < tc.wantMin {
				t.Errorf("Inspect width %d narrower than expected minimum %d at terminal %dx%d",
					gotWidth, tc.wantMin, tc.w, tc.h)
			}
			if gotWidth > tc.wantAtMost {
				t.Errorf("Inspect width %d exceeds expected cap %d at terminal %dx%d",
					gotWidth, tc.wantAtMost, tc.w, tc.h)
			}
		})
	}
}

func TestInspect_OpensAndCloses(t *testing.T) {
	items := filterTestItems()
	items[0].Inspect = func(int) string { return "inspect details for db.migrate" }
	m := newModel("pick", items, DefaultOptions(), 120, 26)
	m.tree.focusedID = "db"
	m.refreshList()
	m.focus = focusRight
	m.list.Select(0)
	m.Update(syntheticKey("i"))
	if m.focus != focusInspect || m.inspect == nil {
		t.Fatalf("inspect did not open; focus=%v inspect=%v", m.focus, m.inspect)
	}
	out := m.View().Content
	if !strings.Contains(out, "db.migrate") {
		t.Errorf("inspect view missing id; got:\n%s", out)
	}
	if !strings.Contains(out, "inspect details") {
		t.Errorf("inspect content not rendered; got:\n%s", out)
	}
	m.Update(syntheticKey("esc"))
	if m.focus != focusRight || m.inspect != nil {
		t.Errorf("esc should close inspect; focus=%v inspect=%v", m.focus, m.inspect)
	}
}

func TestInspect_EnterReturnsInspectAction(t *testing.T) {
	items := filterTestItems()
	items[1].Inspect = func(int) string { return "details" }
	opts := DefaultOptions()
	opts.Mode = ModeInspect
	m := newModel("pick", items, opts, 120, 26)
	m.tree.focusedID = "db"
	m.refreshList()
	m.focus = focusRight
	m.list.Select(1) // db.seed
	m.Update(syntheticKey("i"))
	if m.focus != focusInspect {
		t.Fatalf("not in inspect mode; focus=%v", m.focus)
	}
	_, cmd := m.Update(syntheticKey("enter"))
	if cmd == nil {
		t.Fatal("Enter in inspect must Quit")
	}
	if m.result.Action != ActionInspect {
		t.Errorf("Action=%v, want ActionInspect", m.result.Action)
	}
	if m.items[m.result.Idx].ID != "db.seed" {
		t.Errorf("Idx=%d (%q), want db.seed", m.result.Idx, m.items[m.result.Idx].ID)
	}
}

func TestInspect_EmptyContentShowsPlaceholder(t *testing.T) {
	items := filterTestItems() // no Inspect set
	m := newModel("pick", items, DefaultOptions(), 120, 26)
	m.tree.focusedID = "db"
	m.refreshList()
	m.focus = focusRight
	m.list.Select(0)
	m.Update(syntheticKey("i"))
	if m.inspect == nil {
		t.Fatal("inspect did not open with empty content")
	}
	out := m.View().Content
	if !strings.Contains(out, "no inspect content") {
		t.Errorf("missing placeholder; got:\n%s", out)
	}
}

func TestHelp_FooterEntriesIncludeNavAndActions(t *testing.T) {
	// DefaultOptions uses ModeRun, which includes the two extra entries
	// (edit-params, skip-confirm) on top of the seven shared with ModeInspect.
	m := newModel("pick", filterTestItems(), DefaultOptions(), 120, 26)
	entries := m.helpEntries()
	if len(entries) != 9 {
		t.Fatalf("helpEntries should return 9 entries in ModeRun, got %d", len(entries))
	}
	wantKeys := map[string]bool{
		"↑↓": false, "←→": false, "enter": false, "tab": false,
		"/": false, "i": false, "esc": false, "e": false, "y": false,
	}
	for _, e := range entries {
		if _, ok := wantKeys[e.key]; ok {
			wantKeys[e.key] = true
		}
	}
	for k, seen := range wantKeys {
		if !seen {
			t.Errorf("missing expected help entry key %q", k)
		}
	}

	opts := DefaultOptions()
	opts.Mode = ModeInspect
	m = newModel("pick", filterTestItems(), opts, 120, 26)
	if got := len(m.helpEntries()); got != 7 {
		t.Errorf("helpEntries should return 7 entries in ModeInspect, got %d", got)
	}
}

// EscOnLeft exits the program at top-level.
func TestEsc_OnTopLevelTreeExitsProgram(t *testing.T) {
	m := newModel("pick", filterTestItems(), DefaultOptions(), 120, 26)
	m.Update(syntheticKey("esc"))
	if !m.cancelled {
		t.Errorf("esc at top level should set cancelled")
	}
}

// EscInFilterExitsFilterOnly verifies the §19 semantics: esc inside filter
// returns focus to the right panel without cancelling the program.
func TestEsc_InFilterExitsFilterOnly(t *testing.T) {
	m := newModel("pick", filterTestItems(), DefaultOptions(), 120, 26)
	m.Update(syntheticKey("/"))
	m.Update(syntheticKey("esc"))
	if m.cancelled {
		t.Errorf("esc inside filter must not cancel the program")
	}
	if m.focus == focusFilter {
		t.Errorf("esc inside filter must exit filter mode")
	}
}

// TestFilter_ExitFilter_FocusedIDVisibleAfterRestoration verifies that when
// the pre-filter state had the focused node's parent collapsed (making the node
// invisible after restoration), exitFilter falls back to the nearest visible
// ancestor so the tree cursor is never on a hidden node.
func TestFilter_ExitFilter_FocusedIDVisibleAfterRestoration(t *testing.T) {
	// Items with a two-level hierarchy: services.api.test, services.api.lint.
	// After newModel at depth 3 all nodes are expanded. We then manually
	// collapse "services" so that "services.api" is hidden, enter filter,
	// set a query that matches services.api items, then exit filter. The
	// focusedID must land on "services" (nearest visible ancestor), not
	// "services.api".
	m := newModel("pick", filterTestItems(), DefaultOptions(), 120, 26)

	// Collapse "services" — its children (services.api) become invisible.
	delete(m.tree.expanded, "services")
	m.tree.rebuildVisible()
	if contains(visibleIDs(m.tree), "services.api") {
		t.Fatalf("test setup: services.api should be hidden when services is collapsed")
	}

	// Enter filter and set a query manually to avoid key-binding side-effects
	// (e.g. 'i' in "api" would trigger the inspect overlay before reaching
	// the printable-text branch).
	m.enterFilter()
	if m.filter == nil {
		t.Fatalf("enterFilter did not create filter state")
	}
	m.filter.query = "ser"
	m.refreshFilterMatches()

	// Verify the filter matched services.api items.
	if m.filter.matchCount["services.api"] == 0 {
		t.Fatalf("filter did not match services.api items (query=%q)", m.filter.query)
	}

	// exitFilter should notice services.api is hidden after restoration and
	// walk up to the nearest visible ancestor "services".
	m.exitFilter()
	if m.focus == focusFilter {
		t.Fatalf("filter not exited")
	}

	// focusedID must be visible.
	focused := m.tree.focusedID
	if !contains(visibleIDs(m.tree), focused) && focused != "" {
		t.Errorf("focusedID=%q is not visible after exitFilter; visible=%v", focused, visibleIDs(m.tree))
	}
	// It must be "services" (the nearest visible ancestor of services.api).
	if focused != "services" {
		t.Errorf("focusedID=%q, want %q", focused, "services")
	}
}
