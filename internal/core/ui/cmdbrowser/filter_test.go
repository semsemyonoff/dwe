package cmdbrowser

import (
	"maps"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

func filterTestItems() []Item {
	return []Item{
		{ID: "db.migrate", Description: "apply schema", Type: "shell"},
		{ID: "db.seed", Description: "load fixtures", Type: "shell"},
		{ID: "services.api.test", Description: "run api tests", Type: "shell"},
		{ID: "services.api.lint", Description: "lint api", Type: "shell"},
	}
}

// typeFilter types each rune of s into the active filter session of b via the
// plugin's capture path (b.Update), mirroring how the Frame forwards raw keys
// while CapturingInput() is true.
func typeFilter(b *browser, s string) {
	for _, r := range s {
		b.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// TestBrowser_RenderTreeFilteredRespectsHeight asserts the filtered tree panel
// honors the Frame-provided height budget: 0 rows render nothing, 1 row renders
// only the query line, and larger regions never exceed the budget.
func TestBrowser_RenderTreeFilteredRespectsHeight(t *testing.T) {
	b := newBrowser("pick", filterTestItems(), DefaultOptions())
	b.Resize(tui.Region{Width: 80, Height: 24})
	b.enterFilter()

	if got := b.renderTreeFiltered(tui.Region{Width: 30, Height: 0}); got != "" {
		t.Errorf("height=0 rendered %q, want empty", got)
	}
	one := b.renderTreeFiltered(tui.Region{Width: 30, Height: 1})
	if strings.Contains(one, "\n") {
		t.Errorf("height=1 rendered %d lines, want 1:\n%s", strings.Count(one, "\n")+1, one)
	}
	for _, h := range []int{2, 5, 10} {
		got := b.renderTreeFiltered(tui.Region{Width: 30, Height: h})
		if lines := strings.Count(got, "\n") + 1; lines > h {
			t.Errorf("height=%d rendered %d lines, exceeds budget:\n%s", h, lines, got)
		}
	}
}

// TestBrowser_EnterFilterSetsCapturing verifies the filter action turns on
// CapturingInput so the Frame begins forwarding raw keys to the plugin.
func TestBrowser_EnterFilterSetsCapturing(t *testing.T) {
	b := newBrowser("pick", filterTestItems(), DefaultOptions())
	if b.CapturingInput() {
		t.Fatal("CapturingInput must be false before entering filter")
	}
	b.HandleAction(tui.ActionFilter)
	if b.filter == nil {
		t.Fatal("ActionFilter did not create filter state")
	}
	if !b.CapturingInput() {
		t.Error("CapturingInput must be true while the filter is active")
	}
}

// TestBrowser_FilterLetterShortcutsTypeIntoQuery verifies that letter keys bound
// elsewhere as actions (i / y / e / q) extend the query instead of firing the
// action while the search line is active.
func TestBrowser_FilterLetterShortcutsTypeIntoQuery(t *testing.T) {
	items := filterTestItems()
	items[0].Inspect = func(int) string { return "details" }
	b := newBrowser("pick", items, DefaultOptions())
	b.enterFilter()
	typeFilter(b, "iyeq")
	if b.filter == nil {
		t.Fatal("filter exited unexpectedly while typing action letters")
	}
	if b.filter.query != "iyeq" {
		t.Errorf("query=%q, want %q — action letters must extend the query", b.filter.query, "iyeq")
	}
	if b.inspect != nil {
		t.Error("'i' inside filter must not open inspect")
	}
	if b.skipConfirm {
		t.Error("'y' inside filter must not toggle skip-confirm")
	}
}

// TestBrowser_FilterBackspaceTrims verifies backspace deletes the last rune.
func TestBrowser_FilterBackspaceTrims(t *testing.T) {
	b := newBrowser("pick", filterTestItems(), DefaultOptions())
	b.enterFilter()
	typeFilter(b, "db")
	b.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if b.filter.query != "d" {
		t.Errorf("after backspace query=%q, want %q", b.filter.query, "d")
	}
}

// TestBrowser_FilterLiveNarrowsWithMatchCounts verifies the live filter narrows
// the tree and reports correct per-group match counts as the user types.
func TestBrowser_FilterLiveNarrowsWithMatchCounts(t *testing.T) {
	opts := DefaultOptions()
	opts.AutoCollapseEmpty = true
	b := newBrowser("pick", filterTestItems(), opts)
	b.enterFilter()
	typeFilter(b, "db")
	if b.filter.matchCount["db"] != 2 {
		t.Errorf("db match count=%d, want 2", b.filter.matchCount["db"])
	}
	if b.filter.matchCount["services"] != 0 {
		t.Errorf("services match count=%d, want 0", b.filter.matchCount["services"])
	}
	if b.tree.eng.IsExpanded(b.tree.nodesByID["services"]) {
		t.Error("zero-match 'services' subtree must auto-collapse")
	}
	// The query line renders inside the tree panel with the M/N counts.
	out := stripANSI(b.ViewPanel(panelTree, tui.Region{Width: 30, Height: 12}))
	if !strings.Contains(out, "/db") {
		t.Errorf("tree panel missing query line; got:\n%s", out)
	}
	if !strings.Contains(out, "(2/2)") {
		t.Errorf("tree panel missing M/N count for db; got:\n%s", out)
	}
}

// TestBrowser_FilterEscRestoresPriorState verifies esc restores the snapshotted
// expansion and clears the capture session without selecting anything.
func TestBrowser_FilterEscRestoresPriorState(t *testing.T) {
	opts := DefaultOptions()
	opts.AutoCollapseEmpty = true
	b := newBrowser("pick", filterTestItems(), opts)
	snapshot := b.tree.eng.ExpandedSnapshot()
	want := make(map[string]bool, len(snapshot))
	maps.Copy(want, snapshot)

	b.enterFilter()
	typeFilter(b, "db") // collapses 'services' under AutoCollapseEmpty
	b.Update(syntheticKey("esc"))

	if b.filter != nil || b.CapturingInput() {
		t.Fatal("esc must clear the filter session")
	}
	if b.result != (Result{}) {
		t.Errorf("esc must not produce a Result; got %+v", b.result)
	}
	got := b.tree.eng.ExpandedSnapshot()
	if len(got) != len(want) {
		t.Fatalf("expanded set not restored; want %d entries, got %d", len(want), len(got))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("expanded[%q]=%v, want %v", k, got[k], v)
		}
	}
}

// TestBrowser_FilterEnterCommitsKeepingExpansion verifies enter commits: it
// clears the capture session but KEEPS the filter-induced (auto-collapsed)
// expansion, unlike esc which restores it.
func TestBrowser_FilterEnterCommitsKeepingExpansion(t *testing.T) {
	opts := DefaultOptions()
	opts.AutoCollapseEmpty = true
	b := newBrowser("pick", filterTestItems(), opts)

	b.enterFilter()
	typeFilter(b, "db") // collapses 'services' under AutoCollapseEmpty
	if b.tree.eng.IsExpanded(b.tree.nodesByID["services"]) {
		t.Fatal("test setup: services should be collapsed while filtering")
	}
	b.Update(syntheticKey("enter"))

	if b.filter != nil || b.CapturingInput() {
		t.Fatal("enter must clear the filter session")
	}
	if b.result != (Result{}) {
		t.Errorf("commit must not select an item; got %+v", b.result)
	}
	// Expansion is KEPT (services stays collapsed), not restored.
	if b.tree.eng.IsExpanded(b.tree.nodesByID["services"]) {
		t.Error("commit must keep the filtered expansion (services stays collapsed)")
	}
	// Tree focus lands on the nearest ancestor of the highlighted match.
	if b.tree.focusedID() != "db" {
		t.Errorf("focusedID=%q, want %q after commit", b.tree.focusedID(), "db")
	}
}

// TestBrowser_FilterRoundTripCursorStable verifies that opening the filter,
// typing a query, and exiting via esc lands the tree cursor on the SAME row it
// started on — the engine's RebuildVisible must not re-park the cursor and the
// nearestVisibleAncestor restoration must resolve back to the original node.
// This guards the golden-critical cursor-landing contract after the engine
// extraction.
func TestBrowser_FilterRoundTripCursorStable(t *testing.T) {
	b := newBrowser("pick", filterTestItems(), DefaultOptions())
	b.tree.eng.SetCursorByKey("services.api")
	before := b.tree.focusedID()

	b.enterFilter()
	typeFilter(b, "lint") // matches services.api.lint
	b.Update(syntheticKey("esc"))

	if b.filter != nil {
		t.Fatalf("esc must exit the filter session")
	}
	if got := b.tree.focusedID(); got != before {
		t.Errorf("tree cursor = %q after filter round-trip, want unchanged %q", got, before)
	}
}

// TestBrowser_FilterExitRequestsListFocus verifies both filter exits (enter
// commit and esc cancel) move the active panel to the list AND return a
// FocusRequestMsg{list} so the Frame border tracks the nav target — guarding
// against the focus/border desync that arises when only b.active is updated.
func TestBrowser_FilterExitRequestsListFocus(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"enter-commit", "enter"},
		{"esc-cancel", "esc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newBrowser("pick", filterTestItems(), DefaultOptions())
			// Enter the filter while the tree is focused (the launch default), the
			// exact condition under which a desync would otherwise occur.
			b.active = panelTree
			b.enterFilter()
			typeFilter(b, "db")

			cmd := b.Update(syntheticKey(tc.key))
			if b.active != panelList {
				t.Errorf("active panel = %q after %s, want list", b.active, tc.name)
			}
			if cmd == nil {
				t.Fatalf("%s must return a focus-request command", tc.name)
			}
			msg := cmd()
			fr, ok := msg.(tui.FocusRequestMsg)
			if !ok {
				t.Fatalf("%s cmd produced %T, want tui.FocusRequestMsg", tc.name, msg)
			}
			if fr.Panel != panelList {
				t.Errorf("FocusRequestMsg.Panel = %q, want list", fr.Panel)
			}
		})
	}
}

// TestBrowser_FilterEscFocusedIDVisibleAfterRestoration verifies that when the
// pre-filter state had the focused node's parent collapsed (making the node
// invisible after restoration), exitFilter walks up to the nearest visible
// ancestor so the tree cursor never lands on a hidden node. Ported from the
// deleted *Model TestFilter_ExitFilter_FocusedIDVisibleAfterRestoration.
func TestBrowser_FilterEscFocusedIDVisibleAfterRestoration(t *testing.T) {
	b := newBrowser("pick", filterTestItems(), DefaultOptions())

	// Collapse "services" — its children (services.api) become invisible.
	b.tree.eng.SetExpandedByKey("services", false)
	b.tree.eng.RebuildVisible(nil)
	if contains(visibleIDs(b.tree), "services.api") {
		t.Fatalf("test setup: services.api should be hidden when services is collapsed")
	}

	// Enter filter and set a query manually so the match list highlights a
	// services.api item before exiting.
	b.enterFilter()
	if b.filter == nil {
		t.Fatalf("enterFilter did not create filter state")
	}
	b.filter.query = "ser"
	b.refreshFilterMatches()
	if b.filter.matchCount["services.api"] == 0 {
		t.Fatalf("filter did not match services.api items (query=%q)", b.filter.query)
	}

	// exitFilter must notice services.api is hidden after restoration and walk up
	// to the nearest visible ancestor "services".
	b.exitFilter()
	if b.filter != nil {
		t.Fatalf("filter not exited")
	}
	focused := b.tree.focusedID()
	if !contains(visibleIDs(b.tree), focused) && focused != "" {
		t.Errorf("focusedID=%q is not visible after exitFilter; visible=%v", focused, visibleIDs(b.tree))
	}
	if focused != "services" {
		t.Errorf("focusedID=%q, want %q", focused, "services")
	}
}
