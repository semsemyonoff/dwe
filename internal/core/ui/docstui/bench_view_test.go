package docstui

import (
	"context"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// BenchmarkFrameView measures one full Frame.View() pass for the docs browser
// with a realistic tall document. bubbletea v2 calls model.View() once per
// message; the framework's wheel-coalescing input filter (tui.Run WithFilter)
// caps wheel-driven Updates to ≈60/s so this ~1ms cost is paid at most once per
// frame during a scroll, not once per buffered notch. Kept as a reference /
// regression guard for that per-frame cost.
func BenchmarkFrameView(b *testing.B) {
	fsys := &testFS{files: map[string]string{"index.md": "# Test\n"}}
	roots := []docs.DocRoot{{Name: "test", FS: fsys}}
	m, err := NewModel(context.Background(), roots, "en", nil, nil, 120, 40, "", "Test", "auto")
	if err != nil {
		b.Fatalf("NewModel: %v", err)
	}
	br := newBrowser(context.Background(), m)

	var sb strings.Builder
	for range 2000 {
		sb.WriteString("This is a representative line of rendered documentation content number xyz\n")
	}
	br.Viewport.SetContent(sb.String())

	opts := tui.RunOptions{Brand: "dwe", Project: "Test", Mouse: true}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := tui.RenderFrame(br, opts, 120, 40); err != nil {
			b.Fatalf("RenderFrame: %v", err)
		}
	}
}

// BenchmarkApplyInnerScrollbarANSI guards the scrollbar truncation hot path
// against the O(n²) regression that made mouse scroll hard-freeze: real glamour
// lines are exactly the panel width, so almost every visible line hit the clip
// branch, and the old hand-rolled clip did a string alloc + width scan PER RUNE
// (O(n²)). The ANSI-aware ansi.Truncate is O(n). Content here is long ANSI-heavy
// lines (like glamour output) so every visible line exercises the clip.
func BenchmarkApplyInnerScrollbarANSI(b *testing.B) {
	fsys := &testFS{files: map[string]string{"index.md": "# Test\n"}}
	roots := []docs.DocRoot{{Name: "test", FS: fsys}}
	m, err := NewModel(context.Background(), roots, "en", nil, nil, 120, 48, "", "Test", "auto")
	if err != nil {
		b.Fatalf("NewModel: %v", err)
	}
	br := newBrowser(context.Background(), m)

	var line strings.Builder
	for range 30 {
		line.WriteString("\x1b[38;5;204mword\x1b[0m \x1b[1mbold\x1b[0m ")
	}
	var sb strings.Builder
	for range 300 {
		sb.WriteString(line.String())
		sb.WriteByte('\n')
	}
	br.Viewport.SetContent(sb.String())
	br.viewportInner = tui.Region{Width: 100, Height: 44}
	br.Viewport.SetDimensions(100, 44)
	content := br.Viewport.View()

	b.ReportAllocs()
	for b.Loop() {
		_ = br.applyInnerScrollbar(content, 44)
	}
}
