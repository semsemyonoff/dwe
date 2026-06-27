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

// TestBrowser_InspectReopenAfterClosePushesOnce guards the documented lingering-
// state contract: the Frame pops the inspect overlay on esc WITHOUT notifying the
// plugin, so b.inspect stays non-nil as inert content. Re-opening (on another row)
// must rebuild it and push EXACTLY one fresh overlay — no double-push, no stale idx.
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
