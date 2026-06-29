package docstui

import (
	"context"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// newTestBrowser builds a minimal browser for unit tests. It uses a single
// in-memory docs root so no I/O occurs; all behavioral tests work against the
// stub plugin.
func newTestBrowser(t *testing.T) *browser {
	t.Helper()
	// Use the same testFS defined in tree_widget_test.go (same package).
	fsys := &testFS{files: map[string]string{"index.md": "# Test\n"}}
	roots := []docs.DocRoot{{Name: "test", FS: fsys}}
	m, err := NewModel(
		context.Background(),
		roots,
		"en",
		nil,
		nil,
		80, 24,
		"",
		"Test",
		"auto",
	)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	return newBrowser(m)
}

func TestBrowser_PanelsShapeAndWeights(t *testing.T) {
	b := newTestBrowser(t)
	panels := b.Panels()
	if len(panels) != 2 {
		t.Fatalf("Panels() len=%d, want 2", len(panels))
	}
	if panels[0].ID != panelTree {
		t.Errorf("panels[0].ID = %q, want %q", panels[0].ID, panelTree)
	}
	if panels[1].ID != panelViewport {
		t.Errorf("panels[1].ID = %q, want %q", panels[1].ID, panelViewport)
	}
	if panels[0].Weight != 1 {
		t.Errorf("tree weight = %d, want 1", panels[0].Weight)
	}
	if panels[1].Weight != 5 {
		t.Errorf("viewport weight = %d, want 5", panels[1].Weight)
	}
	for _, p := range panels {
		if p.Weight <= 0 {
			t.Errorf("panel %q has non-positive weight %d", p.ID, p.Weight)
		}
	}
}

func TestBrowser_PanelIDsMatch(t *testing.T) {
	// panelTree and panelViewport must equal what Panels() returns.
	b := newTestBrowser(t)
	panels := b.Panels()
	ids := map[tui.PanelID]bool{}
	for _, p := range panels {
		ids[p.ID] = true
	}
	if !ids[panelTree] {
		t.Errorf("panelTree %q not found in Panels()", panelTree)
	}
	if !ids[panelViewport] {
		t.Errorf("panelViewport %q not found in Panels()", panelViewport)
	}
}

func TestBrowser_ResultIsNil(t *testing.T) {
	b := newTestBrowser(t)
	if b.Result() != nil {
		t.Errorf("Result() = %v, want nil", b.Result())
	}
}

func TestBrowser_CapturingInputFalseByDefault(t *testing.T) {
	b := newTestBrowser(t)
	if b.CapturingInput() {
		t.Errorf("CapturingInput() = true at rest, want false")
	}
}

func TestBrowser_InitReturnsNil(t *testing.T) {
	b := newTestBrowser(t)
	if cmd := b.Init(); cmd != nil {
		t.Errorf("Init() = non-nil cmd, want nil stub")
	}
}

func TestBrowser_CloseReturnsNil(t *testing.T) {
	b := newTestBrowser(t)
	if err := b.Close(); err != nil {
		t.Errorf("Close() = %v, want nil stub", err)
	}
}

func TestBrowser_NilTranslatorDefaultsToNop(t *testing.T) {
	b := newTestBrowser(t)
	if b.tr == nil {
		t.Fatalf("browser.tr is nil; newBrowser must default to NopTranslator")
	}
	// A NopTranslator must return the fallback.
	if got := b.tr.T("en", "tui.help.title", "Help"); got != "Help" {
		t.Errorf("NopTranslator.T fallback = %q, want %q", got, "Help")
	}
}

func TestBrowser_ExplicitTranslatorIsPreserved(t *testing.T) {
	fsys := &testFS{files: map[string]string{"index.md": "# Test\n"}}
	roots := []docs.DocRoot{{Name: "test", FS: fsys}}
	m, err := NewModel(
		context.Background(),
		roots,
		"en",
		i18n.NopTranslator{},
		nil,
		80, 24,
		"",
		"Test",
		"auto",
	)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	b := newBrowser(m)
	if b.tr == nil {
		t.Fatalf("browser.tr is nil when explicit translator provided")
	}
}

func TestBrowser_ResizeCachesBody(t *testing.T) {
	b := newTestBrowser(t)
	body := tui.Region{X: 0, Y: 0, Width: 100, Height: 24}
	b.Resize(body)
	if b.body != body {
		t.Errorf("Resize did not cache body: got %+v, want %+v", b.body, body)
	}
}

func TestBrowser_PendingOverlayNone(t *testing.T) {
	b := newTestBrowser(t)
	_, ok := b.PendingOverlay()
	if ok {
		t.Errorf("PendingOverlay() ok=true for stub, want false")
	}
}

func TestBrowser_ActionsNoError(t *testing.T) {
	b := newTestBrowser(t)
	reg := tui.NewRegistry()
	if err := b.Actions(reg); err != nil {
		t.Errorf("Actions() stub error = %v, want nil", err)
	}
}

func TestBrowser_HandleActionReturnsFalse(t *testing.T) {
	b := newTestBrowser(t)
	_, handled := b.HandleAction(tui.ActionNavUp)
	if handled {
		t.Errorf("HandleAction() stub handled=true, want false")
	}
}

func TestBrowser_ViewPanelCachesInnerRegions(t *testing.T) {
	b := newTestBrowser(t)
	treeInner := tui.Region{X: 1, Y: 1, Width: 10, Height: 20}
	vpInner := tui.Region{X: 12, Y: 1, Width: 68, Height: 20}
	b.ViewPanel(panelTree, treeInner)
	b.ViewPanel(panelViewport, vpInner)
	if b.treeInner != treeInner {
		t.Errorf("treeInner = %+v, want %+v", b.treeInner, treeInner)
	}
	if b.viewportInner != vpInner {
		t.Errorf("viewportInner = %+v, want %+v", b.viewportInner, vpInner)
	}
}

func TestBrowser_SatisfiesPlugin(t *testing.T) {
	// Redundant with the var _ assertion; kept as an explicit test so a removed
	// compile-time assert does not silently uncover this.
	b := newTestBrowser(t)
	var _ tui.Plugin = b
}
