package docstui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

func TestViewportCursorClamp(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent(tallContent(100))
	b.Viewport.SetDimensions(40, 10)

	b.setViewportCursor(-5)
	if b.viewportCursor != 0 {
		t.Errorf("clamp below 0: got %d, want 0", b.viewportCursor)
	}
	last := b.Viewport.TotalLines() - 1
	b.setViewportCursor(10_000)
	if b.viewportCursor != last {
		t.Errorf("clamp above total: got %d, want %d", b.viewportCursor, last)
	}
}

func TestSyncViewportToCursorKeepsCursorVisible(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent(tallContent(100))
	b.Viewport.SetDimensions(40, 10)
	b.active = panelViewport

	b.Viewport.ScrollToLine(0)
	b.setViewportCursor(0)
	b.moveViewportCursor(40) // push the cursor well below the window
	b.syncViewportToCursor()

	top := b.Viewport.YOffset()
	if b.viewportCursor < top || b.viewportCursor >= top+b.Viewport.VisibleHeight() {
		t.Errorf("cursor %d not visible in window [%d,%d)", b.viewportCursor, top, top+b.Viewport.VisibleHeight())
	}
}

func TestPinCursorToWindowFollowsScroll(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent(tallContent(100))
	b.Viewport.SetDimensions(40, 10)
	b.active = panelViewport

	// Cursor at top, then the wheel scrolls the viewport far down without moving
	// the cursor; pinning drags the cursor to the visible window's top edge.
	b.setViewportCursor(0)
	b.Viewport.ScrollToLine(50)
	b.pinCursorToWindow()

	top := b.Viewport.YOffset()
	if b.viewportCursor < top || b.viewportCursor >= top+b.Viewport.VisibleHeight() {
		t.Errorf("pinned cursor %d not in window [%d,%d)", b.viewportCursor, top, top+b.Viewport.VisibleHeight())
	}
}

// TestScrollViewportToClickPinsCursor guards the regression where a scrollbar
// click scrolled the viewport but left the reading cursor off-screen, so the next
// j/k nav snapped the viewport back to the pre-click position.
func TestScrollViewportToClickPinsCursor(t *testing.T) {
	b := newTestBrowser(t)
	b.Viewport.SetContent(tallContent(100))
	b.Viewport.SetDimensions(40, 10)
	b.viewportInner = tui.Region{Width: 40, Height: 10}
	b.active = panelViewport
	b.setViewportCursor(0)

	// Click near the bottom of the scrollbar column (last inner column).
	b.scrollViewportToClick(39, 9)

	top := b.Viewport.YOffset()
	if top == 0 {
		t.Fatal("scrollbar click did not scroll the viewport")
	}
	if b.viewportCursor < top || b.viewportCursor >= top+b.Viewport.VisibleHeight() {
		t.Errorf("cursor %d not pinned into window [%d,%d) after scrollbar click",
			b.viewportCursor, top, top+b.Viewport.VisibleHeight())
	}
}

func TestApplyCursorGlyphOnlyWhenViewportFocused(t *testing.T) {
	b := newTestBrowser(t)
	// Glamour left-indents every line with a margin space; the glyph overwrites
	// that space (width-neutral). Mirror it so the width assertion is meaningful.
	var sb strings.Builder
	for i := range 40 {
		sb.WriteString("  margin line ")
		sb.WriteByte(byte('0' + i%10))
		sb.WriteByte('\n')
	}
	b.Viewport.SetContent(sb.String())
	b.Viewport.SetDimensions(40, 10)
	b.setViewportCursor(0)

	// Tree focused: no glyph.
	b.active = panelTree
	out := b.applyCursorGlyph(b.Viewport.View(), 10)
	if strings.Contains(ansi.Strip(out), cursorGlyph) {
		t.Error("cursor glyph drawn while tree focused")
	}

	// Viewport focused: glyph on the cursor row, width preserved.
	b.active = panelViewport
	out = b.applyCursorGlyph(b.Viewport.View(), 10)
	firstRow := strings.Split(out, "\n")[0]
	if !strings.Contains(ansi.Strip(firstRow), cursorGlyph) {
		t.Errorf("cursor glyph missing on cursor row: %q", ansi.Strip(firstRow))
	}
	plainBefore := ansi.Strip(strings.Split(b.Viewport.View(), "\n")[0])
	plainAfter := ansi.Strip(firstRow)
	if ansi.StringWidth(plainBefore) != ansi.StringWidth(plainAfter) {
		t.Errorf("glyph changed row width: %d → %d", ansi.StringWidth(plainBefore), ansi.StringWidth(plainAfter))
	}
}
