package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
}

func newMousePlugin() *mousePlugin {
	return &mousePlugin{counts: make(map[Action]int)}
}

func (p *mousePlugin) Init() tea.Cmd                        { return nil }
func (p *mousePlugin) Close() error                         { return nil }
func (p *mousePlugin) Resize(Region)                        {}
func (p *mousePlugin) Update(tea.Msg) tea.Cmd               { return nil }
func (p *mousePlugin) ViewPanel(_ PanelID, _ Region) string { return "[mouse]" }
func (p *mousePlugin) Panels() []Panel {
	return []Panel{
		{ID: mousePanelMain, Title: "Main", Weight: 1},
		{ID: mousePanelAlt, Title: "Alt", Weight: 1},
	}
}
func (p *mousePlugin) StatusContext() string { return "" }
func (p *mousePlugin) Actions(reg *Registry) error {
	return RegisterStandard(reg, ActionNavUp, ActionNavDown, ActionSelect)
}
func (p *mousePlugin) HandleAction(a Action) (tea.Cmd, bool) {
	p.counts[a]++
	return nil, true
}
func (p *mousePlugin) PendingOverlay() (Overlay, bool) { return Overlay{}, false }
func (p *mousePlugin) Result() any                     { return nil }

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
			t.Error("first WheelDown: cmd = nil; want non-nil (tick)")
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
