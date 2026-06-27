package cmdbrowser

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// buildRegistry runs the browser's Actions hook against a fresh registry (with
// the framework built-ins pre-registered) and fails on a collision.
func buildRegistry(t *testing.T, b *browser) *tui.Registry {
	t.Helper()
	reg := tui.NewRegistry()
	if err := b.Actions(reg); err != nil {
		t.Fatalf("Actions() error: %v", err)
	}
	return reg
}

func TestBrowser_ActionsRegistered(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	reg := buildRegistry(t, b)

	// Navigation + verbs every mode registers.
	wantKey := map[string]tui.Action{
		"up":     tui.ActionNavUp,
		"k":      tui.ActionNavUp,
		"down":   tui.ActionNavDown,
		"j":      tui.ActionNavDown,
		"left":   tui.ActionNavLeft,
		"right":  tui.ActionNavRight,
		"g":      tui.ActionTop,
		"home":   tui.ActionTop,
		"G":      tui.ActionBottom,
		"end":    tui.ActionBottom,
		"pgup":   tui.ActionPageUp,
		"pgdown": tui.ActionPageDown,
		"enter":  tui.ActionSelect,
		"/":      tui.ActionFilter,
		"i":      tui.ActionInspect,
	}
	for key, want := range wantKey {
		got, ok := reg.Match(key)
		if !ok || got != want {
			t.Errorf("Match(%q) = (%q, %v); want (%q, true)", key, got, ok, want)
		}
	}

	// Wheel bindings must reach nav up/down so Task 9 can scroll the focused panel.
	if a, ok := reg.MatchMouse("wheel-up"); !ok || a != tui.ActionNavUp {
		t.Errorf("MatchMouse(wheel-up) = (%q, %v); want nav.up", a, ok)
	}
	if a, ok := reg.MatchMouse("wheel-down"); !ok || a != tui.ActionNavDown {
		t.Errorf("MatchMouse(wheel-down) = (%q, %v); want nav.down", a, ok)
	}
	if a, ok := reg.MatchMouse("double-click"); !ok || a != tui.ActionSelect {
		t.Errorf("MatchMouse(double-click) = (%q, %v); want select", a, ok)
	}

	// select / filter / inspect regroup under "Actions".
	if bind, ok := reg.Binding(tui.ActionSelect); !ok || bind.Section != sectionActions {
		t.Errorf("ActionSelect section = %q, want %q", bind.Section, sectionActions)
	}
}

func TestBrowser_ActionSetDiffersByMode(t *testing.T) {
	run := buildRegistry(t, newBrowser("pick", pluginTestItems(), DefaultOptions()))

	editOpts := DefaultOptions()
	editOpts.Mode = ModeEdit
	edit := buildRegistry(t, newBrowser("pick", pluginTestItems(), editOpts))

	// ModeRun registers skip-confirm (y) and force-form (e); ModeEdit does not.
	if a, ok := run.Match("y"); !ok || a != actionSkipConfirm {
		t.Errorf("ModeRun Match(y) = (%q, %v); want cmd.skip-confirm", a, ok)
	}
	if a, ok := run.Match("e"); !ok || a != actionForceForm {
		t.Errorf("ModeRun Match(e) = (%q, %v); want cmd.force-form", a, ok)
	}
	if _, ok := edit.Match("y"); ok {
		t.Errorf("ModeEdit must not register y (skip-confirm)")
	}
	if _, ok := edit.Match("e"); ok {
		t.Errorf("ModeEdit must not register e (force-form)")
	}

	// ModeEdit relabels select to "Edit".
	if bind, ok := edit.Binding(tui.ActionSelect); !ok || bind.Desc != "Edit" {
		t.Errorf("ModeEdit select desc = %q, want %q", bind.Desc, "Edit")
	}
}

func TestBrowser_HandleSelectTogglesGroup(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.active = panelTree
	// "services" is expanded at the default depth; select must collapse it.
	b.tree.focusedID = "services"
	if !b.tree.expanded["services"] {
		t.Fatalf("precondition: services should be expanded at default depth")
	}
	cmd, handled := b.HandleAction(tui.ActionSelect)
	if !handled {
		t.Fatalf("HandleAction(select) handled=false, want true")
	}
	if cmd != nil {
		t.Errorf("select on a group should not quit (cmd != nil)")
	}
	if b.tree.expanded["services"] {
		t.Errorf("select on an expanded group should collapse it")
	}
	// A second select re-expands.
	if _, _ = b.HandleAction(tui.ActionSelect); !b.tree.expanded["services"] {
		t.Errorf("second select should re-expand the group")
	}
}

func TestBrowser_HandleSelectRunsListItem(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.active = panelList
	b.tree.focusedID = "db"
	b.refreshList()
	b.list.Select(1) // db.seed -> origIdx 1
	cmd, handled := b.HandleAction(tui.ActionSelect)
	if !handled {
		t.Fatalf("HandleAction(select) handled=false, want true")
	}
	if cmd == nil {
		t.Errorf("select on a list item should quit (cmd == nil)")
	}
	if b.result.Idx != 1 || b.result.Action != ActionRun {
		t.Errorf("result = %+v, want Idx=1 Action=ActionRun", b.result)
	}
	if b.result.SkipConfirm || b.result.ForceParamForm {
		t.Errorf("plain select must not set SkipConfirm/ForceParamForm: %+v", b.result)
	}
}

func TestBrowser_HandleSkipConfirmToggles(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	if _, handled := b.HandleAction(actionSkipConfirm); !handled {
		t.Fatalf("HandleAction(skip-confirm) handled=false, want true")
	}
	if !b.skipConfirm {
		t.Errorf("skip-confirm should toggle skipConfirm on")
	}
	if !strings.Contains(stripANSI(b.StatusContext()), "[--yes ON]") {
		t.Errorf("StatusContext must surface [--yes ON] after toggle")
	}
	if _, _ = b.HandleAction(actionSkipConfirm); b.skipConfirm {
		t.Errorf("second skip-confirm should toggle off")
	}
}

func TestBrowser_HandleForceFormSetsFlagAndSelects(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.active = panelList
	b.tree.focusedID = "db"
	b.refreshList()
	b.list.Select(0) // db.migrate -> origIdx 0
	cmd, handled := b.HandleAction(actionForceForm)
	if !handled {
		t.Fatalf("HandleAction(force-form) handled=false, want true")
	}
	if cmd == nil {
		t.Errorf("force-form should quit (cmd == nil)")
	}
	if b.result.Idx != 0 || b.result.Action != ActionRun || !b.result.ForceParamForm {
		t.Errorf("result = %+v, want Idx=0 Action=ActionRun ForceParamForm=true", b.result)
	}
}

func TestBrowser_HandleForceFormNoopOutsideRunOrTreePanel(t *testing.T) {
	// ModeEdit: force-form is not even registered, and HandleAction guards Mode.
	editOpts := DefaultOptions()
	editOpts.Mode = ModeEdit
	be := newBrowser("pick", pluginTestItems(), editOpts)
	be.active = panelList
	be.tree.focusedID = "db"
	be.refreshList()
	be.list.Select(0)
	if cmd, _ := be.HandleAction(actionForceForm); cmd != nil {
		t.Errorf("force-form in ModeEdit must be a no-op")
	}
	if (be.result != Result{}) {
		t.Errorf("force-form in ModeEdit must not set a result: %+v", be.result)
	}

	// ModeRun but focus on the tree (no list selection context): legacy EditParams
	// only fired in the list panel.
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.active = panelTree
	if cmd, _ := b.HandleAction(actionForceForm); cmd != nil {
		t.Errorf("force-form with tree focus must be a no-op")
	}
	if (b.result != Result{}) {
		t.Errorf("force-form with tree focus must not set a result: %+v", b.result)
	}
}

func TestBrowser_NavRoutesToActivePanel(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.treeInner = tui.Region{Width: 18, Height: 10}

	// Tree focus: nav.down advances the focused tree node.
	b.active = panelTree
	before := b.tree.focusedID
	if _, handled := b.HandleAction(tui.ActionNavDown); !handled {
		t.Fatalf("nav.down handled=false, want true")
	}
	if b.tree.focusedID == before {
		t.Errorf("nav.down on tree should move the focused node off %q", before)
	}

	// List focus: nav.down advances the list cursor instead of the tree.
	b.active = panelList
	b.tree.focusedID = "db"
	b.refreshList()
	b.list.Select(0)
	treeBefore := b.tree.focusedID
	if _, _ = b.HandleAction(tui.ActionNavDown); b.list.Index() != 1 {
		t.Errorf("nav.down on list should move cursor to 1, got %d", b.list.Index())
	}
	if b.tree.focusedID != treeBefore {
		t.Errorf("nav.down on list must not move the tree focus")
	}
}

func TestBrowser_NavLeftRightNoopInList(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b.active = panelList
	b.tree.focusedID = "services"
	expandedBefore := b.tree.expanded["services"]
	b.HandleAction(tui.ActionNavLeft)
	b.HandleAction(tui.ActionNavRight)
	if b.tree.expanded["services"] != expandedBefore {
		t.Errorf("nav.left/right in the list panel must not mutate the tree")
	}
}

func TestBrowser_FilterAndInspectEntryRouted(t *testing.T) {
	b := newBrowser("pick", pluginTestItems(), DefaultOptions())
	if _, handled := b.HandleAction(tui.ActionFilter); !handled {
		t.Fatalf("filter handled=false, want true")
	}
	if !b.CapturingInput() {
		t.Errorf("filter action should enter capture mode (CapturingInput true)")
	}

	b2 := newBrowser("pick", pluginTestItems(), DefaultOptions())
	b2.active = panelList
	b2.tree.focusedID = "db"
	b2.refreshList()
	b2.list.Select(0)
	if _, handled := b2.HandleAction(tui.ActionInspect); !handled {
		t.Fatalf("inspect handled=false, want true")
	}
	if b2.inspect == nil {
		t.Errorf("inspect action should open the inspect viewport")
	}
}
