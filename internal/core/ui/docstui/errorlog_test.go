package docstui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// tallError builds an error string with n short lines so the overlay viewport
// has content to scroll vertically regardless of soft-wrap.
func tallError(n int) string {
	var sb strings.Builder
	for i := range n {
		sb.WriteString("error line ")
		sb.WriteByte(byte('0' + i%10))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func TestScrollErrorOverlayMovesViewport(t *testing.T) {
	b := newTestBrowser(t)
	b.errOverlay = newErrorState(40, 5, 1, 1, tallError(60))

	if got := b.errOverlay.vp.YOffset(); got != 0 {
		t.Fatalf("initial YOffset = %d, want 0", got)
	}

	b.scrollErrorOverlay(2) // down 2 notches
	down := b.errOverlay.vp.YOffset()
	if down <= 0 {
		t.Errorf("scroll down did not advance YOffset: %d", down)
	}
	if !b.errOverlayPending {
		t.Error("scroll did not mark the overlay pending for republish")
	}

	b.errOverlayPending = false
	b.scrollErrorOverlay(-1) // up 1 notch
	if up := b.errOverlay.vp.YOffset(); up >= down {
		t.Errorf("scroll up did not retreat YOffset: %d (was %d)", up, down)
	}
	if !b.errOverlayPending {
		t.Error("scroll up did not mark the overlay pending")
	}
}

// TestWheelMsgRoutesToOpenErrorOverlay verifies the coalesced overlay wheel
// (sentinel panel) scrolls the modal rather than a body panel.
func TestWheelMsgRoutesToOpenErrorOverlay(t *testing.T) {
	b := newTestBrowser(t)
	b.errOverlay = newErrorState(40, 5, 1, 1, tallError(60))

	b.Update(tui.WheelMsg{Panel: tui.OverlayWheelPanel, Delta: 3})
	if got := b.errOverlay.vp.YOffset(); got <= 0 {
		t.Errorf("overlay wheel did not scroll the modal: YOffset=%d", got)
	}
}

// TestCopyErrorToClipboard verifies `c` copies the raw error and flashes a
// confirmation, and is a no-op when the overlay is closed.
func TestCopyErrorToClipboard(t *testing.T) {
	b := newTestBrowser(t)
	b.errOverlay = newErrorState(40, 5, 1, 1, "mmdc render failed: Could not find Chrome")

	cmd := b.updateErrorOverlay(tea.KeyPressMsg{Text: "c"})
	if cmd == nil {
		t.Fatal("`c` returned nil Cmd; expected clipboard + flash batch")
	}
	if !strings.Contains(b.StatusBar.View(), "Error copied to clipboard") {
		t.Errorf("copy did not flash a confirmation: %q", b.StatusBar.View())
	}

	b.errOverlay = nil
	if cmd := b.copyErrorToClipboard(); cmd != nil {
		t.Error("copy on a closed overlay returned a non-nil Cmd")
	}
}

// TestToggleSelectionMode verifies `s` flips selection mode, which drives the
// overlay's ReleaseMouse flag (and thus the Frame's mouse release), and marks the
// overlay for republish.
func TestToggleSelectionMode(t *testing.T) {
	b := newTestBrowser(t)
	b.body = tui.Region{Width: 76, Height: 22}
	b.TermWidth, b.TermHeight = 80, 24
	b.errOverlay = newErrorState(40, 5, 1, 1, "boom")

	if b.errOverlay.overlay().ReleaseMouse {
		t.Fatal("selection mode should start off (ReleaseMouse false)")
	}

	b.errOverlayPending = false
	if cmd := b.updateErrorOverlay(tea.KeyPressMsg{Text: "s"}); cmd != nil {
		t.Errorf("`s` should return nil Cmd, got non-nil")
	}
	if !b.errOverlay.selecting {
		t.Error("`s` did not enable selection mode")
	}
	ov := b.errOverlay.overlay()
	if !ov.ReleaseMouse || !ov.FullScreen {
		t.Errorf("selection mode: ReleaseMouse=%v FullScreen=%v; want both true", ov.ReleaseMouse, ov.FullScreen)
	}
	// The selection overlay must fill the whole TERMINAL (not just the inner body)
	// so no frame chrome remains selectable — that IS the fix for "borders bleed
	// into the copy".
	if ov.Width != b.TermWidth || ov.Height != b.TermHeight {
		t.Errorf("selection overlay = %dx%d; want full terminal %dx%d",
			ov.Width, ov.Height, b.TermWidth, b.TermHeight)
	}
	if !b.errOverlayPending {
		t.Error("`s` did not mark the overlay pending for republish")
	}

	b.updateErrorOverlay(tea.KeyPressMsg{Text: "s"})
	if b.errOverlay.selecting {
		t.Error("second `s` did not disable selection mode")
	}
	// Leaving selection mode shrinks back to the content-fit box, and it is no
	// longer a full-screen takeover.
	back := b.errOverlay.overlay()
	if back.FullScreen || back.ReleaseMouse {
		t.Error("box overlay should not be FullScreen / ReleaseMouse")
	}
	if back.Width >= b.TermWidth {
		t.Errorf("box overlay width %d should be smaller than terminal %d", back.Width, b.TermWidth)
	}
}

// TestWindowResizeReSizesOpenErrorOverlay guards that a terminal resize while the
// error overlay is open re-sizes and re-publishes it, so a full-screen selection
// overlay isn't left as a stale, too-large snapshot the Frame then truncates.
func TestWindowResizeReSizesOpenErrorOverlay(t *testing.T) {
	b := newTestBrowser(t)
	b.body = tui.Region{Width: 96, Height: 30}
	b.TermWidth, b.TermHeight = 100, 32
	b.errOverlay = newErrorState(40, 5, 1, 1, "boom")
	b.errOverlay.selecting = true
	b.applyErrorOverlayDims() // size to the initial 100x32 terminal
	if got := b.errOverlay.overlay().Width; got != 100 {
		t.Fatalf("precondition: overlay width = %d; want 100", got)
	}

	b.errOverlayPending = false
	b.Update(tea.WindowSizeMsg{Width: 60, Height: 20})

	if !b.errOverlayPending {
		t.Error("resize did not re-publish the open overlay (errOverlayPending stayed false)")
	}
	if got := b.errOverlay.overlay().Width; got != 60 {
		t.Errorf("overlay width after resize = %d; want 60 (full new terminal)", got)
	}
}

// TestScrollErrorOverlayNoopWhenClosed guards against a panic / stray pending
// when no overlay is open.
func TestScrollErrorOverlayNoopWhenClosed(t *testing.T) {
	b := newTestBrowser(t)
	b.errOverlay = nil
	b.scrollErrorOverlay(1)
	if b.errOverlayPending {
		t.Error("scroll on a closed overlay marked pending")
	}
}
