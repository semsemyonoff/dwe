package docstui

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// TestBrowser_WheelMsgScrollsViewportByStep verifies that a WheelMsg on the
// viewport panel scrolls the content by exactly wheelViewportStep lines and
// that multiple notches accumulate correctly.
func TestBrowser_WheelMsgScrollsViewportByStep(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("# Long\n\n")
	for range 200 {
		sb.WriteString("Filler prose line.\n\n")
	}
	roots := []docs.DocRoot{{Name: "dwe", FS: flatFS{files: map[string]string{"long.md": sb.String()}}}}
	m, err := NewModel(context.Background(), roots, "en", i18n.NopTranslator{}, &testRenderer{}, 120, 40, "", "DWE", "auto")
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	loadFirstTopic(t, m)
	if total := m.Viewport.TotalLines(); total <= m.Viewport.VisibleHeight() {
		t.Fatalf("test doc not tall enough to scroll: total=%d visible=%d", total, m.Viewport.VisibleHeight())
	}

	br := newBrowser(context.Background(), m)
	vpW := viewportPanelInnerWidth(120)
	vpH := viewportInnerHeight(40)
	br.ViewPanel(panelViewport, tui.Region{Width: vpW, Height: vpH})

	// Scroll to a mid-document position so there is room to go up and down.
	m.Viewport.ScrollToLine(50)
	startOffset := m.Viewport.YOffset()

	// One downward notch: should advance by wheelViewportStep.
	br.Update(tui.WheelMsg{Panel: panelViewport, Delta: 1})
	if got, want := m.Viewport.YOffset(), startOffset+wheelViewportStep; got != want {
		t.Errorf("one down notch: YOffset=%d, want %d", got, want)
	}

	// Two upward notches: should retreat by 2*wheelViewportStep.
	br.Update(tui.WheelMsg{Panel: panelViewport, Delta: -1})
	br.Update(tui.WheelMsg{Panel: panelViewport, Delta: -1})
	if got, want := m.Viewport.YOffset(), startOffset-wheelViewportStep; got != want {
		t.Errorf("two up notches: YOffset=%d, want %d", got, want)
	}
}


// flatFS is a single-directory in-memory docs root that serves file content
// (testFS in tree_widget_test.go intentionally cannot, so it can't drive a
// real topic load).
type flatFS struct{ files map[string]string }

func (flatFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (f flatFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." && name != "" {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	entries := make([]fs.DirEntry, 0, len(f.files))
	for n := range f.files {
		entries = append(entries, dirEnt{name: n})
	}
	return entries, nil
}

func (f flatFS) ReadFile(name string) ([]byte, error) {
	if c, ok := f.files[name]; ok {
		return []byte(c), nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// loadFirstTopic resolves and applies the model's initial async topic load so
// the viewport holds rendered content. The construction-time initCmd was
// dropped (Decision #10); this helper calls loadTopic directly instead.
func loadFirstTopic(t *testing.T, m *Model) {
	t.Helper()
	if m.CurrentTopic == nil {
		t.Fatal("model has no current topic")
	}
	cmd, err := m.loadTopic(m.CurrentTopic)
	if err != nil {
		t.Fatalf("loadTopic: %v", err)
	}
	if cmd == nil {
		return // directory node with no content; nothing to load
	}
	msg, ok := cmd().(topicLoadedMsg)
	if !ok {
		t.Fatalf("expected topicLoadedMsg from cmd, got %T", msg)
	}
	if msg.Err != nil {
		t.Fatalf("topic load error: %v", msg.Err)
	}
	_ = m.applyTopicLoaded(msg)
}

func TestScrollbarThumbRendersForLongDocument(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Long\n\n")
	for range 200 {
		b.WriteString("Filler prose line.\n\n")
	}
	roots := []docs.DocRoot{{Name: "dwe", FS: flatFS{files: map[string]string{"long.md": b.String()}}}}
	m, err := NewModel(context.Background(), roots, "en", i18n.NopTranslator{}, &testRenderer{}, 120, 40, "", "DWE", "auto")
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	loadFirstTopic(t, m)

	if total := m.Viewport.TotalLines(); total <= m.Viewport.VisibleHeight() {
		t.Fatalf("test doc not tall enough to scroll: total=%d visible=%d", total, m.Viewport.VisibleHeight())
	}
	br := newBrowser(context.Background(), m)
	vpW := viewportPanelInnerWidth(120)
	vpH := viewportInnerHeight(40)
	content := br.ViewPanel(panelViewport, tui.Region{Width: vpW, Height: vpH})
	if !strings.Contains(content, scrollbarThumbGlyph) {
		t.Error("expected a scrollbar thumb in the rendered view for a scrollable document")
	}
}

func TestScrollbarAbsentForShortDocument(t *testing.T) {
	roots := []docs.DocRoot{{Name: "dwe", FS: flatFS{files: map[string]string{"short.md": "# Short\n\nOne line.\n"}}}}
	m, err := NewModel(context.Background(), roots, "en", i18n.NopTranslator{}, &testRenderer{}, 120, 40, "", "DWE", "auto")
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	loadFirstTopic(t, m)

	b := newBrowser(context.Background(), m)
	vpW := viewportPanelInnerWidth(120)
	vpH := viewportInnerHeight(40)
	content := b.ViewPanel(panelViewport, tui.Region{Width: vpW, Height: vpH})
	if strings.Contains(content, scrollbarThumbGlyph) {
		t.Error("did not expect a scrollbar thumb when the whole document fits")
	}
}

func TestActiveDiagramFollowsScroll(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Diagrams\n\n```mermaid\ngraph TD; A-->B\n```\n\n")
	for range 80 {
		b.WriteString("Filler prose line.\n\n")
	}
	b.WriteString("```mermaid\ngraph TD; C-->D\n```\n")

	roots := []docs.DocRoot{{Name: "dwe", FS: flatFS{files: map[string]string{"diag.md": b.String()}}}}
	m, err := NewModel(context.Background(), roots, "en", i18n.NopTranslator{}, &testRenderer{}, 120, 40, "", "DWE", "auto")
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	loadFirstTopic(t, m)

	if got := len(m.DiagramState.Diagrams); got != 2 {
		t.Fatalf("expected 2 diagrams, got %d", got)
	}
	// At the top, the first diagram is in view and active.
	if m.DiagramState.Current != 0 {
		t.Errorf("at top, active diagram = %d, want 0", m.DiagramState.Current)
	}

	// Scroll to the bottom: the second diagram comes into view and takes over.
	m.FocusZone = FocusViewport
	m.Viewport.ScrollEnd()
	m.syncActiveDiagram()
	if m.DiagramState.Current != 1 {
		t.Errorf("after scrolling to bottom, active diagram = %d, want 1", m.DiagramState.Current)
	}

	// Back to the top restores the first diagram.
	m.Viewport.ScrollStart()
	m.syncActiveDiagram()
	if m.DiagramState.Current != 0 {
		t.Errorf("after scrolling back to top, active diagram = %d, want 0", m.DiagramState.Current)
	}
}
