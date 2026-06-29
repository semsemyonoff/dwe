package docstui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
	return newBrowser(context.Background(), m)
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

func TestBrowser_InitNilWatcher(t *testing.T) {
	// With no project root the model has no watcher; Init() returns nil
	// (watcher subscription is the only Init cmd source in the browser path).
	b := newTestBrowser(t)
	if b.Watcher != nil {
		t.Skip("test browser unexpectedly has a watcher")
	}
	if cmd := b.Init(); cmd != nil {
		t.Errorf("Init() with nil watcher = non-nil cmd, want nil")
	}
}

func TestBrowser_InitSubscribesToWatcher(t *testing.T) {
	tmpDir := t.TempDir()

	watcher, err := NewWatcher(t.Context(), tmpDir)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	fsys := &testFS{files: map[string]string{"index.md": "# Test\n"}}
	roots := []docs.DocRoot{{Name: "test", FS: fsys}}
	m, merr := NewModel(context.Background(), roots, "en", nil, nil, 80, 24, "", "Test", "auto")
	if merr != nil {
		_ = watcher.Close()
		t.Fatalf("NewModel: %v", merr)
	}
	m.Watcher = watcher

	b := newBrowser(context.Background(), m)
	defer func() { _ = b.Close() }()

	cmd := b.Init()
	if cmd == nil {
		t.Error("Init() with watcher = nil cmd, want non-nil subscription")
	}
}

func TestBrowser_CloseNilWatcherAndPrefetch(t *testing.T) {
	b := newTestBrowser(t)
	if err := b.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestBrowser_CloseIdempotent(t *testing.T) {
	b := newTestBrowser(t)
	if err := b.Close(); err != nil {
		t.Fatalf("first Close() = %v", err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("second Close() = %v, want nil (idempotent)", err)
	}
}

func TestBrowser_CloseClosesWatcher(t *testing.T) {
	tmpDir := t.TempDir()

	watcher, err := NewWatcher(t.Context(), tmpDir)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	fsys := &testFS{files: map[string]string{"index.md": "# Test\n"}}
	roots := []docs.DocRoot{{Name: "test", FS: fsys}}
	m, merr := NewModel(context.Background(), roots, "en", nil, nil, 80, 24, "", "Test", "auto")
	if merr != nil {
		_ = watcher.Close()
		t.Fatalf("NewModel: %v", merr)
	}
	m.Watcher = watcher

	b := newBrowser(context.Background(), m)
	if err := b.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	// goleak (TestMain) verifies the watcher goroutine exited after Close.
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
	b := newBrowser(context.Background(), m)
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

// --- Task 6: async lifecycle ---

func TestBrowser_FirstLoadFiresOnFirstWindowSizeMsg(t *testing.T) {
	b := newTestBrowser(t)
	initialGen := b.loadGen

	// Send the first non-zero WindowSizeMsg: should trigger the initial load.
	b.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if b.CurrentTopic == nil {
		t.Skip("test browser has no initial topic")
	}
	if b.loadGen <= initialGen {
		t.Errorf("first WindowSizeMsg did not trigger loadTopic: loadGen=%d want >%d", b.loadGen, initialGen)
	}
}

func TestBrowser_SecondWindowSizeMsgDoesNotReload(t *testing.T) {
	b := newTestBrowser(t)

	// First msg: fires initial load.
	b.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	genAfterFirst := b.loadGen

	// Second msg with a different terminal size: must NOT fire another load.
	b.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if b.loadGen != genAfterFirst {
		t.Errorf("second WindowSizeMsg triggered reload: loadGen changed from %d to %d", genAfterFirst, b.loadGen)
	}
}

func TestBrowser_ZeroWidthWindowSizeMsgDoesNotLoad(t *testing.T) {
	b := newTestBrowser(t)
	initialGen := b.loadGen

	// Zero-width msg must be ignored (firstLoadDone stays false).
	b.Update(tea.WindowSizeMsg{Width: 0, Height: 24})
	if b.loadGen != initialGen {
		t.Errorf("zero-width WindowSizeMsg triggered loadTopic")
	}
	if b.firstLoadDone {
		t.Errorf("firstLoadDone set on zero-width msg; should still be false")
	}
}

func TestBrowser_WindowSizeMsgUpdatesContentWidth(t *testing.T) {
	b := newTestBrowser(t)
	b.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	want := viewportPanelInnerWidth(80)
	if b.ContentWidth != want {
		t.Errorf("ContentWidth after WindowSizeMsg: got %d, want %d", b.ContentWidth, want)
	}
}

func TestBrowser_ViewportInnerWidthPinAtBuckets(t *testing.T) {
	// Pin test: the plugin's computed viewport inner width must equal the
	// inner.Width the Frame actually passes to ViewPanel(panelViewport, inner).
	// The Frame uses layoutPanels(outer, weights)+contentRegion; we replicate
	// that math here to detect any future drift.
	buckets := []int{60, 79, 80, 99, 100}
	for _, termW := range buckets {
		t.Run(fmt.Sprintf("termW=%d", termW), func(t *testing.T) {
			// Replicate layoutPanels({0,0,termW,H}, {1,5}) + contentRegion.
			const (
				treeWeight = 1
				vpWeight   = 5
				total      = treeWeight + vpWeight
				chrome     = 4 // 2*(border:1+hPad:1)
			)
			treeOuter := termW * treeWeight / total
			vpOuter := termW - treeOuter // last-panel remainder
			wantInnerW := max(vpOuter-chrome, 0)

			got := viewportPanelInnerWidth(termW)
			if got != wantInnerW {
				t.Errorf("termW=%d: viewportPanelInnerWidth=%d, want %d (layoutPanels replica)", termW, got, wantInnerW)
			}

			// Also confirm that after a WindowSizeMsg the plugin tracks the same width.
			b := newTestBrowser(t)
			b.Update(tea.WindowSizeMsg{Width: termW, Height: 24})
			if b.ContentWidth != wantInnerW {
				t.Errorf("termW=%d: ContentWidth=%d after WindowSizeMsg, want %d", termW, b.ContentWidth, wantInnerW)
			}
		})
	}
}

func TestBrowser_FileChangedMsgMatchingPathTriggerReload(t *testing.T) {
	tmpDir := t.TempDir()
	docsDir := filepath.Join(tmpDir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	fsys := &testFS{files: map[string]string{"test.md": "# Test\n"}}
	roots := []docs.DocRoot{{Name: "test", FS: fsys}}
	m, err := NewModel(context.Background(), roots, "en", nil, nil, 80, 24, tmpDir, "Test", "auto")
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	b := newBrowser(context.Background(), m)
	// NewModel creates a watcher for tmpDir/docs (directory exists); Close tears it down.
	defer func() { _ = b.Close() }()

	if b.CurrentTopic == nil || b.CurrentTopic.Node == nil {
		t.Skip("browser has no current topic with a node")
	}

	before := b.loadGen
	// Construct the absolute path the watcher would report for the current topic.
	absPath := filepath.Join(docsDir, b.CurrentTopic.Node.Path)
	b.Update(FileChangedMsg{Path: absPath})

	if b.loadGen <= before {
		t.Errorf("FileChangedMsg with matching path did not trigger reload: loadGen=%d want >%d", b.loadGen, before)
	}
}

func TestBrowser_FileChangedMsgNonMatchingPathNoReload(t *testing.T) {
	tmpDir := t.TempDir()

	fsys := &testFS{files: map[string]string{"test.md": "# Test\n"}}
	roots := []docs.DocRoot{{Name: "test", FS: fsys}}
	m, err := NewModel(context.Background(), roots, "en", nil, nil, 80, 24, tmpDir, "Test", "auto")
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	b := newBrowser(context.Background(), m)

	before := b.loadGen
	// Send a path that does NOT match the current topic.
	b.Update(FileChangedMsg{Path: filepath.Join(tmpDir, "docs", "other.md")})
	if b.loadGen != before {
		t.Errorf("FileChangedMsg with non-matching path triggered reload: loadGen changed from %d to %d", before, b.loadGen)
	}
}

func TestBrowser_ProgressMsgStaleGenerationDropped(t *testing.T) {
	b := newTestBrowser(t)

	// Set up a prefetch with generation > 0.
	ch := make(chan ProgressMsg, 10)
	b.prefetchChan = ch
	b.Prefetch = NewPrefetch(t.Context(), nil, ch)
	defer b.Prefetch.Close()

	// Advance to generation 3.
	b.Prefetch.BeginTopic()
	b.Prefetch.BeginTopic()
	b.Prefetch.BeginTopic()

	// PrefetchProgress starts empty.
	initialProgress := b.PrefetchProgress

	// Send a stale ProgressMsg (generation 1 != current 3).
	b.Update(ProgressMsg{Rendered: 5, Total: 10, Generation: 1})

	// PrefetchProgress must remain unchanged (stale message dropped).
	if b.PrefetchProgress.Rendered != initialProgress.Rendered {
		t.Errorf("stale ProgressMsg updated PrefetchProgress.Rendered: got %d, want %d",
			b.PrefetchProgress.Rendered, initialProgress.Rendered)
	}
}

func TestBrowser_ProgressMsgCurrentGenerationApplied(t *testing.T) {
	b := newTestBrowser(t)

	ch := make(chan ProgressMsg, 10)
	b.prefetchChan = ch
	b.Prefetch = NewPrefetch(t.Context(), nil, ch)
	defer b.Prefetch.Close()

	currentGen := b.Prefetch.Generation()

	// Send a current-generation ProgressMsg.
	b.Update(ProgressMsg{Rendered: 2, Total: 5, Generation: currentGen})

	if b.PrefetchProgress.Rendered != 2 {
		t.Errorf("current-gen ProgressMsg not applied: Rendered=%d, want 2", b.PrefetchProgress.Rendered)
	}
}

func TestBrowser_FocusChangedMsgUpdatesActive(t *testing.T) {
	b := newTestBrowser(t)
	if b.active != panelTree {
		t.Fatalf("initial active panel = %q, want %q", b.active, panelTree)
	}
	b.Update(tui.FocusChangedMsg{Panel: panelViewport})
	if b.active != panelViewport {
		t.Errorf("FocusChangedMsg did not update active: got %q, want %q", b.active, panelViewport)
	}
	b.Update(tui.FocusChangedMsg{Panel: panelTree})
	if b.active != panelTree {
		t.Errorf("FocusChangedMsg back to tree: got %q, want %q", b.active, panelTree)
	}
}
