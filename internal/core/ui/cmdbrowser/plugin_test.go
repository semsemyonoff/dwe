package cmdbrowser

import (
	"strings"
	"testing"

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
