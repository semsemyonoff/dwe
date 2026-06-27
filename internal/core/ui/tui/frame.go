package tui

import (
	"fmt"
	"os"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// frame.go assembles the framework's tea.Model. Frame owns the chrome (panel
// borders via the focus manager, the bottom status line, the terminal
// envelope) and geometry; the plugin owns body content and behaviour. The
// Update loop recomputes geometry on resize, dispatches keys through the
// registry, and forwards everything else to the plugin so plugin async
// messages survive. The View loop lays panels out left→right by weight,
// composites the active overlay centred over the body, appends the status line
// OUTSIDE the composite (so it is never dimmed), and returns a tea.View whose
// envelope fields (AltScreen, MouseMode) the framework owns.

// frameOptions are the private construction knobs for a [Frame]. They are
// defined here (not in RunOptions, Task 8) so the package builds in isolation
// after this task; Task 8's Run maps its public RunOptions into this struct.
type frameOptions struct {
	// mouse enables CellMotion mouse reporting (click + wheel, no motion) when
	// the terminal is capable. Set per-program via RunOptions.Mouse; the
	// rendered mode is MouseModeCellMotion only when this is true AND
	// mouseCapable() returns true.
	mouse bool
	// termEnv returns the $TERM value for the capability gate. Defaults to
	// os.Getenv("TERM") in newFrame; injectable from tests via withTermEnv.
	termEnv func() string
	// brand / project are the left-zone status-line strings (brand · project).
	brand   string
	project string
	// tr / locale resolve the help-modal display strings. Zero values mean "use
	// the framework defaults" (NopTranslator + "en"), applied in newFrame.
	tr     i18n.Translator
	locale string
}

// frameOption mutates the private frameOptions during [newFrame] construction.
type frameOption func(*frameOptions)

// withMouse enables or disables mouse reporting for this frame. When true,
// View emits MouseModeCellMotion (click + wheel) provided the terminal is
// capable (not TERM=dumb).
func withMouse(on bool) frameOption { return func(o *frameOptions) { o.mouse = on } }

// withTermEnv overrides the $TERM probe used by [Frame.mouseCapable]. Pass a
// function returning the desired TERM value; used only in in-package tests to
// avoid dependency on the real environment.
func withTermEnv(fn func() string) frameOption { return func(o *frameOptions) { o.termEnv = fn } }

// withBrand sets the status-line brand string.
func withBrand(s string) frameOption { return func(o *frameOptions) { o.brand = s } }

// withProject sets the status-line project string.
func withProject(s string) frameOption { return func(o *frameOptions) { o.project = s } }

// withTranslator sets the translator used to resolve help-modal display strings.
// A nil translator is ignored (newFrame falls back to i18n.NopTranslator).
func withTranslator(tr i18n.Translator) frameOption {
	return func(o *frameOptions) { o.tr = tr }
}

// withLocale sets the locale passed to the translator for help-modal strings.
// An empty locale is ignored (newFrame falls back to "en").
func withLocale(s string) frameOption { return func(o *frameOptions) { o.locale = s } }

// coalesceWindow is the wheel-burst coalescing interval. The first wheel event
// arms a one-shot tea.Tick; subsequent events within the window accumulate into
// wheelAccum; the tick fires wheelFlushMsg and the frame dispatches the net
// count as Nav steps. Provisional — tune if burst/slow feel is off.
const coalesceWindow = 16 * time.Millisecond

// doubleClickWindow is the maximum interval between two left-clicks in the same
// panel cell that triggers a double-click Select. Provisional — tune after
// real-device testing in the Stage 3 pilot.
const doubleClickWindow = 400 * time.Millisecond

// wheelFlushMsg is the private tick message that fires after coalesceWindow to
// dispatch the accumulated wheel delta as Nav steps. It must not fall through
// to plugin.Update (it is framework-internal).
type wheelFlushMsg struct{}

// frameClock is an injectable time source for double-click detection. The
// production implementation uses time.Now; tests inject a controlled fake.
type frameClock interface {
	now() time.Time
}

// realClock is the production frameClock, delegating to time.Now.
type realClock struct{}

func (realClock) now() time.Time { return time.Now() }

// lastClickRecord stores the previous left-click for double-click detection.
// The zero time.Time is the "no prior click" sentinel — it is never a valid
// prior event, so the !IsZero gate ensures the first click never triggers a
// double-click by itself.
type lastClickRecord struct {
	id   PanelID
	x, y int
	t    time.Time
}

// Frame is the framework's tea.Model. It is parameterised by a single [Plugin]
// and ties together the registry, focus manager, overlay stack, and geometry.
type Frame struct {
	plugin   Plugin
	registry *Registry
	focus    *focusManager
	overlay  overlayStack
	geo      Geometry
	opts     frameOptions

	// wheel accumulator — see handleMouse and the wheelFlushMsg case in Update.
	wheelAccum int
	wheelArmed bool

	// clock and lastClick support double-click detection — see handleClick.
	clock     frameClock
	lastClick lastClickRecord

	// tr / locale resolve help-modal display strings. Stage 0 uses a
	// NopTranslator (English fallbacks) + a fixed locale; the migration stages
	// thread real wiring through here.
	tr     i18n.Translator
	locale string
}

// newFrame constructs a [Frame], validating the plugin's contract BEFORE
// launch so a misconfigured plugin fails at construction, never at View time.
// It returns an error on an empty panel set, an empty or duplicate panel ID
// (the [PanelID] uniqueness invariant the focus manager and renderer key on), a
// non-positive panel weight, or a duplicate action/key surfaced by the plugin's
// Actions hook.
func newFrame(p Plugin, opts ...frameOption) (*Frame, error) {
	var fo frameOptions
	for _, o := range opts {
		o(&fo)
	}
	if fo.termEnv == nil {
		fo.termEnv = func() string { return os.Getenv("TERM") }
	}
	if fo.tr == nil {
		fo.tr = i18n.NopTranslator{}
	}
	if fo.locale == "" {
		fo.locale = "en"
	}

	panels := p.Panels()
	if len(panels) == 0 {
		return nil, fmt.Errorf("tui: plugin declares no panels")
	}
	seen := make(map[PanelID]struct{}, len(panels))
	for _, pl := range panels {
		if pl.ID == "" {
			return nil, fmt.Errorf("tui: panel has empty ID")
		}
		if _, dup := seen[pl.ID]; dup {
			return nil, fmt.Errorf("tui: duplicate panel ID %q", pl.ID)
		}
		seen[pl.ID] = struct{}{}
		if pl.Weight <= 0 {
			return nil, fmt.Errorf("tui: panel %q has non-positive weight %d", pl.ID, pl.Weight)
		}
	}

	reg := NewRegistry()
	if err := p.Actions(reg); err != nil {
		return nil, fmt.Errorf("tui: registering plugin actions: %w", err)
	}

	return &Frame{
		plugin:   p,
		registry: reg,
		focus:    newFocusManager(panels),
		opts:     fo,
		tr:       fo.tr,
		locale:   fo.locale,
		clock:    realClock{},
	}, nil
}

// mouseCapable reports whether the current terminal supports mouse reporting.
// We do NOT actively probe for mouse support: setting CellMotion on a terminal
// that does not understand the enable escape is harmless — the escape is simply
// ignored. The gate exists only to keep TERM=dumb (keyboard-only
// environments) from emitting the escape at all.
func (f *Frame) mouseCapable() bool { return f.opts.termEnv() != "dumb" }

// Init implements tea.Model. It delegates to the plugin so plugin startup
// commands run; the framework has no startup command of its own this stage.
func (f *Frame) Init() tea.Cmd { return f.plugin.Init() }

// Update implements tea.Model.
//
// Routing rules:
//   - tea.WindowSizeMsg recomputes geometry, calls plugin.Resize(body), and is
//     additionally forwarded to plugin.Update (it is a non-key message).
//   - Key messages route through the registry: built-ins (help/focus/quit) are
//     framework-handled and never reach the plugin; a matched plugin action
//     goes to plugin.HandleAction; an unmatched (or plugin-unhandled) key is
//     forwarded raw to plugin.Update. When an overlay is open, "esc" closes the
//     overlay (taking precedence over its ActionQuit alias) and otherwise ONLY
//     the help-close / quit built-ins act — plugin action keys are SWALLOWED,
//     not routed (no acting behind the modal).
//   - Every non-key message is always forwarded to plugin.Update (async
//     preservation), including while the help overlay is open.
//   - tea.MouseMsg is routed to handleMouse: wheel events accumulate for
//     burst coalescing; left-click events are classified via classifyHit and
//     routed (help-hint → help, panel → focus + double-click, blank → swallow);
//     release and motion events are silently ignored.
//
// After both a HandleAction call and a plugin.Update forward the framework
// drains plugin.PendingOverlay so an action-triggered overlay appears
// immediately.
func (f *Frame) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		f.geo = newGeometry(m.Width, m.Height)
		f.plugin.Resize(f.geo.Inner)
		cmd := f.plugin.Update(msg)
		f.drainOverlay()
		return f, cmd
	case tea.KeyPressMsg:
		return f.handleKey(m)
	case tea.MouseMsg:
		return f.handleMouse(m)
	case wheelFlushMsg:
		// Flush the wheel accumulator as Nav steps. No-op while an overlay is
		// open — a tick armed before the modal opened must not dispatch Nav
		// behind it (the accumulator was already cleared when the overlay
		// opened, so this is a true no-op with no generation/token machinery).
		if !f.overlay.Empty() {
			f.wheelAccum = 0
			f.wheelArmed = false
			return f, nil
		}
		accum := f.wheelAccum
		f.wheelAccum = 0
		f.wheelArmed = false
		if accum == 0 {
			return f, nil
		}
		var event string
		steps := accum
		if accum < 0 {
			event = "wheel-up"
			steps = -accum
		} else {
			event = "wheel-down"
		}
		action, ok := f.registry.MatchMouse(event)
		if !ok {
			return f, nil
		}
		var cmds []tea.Cmd
		for range steps {
			if cmd, handled := f.plugin.HandleAction(action); handled {
				cmds = append(cmds, cmd)
			}
		}
		f.drainOverlay()
		return f, tea.Batch(cmds...)
	default:
		cmd := f.plugin.Update(msg)
		f.drainOverlay()
		return f, cmd
	}
}

// handleKey applies the modal-aware key-routing policy described on [Frame.Update].
func (f *Frame) handleKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	keyStr := key.String()

	if !f.overlay.Empty() {
		// A capturing overlay (Overlay.CapturesInput) routes raw input to the
		// plugin: only ctrl+c (hard-quit) and esc (close overlay) survive as
		// framework actions; ? does not open help. See routeWhileCapturing.
		if top, ok := f.overlay.Top(); ok && top.CapturesInput {
			switch routeWhileCapturing(key) {
			case captureHardQuit:
				return f, tea.Quit
			case captureClose:
				f.overlay.Pop()
				// Crossing the overlay boundary resets double-click tracking.
				f.lastClick = lastClickRecord{}
				return f, nil
			default: // captureSwallowToPlugin
				cmd := f.plugin.Update(key)
				f.drainOverlay()
				return f, cmd
			}
		}

		// Modal open: the frame's modal-input policy takes precedence over the
		// registry. "esc" closes the overlay — it never reaches its ActionQuit
		// alias while a modal is open (the Binding.Aliases precedence rule), so
		// dismissing a modal must not quit the program. "?" toggles help closed
		// and "q"/"ctrl+c" quit (the help-close / quit built-ins). Everything
		// else (plugin actions, focus cycling, raw keys) is swallowed so the
		// body never acts behind the modal.
		if keyStr == "esc" {
			f.overlay.Pop()
			// Crossing the overlay boundary resets double-click tracking — see
			// the ActionHelp branch in handleBuiltin.
			f.lastClick = lastClickRecord{}
			return f, nil
		}
		if a, ok := f.registry.Match(keyStr); ok {
			switch a {
			case ActionHelp, ActionQuit:
				return f.handleBuiltin(a)
			}
		}
		return f, nil
	}

	// No overlay, but the plugin is taking raw input (e.g. an inline filter):
	// suspend registry dispatch and forward every key to plugin.Update,
	// reserving only ctrl+c as a hard-quit. esc/enter/printables reach the
	// plugin so it drives its own capture state machine.
	if f.plugin.CapturingInput() {
		if keyStr == "ctrl+c" {
			return f, tea.Quit
		}
		cmd := f.plugin.Update(key)
		f.drainOverlay()
		return f, cmd
	}

	if a, ok := f.registry.Match(keyStr); ok {
		if isBuiltin(a) {
			return f.handleBuiltin(a)
		}
		if cmd, handled := f.plugin.HandleAction(a); handled {
			f.drainOverlay()
			return f, cmd
		}
		// Matched a registry action the plugin declined — fall through and
		// forward the raw key so plugin.Update still sees it.
	}

	cmd := f.plugin.Update(key)
	f.drainOverlay()
	return f, cmd
}

// handleBuiltin runs a framework-owned action. Built-ins never reach the plugin.
func (f *Frame) handleBuiltin(a Action) (tea.Model, tea.Cmd) {
	switch a {
	case ActionHelp:
		// Either branch crosses an overlay boundary, so reset the double-click
		// record: a panel click followed by a keyboard help open/close must not
		// pair with a later click on the same cell into a phantom double-click.
		f.lastClick = lastClickRecord{}
		if f.overlay.Empty() {
			// Clear any pending wheel accumulation before pushing the modal.
			// tea.Tick is one-shot and uncancellable; resetting here ensures
			// the wheelFlushMsg guard (no-op while overlay open) sees an empty
			// accumulator even if a tick fires after the modal opens.
			f.wheelAccum = 0
			f.wheelArmed = false
			f.overlay.Push(buildHelpOverlay(f.registry, f.tr, f.locale, f.geo.Overlay.Width, f.geo.Overlay.Height))
		} else {
			f.overlay.Pop()
		}
	case ActionQuit:
		return f, tea.Quit
	case ActionFocusNext:
		return f.cycleFocus(f.focus.Next)
	case ActionFocusPrev:
		return f.cycleFocus(f.focus.Prev)
	}
	return f, nil
}

// cycleFocus applies a focus move (Next/Prev) and, when it lands on a DIFFERENT
// panel, forwards a [FocusChangedMsg] to the plugin so it can retarget
// navigation. Tab/Shift+Tab are framework built-ins handled entirely in
// handleBuiltin and never reach the plugin otherwise, so this forward is the
// only way the plugin learns of keyboard-driven focus changes.
func (f *Frame) cycleFocus(move func()) (tea.Model, tea.Cmd) {
	before := f.focus.Active()
	move()
	after := f.focus.Active()
	if after == before {
		return f, nil
	}
	cmd := f.plugin.Update(FocusChangedMsg{Panel: after})
	f.drainOverlay()
	return f, cmd
}

// drainOverlay pushes a plugin-requested overlay onto the stack. Mutual
// exclusivity is structural (View only ever composites Top), so Push is safe.
// It also clears any pending wheel accumulation so a stale tick cannot
// dispatch Nav behind a plugin-triggered modal, and resets the double-click
// record so a click before the modal can never pair with one after it.
func (f *Frame) drainOverlay() {
	if ov, ok := f.plugin.PendingOverlay(); ok {
		f.wheelAccum = 0
		f.wheelArmed = false
		f.lastClick = lastClickRecord{}
		f.overlay.Push(ov)
	}
}

// handleMouse dispatches a tea.MouseMsg. Wheel events are accumulated for
// burst coalescing (see wheelFlushMsg case in Update); left-click events are
// classified via classifyHit and routed by zone; release and motion messages
// are silently ignored — CellMotion can emit motion while a button is held
// but we do not act on it.
func (f *Frame) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.MouseWheelMsg:
		if !f.overlay.Empty() {
			return f, nil
		}
		switch m.Button {
		case tea.MouseWheelUp:
			f.wheelAccum--
		case tea.MouseWheelDown:
			f.wheelAccum++
		default:
			// Horizontal wheel (MouseWheelLeft/Right, emitted by trackpads in
			// CellMotion mode) carries no Nav mapping in Stage 2; ignore it so
			// it never arms a tick or pollutes the vertical accumulator.
			return f, nil
		}
		if !f.wheelArmed {
			f.wheelArmed = true
			return f, tea.Tick(coalesceWindow, func(time.Time) tea.Msg { return wheelFlushMsg{} })
		}
		return f, nil
	case tea.MouseClickMsg:
		return f.handleClick(m)
	case tea.MouseReleaseMsg, tea.MouseMotionMsg:
		// CellMotion can emit release and motion events while a button is
		// held; neither triggers any frame or plugin action.
		return f, nil
	default:
		return f, nil
	}
}

// handleClick processes a left-click by classifying the hit zone first:
//   - overlay open → both zoneModal and zoneOutsideModal are swallowed and
//     clear lastClick; outside does NOT dismiss the overlay (locked Stage 2
//     policy)
//   - zoneHelpHint → toggle the help overlay; clear lastClick
//   - zonePanel → set focus; then double-click test (same panel + same cell +
//     within doubleClickWindow + lastClick not zero-sentinel); record otherwise
//   - zoneNone / status space → swallow; clear lastClick so two blank-space
//     clicks can never synthesize a Select
//
// Non-left buttons are silently ignored.
func (f *Frame) handleClick(m tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if m.Button != tea.MouseLeft {
		return f, nil
	}
	var ov *Overlay
	if top, ok := f.overlay.Top(); ok {
		ov = &top
	}
	zone, id := classifyHit(f.geo, f.panelRects(), f.helpHintRegion(), ov, m.X, m.Y)
	switch zone {
	case zoneModal, zoneOutsideModal:
		// Swallowed while a modal is open. Clear the double-click record so a
		// click before the overlay opened can never pair with one after it
		// closes into a phantom double-click.
		f.lastClick = lastClickRecord{}
		return f, nil
	case zoneHelpHint:
		f.lastClick = lastClickRecord{}
		return f.handleBuiltin(ActionHelp)
	case zonePanel:
		// Setting focus may move it to a different panel; forward a
		// FocusChangedMsg in that case (a click on the already-focused panel
		// emits nothing).
		before := f.focus.Active()
		f.focus.Set(id)
		var cmds []tea.Cmd
		if after := f.focus.Active(); after != before {
			cmds = append(cmds, f.plugin.Update(FocusChangedMsg{Panel: after}))
		}
		if !f.lastClick.t.IsZero() &&
			f.lastClick.id == id &&
			f.lastClick.x == m.X &&
			f.lastClick.y == m.Y &&
			f.clock.now().Sub(f.lastClick.t) < doubleClickWindow {
			// Double-click confirmed. Clear the full record so a third click
			// in the same cell within the window does not re-trigger Select.
			// No PanelClickMsg is emitted — the first click of the pair already
			// moved the cursor; this click selects.
			f.lastClick = lastClickRecord{}
			if action, ok := f.registry.MatchMouse("double-click"); ok {
				if cmd, handled := f.plugin.HandleAction(action); handled {
					cmds = append(cmds, cmd)
				}
			}
			f.drainOverlay()
			return f, tea.Batch(cmds...)
		}
		f.lastClick = lastClickRecord{id: id, x: m.X, y: m.Y, t: f.clock.now()}
		// Single click: forward a panel-local PanelClickMsg ONLY when the click
		// lands inside the inner content region. zonePanel covers the panel's
		// OUTER region (border included), so a border click would yield
		// negative/out-of-content coords — focus is set but no message fires.
		if outer, ok := f.panelOuter(id); ok {
			if content := contentRegion(outer); content.contains(m.X, m.Y) {
				lx, ly := panelLocal(outer, m.X, m.Y)
				cmds = append(cmds, f.plugin.Update(PanelClickMsg{Panel: id, X: lx, Y: ly}))
			}
		}
		f.drainOverlay()
		return f, tea.Batch(cmds...)
	default: // zoneNone
		f.lastClick = lastClickRecord{}
		return f, nil
	}
}

// panelLocal translates an absolute terminal click (x, y) to coordinates
// local to the inner content region of the panel whose outer region is outer.
// Local (0, 0) is the top-left cell of the inner region.
//
// The panel-facing click route — row-select and tab-switch forward to plugin —
// lands with the Stage 3 cmdbrowser pilot (mirroring Stage 1's
// routeWhileCapturing deferral). This helper locks the coordinate contract so
// the Stage 3 consumer can rely on it without re-deriving the geometry.
func panelLocal(outer Region, x, y int) (lx, ly int) {
	inner := contentRegion(outer)
	return x - inner.X, y - inner.Y
}

// isBuiltin reports whether a is a framework-owned action.
func isBuiltin(a Action) bool {
	switch a {
	case ActionHelp, ActionQuit, ActionFocusNext, ActionFocusPrev:
		return true
	default:
		return false
	}
}

// captureDecision classifies a message under the capturing-overlay input policy.
// It is the return type of [routeWhileCapturing].
type captureDecision int

const (
	// captureSwallowToPlugin routes the message to the plugin (registry bypassed).
	captureSwallowToPlugin captureDecision = iota
	// captureHardQuit exits the program immediately (ctrl+c hard-quit path).
	captureHardQuit
	// captureClose dismisses the capturing overlay (esc close-overlay path).
	captureClose
)

// routeWhileCapturing classifies msg under the capturing-overlay input policy.
// It is called when the top overlay has [Overlay.CapturesInput] true. While
// such an overlay is Top(), raw input (including printable characters) routes
// to the plugin (registry bypassed), and only ctrl+c (hard-quit) and esc
// (close overlay) survive as framework actions. ? does NOT open help.
//
// This is the exact function frame.Update will call in Stage 3 (drop-in
// integration, not a throwaway shape). The full frame.Update rewiring lands
// with the Stage 3 filter consumer; this stage locks and tests the contract.
func routeWhileCapturing(msg tea.Msg) captureDecision {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return captureSwallowToPlugin
	}
	switch key.String() {
	case "ctrl+c":
		return captureHardQuit
	case "esc":
		return captureClose
	default:
		return captureSwallowToPlugin
	}
}

// View implements tea.Model. It lays the body panels out left→right by weight,
// renders each through the plugin into its inner region, draws focus-aware
// borders, composites the active overlay centred over the body, and appends the
// status line beneath. The returned tea.View carries the framework-owned
// envelope (AltScreen on; CellMotion mouse when enabled and capable).
func (f *Frame) View() tea.View {
	body := f.renderBody()
	if ov, ok := f.overlay.Top(); ok {
		body = Composite(body, ov, f.geo.Overlay)
	}
	status := f.renderStatusLine(f.geo.Status.Width)

	content := body
	if status != "" {
		content = body + "\n" + status
	}

	v := tea.NewView(content)
	// AltScreen is a tea.View field in bubbletea/v2 (not a program option) —
	// the framework owns it so callers never put the program in full-window
	// mode themselves.
	v.AltScreen = true
	// CellMotion enables click + wheel reporting without motion spam. We use
	// the fixed CellMotion mode (not AllMotion) per the spec. The gate only
	// suppresses the enable escape on TERM=dumb; on any other terminal the
	// escape is harmless even if the terminal does not understand it.
	if f.opts.mouse && f.mouseCapable() {
		v.MouseMode = tea.MouseModeCellMotion
	} else {
		v.MouseMode = tea.MouseModeNone
	}
	return v
}

// renderBody lays out and renders the bordered body panels into a single
// outer-sized string. Each panel's outer region comes from [layoutPanels]
// (validated weights from newFrame); the inner region is the single
// outer→inner subtraction the plugin renders into. The focus manager supplies
// each panel's border style, sized to its outer region so focusing never
// shifts the layout.
func (f *Frame) renderBody() string {
	panels := f.plugin.Panels()
	weights := make([]int, len(panels))
	for i, p := range panels {
		weights[i] = p.Weight
	}
	outers := layoutPanels(f.geo.Outer, weights)

	rendered := make([]string, len(panels))
	for i, p := range panels {
		outer := outers[i]
		inner := contentRegion(outer)
		content := f.plugin.ViewPanel(p.ID, inner)
		style := f.focus.BorderFor(p.ID).
			Padding(0, hPadding).
			Width(outer.Width).
			Height(outer.Height)
		rendered[i] = style.Render(content)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}

// renderStatusLine builds the bottom three-zone status line: brand · project on
// the left, the plugin's StatusContext in the centre, and the help-key hint on
// the right. The centre zone is truncated to whatever space the fixed sides
// leave, so the line is always exactly width cells.
func (f *Frame) renderStatusLine(width int) string {
	if width <= 0 {
		return ""
	}

	left := f.brandSegment()
	right := f.helpHint()
	middle := f.plugin.StatusContext()

	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorMuted()))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorAccent())).Bold(true)

	leftR := accent.Render(left)
	rightR := muted.Render(right)

	lw := lipgloss.Width(leftR)
	rw := lipgloss.Width(rightR)
	if lw+rw >= width {
		// No room for the centre zone — clamp the sides to the width.
		return lipgloss.NewStyle().Width(width).MaxWidth(width).Render(leftR + rightR)
	}

	midSpace := width - lw - rw
	mid := muted.MaxWidth(midSpace).Render(middle)
	mid = lipgloss.NewStyle().Width(midSpace).Align(lipgloss.Center).Render(mid)
	return leftR + mid + rightR
}

// brandSegment formats the left status zone as "brand · project" (omitting
// whichever parts are empty).
func (f *Frame) brandSegment() string {
	switch {
	case f.opts.brand != "" && f.opts.project != "":
		return f.opts.brand + " · " + f.opts.project
	case f.opts.brand != "":
		return f.opts.brand
	default:
		return f.opts.project
	}
}

// helpHint formats the right status zone from the registry's help binding, e.g.
// "? help", so the hint key stays in sync with the registered binding.
func (f *Frame) helpHint() string {
	key := "?"
	if b, ok := f.registry.Binding(ActionHelp); ok && len(b.Keys) > 0 {
		key = b.Keys[0]
	}
	return key + " help"
}

// helpHintRegion returns the status-line cell range occupied by the rendered
// help-key hint. The region and the rendered hint share the same width source
// (muted.Render width measurement) so the hit zone and the visible text can
// never drift apart.
func (f *Frame) helpHintRegion() Region {
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorMuted()))
	rw := lipgloss.Width(muted.Render(f.helpHint()))
	return Region{
		X:      f.geo.Status.Width - rw,
		Y:      f.geo.Status.Y,
		Width:  rw,
		Height: 1,
	}
}

// panelRects returns the outer region of each body panel in plugin declaration
// order. The regions are computed via layoutPanels from the panel weights —
// the same call renderBody makes — so hit-test regions match what is rendered.
func (f *Frame) panelRects() []panelRect {
	panels := f.plugin.Panels()
	weights := make([]int, len(panels))
	for i, p := range panels {
		weights[i] = p.Weight
	}
	outers := layoutPanels(f.geo.Outer, weights)
	rects := make([]panelRect, len(panels))
	for i, p := range panels {
		rects[i] = panelRect{ID: p.ID, Region: outers[i]}
	}
	return rects
}

// panelOuter returns the outer region of the panel with the given ID, reusing
// the same layoutPanels math as panelRects/renderBody so the looked-up region
// matches what is rendered and hit-tested. The bool is false for an unknown ID.
func (f *Frame) panelOuter(id PanelID) (Region, bool) {
	for _, r := range f.panelRects() {
		if r.ID == id {
			return r.Region, true
		}
	}
	return Region{}, false
}
