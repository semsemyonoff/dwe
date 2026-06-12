package cmdbrowser

import (
	"strings"
	"testing"
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

// TestModeEdit_EnterReturnsActionEdit asserts that selecting a leaf in ModeEdit
// quits with ActionEdit and the chosen item index.
func TestModeEdit_EnterReturnsActionEdit(t *testing.T) {
	t.Parallel()
	items := []Item{{ID: "vars.db.host"}, {ID: "vars.db.port"}}
	opts := DefaultOptions()
	opts.Mode = ModeEdit
	m := newModel("edit", items, opts, 120, 26)
	m.tree.focusedID = "vars.db"
	m.refreshList()
	m.focus = focusRight
	m.list.Select(1)
	_, cmd := m.Update(syntheticKey("enter"))
	if cmd == nil {
		t.Fatal("Enter on list must return a tea.Cmd (Quit)")
	}
	if m.result.Action != ActionEdit {
		t.Errorf("Action=%v, want ActionEdit", m.result.Action)
	}
	if m.items[m.result.Idx].ID != "vars.db.port" {
		t.Errorf("Idx=%d (%q), want vars.db.port", m.result.Idx, m.items[m.result.Idx].ID)
	}
}

// TestModeEdit_FooterAndBreadcrumb asserts the ModeEdit footer relabels Enter
// to "edit", omits the ModeRun-only edit-params / skip-confirm entries, and the
// breadcrumb names rows "var".
func TestModeEdit_FooterAndBreadcrumb(t *testing.T) {
	t.Parallel()
	items := []Item{{ID: "vars.db.host"}, {ID: "vars.db.port"}}
	opts := DefaultOptions()
	opts.Mode = ModeEdit
	m := newModel("edit", items, opts, 120, 26)

	entries := m.helpEntries()
	if len(entries) != 7 {
		t.Fatalf("ModeEdit helpEntries=%d, want 7 (no edit-params/skip-confirm)", len(entries))
	}
	var enterDesc string
	for _, e := range entries {
		switch e.key {
		case "enter":
			enterDesc = e.desc
		case "e", "y":
			t.Errorf("ModeEdit footer must not include key %q", e.key)
		}
	}
	if enterDesc != "edit" {
		t.Errorf("ModeEdit enter desc = %q, want \"edit\"", enterDesc)
	}

	m.tree.focusedID = "vars.db"
	m.refreshList()
	if bc := m.breadcrumb(); !strings.Contains(bc, "var") || strings.Contains(bc, "command") {
		t.Errorf("ModeEdit breadcrumb = %q, want a \"var\" noun and no \"command\"", bc)
	}
}

// TestModeRun_FooterUnchangedByEditMode is the regression gate: adding ModeEdit
// must not alter the command browser. ModeRun still shows the "edit params" and
// "skip confirm" entries, Enter is labelled "select", and the breadcrumb uses
// the "command" noun.
func TestModeRun_FooterUnchangedByEditMode(t *testing.T) {
	t.Parallel()
	items := []Item{{ID: "db.migrate"}, {ID: "db.seed"}}
	m := newModel("pick", items, DefaultOptions(), 120, 26)

	entries := m.helpEntries()
	if len(entries) != 9 {
		t.Fatalf("ModeRun helpEntries=%d, want 9", len(entries))
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.key] = e.desc
	}
	if got["enter"] != "select" {
		t.Errorf("ModeRun enter desc = %q, want \"select\"", got["enter"])
	}
	if got["e"] != "edit params" {
		t.Errorf("ModeRun must keep \"e edit params\", got %q", got["e"])
	}
	if got["y"] != "skip confirm" {
		t.Errorf("ModeRun must keep \"y skip confirm\", got %q", got["y"])
	}

	m.tree.focusedID = "db"
	m.refreshList()
	if bc := m.breadcrumb(); !strings.Contains(bc, "command") {
		t.Errorf("ModeRun breadcrumb = %q, want a \"command\" noun", bc)
	}
}

// TestModeRun_EditParamsStillForcesForm guards the existing ForceParamForm
// binding: pressing `e` in ModeRun selects the item AND sets ForceParamForm.
// ModeEdit's addition must not touch this path.
func TestModeRun_EditParamsStillForcesForm(t *testing.T) {
	t.Parallel()
	items := []Item{{ID: "db.migrate"}, {ID: "db.seed"}}
	m := newModel("pick", items, DefaultOptions(), 120, 26)
	m.tree.focusedID = "db"
	m.refreshList()
	m.focus = focusRight
	m.list.Select(0)
	_, cmd := m.Update(syntheticKey("e"))
	if cmd == nil {
		t.Fatal("EditParams key must return a tea.Cmd (Quit)")
	}
	if m.result.Action != ActionRun {
		t.Errorf("Action=%v, want ActionRun", m.result.Action)
	}
	if !m.result.ForceParamForm {
		t.Error("EditParams must set ForceParamForm")
	}
}

// TestModeEdit_EditParamsKeyInert confirms the ModeRun-only `e` binding does
// nothing in ModeEdit — `e` is a no-op there (it neither forces a form nor
// quits).
func TestModeEdit_EditParamsKeyInert(t *testing.T) {
	t.Parallel()
	items := []Item{{ID: "vars.db.host"}}
	opts := DefaultOptions()
	opts.Mode = ModeEdit
	m := newModel("edit", items, opts, 120, 26)
	m.tree.focusedID = "vars.db"
	m.refreshList()
	m.focus = focusRight
	m.list.Select(0)
	m.Update(syntheticKey("e"))
	// `e` must not trigger the ModeRun edit-params selection in ModeEdit: no
	// result is recorded and ForceParamForm stays false.
	if m.result.Action != ActionUnknown {
		t.Errorf("ModeEdit `e` recorded a result: Action=%v", m.result.Action)
	}
	if m.result.ForceParamForm {
		t.Error("ModeEdit must not set ForceParamForm")
	}
}
