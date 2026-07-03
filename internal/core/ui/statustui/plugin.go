package statustui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// panelMain is the sole panel ID for the status dashboard's single-panel
// body: tabs are stacked body content (tab strip + shared viewport), not
// side-by-side panels.
const panelMain tui.PanelID = "main"

// plugin is the status dashboard surface on the shared tui framework. It is a
// [tui.Plugin]: the Frame owns chrome (border, status line, `?` help modal,
// alt-screen, mouse); the plugin owns tab-strip + viewport body content.
//
// m holds the model state as a field (not embedded) so the model's helpers
// (setActiveTab, renderTabStrip, buildTabsCmd) are reused without promoting
// any tea.Model methods onto plugin.
type plugin struct {
	m      *model
	cancel context.CancelFunc
}

// Compile-time assertion that plugin implements tui.Plugin.
var _ tui.Plugin = (*plugin)(nil)

// newPlugin builds the plugin around an already-constructed model and the
// cancel function of the context that owns it. Close() calls cancel to stop
// any in-flight buildTabs goroutines.
func newPlugin(m *model, cancel context.CancelFunc) *plugin {
	return &plugin{m: m, cancel: cancel}
}

// Init implements tui.Plugin, preserving the model's existing
// loadGen++ / tea.Batch(spinner.Tick, buildTabsCmd) startup behavior verbatim.
func (p *plugin) Init() tea.Cmd {
	return p.m.Init()
}

// Close implements tui.Plugin. Cancels the run context so any in-flight
// buildTabs goroutines stop.
func (p *plugin) Close() error {
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

// Panels implements tui.Plugin. The status dashboard is a single-panel
// surface.
func (p *plugin) Panels() []tui.Panel {
	return []tui.Panel{{ID: panelMain, Weight: 1}}
}

// Result implements tui.Plugin. The status dashboard is quit-only.
func (p *plugin) Result() any { return nil }

// PendingOverlay implements tui.Plugin. The status dashboard never pushes its
// own overlay.
func (p *plugin) PendingOverlay() (tui.Overlay, bool) { return tui.Overlay{}, false }

// CapturingInput implements tui.Plugin. The status dashboard has no inline
// raw-input capture mode.
func (p *plugin) CapturingInput() bool { return false }

// Resize implements tui.Plugin. The plugin's viewport dimensions are computed
// in ViewPanel from the per-panel inner region it is given there, so there is
// nothing to cache here.
func (p *plugin) Resize(tui.Region) {}

// Update implements tui.Plugin. tabsLoadedMsg handling (stale-gen drop, tabs
// assign, loadedAt/healthIndicator, YOffset restore-on-matching-reload else
// GotoTop) and spinner.TickMsg are preserved VERBATIM from the legacy
// model.Update — see the plan's single most important invariant. Unmatched
// messages (e.g. viewport nav keys the registry left unbound) delegate to
// viewport.Update for scroll handling.
func (p *plugin) Update(msg tea.Msg) tea.Cmd {
	m := p.m

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// ViewPanel resizes the viewport from its per-panel inner region on
		// every render, so there is nothing to recompute here. Deliberately
		// NOT sizing from raw terminal width/height — that would ignore the
		// Frame's border/panel chrome. Sizing is owned by ViewPanel.
		return nil

	case tabsLoadedMsg:
		// Drop stale messages from older reloads.
		if msg.gen != m.loadGen {
			return nil
		}
		m.tabs = msg.tabs
		m.sectionAnchors = msg.anchors
		m.reloadAt = msg.loadedAt
		m.healthIndicator = msg.healthIndicator
		m.loading = false
		m.reloading = false

		// Restore YOffset if this is a reload that matches the active tab.
		if m.reloadGen == msg.gen && m.reloadActive == m.active && len(m.tabs) > m.active {
			m.viewport.SetContent(m.tabs[m.active].content)
			m.viewport.SetYOffset(m.reloadYOffset)
		} else if len(m.tabs) > m.active {
			m.viewport.SetContent(m.tabs[m.active].content)
			m.viewport.GotoTop()
		}
		m.reloadGen = 0
		return nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return cmd

	case tui.PanelClickMsg:
		return p.handlePanelClick(msg)

	case tui.WheelMsg:
		return p.handleWheel(msg)

	case tui.FocusChangedMsg:
		// Single-panel surface: focus never actually moves elsewhere. No-op,
		// kept explicit for parity with the other Frame plugins.
		return nil
	}

	// Delegate unmatched messages to viewport for scroll handling.
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return cmd
}

// ViewPanel implements tui.Plugin. Renders the single-panel body: a centered
// spinner while the initial load is in flight, otherwise the tab strip +
// divider + viewport content. Reuses the model's existing renderTabStrip
// helper so hit-zones in Task 6 match exactly what is drawn here.
func (p *plugin) ViewPanel(id tui.PanelID, inner tui.Region) string {
	if id != panelMain {
		return ""
	}
	if p.m.loading {
		return p.renderLoading(inner)
	}
	return p.renderBody(inner)
}

// renderLoading centers the spinner inside the panel's inner region while the
// initial tab load is in flight. Unlike the legacy full-screen loading view,
// this is body content only — the Frame still draws its chrome around it.
func (p *plugin) renderLoading(inner tui.Region) string {
	return lipgloss.NewStyle().
		Width(max(inner.Width, 0)).
		Height(max(inner.Height, 0)).
		Align(lipgloss.Center).
		AlignVertical(lipgloss.Center).
		Render(p.m.spinner.View())
}

// panelChromeRows is the number of body rows the tab strip + divider occupy
// above the viewport.
const panelChromeRows = 2

// renderBody sizes the viewport to the panel's inner region (minus the
// tab-strip and divider rows) and renders tab strip + divider + viewport
// content. Reloading state does not change body rendering — only
// StatusContext (Task 3) reflects it.
func (p *plugin) renderBody(inner tui.Region) string {
	m := p.m
	w := max(inner.Width, 0)

	m.viewport.SetWidth(w)
	m.viewport.SetHeight(max(inner.Height-panelChromeRows, 0))

	tabStrip := m.renderTabStrip()
	if tabStrip == "" {
		return m.viewport.View()
	}

	dividerLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(styles.ColorMuted())).
		Render(strings.Repeat("─", w))

	return lipgloss.JoinVertical(lipgloss.Top, tabStrip, dividerLine, m.viewport.View())
}

// StatusContext implements tui.Plugin. Returns the middle-zone status
// string: health indicator + "loaded X ago", or a loading/reloading state.
// This is the old renderStatusBar leftParts, minus the help text (the right
// side of the old status bar), which the Frame now supplies itself. Called
// every render so the content is reactive to the model's current state.
func (p *plugin) StatusContext() string {
	m := p.m
	var parts []string
	switch {
	case m.loading:
		parts = append(parts, "·", "loading…")
	case m.reloading:
		parts = append(parts, "·", "reloading…")
	case len(m.tabs) > 0 && m.deps.Cfg != nil:
		parts = append(parts, m.healthIndicator)
		if !m.reloadAt.IsZero() {
			elapsed := time.Since(m.reloadAt)
			parts = append(parts, fmt.Sprintf("loaded %v ago", elapsed.Round(time.Second)))
		}
	}
	return strings.Join(parts, "  ")
}
