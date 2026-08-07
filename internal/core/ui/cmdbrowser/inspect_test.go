package cmdbrowser

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// inspectTestItems are root-level (no dot → no group) so the tree's root focus
// shows them directly in the list, giving a selectable row to inspect without
// expanding a group first. The first item carries a tall Inspect body so the
// viewport actually has off-screen rows to scroll.
func inspectTestItems() []Item {
	// Distinct per-line text (not a repeated identical line) so a scrolled
	// viewport window renders visibly different content — otherwise a page-down
	// would shift YOffset while the rendered text stayed byte-identical.
	var sb strings.Builder
	for i := range 60 {
		sb.WriteString("line of inspect detail ")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteByte('\n')
	}
	tall := sb.String()
	return []Item{
		{ID: "alpha", Description: "first", Type: "shell", Inspect: func(int) string { return tall }},
		{ID: "beta", Description: "second", Type: "shell", Inspect: func(int) string { return "short body" }},
	}
}

// newInspectBrowser builds a browser over inspectTestItems, sized to a real body
// region and focused on the list so selectedOrigIdx resolves a row.
func newInspectBrowser(t *testing.T, opts Options) *browser {
	t.Helper()
	b := newBrowser("pick", inspectTestItems(), opts)
	b.Resize(tui.Region{Width: 80, Height: 24})
	b.active = panelList
	return b
}

func TestBrowser_InspectRequestsCapturingOverlay(t *testing.T) {
	b := newInspectBrowser(t, DefaultOptions())

	// Nothing pending before the action.
	if _, ok := b.PendingOverlay(); ok {
		t.Fatalf("PendingOverlay() = true before openInspect, want false")
	}

	b.openInspect()
	if b.inspect == nil {
		t.Fatalf("openInspect did not open the inspect viewport")
	}
	if b.inspect.inspectIdx != 0 {
		t.Errorf("inspectIdx = %d, want 0 (alpha)", b.inspect.inspectIdx)
	}

	ov, ok := b.PendingOverlay()
	if !ok {
		t.Fatalf("PendingOverlay() = false after openInspect, want true")
	}
	if !ov.CapturesInput {
		t.Errorf("inspect overlay CapturesInput = false, want true")
	}
	if ov.Width <= 0 || ov.Height <= 0 {
		t.Errorf("overlay dims = %dx%d, want positive", ov.Width, ov.Height)
	}
	if !strings.Contains(ov.Content, "inspect detail") {
		t.Errorf("overlay content missing inspect body:\n%s", ov.Content)
	}
	// The bordered box must fit inside the body so Composite never has to clamp.
	if ov.Width > b.body.Width || ov.Height > b.body.Height {
		t.Errorf("overlay %dx%d exceeds body %dx%d", ov.Width, ov.Height, b.body.Width, b.body.Height)
	}

	// Drained exactly once: a follow-up drain (e.g. after a scroll Update) must
	// not re-push a duplicate overlay onto the Frame stack.
	if _, ok := b.PendingOverlay(); ok {
		t.Errorf("PendingOverlay() = true on second drain, want false (no double-push)")
	}
}

// TestBrowser_InspectReopenAfterClosePushesOnce guards the lingering-state
// contract: a b.inspect left non-nil after the overlay was popped is inert
// content. Re-opening (on another row) must rebuild it and push EXACTLY one
// fresh overlay — no double-push, no stale idx.
func TestBrowser_InspectReopenAfterClosePushesOnce(t *testing.T) {
	b := newInspectBrowser(t, DefaultOptions())

	b.openInspect()
	if _, ok := b.PendingOverlay(); !ok {
		t.Fatal("first open should push an overlay")
	}
	if _, ok := b.PendingOverlay(); ok {
		t.Fatal("overlay must drain exactly once per open")
	}
	firstIdx := b.inspect.inspectIdx

	// Esc pops the overlay Frame-side; b.inspect lingers. Re-open on a new row.
	b.list.Select(1) // beta -> origIdx 1
	b.openInspect()
	if b.inspect.inspectIdx == firstIdx {
		t.Errorf("re-open should rebuild inspect for the new row; idx still %d", firstIdx)
	}
	if _, ok := b.PendingOverlay(); !ok {
		t.Fatal("re-open should push a fresh overlay")
	}
	if _, ok := b.PendingOverlay(); ok {
		t.Error("re-open overlay must drain exactly once (no double-push)")
	}
}

func TestBrowser_InspectScrollsViewport(t *testing.T) {
	b := newInspectBrowser(t, DefaultOptions())
	b.openInspect()
	b.PendingOverlay() // simulate the Frame pushing the overlay

	if got := b.inspect.vp.YOffset(); got != 0 {
		t.Fatalf("initial YOffset = %d, want 0", got)
	}
	before := b.inspect.overlay().Content
	// A page-down key (routed here while capturing) scrolls the viewport.
	b.updateInspect(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if got := b.inspect.vp.YOffset(); got <= 0 {
		t.Errorf("YOffset after PgDown = %d, want > 0 (scrolled down)", got)
	}
	// The scroll must republish the overlay so the Frame can refresh it in place;
	// without this the on-screen modal stays frozen at the opening position.
	if _, ok := b.PendingOverlay(); !ok {
		t.Fatal("scroll should re-mark the overlay pending for a refresh")
	}
	if after := b.inspect.overlay().Content; after == before {
		t.Error("overlay content unchanged after scroll; refresh would paint a stale frame")
	}
}

// TestBrowser_InspectScrollbar asserts the inspect overlay overdraws a
// proportional scrollbar (track + thumb glyphs) onto its right border when the
// description overflows the viewport, and omits it entirely when everything fits.
func TestBrowser_InspectScrollbar(t *testing.T) {
	b := newInspectBrowser(t, DefaultOptions())

	// alpha (idx 0) has a tall body that overflows → scrollbar present.
	b.openInspect()
	b.PendingOverlay()
	content := b.inspect.overlay().Content
	if !strings.Contains(content, tui.OverlayScrollbarThumbGlyph) {
		t.Errorf("overflowing inspect body missing scrollbar thumb %q:\n%s", tui.OverlayScrollbarThumbGlyph, content)
	}
	if !strings.Contains(content, tui.OverlayScrollbarTrackGlyph) {
		t.Errorf("overflowing inspect body missing scrollbar track %q:\n%s", tui.OverlayScrollbarTrackGlyph, content)
	}

	// beta (idx 1) has a short body that fits → no scrollbar glyphs.
	b.list.Select(1)
	b.openInspect()
	b.PendingOverlay()
	content = b.inspect.overlay().Content
	if strings.Contains(content, tui.OverlayScrollbarThumbGlyph) || strings.Contains(content, tui.OverlayScrollbarTrackGlyph) {
		t.Errorf("fitting inspect body should not draw a scrollbar:\n%s", content)
	}
}

// TestBrowser_InspectClosedByOverlayClosedMsg asserts the Frame's
// OverlayClosedMsg clears the lingering inspect state so a later unmatched raw
// key cannot re-mark it pending and resurrect the closed modal.
func TestBrowser_InspectClosedByOverlayClosedMsg(t *testing.T) {
	b := newInspectBrowser(t, DefaultOptions())
	b.openInspect()
	b.PendingOverlay() // Frame pushes the overlay
	if b.inspect == nil {
		t.Fatal("inspect should be open before close")
	}

	// Frame pops the capturing overlay on esc and forwards OverlayClosedMsg.
	if cmd := b.Update(tui.OverlayClosedMsg{}); cmd != nil {
		t.Errorf("OverlayClosedMsg should not return a command, got %v", cmd)
	}
	if b.inspect != nil || b.inspectPending {
		t.Fatalf("inspect=%v pending=%v after close, want nil/false", b.inspect, b.inspectPending)
	}

	// A later unmatched raw key forwarded to Update in normal mode must NOT
	// resurrect the closed overlay.
	b.Update(tea.KeyPressMsg{Code: 'x'})
	if _, ok := b.PendingOverlay(); ok {
		t.Error("unmatched key after close re-marked the overlay pending; closed inspect resurrected")
	}
}

func TestBrowser_InspectEnterSelects(t *testing.T) {
	cases := []struct {
		name string
		mode Mode
		want Action
	}{
		{"run", ModeRun, ActionRun},
		{"edit", ModeEdit, ActionEdit},
		{"inspect", ModeInspect, ActionInspect},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.Mode = tc.mode
			b := newInspectBrowser(t, opts)
			b.skipConfirm = true
			b.list.Select(1) // beta (origIdx 1)
			b.openInspect()
			b.PendingOverlay()

			cmd := b.updateInspect(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd == nil {
				t.Fatalf("enter returned nil cmd, want tea.Quit")
			}
			res, ok := b.Result().(Result)
			if !ok {
				t.Fatalf("Result() type = %T", b.Result())
			}
			if res.Idx != 1 {
				t.Errorf("Result.Idx = %d, want 1 (beta)", res.Idx)
			}
			if res.Action != tc.want {
				t.Errorf("Result.Action = %v, want %v", res.Action, tc.want)
			}
			if tc.mode == ModeRun && !res.SkipConfirm {
				t.Errorf("Result.SkipConfirm = false, want true (skip-confirm was on)")
			}
		})
	}
}

func TestBrowser_InspectNoOpWithoutSelection(t *testing.T) {
	b := newBrowser("pick", inspectTestItems(), DefaultOptions())
	b.Resize(tui.Region{Width: 80, Height: 24})
	// Empty the list so no selectable row is focused.
	b.list.SetItems(nil)
	b.active = panelList
	b.openInspect()
	if b.inspect != nil {
		t.Errorf("openInspect opened a viewport with no selectable item")
	}
	if _, ok := b.PendingOverlay(); ok {
		t.Errorf("PendingOverlay() = true with no inspect state, want false")
	}
}

func TestBrowser_InspectActivePanelUnchanged(t *testing.T) {
	// Closing/opening inspect must not change the focused panel — focus is
	// Frame-owned, so inspect leaves b.active alone (no legacy focus restore).
	b := newInspectBrowser(t, DefaultOptions())
	b.active = panelList
	b.openInspect()
	b.PendingOverlay()
	b.updateInspect(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if b.active != panelList {
		t.Errorf("active panel = %q after inspect, want %q", b.active, panelList)
	}
}

// TestBrowser_InspectMouseWheelScrollsViewport asserts that a raw
// tea.MouseWheelMsg forwarded to Update (by the Frame when a capturing overlay
// is open) scrolls the inspect viewport and re-marks it pending for a
// refreshCapturingOverlay refresh.
func TestBrowser_InspectMouseWheelScrollsViewport(t *testing.T) {
	b := newInspectBrowser(t, DefaultOptions())
	b.openInspect()
	b.PendingOverlay() // simulate the Frame pushing the first overlay

	if got := b.inspect.vp.YOffset(); got != 0 {
		t.Fatalf("initial YOffset = %d, want 0", got)
	}

	// A wheel-down MouseWheelMsg forwarded by the Frame (capturing overlay path)
	// must scroll the inspect viewport.
	b.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if got := b.inspect.vp.YOffset(); got <= 0 {
		t.Errorf("YOffset after MouseWheelDown = %d, want > 0", got)
	}
	// The scroll must re-mark the overlay pending so the Frame can refresh it.
	if !b.inspectPending {
		t.Error("inspectPending should be set after mouse wheel scroll")
	}
	if _, ok := b.PendingOverlay(); !ok {
		t.Error("PendingOverlay() = false after wheel scroll; Frame cannot refresh the overlay")
	}

	// A wheel-up scrolls back.
	before := b.inspect.vp.YOffset()
	b.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if got := b.inspect.vp.YOffset(); got >= before {
		t.Errorf("YOffset after MouseWheelUp = %d, want < %d", got, before)
	}
	if _, ok := b.PendingOverlay(); !ok {
		t.Error("PendingOverlay() = false after wheel-up scroll")
	}
}

// TestBrowser_MouseWheelWithoutInspectIsNoop asserts that a raw
// tea.MouseWheelMsg is silently ignored when no inspect overlay is open
// (the Frame only forwards it on the capturing-overlay path, but defensive).
func TestBrowser_MouseWheelWithoutInspectIsNoop(t *testing.T) {
	b := newInspectBrowser(t, DefaultOptions())
	// b.inspect is nil at this point.
	if cmd := b.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown}); cmd != nil {
		t.Errorf("MouseWheelMsg without inspect returned cmd, want nil")
	}
	if _, ok := b.PendingOverlay(); ok {
		t.Error("MouseWheelMsg without inspect pushed an overlay")
	}
}
