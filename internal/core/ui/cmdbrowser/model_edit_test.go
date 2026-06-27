package cmdbrowser

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// TestActionForMode covers the Mode → Action mapping, including the additive
// ModeEdit → ActionEdit case. ModeRun / ModeInspect must be unchanged.
func TestActionForMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode Mode
		want Action
	}{
		{ModeRun, ActionRun},
		{ModeInspect, ActionInspect},
		{ModeEdit, ActionEdit},
		{ModeUnknown, ActionRun}, // defaults to run
	}
	for _, tc := range cases {
		if got := actionForMode(tc.mode); got != tc.want {
			t.Errorf("actionForMode(%v) = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

// TestBrowser_ModeEditSelectReturnsActionEdit asserts that selecting a leaf in
// ModeEdit (the vars browser) commits with ActionEdit and the chosen item index.
// Ported from the deleted *Model TestModeEdit_EnterReturnsActionEdit.
func TestBrowser_ModeEditSelectReturnsActionEdit(t *testing.T) {
	t.Parallel()
	items := []Item{{ID: "vars.db.host"}, {ID: "vars.db.port"}}
	opts := DefaultOptions()
	opts.Mode = ModeEdit
	b := newBrowser("edit", items, opts)
	b.active = panelList
	b.tree.focusedID = "vars.db"
	b.refreshList()
	b.list.Select(1) // vars.db.port

	cmd, handled := b.HandleAction(tui.ActionSelect)
	if !handled {
		t.Fatalf("HandleAction(select) handled=false, want true")
	}
	if cmd == nil {
		t.Fatal("select on a list item in ModeEdit must quit (cmd != nil)")
	}
	if b.result.Action != ActionEdit {
		t.Errorf("Action=%v, want ActionEdit", b.result.Action)
	}
	if b.items[b.result.Idx].ID != "vars.db.port" {
		t.Errorf("Idx=%d (%q), want vars.db.port", b.result.Idx, b.items[b.result.Idx].ID)
	}
}
