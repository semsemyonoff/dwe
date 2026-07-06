package docstui

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/docs/mermaid"
	"github.com/semsemyonoff/dwe/internal/core/docs/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// makeDiagrams returns n placeholder DiagramRefs for status/state tests.
func makeDiagrams(n int) []render.DiagramRef {
	diags := make([]render.DiagramRef, n)
	for i := range diags {
		diags[i] = render.DiagramRef{Source: "graph TD; A-->B", Index: i}
	}
	return diags
}

// missLookuper is a cache-capable renderer whose Lookup always misses, so the
// placeholder takes the "render failed" path once prefetch has finished.
type missLookuper struct{}

func (missLookuper) Lookup(string, mermaid.Theme, int) ([]byte, bool) { return nil, false }

// TestDiagramPlaceholderAdvertisesErrorHint guards the regression where a failed
// diagram surfaced no way to reach the captured mmdc error: the `E` hint must
// appear on the "render failed" line exactly when an error was recorded, and not
// otherwise.
func TestDiagramPlaceholderAdvertisesErrorHint(t *testing.T) {
	b := newTestBrowser(t)
	m := b.Model
	// Mark prefetch as finished so the placeholder reaches the failed branch.
	m.PrefetchProgress = ProgressMsg{Rendered: 1, Total: 1}

	// No recorded error → plain "render failed", no `E`.
	m.Prefetch = &Prefetch{}
	out := m.diagramPlaceholder(0, 1, true, "graph TD; A-->B", mermaid.ThemeDark, 1200, missLookuper{})
	if strings.Contains(out, "render failed") && strings.Contains(out, "`E`") {
		t.Errorf("did not expect an E hint without a recorded error: %q", out)
	}

	// Recorded error → the `E` hint is advertised.
	m.Prefetch = &Prefetch{errs: map[int]string{0: "mmdc render failed: Could not find Chrome"}}
	out = m.diagramPlaceholder(0, 1, true, "graph TD; A-->B", mermaid.ThemeDark, 1200, missLookuper{})
	if !strings.Contains(out, "render failed") {
		t.Fatalf("expected a render-failed placeholder, got %q", out)
	}
	if !strings.Contains(out, "`E`") {
		t.Errorf("expected the E error-log hint on a failed diagram with a recorded error: %q", out)
	}
}

// TestDisabledDiagramAdvertisesDetailsHint guards that a "rendering disabled"
// diagram advertises `E` exactly when the mmdc-missing notice is set (auto/mmdc
// mode with mmdc absent), and stays plain when mermaid was explicitly turned off
// (no notice).
func TestDisabledDiagramAdvertisesDetailsHint(t *testing.T) {
	b := newTestBrowser(t)
	m := b.Model

	// No notice (mode=off) → plain "rendering disabled", no `E`.
	m.MmdcMissingNotice = ""
	out := m.diagramPlaceholder(0, 1, true, "graph TD; A-->B", mermaid.ThemeDark, 1200, nil)
	if !strings.Contains(out, "rendering disabled") {
		t.Fatalf("expected a rendering-disabled placeholder, got %q", out)
	}
	if strings.Contains(out, "`E`") {
		t.Errorf("did not expect an E hint without an install notice: %q", out)
	}

	// Notice present (mmdc missing) → the `E` details hint is advertised on every
	// disabled diagram, active or not (the install guidance is the same for all).
	m.MmdcMissingNotice = "mmdc is not installed"
	for _, active := range []bool{true, false} {
		out = m.diagramPlaceholder(0, 1, active, "graph TD; A-->B", mermaid.ThemeDark, 1200, nil)
		if !strings.Contains(out, "`E`") {
			t.Errorf("expected the E details hint on a disabled diagram (active=%v) with an install notice: %q", active, out)
		}
	}
}

// TestOpenErrorOverlayShowsMmdcNotice guards that pressing `E` on a disabled
// diagram opens the render-error overlay carrying the install guidance, even
// though no prefetch error was recorded (prefetch is skipped when disabled).
func TestOpenErrorOverlayShowsMmdcNotice(t *testing.T) {
	b := newTestBrowser(t)
	b.body = tui.Region{Width: 76, Height: 22}
	b.DiagramState = NewDiagramState([]render.DiagramRef{{Source: "graph TD; A-->B", Index: 0}})
	b.Prefetch = nil // disabled renderer → no prefetch pool
	b.MmdcMissingNotice = "mmdc is not installed, so Mermaid diagrams cannot render."

	b.openErrorOverlay()
	if b.errOverlay == nil {
		t.Fatal("openErrorOverlay did not open the overlay for a disabled diagram")
	}
	if b.errOverlay.status != "rendering disabled" {
		t.Errorf("overlay status = %q; want %q", b.errOverlay.status, "rendering disabled")
	}
	if !strings.Contains(b.errOverlay.errText, "mmdc is not installed") {
		t.Errorf("overlay did not carry the install notice: %q", b.errOverlay.errText)
	}
}

// TestOpenErrorOverlayNoopWhenNothingToShow guards that `E` stays a no-op when
// rendering succeeded (no prefetch error) and there is no install notice — e.g.
// mermaid was explicitly disabled.
func TestOpenErrorOverlayNoopWhenNothingToShow(t *testing.T) {
	b := newTestBrowser(t)
	b.body = tui.Region{Width: 76, Height: 22}
	b.DiagramState = NewDiagramState([]render.DiagramRef{{Source: "graph TD; A-->B", Index: 0}})
	b.Prefetch = &Prefetch{}
	b.MmdcMissingNotice = ""

	b.openErrorOverlay()
	if b.errOverlay != nil {
		t.Error("openErrorOverlay opened an overlay with nothing to show")
	}
}

// TestDoCopyDiagramFlashesStatus verifies that copying a diagram surfaces a
// confirmation in the status line (so the otherwise-invisible clipboard write is
// observable) and that the clear tick removes it only when its generation still
// matches the current flash.
func TestDoCopyDiagramFlashesStatus(t *testing.T) {
	b := newTestBrowser(t)
	b.DiagramState = NewDiagramState([]render.DiagramRef{{Source: "graph TD; A-->B", Index: 0}})

	cmd := b.doCopyDiagram()
	if cmd == nil {
		t.Fatal("doCopyDiagram returned nil Cmd; expected a clipboard + flash batch")
	}
	if !strings.Contains(b.StatusBar.View(), "copied to clipboard") {
		t.Errorf("status did not flash the copy confirmation: %q", b.StatusBar.View())
	}

	// A stale clear tick (older generation) must not wipe the current flash.
	b.Update(statusFlashClearMsg{gen: b.flashGen - 1})
	if !strings.Contains(b.StatusBar.View(), "copied to clipboard") {
		t.Errorf("stale clear tick wiped the flash: %q", b.StatusBar.View())
	}

	// The matching clear tick removes it.
	b.Update(statusFlashClearMsg{gen: b.flashGen})
	if strings.Contains(b.StatusBar.View(), "copied to clipboard") {
		t.Errorf("matching clear tick did not remove the flash: %q", b.StatusBar.View())
	}
}

// TestDoCopyDiagramNoDiagramNoFlash guards that `y` with no current diagram is a
// silent no-op (no stray flash).
func TestDoCopyDiagramNoDiagramNoFlash(t *testing.T) {
	b := newTestBrowser(t)
	b.DiagramState = NewDiagramState(nil)
	if cmd := b.doCopyDiagram(); cmd != nil {
		t.Errorf("expected nil Cmd when there is no diagram, got non-nil")
	}
	if strings.Contains(b.StatusBar.View(), "copied to clipboard") {
		t.Errorf("expected no copy flash without a diagram, got %q", b.StatusBar.View())
	}
}

// TestLoadTopicPlainDirClearsDiagramState guards that landing on a plain
// directory (blank viewport, no content node) drops the previous topic's diagram
// state — otherwise `y`/`[`/`]` would copy or cycle a stale diagram that is no
// longer on screen.
func TestLoadTopicPlainDirClearsDiagramState(t *testing.T) {
	b := newTestBrowser(t)
	// Simulate a previously loaded diagram-bearing topic.
	b.DiagramState = NewDiagramState([]render.DiagramRef{{Source: "graph TD; A-->B", Index: 0}})
	b.currentDiagramLines = []int{3}
	b.currentHeadingLines = []int{1}
	if b.DiagramState.CurrentDiagram() == nil {
		t.Fatal("precondition: expected a current diagram before loading the directory")
	}

	// A plain directory node has no index.md, so contentNodeFor returns nil.
	dir := &TreeNode{Node: &docs.Node{Name: "guides", IsDir: true, Path: "guides"}}
	if _, err := b.loadTopic(dir); err != nil {
		t.Fatalf("loadTopic(plain dir): %v", err)
	}

	if b.DiagramState == nil || b.DiagramState.CurrentDiagram() != nil {
		t.Errorf("plain-dir load did not clear DiagramState: %+v", b.DiagramState)
	}
	if b.currentDiagramLines != nil || b.currentHeadingLines != nil {
		t.Errorf("plain-dir load left stale line maps: diagrams=%v headings=%v",
			b.currentDiagramLines, b.currentHeadingLines)
	}
	// A `y` press now must be a silent no-op (no stale copy, no flash).
	if cmd := b.doCopyDiagram(); cmd != nil {
		t.Errorf("doCopyDiagram after plain-dir load returned a Cmd; want nil (no stale diagram)")
	}
}

// TestActiveDiagramForCursorPrefersVisible guards the wheel-scroll regression:
// with the cursor pinned to the window's top edge, the diagram at/above the
// cursor may have scrolled off above the window — the active diagram must then be
// the topmost one actually visible, so o/y/E act on what's on screen.
func TestActiveDiagramForCursorPrefersVisible(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent(tallContent(100))
	b.Viewport.SetDimensions(40, 15)
	b.currentDiagramLines = []int{10, 30} // diagram 0 @ line 10, diagram 1 @ line 30

	// Wheel scrolled so the window is [20,35): diagram 0 is off-screen above,
	// diagram 1 is visible; cursor pinned to the top edge (20).
	b.Viewport.ScrollToLine(20)
	b.setViewportCursor(20)
	if got := b.activeDiagramForCursor(); got != 1 {
		t.Errorf("after wheel: active = %d; want 1 (visible), not off-screen diagram 0", got)
	}

	// Keyboard cursor resting on diagram 1 with it visible still picks diagram 1.
	b.Viewport.ScrollToLine(25) // window [25,40)
	b.setViewportCursor(30)
	if got := b.activeDiagramForCursor(); got != 1 {
		t.Errorf("cursor on diagram 1: active = %d; want 1", got)
	}
}

// TestErrorHintOnlyOnActiveDiagram guards that the `E` hint (which acts on
// DiagramState.Current) is advertised only on the active failed diagram — never
// on a non-current one where pressing E would be a silent no-op.
func TestErrorHintOnlyOnActiveDiagram(t *testing.T) {
	b := newTestBrowser(t)
	m := b.Model
	m.PrefetchProgress = ProgressMsg{Rendered: 1, Total: 1}
	m.Prefetch = &Prefetch{errs: map[int]string{0: "mmdc render failed: boom"}}

	// Failed diagram, but NOT the active one → no E hint.
	out := m.diagramPlaceholder(0, 2, false, "graph TD; A-->B", mermaid.ThemeDark, 1200, missLookuper{})
	if strings.Contains(out, "`E`") {
		t.Errorf("E hint shown on non-active failed diagram (E would be a no-op): %q", out)
	}
	// Same diagram when active → E hint present.
	out = m.diagramPlaceholder(0, 2, true, "graph TD; A-->B", mermaid.ThemeDark, 1200, missLookuper{})
	if !strings.Contains(out, "`E`") {
		t.Errorf("E hint missing on the active failed diagram: %q", out)
	}
}

func TestNewDiagramState(t *testing.T) {
	diagrams := []render.DiagramRef{
		{Source: "graph TD; A --> B", Index: 0, LineInRendered: 5},
		{Source: "sequenceDiagram; A->>B: Hello", Index: 1, LineInRendered: 10},
	}

	ds := NewDiagramState(diagrams)
	if len(ds.Diagrams) != 2 {
		t.Errorf("expected 2 diagrams, got %d", len(ds.Diagrams))
	}
	if ds.Current != 0 {
		t.Errorf("expected current to be 0, got %d", ds.Current)
	}
}

func TestDiagramStateEmpty(t *testing.T) {
	ds := NewDiagramState(nil)
	if ds.Current != -1 {
		t.Errorf("expected current to be -1 for empty diagrams, got %d", ds.Current)
	}

	diagram := ds.CurrentDiagram()
	if diagram != nil {
		t.Errorf("expected CurrentDiagram to be nil for empty diagrams")
	}
}

func TestDiagramStateNavigation(t *testing.T) {
	diagrams := []render.DiagramRef{
		{Source: "1", Index: 0},
		{Source: "2", Index: 1},
		{Source: "3", Index: 2},
	}

	ds := NewDiagramState(diagrams)

	// Test Next
	ds.Next()
	if ds.Current != 1 {
		t.Errorf("expected current to be 1 after Next(), got %d", ds.Current)
	}

	ds.Next()
	if ds.Current != 2 {
		t.Errorf("expected current to be 2, got %d", ds.Current)
	}

	// Test wrapping
	ds.Next()
	if ds.Current != 0 {
		t.Errorf("expected current to wrap to 0, got %d", ds.Current)
	}

	// Test Prev
	ds.Prev()
	if ds.Current != 2 {
		t.Errorf("expected current to be 2 after Prev(), got %d", ds.Current)
	}

	ds.Prev()
	if ds.Current != 1 {
		t.Errorf("expected current to be 1, got %d", ds.Current)
	}

	// Test CurrentDiagram
	diagram := ds.CurrentDiagram()
	if diagram == nil {
		t.Fatalf("expected CurrentDiagram to not be nil")
	}
	if diagram.Source != "2" {
		t.Errorf("expected source '2', got '%s'", diagram.Source)
	}
}

func TestDiagramStateUpdate(t *testing.T) {
	ds := NewDiagramState(nil)

	// Update with new diagrams
	newDiagrams := []render.DiagramRef{
		{Source: "new1", Index: 0},
		{Source: "new2", Index: 1},
	}

	ds.Update(newDiagrams)
	if ds.Current != 0 {
		t.Errorf("expected current to reset to 0 after Update(), got %d", ds.Current)
	}
	if len(ds.Diagrams) != 2 {
		t.Errorf("expected 2 diagrams after Update(), got %d", len(ds.Diagrams))
	}

	// Update to empty
	ds.Update(nil)
	if ds.Current != -1 {
		t.Errorf("expected current to be -1 after Update(nil), got %d", ds.Current)
	}
}

func TestDiagramStateEmptyNavigation(t *testing.T) {
	ds := NewDiagramState(nil)

	// Navigation on empty should not crash
	ds.Next()
	if ds.Current != -1 {
		t.Errorf("expected current to stay -1 on Next() with no diagrams")
	}

	ds.Prev()
	if ds.Current != -1 {
		t.Errorf("expected current to stay -1 on Prev() with no diagrams")
	}
}
