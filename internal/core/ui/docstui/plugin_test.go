package docstui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

func TestBrowser_ProgressMsgDebouncesDiagramRefresh(t *testing.T) {
	b := newTestBrowser(t)
	ch := make(chan ProgressMsg, 10)
	b.prefetchChan = ch
	b.Prefetch = NewPrefetch(t.Context(), nil, ch)
	defer b.Prefetch.Close()
	gen := b.Prefetch.Generation()

	// First ProgressMsg arms the debounce — it does NOT SetContent synchronously
	// (the per-completion storm that hitched scrolling on diagram docs).
	cmd := b.Update(ProgressMsg{Rendered: 1, Total: 3, Generation: gen})
	if !b.diagramRefreshPending {
		t.Error("ProgressMsg did not mark a diagram refresh pending")
	}
	if !b.diagramRefreshTickInFlight {
		t.Error("first ProgressMsg did not arm the debounce tick")
	}
	if cmd == nil {
		t.Error("ProgressMsg returned nil cmd; want the refresh tick batched with the next wait")
	}

	// A second ProgressMsg rides the in-flight tick (coalescing) — no panic, still pending.
	b.Update(ProgressMsg{Rendered: 2, Total: 3, Generation: gen})
	if !b.diagramRefreshPending {
		t.Error("second ProgressMsg cleared the pending refresh")
	}

	// The tick applies once and clears the debounce when no scroll is in flight.
	b.applyDiagramRefresh()
	if b.diagramRefreshPending || b.diagramRefreshTickInFlight {
		t.Error("diagram refresh did not clear after applying")
	}
}

func TestBrowser_DiagramRefreshDefersDuringScroll(t *testing.T) {
	b := newTestBrowser(t)
	b.diagramRefreshPending = true
	b.diagramRefreshTickInFlight = true
	b.wheel.tickInFlight = true // a wheel burst is active

	// While a scroll is in flight the refresh re-arms instead of touching the
	// viewport (background diagrams must not hitch the foreground scroll).
	cmd := b.applyDiagramRefresh()
	if cmd == nil {
		t.Error("diagram refresh during a scroll did not re-arm (defer)")
	}
	if !b.diagramRefreshPending {
		t.Error("diagram refresh cleared the pending flag mid-scroll; want it deferred")
	}
	if !b.diagramRefreshTickInFlight {
		t.Error("deferred diagram refresh did not keep a tick in flight")
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

// --- Task 7: inline filter capture ---

// newMultiFileBrowser builds a browser backed by multiple file nodes so the
// filter has a non-trivial visible set to narrow down.
func newMultiFileBrowser(t *testing.T) *browser {
	t.Helper()
	files := map[string]string{
		"alpha.md": "# Alpha\n",
		"beta.md":  "# Beta\n",
		"gamma.md": "# Gamma\n",
		"delta.md": "# Delta\n",
	}
	roots := []docs.DocRoot{{Name: "dwe", FS: &testFS{files: files}}}
	return newTestBrowserWithRoots(t, roots)
}

func TestBrowser_CapturingInputFalseAtRest(t *testing.T) {
	b := newTestBrowser(t)
	if b.CapturingInput() {
		t.Error("CapturingInput() = true at rest, want false")
	}
}

func TestBrowser_EnterFilterSetsCapturing(t *testing.T) {
	b := newTestBrowser(t)
	b.enterFilter()
	if !b.CapturingInput() {
		t.Error("CapturingInput() = false after enterFilter, want true")
	}
}

func TestBrowser_ExitFilterClearsCapturing(t *testing.T) {
	b := newTestBrowser(t)
	b.enterFilter()
	b.exitFilter()
	if b.CapturingInput() {
		t.Error("CapturingInput() = true after exitFilter, want false")
	}
}

func TestBrowser_CommitFilterClearsCapturing(t *testing.T) {
	b := newTestBrowser(t)
	b.enterFilter()
	b.commitFilter()
	if b.CapturingInput() {
		t.Error("CapturingInput() = true after commitFilter, want false")
	}
}

func TestBrowser_FilterEditNarrowsVisibleSet(t *testing.T) {
	b := newMultiFileBrowser(t)
	before := len(b.Tree.VisibleNodes())
	if before == 0 {
		t.Fatal("browser has no visible nodes before filter")
	}

	b.enterFilter()
	// Type "al" — should match "alpha" only (and potentially "delta" which
	// contains "al"). Check that the set shrank (not all items shown).
	b.Update(tea.KeyPressMsg{Text: "al"})

	after := len(b.Tree.VisibleNodes())
	if after >= before {
		t.Errorf("visible set after typing 'al': got %d, want < %d", after, before)
	}
}

func TestBrowser_FilterBackspaceExpands(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.enterFilter()

	// Type a query that narrows to a small set.
	b.Update(tea.KeyPressMsg{Text: "alpha"})
	narrow := len(b.Tree.VisibleNodes())

	// Backspace erases one rune — "alph" — should widen the visible set.
	b.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	wider := len(b.Tree.VisibleNodes())

	if wider < narrow {
		t.Errorf("backspace shrunk visible set: was %d, got %d", narrow, wider)
	}
}

func TestBrowser_FilterEscRestoresCursor(t *testing.T) {
	b := newMultiFileBrowser(t)
	originalCursor := b.Tree.Cursor()
	if originalCursor == nil {
		t.Fatal("no initial cursor")
	}

	b.enterFilter()
	// Move to a different node inside filter mode.
	b.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	// Now exit via Esc — cursor should return to originalCursor.
	b.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if b.Tree.Cursor() != originalCursor {
		t.Errorf("exitFilter did not restore cursor: got %v, want %v",
			b.Tree.Cursor(), originalCursor)
	}
}

// TestBrowser_FilterEscRestoresCurrentTopic guards that cancelling the filter
// restores CurrentTopic (and the viewport) to the pre-filter selection, not the
// last previewed topic — otherwise a later CurrentTopic-based action (locale
// switch) would operate on the wrong topic.
func TestBrowser_FilterEscRestoresCurrentTopic(t *testing.T) {
	b := newMultiFileBrowser(t)
	originalCursor := b.Tree.Cursor()
	if originalCursor == nil {
		t.Fatal("no initial cursor")
	}
	// Establish the pre-filter loaded topic.
	b.CurrentTopic = originalCursor

	b.enterFilter()
	// Preview a different node inside filter mode (afterTreeMove sets CurrentTopic).
	b.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if b.CurrentTopic == originalCursor {
		t.Fatal("precondition: filter nav did not move CurrentTopic off the original")
	}
	// Cancel — CurrentTopic must snap back to the restored cursor.
	b.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if b.CurrentTopic != originalCursor {
		t.Errorf("exitFilter left CurrentTopic on the previewed topic: got %v, want %v",
			b.CurrentTopic, originalCursor)
	}
}

func TestBrowser_FilterEscClearsFilter(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.enterFilter()
	b.Update(tea.KeyPressMsg{Text: "alpha"})
	b.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if b.CapturingInput() {
		t.Error("CapturingInput() still true after Esc, want false")
	}
	// After cancel, all nodes should be visible (filter cleared).
	if b.Filter.Query != "" {
		t.Errorf("filter query non-empty after Esc: %q", b.Filter.Query)
	}
}

func TestBrowser_FilterEnterCommitsAndExpandsAncestors(t *testing.T) {
	// Build a tree with a nested node to check ancestor expansion.
	files := map[string]string{
		"parent/index.md": "# Parent\n",
		"parent/child.md": "# Child\n",
	}
	roots := []docs.DocRoot{{Name: "dwe", FS: &testFS{files: files}}}
	b := newTestBrowserWithRoots(t, roots)

	// Find a node inside a collapsed directory.
	var child *TreeNode
	for _, n := range b.Tree.VisibleNodes() {
		if n.Node != nil && n.Node.IsDir {
			// Expand so we can access children.
			b.Tree.SetExpanded(n, true)
			b.Tree.recomputeVisible()
			break
		}
	}
	for _, n := range b.Tree.VisibleNodes() {
		if n.Node != nil && !n.Node.IsDir && n.Parent != nil && n.Parent != b.Tree.root {
			child = n
			break
		}
	}
	if child == nil {
		t.Skip("no nested child node found in test tree")
	}

	// Now collapse the parent so the child is hidden.
	if child.Parent != nil {
		b.Tree.SetExpanded(child.Parent, false)
		b.Tree.recomputeVisible()
	}

	b.enterFilter()
	// Set cursor to the child via direct placement (filters show all).
	b.Tree.SetCursor(child)
	// Commit — should expand ancestors so child is visible after filter cleared.
	b.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if b.CapturingInput() {
		t.Error("CapturingInput() still true after Enter commit, want false")
	}

	// Child must be in the visible set (ancestors were expanded on commit).
	if !slices.Contains(b.Tree.VisibleNodes(), child) {
		t.Error("child not in visible set after commit — ancestors not expanded")
	}

	// Commit must also load the picked topic into the viewport (selectCursor),
	// not just expand ancestors — otherwise the viewport keeps showing the
	// previously loaded topic until the next arrow keypress.
	if b.CurrentTopic != child {
		t.Errorf("CurrentTopic after commit = %v, want picked child node", b.CurrentTopic)
	}
}

func TestBrowser_FilterVisibleSetMatchesApplyFilter(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.enterFilter()

	// Type "beta".
	b.Update(tea.KeyPressMsg{Text: "beta"})

	// Build what ApplyFilter would produce directly.
	ref := NewTreeFilter()
	ref.Open()
	ref.Append('b')
	ref.Append('e')
	ref.Append('t')
	ref.Append('a')
	b.Tree.ApplyFilter(ref)
	want := len(b.Tree.VisibleNodes())

	// Re-apply the browser's own filter state and compare counts.
	b.Tree.ApplyFilter(b.Filter)
	got := len(b.Tree.VisibleNodes())

	if got != want {
		t.Errorf("filter visible count = %d, want %d (from ApplyFilter ref)", got, want)
	}
}

func TestBrowser_ActionFilterCallsEnterFilter(t *testing.T) {
	b := newTestBrowser(t)
	_, handled := b.HandleAction(tui.ActionFilter)
	if !handled {
		t.Error("HandleAction(ActionFilter) returned handled=false, want true")
	}
	if !b.CapturingInput() {
		t.Error("HandleAction(ActionFilter) did not enter filter mode (CapturingInput still false)")
	}
}

func TestBrowser_RenderTreeFilteredShowsHeader(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.enterFilter()
	b.Filter.Append('a')
	b.Filter.Append('l')
	b.Tree.ApplyFilter(b.Filter)

	inner := tui.Region{Width: 30, Height: 5}
	out := b.renderTreeFiltered(inner)
	if !strings.Contains(out, "/") {
		t.Error("renderTreeFiltered output missing '/' prompt character")
	}
	if !strings.Contains(out, "match") {
		t.Error("renderTreeFiltered output missing match count")
	}
}

func TestBrowser_RenderTreeFilteredZeroHeight(t *testing.T) {
	b := newTestBrowser(t)
	b.enterFilter()
	out := b.renderTreeFiltered(tui.Region{Width: 30, Height: 0})
	if out != "" {
		t.Errorf("renderTreeFiltered with height=0 returned %q, want empty", out)
	}
}

func TestBrowser_RenderTreeFilteredOneRow(t *testing.T) {
	b := newTestBrowser(t)
	b.enterFilter()
	// Height=1 should show header only (no tree body).
	out := b.renderTreeFiltered(tui.Region{Width: 30, Height: 1})
	if strings.Contains(out, "\n") {
		t.Errorf("renderTreeFiltered with height=1 returned multi-line: %q", out)
	}
}

func TestBrowser_ViewPanelShowsFilterHeaderWhenActive(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.enterFilter()

	inner := tui.Region{Width: 30, Height: 5}
	out := b.ViewPanel(panelTree, inner)
	if !strings.Contains(out, "/") {
		t.Errorf("ViewPanel(tree) in filter mode missing '/' header; got: %q", out)
	}
}

func TestBrowser_ViewPanelNormalTreeWhenNotFiltering(t *testing.T) {
	b := newTestBrowser(t)
	inner := tui.Region{Width: 30, Height: 5}
	out := b.ViewPanel(panelTree, inner)
	// Normal tree should NOT contain the filter prompt character as a prefix.
	// (It's fine for node labels to contain '/' — just check filter is off.)
	if b.CapturingInput() {
		t.Error("CapturingInput() should be false when not filtering")
	}
	_ = out // non-empty is asserted in existing TestTreeViewPanel_RendersRows
}

// --- Task 8: mouse wiring (PanelClick / FocusChanged) ---

func TestBrowser_FocusChangedMsgSwitchesNavRouting(t *testing.T) {
	// After switching focus to the viewport, nav actions should route there
	// (viewport scroll) rather than to the tree (cursor move). We verify by
	// checking that HandleAction(ActionNavUp) on the viewport calls ScrollUp
	// rather than MoveUp on the tree. The simplest proxy is checking the active
	// panel field after the FocusChangedMsg — the routing logic in HandleAction
	// reads b.active directly (see actions.go navLine).
	b := newTestBrowser(t)
	if b.active != panelTree {
		t.Fatalf("initial active = %q, want tree", b.active)
	}

	// Switch focus to viewport.
	b.Update(tui.FocusChangedMsg{Panel: panelViewport})
	if b.active != panelViewport {
		t.Errorf("after FocusChangedMsg(viewport): active=%q, want %q", b.active, panelViewport)
	}

	// Switch back to tree.
	b.Update(tui.FocusChangedMsg{Panel: panelTree})
	if b.active != panelTree {
		t.Errorf("after FocusChangedMsg(tree): active=%q, want %q", b.active, panelTree)
	}
}

func TestBrowser_PanelClickTreeMovesCursor(t *testing.T) {
	b := newMultiFileBrowser(t)
	// Render the tree at height=5 so topIdx is calibrated and visible is populated.
	b.ViewPanel(panelTree, tui.Region{Width: 30, Height: 5})

	visible := b.Tree.VisibleNodes()
	if len(visible) < 2 {
		t.Skip("need at least 2 visible nodes for click test")
	}

	// Click on row 1 (the second visible row).
	before := b.Tree.Cursor()
	b.Update(tui.PanelClickMsg{Panel: panelTree, X: 0, Y: 1})

	after := b.Tree.Cursor()
	if after == before {
		t.Error("PanelClickMsg on tree row 1 did not move cursor")
	}
	// The cursor should now be the second visible node.
	if after != visible[1] {
		t.Errorf("PanelClickMsg row=1: cursor=%v, want visible[1]=%v", after, visible[1])
	}
}

func TestBrowser_PanelClickTreeLoadsTopic(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.ViewPanel(panelTree, tui.Region{Width: 30, Height: 5})

	visible := b.Tree.VisibleNodes()
	if len(visible) < 2 {
		t.Fatal("fixture must expose at least 2 visible tree rows")
	}

	// Clicking a tree row must follow the same path as keyboard nav
	// (afterTreeMove → selectCursor): it repositions the cursor AND requests a
	// topic load. Before the fix the click moved the highlight but dropped the
	// async load Cmd, leaving the viewport on the previously loaded topic until
	// the next key press. selectCursor syncs CurrentTopic synchronously, so a
	// CurrentTopic that tracks the clicked row proves the load path ran.
	cmd := b.Update(tui.PanelClickMsg{Panel: panelTree, X: 0, Y: 1})

	if b.CurrentTopic != visible[1] {
		t.Errorf("click did not sync CurrentTopic to the clicked row: got %v, want %v", b.CurrentTopic, visible[1])
	}
	if cmd == nil {
		t.Error("click on an unloaded tree row returned nil Cmd; expected the async topic-load Cmd")
	}
}

func TestBrowser_PanelClickTreePastLastRowIsNoop(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.ViewPanel(panelTree, tui.Region{Width: 30, Height: 20})

	visible := b.Tree.VisibleNodes()
	if len(visible) == 0 {
		t.Skip("no visible nodes")
	}

	// Set cursor to the first node so we have a known starting position.
	b.Tree.eng.FocusRow(0)
	before := b.Tree.Cursor()

	// Click at row = len(visible) (one past the last row) — must be a no-op.
	b.Update(tui.PanelClickMsg{Panel: panelTree, X: 0, Y: len(visible)})
	if b.Tree.Cursor() != before {
		t.Errorf("PanelClickMsg past last row moved cursor: got %v, want %v", b.Tree.Cursor(), before)
	}
}

func TestBrowser_PanelClickViewportIsNoop(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent("Line 1\nLine 2\nLine 3\n")
	b.ViewPanel(panelViewport, tui.Region{Width: 60, Height: 10})

	before := b.Viewport.YOffset()
	// Click in viewport — no per-row click targets today, should be a no-op.
	b.Update(tui.PanelClickMsg{Panel: panelViewport, X: 5, Y: 1})
	if b.Viewport.YOffset() != before {
		t.Errorf("PanelClickMsg on viewport changed YOffset: got %d, want %d", b.Viewport.YOffset(), before)
	}
}

func TestBrowser_PanelClickWhileFilteringIsNoop(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.ViewPanel(panelTree, tui.Region{Width: 30, Height: 5})

	b.enterFilter()
	// Cursor position before the click.
	before := b.Tree.Cursor()

	// While filter owns input, PanelClickMsg should be dropped.
	b.Update(tui.PanelClickMsg{Panel: panelTree, X: 0, Y: 1})
	if b.Tree.Cursor() != before {
		t.Errorf("PanelClickMsg while filter active moved cursor; want no-op")
	}
}

// --- Task 9: StatusContext + i18n keys ---

func TestBrowser_StatusContextEmptyWhenNilStatusBar(t *testing.T) {
	b := newTestBrowser(t)
	b.StatusBar = nil
	if got := b.StatusContext(); got != "" {
		t.Errorf("StatusContext() with nil StatusBar = %q, want empty string", got)
	}
}

func TestBrowser_StatusContextEmptyWhenNilModel(t *testing.T) {
	b := &browser{} // zero value: Model is nil
	if got := b.StatusContext(); got != "" {
		t.Errorf("StatusContext() with nil Model = %q, want empty string", got)
	}
}

func TestBrowser_StatusContextPath(t *testing.T) {
	b := newTestBrowser(t)
	b.StatusBar.SetPath("reference/config/workspace.md")
	got := b.StatusContext()
	if !strings.Contains(got, "reference/config/workspace.md") {
		t.Errorf("StatusContext() = %q, expected to contain path", got)
	}
}

func TestBrowser_StatusContextProgress(t *testing.T) {
	b := newTestBrowser(t)
	b.StatusBar.SetPath("some/path.md")
	// 7 diagrams in the topic; the pool has rendered 3 → the "⏳ 3/7" prefetch
	// suffix shows alongside the "📊 1/7" focused indicator.
	b.DiagramState = NewDiagramState(makeDiagrams(7))
	b.StatusBar.SetProgress(3)
	got := b.StatusContext()
	if !strings.Contains(got, "3/7") {
		t.Errorf("StatusContext() = %q, expected to contain prefetch progress '3/7'", got)
	}
	if !strings.Contains(got, "📊") {
		t.Errorf("StatusContext() = %q, expected to contain diagram icon", got)
	}
}

func TestBrowser_StatusContextFocusedDiagramWhenDisabled(t *testing.T) {
	b := newTestBrowser(t)
	b.StatusBar.SetPath("some/path.md")
	// Rendering disabled → no prefetch, rendered stays 0. The focused-diagram
	// indicator must still show so the user knows which source `y` copies.
	b.DiagramState = NewDiagramState(makeDiagrams(4))
	b.DiagramState.Current = 2
	got := b.StatusContext()
	if !strings.Contains(got, "📊 3/4") {
		t.Errorf("StatusContext() = %q, expected focused indicator '📊 3/4'", got)
	}
	// With nothing rendered there is no "⏳" prefetch suffix.
	if strings.Contains(got, "⏳") {
		t.Errorf("StatusContext() = %q, unexpected prefetch suffix with nothing rendered", got)
	}
}

func TestBrowser_StatusContextLang(t *testing.T) {
	b := newTestBrowser(t)
	b.StatusBar.SetLanguage("ru")
	got := b.StatusContext()
	if !strings.Contains(got, "[ru]") {
		t.Errorf("StatusContext() = %q, expected to contain '[ru]'", got)
	}
}

func TestBrowser_StatusContextFull(t *testing.T) {
	b := newTestBrowser(t)
	b.StatusBar.SetPath("guides/getting-started.md")
	b.DiagramState = NewDiagramState(makeDiagrams(5))
	b.StatusBar.SetProgress(2)
	b.StatusBar.SetLanguage("en")
	got := b.StatusContext()
	for _, want := range []string{"guides/getting-started.md", "2/5", "📊", "[en]"} {
		if !strings.Contains(got, want) {
			t.Errorf("StatusContext() = %q, missing %q", got, want)
		}
	}
}

func TestBrowser_BuildHelp_ContainsDiagramsSection(t *testing.T) {
	b := newTestBrowser(t)
	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	ov, err := tui.BuildHelp(b, store, "en", 100, 40)
	if err != nil {
		t.Fatalf("BuildHelp: %v", err)
	}
	plain := stripANSI(ov.Content)
	if !strings.Contains(plain, "Diagrams") {
		t.Errorf("BuildHelp output missing 'Diagrams' section:\n%s", plain)
	}
}

func TestBrowser_BuildHelp_ContainsLocalesSection(t *testing.T) {
	b := newTestBrowser(t)
	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	ov, err := tui.BuildHelp(b, store, "en", 100, 40)
	if err != nil {
		t.Fatalf("BuildHelp: %v", err)
	}
	plain := stripANSI(ov.Content)
	if !strings.Contains(plain, "Locales") {
		t.Errorf("BuildHelp output missing 'Locales' section:\n%s", plain)
	}
}

func TestBrowser_BuildHelp_ContainsNavigationSection(t *testing.T) {
	b := newTestBrowser(t)
	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	ov, err := tui.BuildHelp(b, store, "en", 100, 40)
	if err != nil {
		t.Fatalf("BuildHelp: %v", err)
	}
	plain := stripANSI(ov.Content)
	if !strings.Contains(plain, "Navigation") {
		t.Errorf("BuildHelp output missing 'Navigation' section:\n%s", plain)
	}
}

func TestBrowser_BuildHelp_DiagramActionsPresent(t *testing.T) {
	b := newTestBrowser(t)
	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	ov, err := tui.BuildHelp(b, store, "en", 100, 40)
	if err != nil {
		t.Fatalf("BuildHelp: %v", err)
	}
	plain := stripANSI(ov.Content)
	for _, want := range []string{"Previous diagram", "Next diagram", "Open diagram", "Copy diagram source"} {
		if !strings.Contains(plain, want) {
			t.Errorf("BuildHelp output missing diagram action %q:\n%s", want, plain)
		}
	}
}

func TestBrowser_BuildHelp_LocaleActionsPresent(t *testing.T) {
	b := newTestBrowser(t)
	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	ov, err := tui.BuildHelp(b, store, "en", 100, 40)
	if err != nil {
		t.Fatalf("BuildHelp: %v", err)
	}
	plain := stripANSI(ov.Content)
	for _, want := range []string{"Cycle language", "Show English"} {
		if !strings.Contains(plain, want) {
			t.Errorf("BuildHelp output missing locale action %q:\n%s", want, plain)
		}
	}
}

func TestBrowser_BuildHelp_NopTranslatorFallsBackToEnglish(t *testing.T) {
	b := newTestBrowser(t)
	// NopTranslator always returns the fallback (English Binding.Desc values).
	ov, err := tui.BuildHelp(b, i18n.NopTranslator{}, "en", 100, 40)
	if err != nil {
		t.Fatalf("BuildHelp with NopTranslator: %v", err)
	}
	plain := stripANSI(ov.Content)
	// Section labels fall back to English names (the fallback arg to T).
	if !strings.Contains(plain, "Diagrams") {
		t.Errorf("BuildHelp/NopTranslator missing 'Diagrams' fallback:\n%s", plain)
	}
	if !strings.Contains(plain, "Locales") {
		t.Errorf("BuildHelp/NopTranslator missing 'Locales' fallback:\n%s", plain)
	}
}

// --- Task 4 wheel: WheelMsg routing ---

func TestBrowser_WheelMsgViewportScrollsDown(t *testing.T) {
	b := newTestBrowser(t)
	// Load tall content so the offset can actually advance.
	b.Viewport.SetContent(tallContent(200))
	b.ViewPanel(panelViewport, tui.Region{Width: 60, Height: 10})
	// Scroll to the middle so there is room above and below.
	b.Viewport.ScrollToLine(50)
	before := b.Viewport.YOffset()
	beforeActive := b.active

	b.Update(tui.WheelMsg{Panel: panelViewport, Delta: 1})

	after := b.Viewport.YOffset()
	if after != before+wheelViewportStep {
		t.Errorf("WheelMsg down: YOffset got %d, want %d", after, before+wheelViewportStep)
	}
	// Focus must not change.
	if b.active != beforeActive {
		t.Errorf("WheelMsg changed active panel: got %q, want %q", b.active, beforeActive)
	}
}

func TestBrowser_WheelMsgViewportScrollsUp(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent(tallContent(200))
	b.ViewPanel(panelViewport, tui.Region{Width: 60, Height: 10})
	b.Viewport.ScrollToLine(50)
	before := b.Viewport.YOffset()
	beforeActive := b.active

	b.Update(tui.WheelMsg{Panel: panelViewport, Delta: -1})

	after := b.Viewport.YOffset()
	if after != before-wheelViewportStep {
		t.Errorf("WheelMsg up: YOffset got %d, want %d", after, before-wheelViewportStep)
	}
	if b.active != beforeActive {
		t.Errorf("WheelMsg changed active panel: got %q, want %q", b.active, beforeActive)
	}
}

func TestBrowser_WheelMsgTreeMovesDown(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.ViewPanel(panelTree, tui.Region{Width: 30, Height: 10})
	before := b.Tree.Cursor()
	beforeActive := b.active

	b.Update(tui.WheelMsg{Panel: panelTree, Delta: 1})

	after := b.Tree.Cursor()
	if after == before {
		t.Error("WheelMsg{panelTree, +1} did not move the tree cursor")
	}
	if b.active != beforeActive {
		t.Errorf("WheelMsg changed active panel: got %q, want %q", b.active, beforeActive)
	}
}

func TestBrowser_WheelMsgTreeMovesUp(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.ViewPanel(panelTree, tui.Region{Width: 30, Height: 10})
	// Move down first so there is room to move up.
	b.Tree.MoveDown()
	b.Tree.MoveDown()
	before := b.Tree.Cursor()
	beforeActive := b.active

	b.Update(tui.WheelMsg{Panel: panelTree, Delta: -1})

	after := b.Tree.Cursor()
	if after == before {
		t.Error("WheelMsg{panelTree, -1} did not move the tree cursor")
	}
	if b.active != beforeActive {
		t.Errorf("WheelMsg changed active panel: got %q, want %q", b.active, beforeActive)
	}
}

func TestBrowser_WheelMsgTreeDefersTopicLoad(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.ViewPanel(panelTree, tui.Region{Width: 30, Height: 10})

	// A tree notch arms the debounce: it returns a non-nil tick Cmd and marks a
	// pending load, but does NOT load the topic synchronously (the load is
	// deferred to the debounce flush).
	cmd := b.Update(tui.WheelMsg{Panel: panelTree, Delta: 1})
	if cmd == nil {
		t.Fatal("first tree notch returned nil Cmd; expected a debounce tick Cmd")
	}
	if !b.wheel.loadPending {
		t.Error("tree notch did not mark a pending load")
	}
	if !b.wheel.tickInFlight {
		t.Error("tree notch did not schedule a debounce tick")
	}
}

func TestBrowser_WheelMsgTreeCoalescesBurst(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.ViewPanel(panelTree, tui.Region{Width: 30, Height: 10})

	// First notch schedules the only tick of the burst.
	if cmd := b.Update(tui.WheelMsg{Panel: panelTree, Delta: 1}); cmd == nil {
		t.Fatal("first notch returned nil Cmd; expected a debounce tick")
	}
	gen1 := b.wheel.gen

	// Subsequent notches ride the in-flight tick: no new tick Cmd, gen advances.
	for i := range 3 {
		if cmd := b.Update(tui.WheelMsg{Panel: panelTree, Delta: 1}); cmd != nil {
			t.Errorf("notch %d returned a tick Cmd; burst must coalesce onto one tick", i+2)
		}
	}
	if b.wheel.gen <= gen1 {
		t.Errorf("generation did not advance across the burst: got %d, want > %d", b.wheel.gen, gen1)
	}

	// A stale tick (gen from before the burst settled) re-arms rather than loading.
	if cmd := b.handleWheelDebounce(wheelDebounceMsg{gen: gen1}); cmd == nil {
		t.Error("stale debounce tick returned nil; expected a re-armed tick")
	}
	if !b.wheel.loadPending {
		t.Error("stale debounce tick cleared the pending load")
	}

	// The current-generation tick flushes the single pending load.
	cmd := b.handleWheelDebounce(wheelDebounceMsg{gen: b.wheel.gen})
	if cmd == nil {
		t.Error("settled debounce tick returned nil; expected the topic-load Cmd")
	}
	if b.wheel.loadPending || b.wheel.tickInFlight {
		t.Error("flush did not clear the debounce state")
	}
}

func TestBrowser_WheelMsgTreeInterruptedByAction(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.ViewPanel(panelTree, tui.Region{Width: 30, Height: 10})

	b.Update(tui.WheelMsg{Panel: panelTree, Delta: 1})
	if !b.wheel.loadPending {
		t.Fatal("precondition: expected a pending load after a notch")
	}

	// A bound key (here ActionNavDown) interrupts the burst: the pending wheel
	// load is dropped so the action's own load takes over.
	b.HandleAction(tui.ActionNavDown)
	if b.wheel.loadPending {
		t.Error("HandleAction did not cancel the pending wheel load")
	}

	// When the stale tick finally fires it finds nothing pending and flushes to nil.
	if cmd := b.handleWheelDebounce(wheelDebounceMsg{gen: b.wheel.gen}); cmd != nil {
		t.Error("debounce flush after interrupt returned a Cmd; expected nil")
	}
	if b.wheel.tickInFlight {
		t.Error("flush after interrupt did not reset tickInFlight")
	}
}

func TestBrowser_WheelMsgTreeInterruptedByClick(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.ViewPanel(panelTree, tui.Region{Width: 30, Height: 10})

	b.Update(tui.WheelMsg{Panel: panelTree, Delta: 1})
	if !b.wheel.loadPending {
		t.Fatal("precondition: expected a pending load after a notch")
	}

	// A click interrupts the burst too.
	b.Update(tui.PanelClickMsg{Panel: panelTree, Y: 0})
	if b.wheel.loadPending {
		t.Error("PanelClickMsg did not cancel the pending wheel load")
	}
}

func TestBrowser_WheelViewportDefersDiagramSync(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent(tallContent(200))
	b.ViewPanel(panelViewport, tui.Region{Width: 80, Height: 10})
	b.Viewport.ScrollToLine(50)
	before := b.Viewport.YOffset()

	cmd := b.Update(tui.WheelMsg{Panel: panelViewport, Delta: 1})

	// ScrollBy stays immediate (O(1)); the diagram re-sync is deferred.
	if got := b.Viewport.YOffset(); got != before+wheelViewportStep {
		t.Errorf("viewport wheel YOffset = %d; want %d", got, before+wheelViewportStep)
	}
	if !b.wheel.syncPending {
		t.Error("viewport wheel did not arm a deferred diagram sync")
	}
	if cmd == nil {
		t.Error("viewport wheel returned nil cmd; want the debounce tick")
	}
}

func TestBrowser_ScrollbarClickScrollsProportionally(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent(tallContent(200))
	b.ViewPanel(panelViewport, tui.Region{Width: 80, Height: 10})
	// h=10 → maxOffset = total-h; scrollbar column = width-1 = 79.
	maxOffset := b.Viewport.TotalLines() - 10

	// Click the bottom of the scrollbar track → jump to the bottom.
	b.handlePanelClick(tui.PanelClickMsg{Panel: panelViewport, X: 79, Y: 9})
	if got := b.Viewport.YOffset(); got != maxOffset {
		t.Errorf("scrollbar click at bottom: YOffset = %d, want %d", got, maxOffset)
	}
	// Click the top → jump to the top.
	b.handlePanelClick(tui.PanelClickMsg{Panel: panelViewport, X: 79, Y: 0})
	if got := b.Viewport.YOffset(); got != 0 {
		t.Errorf("scrollbar click at top: YOffset = %d, want 0", got)
	}
	// A click off the scrollbar column is ignored (no jump).
	b.Viewport.ScrollToLine(50)
	b.handlePanelClick(tui.PanelClickMsg{Panel: panelViewport, X: 10, Y: 5})
	if got := b.Viewport.YOffset(); got != 50 {
		t.Errorf("non-scrollbar viewport click changed YOffset to %d; want 50 (unchanged)", got)
	}
}

func TestBrowser_ScrollbarClickShortDocumentNoop(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent(tallContent(3)) // fits in 10 rows → no scrollbar
	b.ViewPanel(panelViewport, tui.Region{Width: 80, Height: 10})
	b.handlePanelClick(tui.PanelClickMsg{Panel: panelViewport, X: 79, Y: 9})
	if got := b.Viewport.YOffset(); got != 0 {
		t.Errorf("scrollbar click on a short document scrolled to %d; want 0 (no scrollbar)", got)
	}
}

func TestBrowser_WheelMsgWhileFilteringIsNoop(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.ViewPanel(panelTree, tui.Region{Width: 30, Height: 10})
	b.enterFilter()
	beforeCursor := b.Tree.Cursor()

	// Wheel while filter is active: must be a no-op (the Frame already swallows
	// it, but handleWheel guards defensively too).
	b.Update(tui.WheelMsg{Panel: panelTree, Delta: 1})

	if b.Tree.Cursor() != beforeCursor {
		t.Errorf("WheelMsg while filtering moved cursor: got %v, want %v", b.Tree.Cursor(), beforeCursor)
	}
}

func TestBrowser_WheelMsgDoesNotChangeActivePanel(t *testing.T) {
	b := newMultiFileBrowser(t)
	b.ViewPanel(panelTree, tui.Region{Width: 30, Height: 10})
	// Start with tree focused.
	if b.active != panelTree {
		t.Fatalf("initial active = %q, want tree", b.active)
	}
	// Wheel over viewport panel — must not switch focus.
	b.Viewport.SetContent(tallContent(200))
	b.ViewPanel(panelViewport, tui.Region{Width: 60, Height: 10})
	b.Viewport.ScrollToLine(50)

	b.Update(tui.WheelMsg{Panel: panelViewport, Delta: 1})

	if b.active != panelTree {
		t.Errorf("WheelMsg on viewport changed active panel: got %q, want tree", b.active)
	}
}
