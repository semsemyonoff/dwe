package tui

import (
	"fmt"

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
	// mouse gates the Stage 2 mouse seam. It is plumbed through to View but
	// rendered as tea.MouseModeNone this stage regardless of its value.
	mouse bool
	// brand / project are the left-zone status-line strings (brand · project).
	brand   string
	project string
}

// frameOption mutates the private frameOptions during [newFrame] construction.
type frameOption func(*frameOptions)

// withMouse sets the (inert, Stage 2) mouse seam flag.
func withMouse(on bool) frameOption { return func(o *frameOptions) { o.mouse = on } }

// withBrand sets the status-line brand string.
func withBrand(s string) frameOption { return func(o *frameOptions) { o.brand = s } }

// withProject sets the status-line project string.
func withProject(s string) frameOption { return func(o *frameOptions) { o.project = s } }

// Frame is the framework's tea.Model. It is parameterised by a single [Plugin]
// and ties together the registry, focus manager, overlay stack, and geometry.
type Frame struct {
	plugin   Plugin
	registry *Registry
	focus    *focusManager
	overlay  overlayStack
	geo      Geometry
	opts     frameOptions

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
		tr:       i18n.NopTranslator{},
		locale:   "en",
	}, nil
}

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
//     forwarded raw to plugin.Update. When an overlay is open, ONLY the
//     help-close / quit built-ins act — plugin action keys are SWALLOWED, not
//     routed (no acting behind the modal).
//   - Every non-key message is always forwarded to plugin.Update (async
//     preservation), including while the help overlay is open.
//   - tea.MouseMsg is ignored this stage (Stage 2 seam).
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
		// Stage 2: the mouse layer lands here. For now mouse messages are
		// ignored so the (inert) seam never acts.
		return f, nil
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
		// Modal open: only the help-close and quit built-ins act; everything
		// else (plugin actions, focus cycling, raw keys) is swallowed so the
		// body never acts behind the modal.
		if a, ok := f.registry.Match(keyStr); ok {
			switch a {
			case ActionHelp, ActionQuit:
				return f.handleBuiltin(a)
			}
		}
		return f, nil
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
		if f.overlay.Empty() {
			f.overlay.Push(buildHelpOverlay(f.registry, f.tr, f.locale, f.geo.Overlay.Width, f.geo.Overlay.Height))
		} else {
			f.overlay.Pop()
		}
	case ActionQuit:
		return f, tea.Quit
	case ActionFocusNext:
		f.focus.Next()
	case ActionFocusPrev:
		f.focus.Prev()
	}
	return f, nil
}

// drainOverlay pushes a plugin-requested overlay onto the stack. Mutual
// exclusivity is structural (View only ever composites Top), so Push is safe.
func (f *Frame) drainOverlay() {
	if ov, ok := f.plugin.PendingOverlay(); ok {
		f.overlay.Push(ov)
	}
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
// envelope (AltScreen on; the inert mouse seam).
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
	// Stage 2 mouse seam: the opts.mouse flag is plumbed but the rendered mode
	// is hardcoded to None this stage. Stage 2 lights this up.
	v.MouseMode = tea.MouseModeNone
	_ = f.opts.mouse // read so the seam is wired; inert until Stage 2.
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
