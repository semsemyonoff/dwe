package cmdbrowser

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

func pluginTestItems() []Item {
	return []Item{
		{ID: "db.migrate", Description: "apply schema", Type: "shell"},
		{ID: "db.seed", Description: "load fixtures", Type: "shell"},
		{ID: "services.api.test", Description: "run api tests", Type: "shell"},
	}
}

func TestBrowser_PanelsShapeAndWeights(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	panels := b.Panels()
	if len(panels) != 2 {
		t.Fatalf("Panels() len=%d, want 2", len(panels))
	}
	if panels[0].ID != panelTree || panels[1].ID != panelList {
		t.Errorf("panel IDs = %q, %q; want %q, %q", panels[0].ID, panels[1].ID, panelTree, panelList)
	}
	if panels[0].Weight != 2 || panels[1].Weight != 7 {
		t.Errorf("weights = %d, %d; want 2, 7", panels[0].Weight, panels[1].Weight)
	}
	for _, p := range panels {
		if p.Weight <= 0 {
			t.Errorf("panel %q has non-positive weight %d", p.ID, p.Weight)
		}
	}
}

func TestBrowser_DefaultsResultAndCapturing(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	if b.CapturingInput() {
		t.Errorf("CapturingInput() = true at rest, want false (no filter active)")
	}
	res, ok := b.Result().(Result)
	if !ok {
		t.Fatalf("Result() type = %T, want cmdbrowser.Result", b.Result())
	}
	if (res != Result{}) {
		t.Errorf("Result() = %+v, want zero Result", res)
	}
	// Entering a filter session flips CapturingInput() on.
	b.filter = &filterState{}
	if !b.CapturingInput() {
		t.Errorf("CapturingInput() = false while filter active, want true")
	}
}

func TestBrowser_InitCloseLifecycle(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	if cmd := b.Init(); cmd != nil {
		t.Errorf("Init() = non-nil cmd, want nil")
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestBrowser_NilTranslatorIsSafe(t *testing.T) {
	opts := DefaultOptions()
	opts.Translator = nil
	b := newBrowser("pick", pluginTestItems(), opts)
	if b.tr == nil {
		t.Fatalf("browser.tr is nil; newBrowser must default to NopTranslator")
	}
	// A nil translator must not panic when consulted via the no-op.
	if got := b.tr.T("en", "tui.help.title", "Help"); got != "Help" {
		t.Errorf("NopTranslator.T fallback = %q, want %q", got, "Help")
	}
}

func TestBrowser_StatusContextBreadcrumb(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	// Focus is on the first visible group (root has only sub-groups). The
	// breadcrumb should name the focused group and a command count.
	got := stripANSI(b.StatusContext())
	if !strings.Contains(got, "command") {
		t.Errorf("StatusContext()=%q, want it to mention the command noun", got)
	}
	if strings.Contains(got, "[--yes ON]") {
		t.Errorf("StatusContext()=%q, want no skip-confirm indicator by default", got)
	}
}

// TestBrowser_BreadcrumbPathAndPlural verifies the breadcrumb renders the
// focused group's dotted path with " › " separators and singularizes/pluralizes
// the command noun by count. Ported from the deleted *Model
// TestModel_BreadcrumbFormatting.
func TestBrowser_BreadcrumbPathAndPlural(t *testing.T) {
	items := []Item{
		{ID: "db.migrate"},
		{ID: "services.main.cs.list"},
		{ID: "services.main.cs.update"},
	}
	b := newBrowser("pick", items, DefaultOptions())
	b.tree.focusedID = "services.main.cs"
	b.refreshList()
	got := stripANSI(b.breadcrumb())
	if !strings.Contains(got, "services › main › cs") {
		t.Errorf("breadcrumb missing path; got %q", got)
	}
	if !strings.Contains(got, "· 2 commands") {
		t.Errorf("breadcrumb missing plural count; got %q", got)
	}

	b.tree.focusedID = "db"
	b.refreshList()
	got = stripANSI(b.breadcrumb())
	if !strings.Contains(got, "· 1 command") || strings.Contains(got, "· 1 commands") {
		t.Errorf("breadcrumb should singularize; got %q", got)
	}
}

func TestBrowser_StatusContextSkipConfirmIndicator(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.skipConfirm = true
	got := stripANSI(b.StatusContext())
	if !strings.Contains(got, "[--yes ON]") {
		t.Errorf("StatusContext()=%q, want [--yes ON] indicator when skipConfirm", got)
	}
}

func TestBrowser_StatusContextSkipConfirmHiddenOutsideModeRun(t *testing.T) {
	opts := DefaultOptions()
	opts.Mode = ModeEdit
	b := newBrowser("pick", []Item{{ID: "vars.db.host"}, {ID: "vars.db.port"}}, opts)
	b.skipConfirm = true
	got := stripANSI(b.StatusContext())
	if strings.Contains(got, "[--yes ON]") {
		t.Errorf("StatusContext()=%q, want no skip-confirm indicator outside ModeRun", got)
	}
	// ModeEdit names rows "var", not "command".
	if !strings.Contains(got, "var") {
		t.Errorf("StatusContext()=%q, want the vars noun in ModeEdit", got)
	}
}

func TestBrowser_ResizeCachesBody(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	body := tui.Region{X: 0, Y: 0, Width: 100, Height: 24}
	b.Resize(body)
	if b.body != body {
		t.Errorf("Resize did not cache body: got %+v, want %+v", b.body, body)
	}
}

func TestBrowser_ViewListFitsInnerRegion(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.tree.focusedID = "db"
	b.refreshList()
	inner := tui.Region{X: 0, Y: 0, Width: 74, Height: 12}
	out := b.ViewPanel(panelList, inner)
	lines := strings.Split(out, "\n")
	if len(lines) > inner.Height {
		t.Errorf("list rendered %d lines, exceeds inner height %d", len(lines), inner.Height)
	}
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w > inner.Width {
			t.Errorf("list line %d width %d exceeds inner width %d: %q", i, w, inner.Width, stripANSI(ln))
		}
	}
}

func TestBrowser_ViewListBadgeVisibilityByWidth(t *testing.T) {
	items := []Item{{ID: "db.migrate", Description: "apply schema", Type: "shell", ParamCount: 2}}
	b := newBrowser("pick", items, DefaultOptions())
	b.tree.focusedID = "db"
	b.refreshList()

	// At/above the inner badge threshold the type badge and param count show.
	wide := stripANSI(b.ViewPanel(panelList, tui.Region{Width: listBadgesMinWidth, Height: 10}))
	if !strings.Contains(wide, "[shell]") {
		t.Errorf("at inner width %d badge should show; got %q", listBadgesMinWidth, wide)
	}
	if !strings.Contains(wide, "[2]") {
		t.Errorf("at inner width %d param count should show; got %q", listBadgesMinWidth, wide)
	}

	// One cell below the threshold both are hidden — matches the legacy
	// terminal-width≥100 boundary recomputed against the inner width.
	narrow := stripANSI(b.ViewPanel(panelList, tui.Region{Width: listBadgesMinWidth - 1, Height: 10}))
	if strings.Contains(narrow, "[shell]") {
		t.Errorf("below inner width %d badge should hide; got %q", listBadgesMinWidth, narrow)
	}
	if strings.Contains(narrow, "[2]") {
		t.Errorf("below inner width %d param count should hide; got %q", listBadgesMinWidth, narrow)
	}
}

func TestBrowser_ViewListBadgesRespectShowTypeBadgesOption(t *testing.T) {
	items := []Item{{ID: "db.migrate", Type: "shell"}}
	opts := DefaultOptions()
	opts.ShowTypeBadges = false
	b := newBrowser("pick", items, opts)
	b.tree.focusedID = "db"
	b.refreshList()
	out := stripANSI(b.ViewPanel(panelList, tui.Region{Width: 90, Height: 10}))
	if strings.Contains(out, "[shell]") {
		t.Errorf("ShowTypeBadges=false must suppress badge even at wide width; got %q", out)
	}
}

func TestBrowser_SelectedOrigIdxRoundTrips(t *testing.T) {
	items := []Item{
		{ID: "db.migrate"},
		{ID: "db.seed"},
		{ID: "services.api.test"},
	}
	b := newBrowser("pick", items, DefaultOptions())
	b.tree.focusedID = "db"
	b.refreshList()
	// The db group lists migrate (origIdx 0) and seed (origIdx 1). Selecting
	// the second list row must resolve back to the original items index 1.
	b.list.Select(1)
	idx, ok := b.selectedOrigIdx()
	if !ok {
		t.Fatalf("selectedOrigIdx ok=false, want a selectable row")
	}
	if items[idx].ID != "db.seed" {
		t.Errorf("selectedOrigIdx=%d (%q), want db.seed", idx, items[idx].ID)
	}
}

func TestBrowser_SelectedOrigIdxEmptyList(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.list.SetItems(nil)
	if _, ok := b.selectedOrigIdx(); ok {
		t.Errorf("selectedOrigIdx ok=true on empty list, want false")
	}
}

func TestBrowser_FocusChangedMsgTracksActivePanel(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	if b.active != panelTree {
		t.Fatalf("initial active panel = %q, want %q", b.active, panelTree)
	}
	if cmd := b.Update(tui.FocusChangedMsg{Panel: panelList}); cmd != nil {
		t.Errorf("FocusChangedMsg returned non-nil cmd, want nil")
	}
	if b.active != panelList {
		t.Errorf("active panel = %q after FocusChangedMsg, want %q", b.active, panelList)
	}
	b.Update(tui.FocusChangedMsg{Panel: panelTree})
	if b.active != panelTree {
		t.Errorf("active panel = %q after second FocusChangedMsg, want %q", b.active, panelTree)
	}
}

func TestBrowser_PanelClickMovesTreeCursorNoToggle(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.ViewPanel(panelTree, tui.Region{Width: 18, Height: 10})
	// Visible rows (depth-1 expansion): [db, services, services.api]. The
	// "services" group is expanded at launch; a single click must move the
	// cursor onto it WITHOUT toggling its expansion (Decision 7).
	if !b.tree.expanded["services"] {
		t.Fatalf("precondition: services should be expanded at launch")
	}
	b.Update(tui.PanelClickMsg{Panel: panelTree, X: 0, Y: 1})
	if b.tree.focusedID != "services" {
		t.Errorf("tree focusedID = %q after click row 1, want %q", b.tree.focusedID, "services")
	}
	if !b.tree.expanded["services"] {
		t.Errorf("single click toggled expansion; services should stay expanded")
	}
}

func TestBrowser_PanelClickPastLastTreeRowIsNoOp(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.ViewPanel(panelTree, tui.Region{Width: 18, Height: 10})
	before := b.tree.focusedID
	// Three visible rows; clicking empty space below them must not move the cursor.
	b.Update(tui.PanelClickMsg{Panel: panelTree, X: 0, Y: 9})
	if b.tree.focusedID != before {
		t.Errorf("focusedID = %q after click past last row, want unchanged %q", b.tree.focusedID, before)
	}
}

func TestBrowser_PanelClickMovesListSelection(t *testing.T) {
	items := []Item{{ID: "db.migrate"}, {ID: "db.seed"}}
	b := newBrowser("pick", items, DefaultOptions())
	b.tree.focusedID = "db"
	b.refreshList()
	b.active = panelList
	b.ViewPanel(panelList, tui.Region{Width: 74, Height: 12})
	// rowHeight = Height(2) + Spacing(1) = 3, so local row 3 maps to the second
	// list item (origIdx 1 → db.seed).
	b.Update(tui.PanelClickMsg{Panel: panelList, X: 0, Y: 3})
	idx, ok := b.selectedOrigIdx()
	if !ok {
		t.Fatalf("selectedOrigIdx ok=false after list click")
	}
	if items[idx].ID != "db.seed" {
		t.Errorf("list click selected %q, want db.seed", items[idx].ID)
	}
}

func TestBrowser_PanelClickIgnoredWhileFiltering(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.ViewPanel(panelTree, tui.Region{Width: 18, Height: 10})
	before := b.tree.focusedID
	b.filter = &filterState{}
	b.Update(tui.PanelClickMsg{Panel: panelTree, X: 0, Y: 1})
	if b.tree.focusedID != before {
		t.Errorf("focusedID = %q after click while filtering, want unchanged %q", b.tree.focusedID, before)
	}
}

func TestBrowser_PanelClickWorksAfterInspectClosed(t *testing.T) {
	// Regression: the Frame pops the inspect overlay on esc without notifying the
	// plugin, so b.inspect lingers non-nil. handlePanelClick must not key off it,
	// otherwise every click is swallowed for the rest of the session once inspect
	// has been opened and closed once.
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.ViewPanel(panelTree, tui.Region{Width: 18, Height: 10})

	// Open inspect (sets b.inspect + inspectPending), drain the pending overlay,
	// then simulate the Frame-side esc close: the overlay is popped but b.inspect
	// is deliberately left lingering (the plugin is never told).
	b.openInspect()
	if b.inspect == nil {
		t.Fatalf("precondition: openInspect should set b.inspect")
	}
	if _, ok := b.PendingOverlay(); !ok {
		t.Fatalf("precondition: inspect overlay should be pending")
	}
	// b.inspect is still non-nil here, mirroring the post-close session state.

	b.Update(tui.PanelClickMsg{Panel: panelTree, X: 0, Y: 1})
	if b.tree.focusedID != "services" {
		t.Errorf("tree focusedID = %q after click following inspect close, want %q (click was swallowed by stale b.inspect)", b.tree.focusedID, "services")
	}
}

func TestBrowser_WheelScrollsFocusedPanel(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.ViewPanel(panelTree, tui.Region{Width: 18, Height: 10})

	// Tree focused: a wheel-down (delivered as ActionNavDown via HandleAction)
	// moves the tree cursor.
	b.active = panelTree
	if _, handled := b.HandleAction(tui.ActionNavDown); !handled {
		t.Fatalf("ActionNavDown not handled")
	}
	if b.tree.focusedID != "services" {
		t.Errorf("tree cursor = %q after wheel-down, want %q", b.tree.focusedID, "services")
	}

	// List focused: a wheel-down advances the list selection instead.
	b.tree.focusedID = "db"
	b.refreshList()
	b.active = panelList
	b.ViewPanel(panelList, tui.Region{Width: 74, Height: 12})
	beforeIdx := b.list.Index()
	if _, handled := b.HandleAction(tui.ActionNavDown); !handled {
		t.Fatalf("ActionNavDown not handled on list")
	}
	if b.list.Index() != beforeIdx+1 {
		t.Errorf("list index = %d after wheel-down, want %d", b.list.Index(), beforeIdx+1)
	}
}

func TestBrowser_DoubleClickSelectGroupVsListItem(t *testing.T) {
	items := []Item{{ID: "services.api.test"}, {ID: "services.web.build"}}
	b := newBrowser("pick", items, DefaultOptions())
	b.ViewPanel(panelTree, tui.Region{Width: 18, Height: 10})

	// Double-click on a tree group (delivered as ActionSelect) toggles expansion
	// and does NOT quit.
	b.active = panelTree
	b.tree.focusedID = "services"
	wasExpanded := b.tree.expanded["services"]
	cmd, handled := b.HandleAction(tui.ActionSelect)
	if !handled {
		t.Fatalf("ActionSelect on group not handled")
	}
	if cmd != nil {
		t.Errorf("ActionSelect on group returned a cmd, want nil (no quit)")
	}
	if b.tree.expanded["services"] == wasExpanded {
		t.Errorf("ActionSelect on group did not toggle expansion")
	}

	// Double-click on a list item commits a Result and quits.
	b.tree.focusedID = "services.api"
	b.refreshList()
	b.active = panelList
	cmd, handled = b.HandleAction(tui.ActionSelect)
	if !handled {
		t.Fatalf("ActionSelect on list item not handled")
	}
	if cmd == nil {
		t.Errorf("ActionSelect on list item returned nil cmd, want tea.Quit")
	}
	res := b.Result().(Result)
	if items[res.Idx].ID != "services.api.test" {
		t.Errorf("Result.Idx points at %q, want services.api.test", items[res.Idx].ID)
	}
}

func TestBrowser_ViewPanelCachesInnerRegions(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	treeInner := tui.Region{X: 1, Y: 1, Width: 18, Height: 20}
	listInner := tui.Region{X: 21, Y: 1, Width: 70, Height: 20}
	b.ViewPanel(panelTree, treeInner)
	b.ViewPanel(panelList, listInner)
	if b.treeInner != treeInner {
		t.Errorf("treeInner = %+v, want %+v", b.treeInner, treeInner)
	}
	if b.listInner != listInner {
		t.Errorf("listInner = %+v, want %+v", b.listInner, listInner)
	}
}
