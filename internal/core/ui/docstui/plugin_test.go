package docstui

import (
	"context"
	"strings"
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

func TestBrowser_HandleActionKnownActionReturnsTrue(t *testing.T) {
	b := newTestBrowser(t)
	_, handled := b.HandleAction(tui.ActionNavUp)
	if !handled {
		t.Errorf("HandleAction(ActionNavUp) handled=false, want true")
	}
}

func TestBrowser_HandleActionUnknownReturnsFalse(t *testing.T) {
	b := newTestBrowser(t)
	_, handled := b.HandleAction(tui.Action("no.such.action"))
	if handled {
		t.Errorf("HandleAction(unknown) handled=true, want false")
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

// --- Task 4: viewport panel render ---

// tallContent returns a long string that exceeds any normal panel height so
// the scrollbar logic activates. Each line is a fixed-width prose line.
func tallContent(lines int) string {
	var sb strings.Builder
	for range lines {
		sb.WriteString("Lorem ipsum dolor sit amet, consectetur adipiscing elit.\n")
	}
	return sb.String()
}

func TestBrowser_ViewPanelViewport_SizesViewport(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent("Hello viewport\n")
	inner := tui.Region{X: 0, Y: 0, Width: 60, Height: 10}
	result := b.ViewPanel(panelViewport, inner)
	if b.viewportInner != inner {
		t.Errorf("viewportInner not cached: got %+v, want %+v", b.viewportInner, inner)
	}
	// Result should be non-empty (has at least one content line).
	if result == "" {
		t.Error("ViewPanel(viewport) returned empty string for non-empty content")
	}
	// Viewport display height should match the inner height.
	if b.Viewport.VisibleHeight() != inner.Height {
		t.Errorf("Viewport.VisibleHeight() = %d, want %d", b.Viewport.VisibleHeight(), inner.Height)
	}
}

func TestBrowser_ViewPanelViewport_ScrollbarPresentForTallContent(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent(tallContent(200))
	inner := tui.Region{Width: 60, Height: 10}
	result := b.ViewPanel(panelViewport, inner)
	if b.Viewport.TotalLines() <= b.Viewport.VisibleHeight() {
		t.Skip("content not tall enough to trigger scrollbar")
	}
	if !strings.Contains(result, scrollbarThumbGlyph) {
		t.Error("expected scrollbar thumb in viewport panel for tall content")
	}
}

func TestBrowser_ViewPanelViewport_ScrollbarAbsentForShortContent(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent("One line.\n")
	inner := tui.Region{Width: 60, Height: 10}
	result := b.ViewPanel(panelViewport, inner)
	if strings.Contains(result, scrollbarThumbGlyph) {
		t.Error("did not expect scrollbar thumb when content fits in panel")
	}
}

func TestBrowser_ViewPanelViewport_ResizePreservesYOffset(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent(tallContent(200))
	inner := tui.Region{Width: 60, Height: 10}
	b.ViewPanel(panelViewport, inner)

	// Scroll to a non-zero offset.
	b.Viewport.ScrollToLine(30)
	wantOffset := b.Viewport.YOffset()
	wantLoadGen := b.loadGen

	// Resize: call ViewPanel with a different inner width.
	inner2 := tui.Region{Width: 80, Height: 10}
	b.ViewPanel(panelViewport, inner2)

	// YOffset must be preserved (resize only changes display window, not content).
	if got := b.Viewport.YOffset(); got != wantOffset {
		t.Errorf("resize changed YOffset: got %d, want %d", got, wantOffset)
	}
	// loadGen must not have changed (no topic reload triggered by resize).
	if b.loadGen != wantLoadGen {
		t.Errorf("resize triggered a content reload: loadGen changed from %d to %d", wantLoadGen, b.loadGen)
	}
}

func TestBrowser_ViewPanelViewport_DiagramPlaceholderVisible(t *testing.T) {
	b := newTestBrowser(t)
	// Simulate content with an already-inlined diagram placeholder (as set by
	// applyTopicLoaded → inlineDiagrams → SetContent).
	placeholder := "<📊 Diagram 1/1 — rendering…>"
	b.Viewport.SetContent("Before\n" + placeholder + "\nAfter\n")
	inner := tui.Region{Width: 60, Height: 10}
	result := b.ViewPanel(panelViewport, inner)
	if !strings.Contains(result, "📊") {
		t.Errorf("diagram placeholder not found in viewport panel output; got: %q", result)
	}
}

func TestBrowser_ViewPanelViewport_HeadingLinesIntact(t *testing.T) {
	b := newTestBrowser(t)
	// Pre-set heading-line indices as applyTopicLoaded would.
	b.currentHeadingLines = []int{5, 20, 40}
	inner := tui.Region{Width: 60, Height: 10}
	b.ViewPanel(panelViewport, inner)
	// ViewPanel must not touch currentHeadingLines.
	if len(b.currentHeadingLines) != 3 ||
		b.currentHeadingLines[0] != 5 ||
		b.currentHeadingLines[1] != 20 ||
		b.currentHeadingLines[2] != 40 {
		t.Errorf("ViewPanel mutated currentHeadingLines: %v", b.currentHeadingLines)
	}
}

func TestBrowser_ViewPanelViewport_NilViewport(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport = nil
	inner := tui.Region{Width: 60, Height: 10}
	if got := b.ViewPanel(panelViewport, inner); got != "" {
		t.Errorf("ViewPanel with nil Viewport = %q, want empty string", got)
	}
}
