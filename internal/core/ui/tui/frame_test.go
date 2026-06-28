package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// frameGoldenHeight is the terminal height the full-frame goldens render at.
// Width varies across the buckets; height is fixed so the goldens differ only
// by width.
const frameGoldenHeight = 24

// newTestFrame builds a Frame over a fresh stub plugin and drives one
// WindowSizeMsg so geometry is populated before View is called. It returns both
// so tests can inspect plugin observations.
func newTestFrame(t *testing.T, w, h int, opts ...frameOption) (*Frame, *stubPlugin) {
	t.Helper()
	p := newStubPlugin()
	all := append([]frameOption{withBrand("dwe"), withProject("demo")}, opts...)
	f, err := newFrame(p, all...)
	if err != nil {
		t.Fatalf("newFrame: %v", err)
	}
	f.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return f, p
}

// key builds a KeyPressMsg whose String() matches the registry key form
// ("?", "o", "tab", "q", "esc", "ctrl+c", ...).
func key(s string) tea.KeyPressMsg {
	switch s {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	default:
		r := []rune(s)
		return tea.KeyPressMsg{Code: r[0], Text: s}
	}
}

// TestFrame_FullFrameGolden pins the full-frame layout at every width bucket
// (ANSI stripped — see stripANSI). It asserts every rendered row is exactly the
// frame width, the row count equals the terminal height (no overflow), the
// status line is the final row, and the byte-stable layout matches the golden.
// Regenerate with UPDATE_GOLDEN=1 go test ./internal/core/ui/tui/...
func TestFrame_FullFrameGolden(t *testing.T) {
	for _, w := range []int{60, 79, 80, 99, 100} {
		t.Run("width_"+itoa(w), func(t *testing.T) {
			f, _ := newTestFrame(t, w, frameGoldenHeight)
			content := f.View().Content
			plain := stripANSI(content)

			rows := strings.Split(plain, "\n")
			if len(rows) != frameGoldenHeight {
				t.Errorf("row count = %d; want terminal height %d", len(rows), frameGoldenHeight)
			}
			for i, row := range rows {
				if got := lipgloss.Width(row); got != w {
					t.Errorf("row %d width = %d; want frame width %d; row=%q", i, got, w, row)
				}
			}
			// The status line is the final row and carries the help hint.
			if last := rows[len(rows)-1]; !strings.Contains(last, "? help") {
				t.Errorf("final row is not the status line: %q", last)
			}

			assertGolden(t, "frame_"+itoa(w)+".golden", plain)
		})
	}
}

// TestFrame_HelpOpenGolden opens the help overlay and pins the composited frame:
// the overlay is centred over the body, the status line is still present (and,
// being composed outside Composite, never dimmed), and the total frame
// dimensions are unchanged.
func TestFrame_HelpOpenGolden(t *testing.T) {
	f, _ := newTestFrame(t, 80, frameGoldenHeight)
	f.Update(key("?")) // open help

	content := f.View().Content
	plain := stripANSI(content)

	rows := strings.Split(plain, "\n")
	if len(rows) != frameGoldenHeight {
		t.Errorf("help-open row count = %d; want %d (unchanged)", len(rows), frameGoldenHeight)
	}
	for i, row := range rows {
		if got := lipgloss.Width(row); got != 80 {
			t.Errorf("help-open row %d width = %d; want 80; row=%q", i, got, row)
		}
	}
	if last := rows[len(rows)-1]; !strings.Contains(last, "? help") {
		t.Errorf("status line missing/altered while help open: %q", last)
	}
	if !strings.Contains(plain, "Help") {
		t.Errorf("help overlay title not composited:\n%s", plain)
	}

	assertGolden(t, "frame_help_open.golden", plain)
}

// TestFrame_HelpOpenSmallTerminal asserts opening the help overlay at the
// smallest terminals the launch gate permits (minHeight..) never grows the frame
// past the terminal bounds. The help modal is taller than the body at these
// sizes, so without the height clamp in buildHelpOverlay the composite overflows
// (rows > h and rows narrower than w) — alt-screen corruption. Regression guard.
func TestFrame_HelpOpenSmallTerminal(t *testing.T) {
	for _, h := range []int{minHeight, 11, 12, 13} {
		t.Run("height_"+itoa(h), func(t *testing.T) {
			f, _ := newTestFrame(t, minWidth, h)
			f.Update(key("?")) // open help

			plain := stripANSI(f.View().Content)
			rows := strings.Split(plain, "\n")
			if len(rows) != h {
				t.Errorf("help-open row count = %d; want terminal height %d", len(rows), h)
			}
			for i, row := range rows {
				if got := lipgloss.Width(row); got != minWidth {
					t.Errorf("help-open row %d width = %d; want frame width %d; row=%q", i, got, minWidth, row)
				}
			}
		})
	}
}

// TestFrame_View_Envelope asserts the framework owns the tea.View envelope:
// AltScreen is always on; MouseMode is CellMotion when mouse=true + capable,
// and None when mouse=false or TERM=dumb.
func TestFrame_View_Envelope(t *testing.T) {
	nonDumb := withTermEnv(func() string { return "xterm-256color" })
	for _, mouse := range []bool{false, true} {
		f, _ := newTestFrame(t, 80, frameGoldenHeight, withMouse(mouse), nonDumb)
		v := f.View()
		if !v.AltScreen {
			t.Errorf("mouse=%v: View must request AltScreen", mouse)
		}
		var wantMode tea.MouseMode
		if mouse {
			wantMode = tea.MouseModeCellMotion
		}
		if v.MouseMode != wantMode {
			t.Errorf("mouse=%v: MouseMode = %v; want %v", mouse, v.MouseMode, wantMode)
		}
	}
}

// TestFrame_MouseCapabilityGate table-tests the capability gate: mouse=false
// always yields None; mouse=true + non-dumb yields CellMotion; mouse=true +
// TERM=dumb yields None; the default termEnv (real os.Getenv) does not panic.
func TestFrame_MouseCapabilityGate(t *testing.T) {
	t.Run("mouse_off_always_none", func(t *testing.T) {
		f, _ := newTestFrame(t, 80, frameGoldenHeight,
			withMouse(false), withTermEnv(func() string { return "xterm-256color" }))
		if got := f.View().MouseMode; got != tea.MouseModeNone {
			t.Errorf("mouse=false non-dumb: got %v; want MouseModeNone", got)
		}
	})
	t.Run("mouse_on_non_dumb", func(t *testing.T) {
		f, _ := newTestFrame(t, 80, frameGoldenHeight,
			withMouse(true), withTermEnv(func() string { return "xterm-256color" }))
		if got := f.View().MouseMode; got != tea.MouseModeCellMotion {
			t.Errorf("mouse=true xterm-256color: got %v; want MouseModeCellMotion", got)
		}
	})
	t.Run("mouse_on_dumb_term", func(t *testing.T) {
		f, _ := newTestFrame(t, 80, frameGoldenHeight,
			withMouse(true), withTermEnv(func() string { return "dumb" }))
		if got := f.View().MouseMode; got != tea.MouseModeNone {
			t.Errorf("mouse=true TERM=dumb: got %v; want MouseModeNone", got)
		}
	})
	t.Run("default_termenv_no_panic", func(t *testing.T) {
		// No withTermEnv — the default os.Getenv("TERM") path must not panic.
		f, _ := newTestFrame(t, 80, frameGoldenHeight, withMouse(true))
		_ = f.View()
	})
}

// TestFrame_StatusLineZones asserts the three-zone status line: brand/project on
// the left, plugin context in the middle, help hint on the right; and that an
// over-long plugin context is truncated so the line stays exactly frame width.
func TestFrame_StatusLineZones(t *testing.T) {
	f, p := newTestFrame(t, 80, frameGoldenHeight)
	status := stripANSI(f.renderStatusLine(80))
	if lipgloss.Width(status) != 80 {
		t.Errorf("status width = %d; want 80", lipgloss.Width(status))
	}
	if !strings.HasPrefix(status, "dwe · demo") {
		t.Errorf("status left zone = %q; want brand · project prefix", status)
	}
	if !strings.Contains(status, "stub: ready") {
		t.Errorf("status middle zone missing plugin context: %q", status)
	}
	if !strings.HasSuffix(status, "? help") {
		t.Errorf("status right zone = %q; want '? help' suffix", status)
	}

	// Over-long middle zone is truncated to keep the line exactly width cells.
	p.statusCtx = strings.Repeat("X", 500)
	long := stripANSI(f.renderStatusLine(80))
	if lipgloss.Width(long) != 80 {
		t.Errorf("truncated status width = %d; want 80", lipgloss.Width(long))
	}
	if !strings.HasPrefix(long, "dwe · demo") || !strings.HasSuffix(long, "? help") {
		t.Errorf("truncation lost a fixed zone: %q", long)
	}
}

// TestFrame_AsyncPreservation asserts an arbitrary tea.Msg forwarded through
// Frame.Update reaches the plugin's Update — both with no overlay and with the
// help overlay open (async still flows behind a modal).
func TestFrame_AsyncPreservation(t *testing.T) {
	t.Run("no_overlay", func(t *testing.T) {
		f, p := newTestFrame(t, 80, frameGoldenHeight)
		f.Update(stubMsg{payload: "async-1"})
		if !receivedMsg(p, "async-1") {
			t.Errorf("async message did not reach plugin.Update; got %v", p.gotMsgs)
		}
	})
	t.Run("help_open", func(t *testing.T) {
		f, p := newTestFrame(t, 80, frameGoldenHeight)
		f.Update(key("?")) // open help
		f.Update(stubMsg{payload: "async-2"})
		if !receivedMsg(p, "async-2") {
			t.Errorf("async message did not reach plugin.Update while help open; got %v", p.gotMsgs)
		}
	})
}

// TestFrame_PluginActionDispatch asserts a plugin action key routes through
// HandleAction while a built-in key does not (and instead drives the framework).
func TestFrame_PluginActionDispatch(t *testing.T) {
	f, p := newTestFrame(t, 80, frameGoldenHeight)

	f.Update(key("o")) // stubActionOpen
	if p.handledAction != stubActionOpen {
		t.Errorf("plugin action key did not route to HandleAction; handled=%q", p.handledAction)
	}

	// A built-in focus key must not reach the plugin and must move focus.
	before := f.focus.Active()
	p.handledAction = ""
	f.Update(key("tab")) // ActionFocusNext (built-in)
	if p.handledAction != "" {
		t.Errorf("built-in key leaked to plugin.HandleAction: %q", p.handledAction)
	}
	afterNext := f.focus.Active()
	if afterNext == before {
		t.Errorf("focus did not advance on built-in tab")
	}

	// shift+tab dispatches ActionFocusPrev — focus moves back (not forward).
	f.Update(key("shift+tab"))
	if p.handledAction != "" {
		t.Errorf("built-in shift+tab leaked to plugin.HandleAction: %q", p.handledAction)
	}
	if f.focus.Active() == afterNext {
		t.Errorf("focus did not retreat on built-in shift+tab")
	}
	if f.focus.Active() != before {
		t.Errorf("shift+tab did not return focus to the original panel (FocusPrev mis-wired to Next?)")
	}
}

// TestFrame_FocusRequestMovesFocusAndEchoes asserts a plugin-issued
// FocusRequestMsg moves Frame focus and echoes a FocusChangedMsg back, while a
// request for the already-focused panel (or an unknown ID) is a no-op.
func TestFrame_FocusRequestMovesFocusAndEchoes(t *testing.T) {
	focusEchoes := func(p *stubPlugin) []FocusChangedMsg {
		var out []FocusChangedMsg
		for _, m := range p.gotMsgs {
			if fc, ok := m.(FocusChangedMsg); ok {
				out = append(out, fc)
			}
		}
		return out
	}

	f, p := newTestFrame(t, 80, frameGoldenHeight)
	if f.focus.Active() != stubPanelLeft {
		t.Fatalf("initial focus = %q, want %q", f.focus.Active(), stubPanelLeft)
	}

	// Request a different panel: focus moves and exactly one FocusChangedMsg is
	// forwarded so the plugin's own active-panel tracking stays in sync.
	f.Update(FocusRequestMsg{Panel: stubPanelRight})
	if f.focus.Active() != stubPanelRight {
		t.Errorf("focus did not move on FocusRequestMsg; got %q", f.focus.Active())
	}
	if echoes := focusEchoes(p); len(echoes) != 1 || echoes[0].Panel != stubPanelRight {
		t.Errorf("FocusChangedMsg echoes = %+v, want one {right}", echoes)
	}

	// Requesting the already-focused panel must not re-echo.
	p.gotMsgs = nil
	f.Update(FocusRequestMsg{Panel: stubPanelRight})
	if f.focus.Active() != stubPanelRight {
		t.Errorf("focus changed on no-op request; got %q", f.focus.Active())
	}
	if echoes := focusEchoes(p); len(echoes) != 0 {
		t.Errorf("no-op FocusRequestMsg echoed FocusChangedMsg = %+v, want none", echoes)
	}

	// An unknown panel ID leaves focus unchanged and echoes nothing.
	p.gotMsgs = nil
	f.Update(FocusRequestMsg{Panel: PanelID("nope")})
	if f.focus.Active() != stubPanelRight {
		t.Errorf("unknown FocusRequestMsg changed focus; got %q", f.focus.Active())
	}
	if echoes := focusEchoes(p); len(echoes) != 0 {
		t.Errorf("unknown FocusRequestMsg echoed FocusChangedMsg = %+v, want none", echoes)
	}
}

// TestFrame_DeclinedActionFallsThrough asserts a key matching a registered plugin
// action whose HandleAction declines it (returns handled=false) still forwards the
// raw key to plugin.Update — the documented "decline → forward raw key" contract.
func TestFrame_DeclinedActionFallsThrough(t *testing.T) {
	p := newStubPlugin()
	f, err := newFrame(declineActionPlugin{p}, withBrand("dwe"), withProject("demo"))
	if err != nil {
		t.Fatalf("newFrame: %v", err)
	}
	f.Update(tea.WindowSizeMsg{Width: 80, Height: frameGoldenHeight})

	f.Update(key("d")) // registered action that HandleAction declines
	if !receivedKey(p, "d") {
		t.Errorf("declined action key did not fall through to plugin.Update; got %v", p.gotMsgs)
	}
}

// TestFrame_ModalInputSwallowed asserts a plugin action key is swallowed while
// the help overlay is open — it never reaches HandleAction (no acting behind the
// modal).
func TestFrame_ModalInputSwallowed(t *testing.T) {
	f, p := newTestFrame(t, 80, frameGoldenHeight)
	f.Update(key("?")) // open help
	f.Update(key("o")) // plugin action while modal open
	if p.handledAction != "" {
		t.Errorf("plugin action key acted behind the modal: handled=%q", p.handledAction)
	}
	// The help-close built-in still works while the modal is open.
	f.Update(key("?"))
	if !f.overlay.Empty() {
		t.Errorf("help key did not close the overlay")
	}
}

// TestFrame_EscClosesOverlayWithoutQuitting asserts the modal-input precedence
// rule: while an overlay is open, "esc" closes the overlay rather than reaching
// its ActionQuit alias and quitting the program.
func TestFrame_EscClosesOverlayWithoutQuitting(t *testing.T) {
	f, _ := newTestFrame(t, 80, frameGoldenHeight)
	f.Update(key("?")) // open help
	if f.overlay.Empty() {
		t.Fatal("help overlay did not open")
	}
	_, cmd := f.Update(key("esc"))
	if !f.overlay.Empty() {
		t.Errorf("esc did not close the overlay while a modal was open")
	}
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Errorf("esc quit the program instead of closing the overlay")
		}
	}
}

// TestFrame_QuitDispatch asserts the program's primary exit path: in normal mode
// (no overlay) the quit keys route through the registry to ActionQuit and the
// framework returns tea.Quit. "esc" reaches ActionQuit only here (no modal open);
// the modal-open case is the negative covered by TestFrame_EscClosesOverlayWithoutQuitting.
func TestFrame_QuitDispatch(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c", "esc"} {
		t.Run(k, func(t *testing.T) {
			f, p := newTestFrame(t, 80, frameGoldenHeight)
			_, cmd := f.Update(key(k))
			if !isQuitCmd(cmd) {
				t.Errorf("%q in normal mode did not return tea.Quit", k)
			}
			if p.handledAction != "" {
				t.Errorf("quit key %q leaked to plugin.HandleAction: %q", k, p.handledAction)
			}
		})
	}
}

// TestFrame_MouseMsgSwallowed asserts that a non-left-button mouse click is
// silently ignored: no command is returned and the plugin never sees it.
func TestFrame_MouseMsgSwallowed(t *testing.T) {
	f, p := newTestFrame(t, 80, frameGoldenHeight)
	before := len(p.gotMsgs)
	// Button=0 (MouseNone, not MouseLeft) — must be a no-op.
	_, cmd := f.Update(tea.MouseClickMsg{})
	if cmd != nil {
		t.Errorf("non-left-button click produced a command; want nil")
	}
	if len(p.gotMsgs) != before {
		t.Errorf("non-left-button click leaked to plugin.Update; gotMsgs grew from %d to %d", before, len(p.gotMsgs))
	}
}

// TestFrame_ResizePropagates asserts a WindowSizeMsg recomputes geometry and
// hands the plugin the inner body region, and that the message itself is also
// forwarded to plugin.Update (it is a non-key message — async preservation).
func TestFrame_ResizePropagates(t *testing.T) {
	f, p := newTestFrame(t, 80, frameGoldenHeight)
	want := f.geo.Inner
	if p.lastResize != want {
		t.Errorf("plugin.Resize got %+v; want inner body region %+v", p.lastResize, want)
	}
	if !receivedWindowSize(p, 80, frameGoldenHeight) {
		t.Errorf("WindowSizeMsg was not forwarded to plugin.Update; got %v", p.gotMsgs)
	}
	// A second resize re-propagates.
	f.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if p.lastResize != f.geo.Inner {
		t.Errorf("second resize: plugin.Resize got %+v; want %+v", p.lastResize, f.geo.Inner)
	}
	if !receivedWindowSize(p, 100, 30) {
		t.Errorf("second WindowSizeMsg was not forwarded to plugin.Update; got %v", p.gotMsgs)
	}
}

// TestFrame_StatusLineNarrowClamp asserts that when the fixed left/right zones
// already meet or exceed the available width, renderStatusLine clamps the line to
// exactly width cells (the no-room-for-centre branch).
func TestFrame_StatusLineNarrowClamp(t *testing.T) {
	f, _ := newTestFrame(t, 80, frameGoldenHeight)
	for _, w := range []int{10, 12, 15} {
		got := lipgloss.Width(stripANSI(f.renderStatusLine(w)))
		if got != w {
			t.Errorf("narrow status width = %d; want clamped to %d", got, w)
		}
	}
}

// TestFrame_BrandSegment covers all four brand/project presence combinations of
// the left status zone.
func TestFrame_BrandSegment(t *testing.T) {
	tests := []struct {
		brand, project, want string
	}{
		{"dwe", "demo", "dwe · demo"},
		{"dwe", "", "dwe"},
		{"", "demo", "demo"},
		{"", "", ""},
	}
	for _, tc := range tests {
		f := &Frame{opts: frameOptions{brand: tc.brand, project: tc.project}}
		if got := f.brandSegment(); got != tc.want {
			t.Errorf("brandSegment(brand=%q, project=%q) = %q; want %q", tc.brand, tc.project, got, tc.want)
		}
	}
}

// TestFrame_ActionTriggeredOverlay asserts a plugin overlay surfaced via
// PendingOverlay after a HandleAction appears immediately.
func TestFrame_ActionTriggeredOverlay(t *testing.T) {
	f, p := newTestFrame(t, 80, frameGoldenHeight)
	p.pending = &Overlay{Content: "modal", Width: 5, Height: 1}
	f.Update(key("o")) // HandleAction → drain PendingOverlay
	if f.overlay.Empty() {
		t.Errorf("action-triggered overlay was not surfaced")
	}
}

// TestFrame_CapturingOverlayRefreshesInPlace asserts a captured key that mutates
// a capturing overlay's state (e.g. an inspect viewport scroll) refreshes the
// visible top overlay in place: the freshly republished snapshot replaces the
// old one, the on-screen content updates, and the stack does NOT grow one stale
// layer per key.
func TestFrame_CapturingOverlayRefreshesInPlace(t *testing.T) {
	f, p := newTestFrame(t, 80, frameGoldenHeight)
	p.pending = &Overlay{Content: "frame-0", Width: 7, Height: 1, CapturesInput: true}
	f.drainOverlay() // push the capturing overlay
	if got := len(f.overlay.layers); got != 1 {
		t.Fatalf("overlay depth after open = %d, want 1", got)
	}
	if top, _ := f.overlay.Top(); top.Content != "frame-0" {
		t.Fatalf("top content = %q, want frame-0", top.Content)
	}

	// A captured key republishes a fresh snapshot (a "scrolled" frame).
	p.republishOnUpdate = &Overlay{Content: "frame-1", Width: 7, Height: 1, CapturesInput: true}
	f.Update(key("down"))
	f.Update(key("down")) // a second captured key must still not grow the stack

	if got := len(f.overlay.layers); got != 1 {
		t.Errorf("overlay depth after captured scroll keys = %d, want 1 (refresh must replace, not push)", got)
	}
	if top, _ := f.overlay.Top(); top.Content != "frame-1" {
		t.Errorf("top content after scroll = %q, want frame-1 (overlay frozen — refresh did not paint)", top.Content)
	}
}

// TestFrame_CapturingOverlayCloseNotifiesPlugin asserts that closing a
// CapturesInput overlay with esc pops it AND forwards an OverlayClosedMsg to the
// plugin, so the plugin can clear the state that produced the overlay.
func TestFrame_CapturingOverlayCloseNotifiesPlugin(t *testing.T) {
	f, p := newTestFrame(t, 80, frameGoldenHeight)
	p.pending = &Overlay{Content: "modal", Width: 5, Height: 1, CapturesInput: true}
	f.drainOverlay() // push the capturing overlay
	if f.overlay.Empty() {
		t.Fatal("capturing overlay was not pushed")
	}
	before := len(p.gotMsgs)

	f.Update(key("esc")) // captureClose

	if !f.overlay.Empty() {
		t.Error("esc did not pop the capturing overlay")
	}
	var got bool
	for _, m := range p.gotMsgs[before:] {
		if _, ok := m.(OverlayClosedMsg); ok {
			got = true
		}
	}
	if !got {
		t.Errorf("plugin was not sent OverlayClosedMsg on close; got %v", p.gotMsgs[before:])
	}
}

// TestNewFrame_ConstructionErrors asserts newFrame validates the plugin contract
// before launch: duplicate key, empty panel set, and non-positive weight all
// fail at construction.
func TestNewFrame_ConstructionErrors(t *testing.T) {
	t.Run("duplicate_key", func(t *testing.T) {
		if _, err := newFrame(dupKeyPlugin{newStubPlugin()}); err == nil {
			t.Error("newFrame accepted a plugin registering a duplicate key")
		}
	})
	t.Run("no_panels", func(t *testing.T) {
		if _, err := newFrame(noPanelPlugin{newStubPlugin()}); err == nil {
			t.Error("newFrame accepted a plugin with no panels")
		}
	})
	t.Run("bad_weight", func(t *testing.T) {
		if _, err := newFrame(badWeightPlugin{newStubPlugin()}); err == nil {
			t.Error("newFrame accepted a panel with a non-positive weight")
		}
	})
	t.Run("empty_id", func(t *testing.T) {
		if _, err := newFrame(emptyIDPlugin{newStubPlugin()}); err == nil {
			t.Error("newFrame accepted a panel with an empty ID")
		}
	})
	t.Run("duplicate_id", func(t *testing.T) {
		if _, err := newFrame(dupIDPlugin{newStubPlugin()}); err == nil {
			t.Error("newFrame accepted panels sharing a duplicate ID")
		}
	})
	t.Run("valid", func(t *testing.T) {
		if _, err := newFrame(newStubPlugin()); err != nil {
			t.Errorf("newFrame rejected a valid plugin: %v", err)
		}
	})
}

// TestFrame_Init delegates to the plugin's Init.
func TestFrame_Init(t *testing.T) {
	f, p := newTestFrame(t, 80, frameGoldenHeight)
	f.Init()
	if !p.initCalled {
		t.Error("Frame.Init did not delegate to plugin.Init")
	}
}

// TestRouteWhileCapturing asserts the pure capturing-overlay routing helper
// classifies messages according to the Stage 1 contract: ctrl+c → hard-quit,
// esc → close overlay, everything else (printables, ?, non-key messages) →
// swallow-to-plugin (registry bypassed).
func TestRouteWhileCapturing(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.Msg
		want captureDecision
	}{
		{
			name: "printable key swallowed to plugin",
			msg:  key("a"),
			want: captureSwallowToPlugin,
		},
		{
			name: "help key (?) swallowed to plugin — does NOT open help while capturing",
			msg:  key("?"),
			want: captureSwallowToPlugin,
		},
		{
			name: "quit key (q) swallowed to plugin — registry bypassed while capturing",
			msg:  key("q"),
			want: captureSwallowToPlugin,
		},
		{
			name: "ctrl+c triggers hard-quit",
			msg:  key("ctrl+c"),
			want: captureHardQuit,
		},
		{
			name: "esc triggers close-overlay",
			msg:  key("esc"),
			want: captureClose,
		},
		{
			name: "non-key message (async) swallowed to plugin",
			msg:  stubMsg{payload: "async"},
			want: captureSwallowToPlugin,
		},
		{
			name: "window resize (non-key) swallowed to plugin",
			msg:  tea.WindowSizeMsg{Width: 80, Height: 24},
			want: captureSwallowToPlugin,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := routeWhileCapturing(tc.msg)
			if got != tc.want {
				t.Errorf("routeWhileCapturing(%T) = %v; want %v", tc.msg, got, tc.want)
			}
		})
	}
}

// TestOverlay_CapturesInputField verifies the CapturesInput field exists on the
// Overlay type and propagates through the overlayStack so the Stage 3 consumer
// can inspect it via Top().
func TestOverlay_CapturesInputField(t *testing.T) {
	// Non-capturing overlay (default): CapturesInput should be false.
	plain := Overlay{Content: "plain", Width: 5, Height: 1}
	if plain.CapturesInput {
		t.Error("zero-value Overlay.CapturesInput should be false")
	}

	// Capturing overlay: field must be preserved through the stack round-trip.
	capturing := Overlay{Content: "filter", Width: 6, Height: 1, CapturesInput: true}

	var s overlayStack
	s.Push(plain)
	s.Push(capturing)

	top, ok := s.Top()
	if !ok {
		t.Fatal("stack with two overlays: Top() reported false")
	}
	if !top.CapturesInput {
		t.Error("top capturing overlay: CapturesInput should be true after push/top round-trip")
	}

	// After popping the capturing overlay, the non-capturing one is on top.
	s.Pop()
	below, ok := s.Top()
	if !ok {
		t.Fatal("stack with one overlay remaining: Top() reported false")
	}
	if below.CapturesInput {
		t.Error("non-capturing overlay below: CapturesInput should be false")
	}
}

// TestFrame_NonCapturingOverlayUnaffected asserts that a non-capturing overlay
// (CapturesInput == false) continues to use the normal modal-swallow policy:
// plugin action keys are swallowed (no acting behind the modal), while
// framework built-ins (help, quit) still work. This verifies that adding the
// CapturesInput field does not alter existing Stage 0 frame behaviour.
func TestFrame_NonCapturingOverlayUnaffected(t *testing.T) {
	f, p := newTestFrame(t, 80, frameGoldenHeight)

	// Push a non-capturing overlay (the default value).
	nonCapturing := Overlay{Content: "modal", Width: 5, Height: 1, CapturesInput: false}
	f.overlay.Push(nonCapturing)

	// Plugin action key while non-capturing overlay is open: must be swallowed.
	f.Update(key("o"))
	if p.handledAction != "" {
		t.Errorf("plugin action acted behind non-capturing overlay: handled=%q", p.handledAction)
	}

	// Help key while non-capturing overlay is open: closes the overlay.
	f.Update(key("?"))
	if !f.overlay.Empty() {
		t.Error("help key did not close the non-capturing overlay")
	}
}

// TestFrame_CapturingInputForwardsRawKeys asserts the no-overlay capture branch:
// while plugin.CapturingInput() is true, an action-letter key is forwarded raw to
// plugin.Update (NOT dispatched through HandleAction), and ctrl+c is still
// reserved as a hard-quit.
func TestFrame_CapturingInputForwardsRawKeys(t *testing.T) {
	f, p := newTestFrame(t, 80, frameGoldenHeight)
	p.capturing = true

	// "o" is a registered plugin action (stubActionOpen). While capturing it must
	// reach plugin.Update raw and NOT route through HandleAction.
	f.Update(key("o"))
	if p.handledAction != "" {
		t.Errorf("action key dispatched while capturing: handled=%q; want raw forward", p.handledAction)
	}
	if !receivedKey(p, "o") {
		t.Errorf("captured key did not reach plugin.Update; got %v", p.gotMsgs)
	}

	// "?" must NOT open help while capturing — it is a raw key to the plugin.
	f.Update(key("?"))
	if !f.overlay.Empty() {
		t.Error("help opened while capturing; want '?' forwarded raw to plugin")
	}
	if !receivedKey(p, "?") {
		t.Errorf("captured '?' did not reach plugin.Update; got %v", p.gotMsgs)
	}

	// ctrl+c stays reserved as a hard-quit even while capturing.
	_, cmd := f.Update(key("ctrl+c"))
	if !isQuitCmd(cmd) {
		t.Error("ctrl+c while capturing did not quit; it must stay reserved")
	}
}

// TestFrame_CapturingInputDisabledRestoresDispatch asserts that toggling
// CapturingInput back to false restores normal registry dispatch (the action
// routes through HandleAction again).
func TestFrame_CapturingInputDisabledRestoresDispatch(t *testing.T) {
	f, p := newTestFrame(t, 80, frameGoldenHeight)
	p.capturing = false
	f.Update(key("o"))
	if p.handledAction != stubActionOpen {
		t.Errorf("not-capturing action key did not dispatch; handled=%q", p.handledAction)
	}
}

// TestFrame_CapturingOverlayRoutesNavKeys asserts the modal-open capturing
// branch: when the top overlay has CapturesInput=true, navigation keys
// (arrows/pgup/home) route raw to plugin.Update, "?" does not open help, esc
// closes the overlay, and ctrl+c quits.
func TestFrame_CapturingOverlayRoutesNavKeys(t *testing.T) {
	t.Run("nav_keys_reach_plugin", func(t *testing.T) {
		f, p := newTestFrame(t, 80, frameGoldenHeight)
		f.overlay.Push(Overlay{Content: "inspect", Width: 6, Height: 2, CapturesInput: true})
		for _, k := range []string{"up", "down", "pgup", "home"} {
			f.Update(key(k))
			if !receivedKey(p, k) {
				t.Errorf("capturing overlay did not forward %q to plugin; got %v", k, p.gotMsgs)
			}
		}
	})

	t.Run("question_mark_does_not_open_help", func(t *testing.T) {
		f, p := newTestFrame(t, 80, frameGoldenHeight)
		f.overlay.Push(Overlay{Content: "inspect", Width: 6, Height: 2, CapturesInput: true})
		f.Update(key("?"))
		// Still exactly one overlay (the capturing one); no help pushed.
		if _, ok := f.overlay.Top(); !ok || !f.captureOverlayOpen() {
			t.Error("'?' altered the capturing overlay stack; want raw forward")
		}
		if !receivedKey(p, "?") {
			t.Errorf("'?' not forwarded to plugin while capturing overlay open; got %v", p.gotMsgs)
		}
	})

	t.Run("esc_closes_overlay", func(t *testing.T) {
		f, _ := newTestFrame(t, 80, frameGoldenHeight)
		f.overlay.Push(Overlay{Content: "inspect", Width: 6, Height: 2, CapturesInput: true})
		_, cmd := f.Update(key("esc"))
		if !f.overlay.Empty() {
			t.Error("esc did not close the capturing overlay")
		}
		if isQuitCmd(cmd) {
			t.Error("esc quit the program instead of closing the capturing overlay")
		}
	})

	t.Run("ctrl_c_hard_quits", func(t *testing.T) {
		f, _ := newTestFrame(t, 80, frameGoldenHeight)
		f.overlay.Push(Overlay{Content: "inspect", Width: 6, Height: 2, CapturesInput: true})
		_, cmd := f.Update(key("ctrl+c"))
		if !isQuitCmd(cmd) {
			t.Error("ctrl+c did not quit while a capturing overlay was open")
		}
	})
}

// TestFrame_TranslatorLocaleFlowIntoHelp asserts the translator + locale wired in
// via withTranslator/withLocale reach the help overlay: the modal title and a
// section/action string are resolved through the translator at the supplied
// locale.
func TestFrame_TranslatorLocaleFlowIntoHelp(t *testing.T) {
	tr := prefixTranslator{}
	f, _ := newTestFrame(t, 80, frameGoldenHeight, withTranslator(tr), withLocale("ru"))
	f.Update(key("?")) // open help
	plain := stripANSI(f.View().Content)
	// prefixTranslator returns "<locale>:<fallback>" from T, so the localized
	// title is "ru:Help".
	if !strings.Contains(plain, "ru:Help") {
		t.Errorf("help title not resolved through translator/locale; modal:\n%s", plain)
	}
	// The stub registers a "Stub" section; its label resolves through T too.
	if !strings.Contains(plain, "ru:Stub") {
		t.Errorf("help section label not resolved through translator/locale; modal:\n%s", plain)
	}
}

// TestRenderFrameHarness asserts the exported cross-package RenderFrame harness
// produces a frame string (the same content a Frame.View would) and surfaces
// construction errors.
func TestRenderFrameHarness(t *testing.T) {
	out, err := RenderFrame(newStubPlugin(), RunOptions{Brand: "dwe", Project: "demo"}, 80, frameGoldenHeight)
	if err != nil {
		t.Fatalf("RenderFrame: %v", err)
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "? help") {
		t.Errorf("RenderFrame output missing status line:\n%s", plain)
	}
	rows := strings.Split(plain, "\n")
	if len(rows) != frameGoldenHeight {
		t.Errorf("RenderFrame row count = %d; want %d", len(rows), frameGoldenHeight)
	}

	// A contract violation (no panels) surfaces as an error, no render.
	if _, err := RenderFrame(noPanelPlugin{newStubPlugin()}, RunOptions{}, 80, 24); err == nil {
		t.Error("RenderFrame accepted a plugin with no panels")
	}
}

// TestBuildHelpHarness asserts the exported BuildHelp harness builds an overlay
// resolving strings through the translator/locale, and surfaces Actions errors.
func TestBuildHelpHarness(t *testing.T) {
	ov, err := BuildHelp(newStubPlugin(), prefixTranslator{}, "ru", 80, 24)
	if err != nil {
		t.Fatalf("BuildHelp: %v", err)
	}
	plain := stripANSI(ov.Content)
	if !strings.Contains(plain, "ru:Help") {
		t.Errorf("BuildHelp overlay title not localized:\n%s", plain)
	}
	if ov.Width <= 0 || ov.Height <= 0 {
		t.Errorf("BuildHelp overlay has degenerate dimensions: %dx%d", ov.Width, ov.Height)
	}

	// nil translator falls back to NopTranslator (English) without panicking.
	if _, err := BuildHelp(newStubPlugin(), nil, "", 80, 24); err != nil {
		t.Fatalf("BuildHelp with nil translator: %v", err)
	}

	// A duplicate-key plugin surfaces the Actions error.
	if _, err := BuildHelp(dupKeyPlugin{newStubPlugin()}, nil, "en", 80, 24); err == nil {
		t.Error("BuildHelp accepted a plugin registering a duplicate key")
	}
}

// captureOverlayOpen reports whether the top overlay captures input — a small
// test helper used by the capturing-overlay routing tests.
func (f *Frame) captureOverlayOpen() bool {
	top, ok := f.overlay.Top()
	return ok && top.CapturesInput
}

// prefixTranslator is a fake i18n.Translator whose T returns "<locale>:<fallback>"
// so tests can assert BOTH that the help overlay routes through the translator AND
// that the supplied locale flows through. All other methods delegate to the
// no-op behaviour (return the fallback).
type prefixTranslator struct{ i18n.NopTranslator }

func (prefixTranslator) T(locale, _, fallback string) string { return locale + ":" + fallback }

// --- test plugins for the construction-error cases ---

type dupKeyPlugin struct{ *stubPlugin }

func (p dupKeyPlugin) Actions(reg *Registry) error {
	// "q" is already claimed by the built-in ActionQuit.
	return reg.Register("stub.dup", Binding{Keys: []string{"q"}, Desc: "dup", Section: "Stub"})
}

type noPanelPlugin struct{ *stubPlugin }

func (p noPanelPlugin) Panels() []Panel { return nil }

type badWeightPlugin struct{ *stubPlugin }

func (p badWeightPlugin) Panels() []Panel {
	return []Panel{{ID: "only", Title: "Only", Weight: 0}}
}

type emptyIDPlugin struct{ *stubPlugin }

func (p emptyIDPlugin) Panels() []Panel {
	return []Panel{{ID: "", Title: "Blank", Weight: 1}}
}

type dupIDPlugin struct{ *stubPlugin }

func (p dupIDPlugin) Panels() []Panel {
	return []Panel{
		{ID: "same", Title: "First", Weight: 1},
		{ID: "same", Title: "Second", Weight: 1},
	}
}

// declineActionPlugin registers a key but declines the matched action, exercising
// the Frame's "decline → forward raw key to plugin.Update" fall-through branch.
type declineActionPlugin struct{ *stubPlugin }

func (p declineActionPlugin) Actions(reg *Registry) error {
	return reg.Register("stub.decline", Binding{Keys: []string{"d"}, Desc: "Declined", Section: "Stub"})
}

func (p declineActionPlugin) HandleAction(Action) (tea.Cmd, bool) { return nil, false }

// receivedKey reports whether the plugin's Update recorded a KeyPressMsg whose
// String() matches s.
func receivedKey(p *stubPlugin, s string) bool {
	for _, m := range p.gotMsgs {
		if km, ok := m.(tea.KeyPressMsg); ok && km.String() == s {
			return true
		}
	}
	return false
}

// receivedWindowSize reports whether the plugin's Update recorded a
// WindowSizeMsg of the given dimensions.
func receivedWindowSize(p *stubPlugin, w, h int) bool {
	for _, m := range p.gotMsgs {
		if ws, ok := m.(tea.WindowSizeMsg); ok && ws.Width == w && ws.Height == h {
			return true
		}
	}
	return false
}

// isQuitCmd reports whether cmd, when executed, yields a tea.QuitMsg.
func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// receivedMsg reports whether the plugin's Update recorded a stubMsg with the
// given payload.
func receivedMsg(p *stubPlugin, payload string) bool {
	for _, m := range p.gotMsgs {
		if sm, ok := m.(stubMsg); ok && sm.payload == payload {
			return true
		}
	}
	return false
}

// --- mousePlugin: a dedicated test plugin for wheel/click tests (Task 4+) ---
//
// We do NOT add Nav/Select to stubPlugin because its Actions feeds the
// frame_help_open.golden and adding mouse defaults would change that golden,
// breaking the byte-stable regression guard.

const (
	mousePanelMain PanelID = "mouse-main"
	mousePanelAlt  PanelID = "mouse-alt"
)

// mousePlugin is a minimal Plugin for mouse routing tests. It registers the
// stdlib Nav and Select actions (so MatchMouse resolves wheel-up/down and
// double-click) and counts per-action invocations so tests can assert
// "HandleAction(ActionNavDown) was called exactly N times." Two equal-weight
// panels let click tests verify focus switching between panels.
type mousePlugin struct {
	counts map[Action]int
	// msgs records every message forwarded through Update so click/focus
	// delivery (PanelClickMsg / FocusChangedMsg) can be asserted.
	msgs []tea.Msg
	// capturing toggles CapturingInput() so mouse-during-capture tests can
	// assert the frame suppresses wheel/click dispatch (inline-filter parity
	// with the keyboard capture branch).
	capturing bool
	// captureOnFilter makes HandleAction(ActionFilter) flip capturing to true,
	// modelling the inline filter opening on '/'. Lets a test exercise the
	// frame's reset-on-entering-capture transition.
	captureOnFilter bool
}

func newMousePlugin() *mousePlugin {
	return &mousePlugin{counts: make(map[Action]int)}
}

func (p *mousePlugin) Init() tea.Cmd { return nil }
func (p *mousePlugin) Close() error  { return nil }
func (p *mousePlugin) Resize(Region) {}
func (p *mousePlugin) Update(msg tea.Msg) tea.Cmd {
	p.msgs = append(p.msgs, msg)
	return nil
}
func (p *mousePlugin) ViewPanel(_ PanelID, _ Region) string { return "[mouse]" }
func (p *mousePlugin) Panels() []Panel {
	return []Panel{
		{ID: mousePanelMain, Title: "Main", Weight: 1},
		{ID: mousePanelAlt, Title: "Alt", Weight: 1},
	}
}
func (p *mousePlugin) StatusContext() string { return "" }
func (p *mousePlugin) Actions(reg *Registry) error {
	return RegisterStandard(reg, ActionNavUp, ActionNavDown, ActionSelect, ActionFilter)
}
func (p *mousePlugin) HandleAction(a Action) (tea.Cmd, bool) {
	p.counts[a]++
	if a == ActionFilter && p.captureOnFilter {
		p.capturing = true
	}
	return nil, true
}
func (p *mousePlugin) PendingOverlay() (Overlay, bool) { return Overlay{}, false }
func (p *mousePlugin) Result() any                     { return nil }
func (p *mousePlugin) CapturingInput() bool            { return p.capturing }

// panelClicks returns every PanelClickMsg forwarded to the plugin, in order.
func (p *mousePlugin) panelClicks() []PanelClickMsg {
	var out []PanelClickMsg
	for _, m := range p.msgs {
		if pc, ok := m.(PanelClickMsg); ok {
			out = append(out, pc)
		}
	}
	return out
}

// focusChanges returns every FocusChangedMsg forwarded to the plugin, in order.
func (p *mousePlugin) focusChanges() []FocusChangedMsg {
	var out []FocusChangedMsg
	for _, m := range p.msgs {
		if fc, ok := m.(FocusChangedMsg); ok {
			out = append(out, fc)
		}
	}
	return out
}

// fakeClock is an injectable frameClock for click-routing tests. Initialise t
// to a non-zero time; advance moves it forward.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// testClock returns a fakeClock at a known non-zero time so the zero-sentinel
// check (!lastClick.t.IsZero()) is never accidentally bypassed.
func testClock() *fakeClock {
	return &fakeClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
}

// leftClick builds a MouseClickMsg with the left button at (x, y).
func leftClick(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// newMouseFrame builds a Frame over a mousePlugin with mouse=true + xterm-256color
// so wheel routing is active. opts are appended last.
func newMouseFrame(t *testing.T, w, h int, opts ...frameOption) (*Frame, *mousePlugin) {
	t.Helper()
	p := newMousePlugin()
	all := make([]frameOption, 0, 4+len(opts))
	all = append(all,
		withBrand("dwe"), withProject("demo"),
		withMouse(true),
		withTermEnv(func() string { return "xterm-256color" }),
	)
	all = append(all, opts...)
	f, err := newFrame(p, all...)
	if err != nil {
		t.Fatalf("newMouseFrame: %v", err)
	}
	f.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return f, p
}

// wheelDown / wheelUp build MouseWheelMsgs for test injection.
func wheelDown() tea.Msg { return tea.MouseWheelMsg{Button: tea.MouseWheelDown} }
func wheelUp() tea.Msg   { return tea.MouseWheelMsg{Button: tea.MouseWheelUp} }

// wheelLeft / wheelRight build horizontal MouseWheelMsgs (trackpad tilt-scroll).
func wheelLeft() tea.Msg  { return tea.MouseWheelMsg{Button: tea.MouseWheelLeft} }
func wheelRight() tea.Msg { return tea.MouseWheelMsg{Button: tea.MouseWheelRight} }

// flush injects the private tick message directly so tests need not wait 16ms.
func flush() tea.Msg { return wheelFlushMsg{} }

// TestFrame_WheelCoalescing exercises the wheel accumulator state machine.
func TestFrame_WheelCoalescing(t *testing.T) {
	t.Run("burst_N_down_yields_N_nav_down", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		const N = 5
		for range N {
			f.Update(wheelDown())
		}
		f.Update(flush())
		if got := p.counts[ActionNavDown]; got != N {
			t.Errorf("burst %d WheelDown: HandleAction(NavDown) count = %d; want %d", N, got, N)
		}
		if p.counts[ActionNavUp] != 0 {
			t.Errorf("burst down: unexpected NavUp count %d", p.counts[ActionNavUp])
		}
		// accumulator and armed flag must be reset after flush
		if f.wheelAccum != 0 {
			t.Errorf("wheelAccum after flush = %d; want 0", f.wheelAccum)
		}
		if f.wheelArmed {
			t.Errorf("wheelArmed after flush should be false")
		}
	})

	t.Run("burst_N_up_yields_N_nav_up", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		const N = 3
		for range N {
			f.Update(wheelUp())
		}
		f.Update(flush())
		if got := p.counts[ActionNavUp]; got != N {
			t.Errorf("burst %d WheelUp: HandleAction(NavUp) count = %d; want %d", N, got, N)
		}
	})

	t.Run("slow_one_per_flush", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		f.Update(wheelDown())
		f.Update(flush())
		if p.counts[ActionNavDown] != 1 {
			t.Errorf("first slow flush: NavDown count = %d; want 1", p.counts[ActionNavDown])
		}
		// Second tick: arm a new tick with a second wheel event.
		f.Update(wheelDown())
		f.Update(flush())
		if p.counts[ActionNavDown] != 2 {
			t.Errorf("second slow flush: NavDown count = %d; want 2", p.counts[ActionNavDown])
		}
	})

	t.Run("mixed_up_down_nets_direction", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		// 3 down + 2 up → net 1 down
		for range 3 {
			f.Update(wheelDown())
		}
		for range 2 {
			f.Update(wheelUp())
		}
		f.Update(flush())
		if p.counts[ActionNavDown] != 1 {
			t.Errorf("net 1 down: NavDown count = %d; want 1", p.counts[ActionNavDown])
		}
		if p.counts[ActionNavUp] != 0 {
			t.Errorf("net 1 down: unexpected NavUp count %d", p.counts[ActionNavUp])
		}
	})

	t.Run("mixed_up_down_nets_up", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		// 2 down + 4 up → net 2 up
		for range 2 {
			f.Update(wheelDown())
		}
		for range 4 {
			f.Update(wheelUp())
		}
		f.Update(flush())
		if p.counts[ActionNavUp] != 2 {
			t.Errorf("net 2 up: NavUp count = %d; want 2", p.counts[ActionNavUp])
		}
		if p.counts[ActionNavDown] != 0 {
			t.Errorf("net 2 up: unexpected NavDown count %d", p.counts[ActionNavDown])
		}
	})

	t.Run("first_wheel_arms_tick_subsequent_nil", func(t *testing.T) {
		f, _ := newMouseFrame(t, 80, frameGoldenHeight)
		// First wheel event must return a non-nil cmd (the tick).
		_, cmd1 := f.Update(wheelDown())
		if cmd1 == nil {
			t.Fatal("first WheelDown: cmd = nil; want non-nil (tick)")
		}
		// Execute the real tick and confirm it yields wheelFlushMsg — without
		// this the coalescing tests (which inject flush() directly) would still
		// pass even if the tick closure returned the wrong message type.
		if _, ok := cmd1().(wheelFlushMsg); !ok {
			t.Errorf("tick cmd produced %T; want wheelFlushMsg", cmd1())
		}
		// Second and third in-window wheel events must return nil (already armed).
		_, cmd2 := f.Update(wheelDown())
		if cmd2 != nil {
			t.Error("second WheelDown: cmd should be nil (tick already armed)")
		}
		_, cmd3 := f.Update(wheelUp())
		if cmd3 != nil {
			t.Error("third wheel (up) while armed: cmd should be nil")
		}
	})

	t.Run("stale_flush_after_flush_is_noop", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		f.Update(wheelDown())
		f.Update(flush())
		before := p.counts[ActionNavDown]
		// A second flush with no new wheel event must not dispatch anything.
		f.Update(flush())
		if p.counts[ActionNavDown] != before {
			t.Errorf("second flush (empty accum): NavDown grew from %d to %d", before, p.counts[ActionNavDown])
		}
	})

	t.Run("horizontal_wheel_ignored", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		// Horizontal wheel events must neither arm a tick nor touch the
		// vertical accumulator — they carry no Nav mapping in Stage 2.
		_, cmdL := f.Update(wheelLeft())
		if cmdL != nil {
			t.Error("WheelLeft returned a cmd; want nil (no tick armed)")
		}
		_, cmdR := f.Update(wheelRight())
		if cmdR != nil {
			t.Error("WheelRight returned a cmd; want nil (no tick armed)")
		}
		if f.wheelAccum != 0 {
			t.Errorf("wheelAccum after horizontal wheel = %d; want 0", f.wheelAccum)
		}
		if f.wheelArmed {
			t.Error("wheelArmed after horizontal wheel should be false")
		}
		// A subsequent flush must dispatch no Nav in either direction.
		f.Update(flush())
		if p.counts[ActionNavDown] != 0 || p.counts[ActionNavUp] != 0 {
			t.Errorf("horizontal wheel produced Nav: down=%d up=%d; want 0/0",
				p.counts[ActionNavDown], p.counts[ActionNavUp])
		}
	})

	t.Run("swallowed_while_overlay_open", func(t *testing.T) {
		f, _ := newMouseFrame(t, 80, frameGoldenHeight)
		// Open the help overlay via key (keyboard path; '?' registered by NewRegistry).
		f.Update(key("?"))
		if f.overlay.Empty() {
			t.Fatal("help overlay did not open")
		}
		_, cmd := f.Update(wheelDown())
		if cmd != nil {
			t.Error("WheelDown while overlay open returned a cmd; want nil (swallowed)")
		}
		if f.wheelAccum != 0 {
			t.Errorf("wheelAccum after swallowed wheel = %d; want 0", f.wheelAccum)
		}
		if f.wheelArmed {
			t.Error("wheelArmed after swallowed wheel should be false")
		}
	})

	t.Run("wheel_then_modal_then_flush_zero_nav", func(t *testing.T) {
		// Codex finding #1: arm the tick with a wheel event, then open a modal;
		// the delayed flush must not dispatch Nav behind the modal.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		f.Update(wheelDown()) // arms the tick + accumulates
		// Open help modal (clears accumulator on push).
		f.Update(key("?"))
		if f.overlay.Empty() {
			t.Fatal("help overlay did not open")
		}
		// Inject the flush that would have arrived from the armed tick.
		f.Update(flush())
		if p.counts[ActionNavDown] != 0 {
			t.Errorf("flush behind modal: NavDown count = %d; want 0", p.counts[ActionNavDown])
		}
		// Accumulator and armed flag must be clear after the no-op flush.
		if f.wheelAccum != 0 {
			t.Errorf("wheelAccum after modal flush = %d; want 0", f.wheelAccum)
		}
		if f.wheelArmed {
			t.Error("wheelArmed after modal flush should be false")
		}
	})

	t.Run("swallowed_while_capturing", func(t *testing.T) {
		// While the plugin captures raw input (inline filter, no overlay) wheel
		// events must neither arm a tick nor dispatch Nav — parity with the
		// keyboard capture branch, which forwards keys raw and never dispatches.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		p.capturing = true
		_, cmd := f.Update(wheelDown())
		if cmd != nil {
			t.Error("WheelDown while capturing returned a cmd; want nil (swallowed)")
		}
		if f.wheelAccum != 0 || f.wheelArmed {
			t.Errorf("capturing wheel armed state: accum=%d armed=%v; want 0/false", f.wheelAccum, f.wheelArmed)
		}
	})

	t.Run("armed_wheel_then_capture_then_flush_zero_nav", func(t *testing.T) {
		// Arm the tick with a wheel event, then the plugin enters capture (inline
		// filter); the delayed flush must not dispatch Nav behind the filter.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		f.Update(wheelDown()) // arms the tick + accumulates
		p.capturing = true    // filter takes over before the flush arrives
		f.Update(flush())
		if p.counts[ActionNavDown] != 0 {
			t.Errorf("flush behind filter: NavDown count = %d; want 0", p.counts[ActionNavDown])
		}
		if f.wheelAccum != 0 || f.wheelArmed {
			t.Errorf("capturing flush armed state: accum=%d armed=%v; want 0/false", f.wheelAccum, f.wheelArmed)
		}
	})

	t.Run("armed_wheel_then_action_capture_then_flush_zero_nav", func(t *testing.T) {
		// Same hazard as above, but capture is entered via the '/' ActionFilter
		// (not by externally flipping the flag). The transition must reset the
		// armed wheel so a tick armed before '/' cannot flush Nav once the filter
		// closes and its CapturingInput guard lifts.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		p.captureOnFilter = true
		f.Update(wheelDown()) // arms the tick + accumulates
		f.Update(key("/"))    // ActionFilter → plugin enters capture
		if !p.capturing {
			t.Fatal("plugin did not enter capture on '/'")
		}
		if f.wheelAccum != 0 || f.wheelArmed {
			t.Errorf("armed wheel not reset on entering capture: accum=%d armed=%v; want 0/false", f.wheelAccum, f.wheelArmed)
		}
		// Filter closes, then the stale tick fires — it must dispatch no Nav.
		p.capturing = false
		f.Update(flush())
		if p.counts[ActionNavDown] != 0 {
			t.Errorf("stale flush after filter close: NavDown = %d; want 0", p.counts[ActionNavDown])
		}
	})
}

// TestFrame_ClickRouting exercises the full click-routing policy wired in Task 5.
func TestFrame_ClickRouting(t *testing.T) {
	// Coordinates derived from mouseFrameGeometry:
	//   (5, 5)   → panel main (outer {0,0,40,23})
	//   (50, 5)  → panel alt  (outer {40,0,40,23})
	//   (75, 23) → help-hint  (x ∈ [74,80), y=23)
	//   (3, 23)  → zoneNone   (y=23, x<74, not in any panel)

	t.Run("click_on_panel_changes_focus", func(t *testing.T) {
		f, _ := newMouseFrame(t, 80, frameGoldenHeight)
		if f.focus.Active() != mousePanelMain {
			t.Fatalf("initial focus = %q; want %q", f.focus.Active(), mousePanelMain)
		}
		f.Update(leftClick(50, 5)) // click in alt panel
		if got := f.focus.Active(); got != mousePanelAlt {
			t.Errorf("after click alt panel: focus = %q; want %q", got, mousePanelAlt)
		}
		f.Update(leftClick(5, 5)) // click in main panel
		if got := f.focus.Active(); got != mousePanelMain {
			t.Errorf("after click main panel: focus = %q; want %q", got, mousePanelMain)
		}
	})

	t.Run("click_on_help_hint_opens_help", func(t *testing.T) {
		f, _ := newMouseFrame(t, 80, frameGoldenHeight)
		if !f.overlay.Empty() {
			t.Fatal("overlay non-empty before click")
		}
		f.Update(leftClick(75, 23)) // click in help hint
		if f.overlay.Empty() {
			t.Error("help overlay did not open on help-hint click")
		}
	})

	t.Run("double_click_same_panel_cell_triggers_select", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		clk := testClock()
		f.clock = clk
		// Two left-clicks in the same panel + same cell within the window.
		f.Update(leftClick(5, 5)) // first click — record
		f.Update(leftClick(5, 5)) // second click — double-click
		if got := p.counts[ActionSelect]; got != 1 {
			t.Errorf("double-click: ActionSelect count = %d; want 1", got)
		}
	})

	t.Run("entering_capture_via_action_resets_stale_click", func(t *testing.T) {
		// Regression: a click, then '/' opening the inline filter, then a quick
		// esc closing it, then a click on the SAME cell must NOT pair into a
		// double-click. Entering capture via the action resets lastClick, exactly
		// as crossing an overlay boundary does.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		p.captureOnFilter = true
		clk := testClock()
		f.clock = clk
		f.Update(leftClick(5, 5)) // first click records lastClick
		if f.lastClick.t.IsZero() {
			t.Fatal("first click did not record lastClick")
		}
		f.Update(key("/")) // ActionFilter → plugin enters capture
		if !p.capturing {
			t.Fatal("plugin did not enter capture on '/'")
		}
		if !f.lastClick.t.IsZero() {
			t.Error("lastClick not reset on entering capture")
		}
		// Filter closes; a click on the same cell (still within the window, since
		// the clock never advanced) must not synthesize a phantom double-click.
		p.capturing = false
		f.Update(leftClick(5, 5))
		if got := p.counts[ActionSelect]; got != 0 {
			t.Errorf("phantom double-click after filter close: ActionSelect = %d; want 0", got)
		}
	})

	t.Run("triple_click_triggers_select_once", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		clk := testClock()
		f.clock = clk
		f.Update(leftClick(5, 5)) // first
		f.Update(leftClick(5, 5)) // second → Select (clears lastClick)
		f.Update(leftClick(5, 5)) // third  → first of a new pair, not a Select
		if got := p.counts[ActionSelect]; got != 1 {
			t.Errorf("triple-click: ActionSelect count = %d; want 1", got)
		}
		// The third click should have recorded a new lastClick.
		if f.lastClick.t.IsZero() {
			t.Error("after triple-click, lastClick.t is zero; want the third click recorded")
		}
	})

	t.Run("double_click_outside_window_no_select", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		clk := testClock()
		f.clock = clk
		f.Update(leftClick(5, 5))                         // first click
		clk.advance(doubleClickWindow + time.Millisecond) // advance past window
		f.Update(leftClick(5, 5))                         // second click — too late
		if got := p.counts[ActionSelect]; got != 0 {
			t.Errorf("outside-window double-click: ActionSelect count = %d; want 0", got)
		}
	})

	t.Run("double_click_blank_status_no_select", func(t *testing.T) {
		// Two clicks on blank status space (zoneNone) must never produce Select.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		clk := testClock()
		f.clock = clk
		f.Update(leftClick(3, 23)) // zoneNone — clears lastClick
		f.Update(leftClick(3, 23)) // zoneNone — clears lastClick again
		if got := p.counts[ActionSelect]; got != 0 {
			t.Errorf("blank-space double-click: ActionSelect count = %d; want 0", got)
		}
	})

	t.Run("double_click_help_hint_no_select", func(t *testing.T) {
		// Two clicks on the help-hint zone: help is toggled (opened), second
		// click is swallowed (overlay open). No Select must fire.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		clk := testClock()
		f.clock = clk
		f.Update(leftClick(75, 23)) // opens help overlay; clears lastClick
		f.Update(leftClick(75, 23)) // overlay open → swallowed
		if got := p.counts[ActionSelect]; got != 0 {
			t.Errorf("help-hint double-click: ActionSelect count = %d; want 0", got)
		}
	})

	t.Run("keyboard_help_open_close_resets_double_click", func(t *testing.T) {
		// Panel click, then a keyboard help open+close, then a click on the same
		// cell within the window must NOT synthesize a Select — the overlay
		// boundary resets the double-click record.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		clk := testClock()
		f.clock = clk
		f.Update(leftClick(5, 5)) // first click — record
		f.Update(key("?"))        // open help (clears lastClick)
		f.Update(key("?"))        // close help (clears lastClick)
		f.Update(leftClick(5, 5)) // same cell, within window — must be a fresh first click
		if got := p.counts[ActionSelect]; got != 0 {
			t.Errorf("help open/close between clicks: ActionSelect count = %d; want 0", got)
		}
	})

	t.Run("esc_close_overlay_resets_double_click", func(t *testing.T) {
		// Same as above but the overlay is dismissed via esc, which pops through
		// a different code path; it must reset the double-click record too.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		clk := testClock()
		f.clock = clk
		f.Update(leftClick(5, 5)) // first click — record
		f.Update(key("?"))        // open help (clears lastClick)
		f.Update(key("esc"))      // close help via esc (clears lastClick)
		f.Update(leftClick(5, 5)) // same cell, within window
		if got := p.counts[ActionSelect]; got != 0 {
			t.Errorf("esc-close between clicks: ActionSelect count = %d; want 0", got)
		}
	})

	t.Run("swallowed_overlay_click_resets_double_click", func(t *testing.T) {
		// Panel click, plugin overlay opens, a click is swallowed while it is up,
		// overlay closes, then a click on the original cell. The swallowed click
		// must have cleared the record so no phantom Select fires.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		clk := testClock()
		f.clock = clk
		f.Update(leftClick(5, 5)) // first click — record
		f.overlay.Push(Overlay{Content: "modal", Width: 5, Height: 1})
		f.Update(leftClick(5, 5)) // swallowed while overlay open (clears lastClick)
		f.overlay.Pop()
		f.Update(leftClick(5, 5)) // fresh first click after the modal closed
		if got := p.counts[ActionSelect]; got != 0 {
			t.Errorf("swallowed overlay click between clicks: ActionSelect count = %d; want 0", got)
		}
	})

	t.Run("zero_clock_first_click_not_double_click", func(t *testing.T) {
		// A clock that returns time.Time{} (zero): lastClick.t starts zero, so
		// the first click at (0,0) must NOT be treated as a double-click
		// — the !IsZero sentinel gate prevents it.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		f.clock = &fakeClock{t: time.Time{}} // zero-start clock
		f.Update(leftClick(0, 0))            // (0,0) is in main panel
		if got := p.counts[ActionSelect]; got != 0 {
			t.Errorf("zero-clock first click: ActionSelect count = %d; want 0", got)
		}
	})

	t.Run("different_panel_no_select", func(t *testing.T) {
		// Click in main, then click in alt: different panel ID → no double-click.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		clk := testClock()
		f.clock = clk
		f.Update(leftClick(5, 5))  // main panel
		f.Update(leftClick(50, 5)) // alt panel — different id
		if got := p.counts[ActionSelect]; got != 0 {
			t.Errorf("different-panel: ActionSelect count = %d; want 0", got)
		}
	})

	t.Run("non_left_button_ignored", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		clk := testClock()
		f.clock = clk
		// Right-click in a panel — must not set focus or record a click.
		before := f.focus.Active()
		f.Update(tea.MouseClickMsg{X: 50, Y: 5, Button: tea.MouseRight})
		if f.focus.Active() != before {
			t.Errorf("right-click changed focus; want unchanged")
		}
		if p.counts[ActionSelect] != 0 {
			t.Errorf("right-click: ActionSelect count = %d; want 0", p.counts[ActionSelect])
		}
		if !f.lastClick.t.IsZero() {
			t.Error("right-click recorded a lastClick; want zero sentinel")
		}
	})

	t.Run("click_with_overlay_does_not_pop_it", func(t *testing.T) {
		f, _ := newMouseFrame(t, 80, frameGoldenHeight)
		f.Update(key("?")) // open help overlay
		if f.overlay.Empty() {
			t.Fatal("overlay did not open")
		}
		f.Update(leftClick(5, 5)) // click anywhere while overlay is open
		if f.overlay.Empty() {
			t.Error("click while overlay open dismissed the overlay; want swallow (no dismiss)")
		}
	})

	t.Run("double_click_swallowed_while_capturing", func(t *testing.T) {
		// While the plugin captures raw input (inline filter, no overlay) a
		// double-click must NOT dispatch ActionSelect — it would quit and run
		// the focused command behind an active filter. Mirrors the keyboard
		// capture branch, which forwards keys raw and never dispatches.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		clk := testClock()
		f.clock = clk
		p.capturing = true
		f.Update(leftClick(5, 5)) // first click — swallowed, lastClick stays zero
		f.Update(leftClick(5, 5)) // second click — must NOT synthesize Select
		if got := p.counts[ActionSelect]; got != 0 {
			t.Errorf("capturing double-click: ActionSelect count = %d; want 0", got)
		}
		if !f.lastClick.t.IsZero() {
			t.Error("capturing click recorded a lastClick; want zero sentinel")
		}
	})

	t.Run("click_does_not_change_focus_while_capturing", func(t *testing.T) {
		// A click on another panel must not drift focus mid-query.
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		p.capturing = true
		before := f.focus.Active()
		f.Update(leftClick(50, 5)) // alt panel click — swallowed
		if got := f.focus.Active(); got != before {
			t.Errorf("capturing click changed focus to %q; want unchanged %q", got, before)
		}
		if len(p.panelClicks()) != 0 {
			t.Error("capturing click forwarded a PanelClickMsg; want none")
		}
	})

	t.Run("release_message_ignored", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		before := len(p.counts)
		_, cmd := f.Update(tea.MouseReleaseMsg{})
		if cmd != nil {
			t.Error("MouseReleaseMsg produced a cmd; want nil")
		}
		if len(p.counts) != before {
			t.Error("MouseReleaseMsg updated action counts; want no-op")
		}
	})

	t.Run("motion_message_ignored", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		before := len(p.counts)
		_, cmd := f.Update(tea.MouseMotionMsg{})
		if cmd != nil {
			t.Error("MouseMotionMsg produced a cmd; want nil")
		}
		if len(p.counts) != before {
			t.Error("MouseMotionMsg updated action counts; want no-op")
		}
	})
}

// TestFrame_PanelClickAndFocusDelivery covers the Task 2 seam: single-click
// inside a panel's content forwards a PanelClickMsg with panel-local coords and
// sets focus; a border click sets focus but emits NO PanelClickMsg; Tab emits a
// FocusChangedMsg; and a double-click moves the cursor (first click) then
// dispatches Select without a second PanelClickMsg.
//
// Geometry at 80x24 (weights {1,1}):
//
//	main outer {0,0,40,23},  content X∈[2,38),  Y∈[1,22)
//	alt  outer {40,0,40,23}, content X∈[42,78), Y∈[1,22)
func TestFrame_PanelClickAndFocusDelivery(t *testing.T) {
	t.Run("content_click_forwards_panel_local_coords_and_focus", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		f.Update(leftClick(50, 5)) // alt panel content
		if got := f.focus.Active(); got != mousePanelAlt {
			t.Fatalf("focus = %q; want %q", got, mousePanelAlt)
		}
		clicks := p.panelClicks()
		if len(clicks) != 1 {
			t.Fatalf("PanelClickMsg count = %d; want 1 (%+v)", len(clicks), p.msgs)
		}
		want := PanelClickMsg{Panel: mousePanelAlt, X: 50 - 42, Y: 5 - 1}
		if clicks[0] != want {
			t.Errorf("PanelClickMsg = %+v; want %+v", clicks[0], want)
		}
		// Focus moved main→alt, so exactly one FocusChangedMsg.
		fc := p.focusChanges()
		if len(fc) != 1 || fc[0].Panel != mousePanelAlt {
			t.Errorf("FocusChangedMsg = %+v; want one {alt}", fc)
		}
	})

	t.Run("border_click_sets_focus_but_no_panel_click", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		// (40,5): inside alt OUTER (left border column) but outside its content
		// (content X starts at 42).
		f.Update(leftClick(40, 5))
		if got := f.focus.Active(); got != mousePanelAlt {
			t.Errorf("border click focus = %q; want %q", got, mousePanelAlt)
		}
		if clicks := p.panelClicks(); len(clicks) != 0 {
			t.Errorf("border click emitted PanelClickMsg %+v; want none", clicks)
		}
	})

	t.Run("click_same_panel_no_focus_change_still_forwards_click", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		// Initial focus is main; clicking main content must NOT emit
		// FocusChangedMsg but must still forward the PanelClickMsg.
		f.Update(leftClick(5, 5))
		if fc := p.focusChanges(); len(fc) != 0 {
			t.Errorf("same-panel click emitted FocusChangedMsg %+v; want none", fc)
		}
		clicks := p.panelClicks()
		want := PanelClickMsg{Panel: mousePanelMain, X: 5 - 2, Y: 5 - 1}
		if len(clicks) != 1 || clicks[0] != want {
			t.Errorf("PanelClickMsg = %+v; want one %+v", clicks, want)
		}
	})

	t.Run("tab_emits_focus_changed", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		f.Update(key("tab"))
		if got := f.focus.Active(); got != mousePanelAlt {
			t.Fatalf("after tab focus = %q; want %q", got, mousePanelAlt)
		}
		fc := p.focusChanges()
		if len(fc) != 1 || fc[0].Panel != mousePanelAlt {
			t.Errorf("FocusChangedMsg after tab = %+v; want one {alt}", fc)
		}
		// Shift+Tab back to main emits another.
		f.Update(key("shift+tab"))
		if fc := p.focusChanges(); len(fc) != 2 || fc[1].Panel != mousePanelMain {
			t.Errorf("FocusChangedMsg after shift+tab = %+v; want second {main}", fc)
		}
	})

	t.Run("double_click_moves_cursor_then_selects", func(t *testing.T) {
		f, p := newMouseFrame(t, 80, frameGoldenHeight)
		clk := testClock()
		f.clock = clk
		f.Update(leftClick(5, 5)) // first — moves cursor (PanelClickMsg)
		f.Update(leftClick(5, 5)) // second — double-click → Select, no 2nd PanelClickMsg
		if got := len(p.panelClicks()); got != 1 {
			t.Errorf("PanelClickMsg count over a double-click = %d; want 1", got)
		}
		if got := p.counts[ActionSelect]; got != 1 {
			t.Errorf("ActionSelect count = %d; want 1", got)
		}
	})
}

// TestFrame_PanelLocal verifies the panelLocal coordinate-translation contract
// for bordered panels at several geometry buckets.
func TestFrame_PanelLocal(t *testing.T) {
	tests := []struct {
		name   string
		outer  Region
		x, y   int
		wantLX int
		wantLY int
	}{
		{
			name:  "main_panel_at_0_0",
			outer: Region{X: 0, Y: 0, Width: 40, Height: 23},
			// inner = {X:2, Y:1, W:36, H:21}; click at (5,5) → local (3,4)
			x: 5, y: 5, wantLX: 3, wantLY: 4,
		},
		{
			name:  "main_panel_top_left_inner",
			outer: Region{X: 0, Y: 0, Width: 40, Height: 23},
			// inner top-left: X=2, Y=1 → local (0,0)
			x: 2, y: 1, wantLX: 0, wantLY: 0,
		},
		{
			name:  "alt_panel_at_40_0",
			outer: Region{X: 40, Y: 0, Width: 40, Height: 23},
			// inner = {X:42, Y:1, W:36, H:21}; click at (50,5) → local (8,4)
			x: 50, y: 5, wantLX: 8, wantLY: 4,
		},
		{
			name:  "wide_panel_80_0",
			outer: Region{X: 0, Y: 0, Width: 80, Height: 23},
			// inner = {X:2, Y:1, W:76, H:21}; click at (10,10) → local (8,9)
			x: 10, y: 10, wantLX: 8, wantLY: 9,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lx, ly := panelLocal(tc.outer, tc.x, tc.y)
			if lx != tc.wantLX || ly != tc.wantLY {
				t.Errorf("panelLocal(%+v, %d, %d) = (%d,%d); want (%d,%d)",
					tc.outer, tc.x, tc.y, lx, ly, tc.wantLX, tc.wantLY)
			}
		})
	}
}

// assertGolden compares got against testdata/<name>, writing it when
// UPDATE_GOLDEN is set.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("creating testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		t.Logf("updated golden: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v", path, err)
	}
	if got != string(want) {
		t.Errorf("golden %s mismatch:\ngot:\n%s\n\nwant:\n%s", name, got, want)
	}
}
