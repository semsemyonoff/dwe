package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
// AltScreen is on and the mouse seam renders MouseModeNone this stage.
func TestFrame_View_Envelope(t *testing.T) {
	for _, mouse := range []bool{false, true} {
		f, _ := newTestFrame(t, 80, frameGoldenHeight, withMouse(mouse))
		v := f.View()
		if !v.AltScreen {
			t.Errorf("mouse=%v: View must request AltScreen", mouse)
		}
		if v.MouseMode != tea.MouseModeNone {
			t.Errorf("mouse=%v: MouseMode = %v; want MouseModeNone (Stage 2 seam inert)", mouse, v.MouseMode)
		}
	}
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

// TestFrame_MouseMsgSwallowed asserts a mouse message is ignored this stage: it
// never reaches the plugin and the frame returns no command (the inert Stage 2 seam).
func TestFrame_MouseMsgSwallowed(t *testing.T) {
	f, p := newTestFrame(t, 80, frameGoldenHeight)
	before := len(p.gotMsgs)
	_, cmd := f.Update(tea.MouseClickMsg{})
	if cmd != nil {
		t.Errorf("mouse message produced a command; want nil (ignored this stage)")
	}
	if len(p.gotMsgs) != before {
		t.Errorf("mouse message leaked to plugin.Update; gotMsgs grew from %d to %d", before, len(p.gotMsgs))
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
