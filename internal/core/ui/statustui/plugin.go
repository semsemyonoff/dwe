package statustui

import (
	"context"

	tea "charm.land/bubbletea/v2"

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
// m holds the existing model state as a field, NOT embedded — embedding would
// promote the legacy *model's tea.Model methods (View/Update) onto plugin,
// which does not satisfy tui.Plugin and would defeat the point of keeping the
// two launch surfaces independent during the Task 1-6 coexistence window (see
// the plan's compile-clean coexistence rule). The legacy *model launch path
// (View(), the legacy Update, keys.go, renderTitleBar/renderStatusBar) stays
// intact and is the live `dwe status` launch path until the Task 7 cutover.
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

// Resize implements tui.Plugin. Stubbed here; filled in Task 2.
func (p *plugin) Resize(body tui.Region) {}

// Update implements tui.Plugin. Stubbed here; filled in Task 5.
func (p *plugin) Update(msg tea.Msg) tea.Cmd { return nil }

// ViewPanel implements tui.Plugin. Stubbed here; filled in Task 2.
func (p *plugin) ViewPanel(id tui.PanelID, inner tui.Region) string { return "" }

// StatusContext implements tui.Plugin. Stubbed here; filled in Task 3.
func (p *plugin) StatusContext() string { return "" }

// Actions implements tui.Plugin. Stubbed here; filled in Task 4.
func (p *plugin) Actions(reg *tui.Registry) error { return nil }

// HandleAction implements tui.Plugin. Stubbed here; filled in Task 4.
func (p *plugin) HandleAction(a tui.Action) (tea.Cmd, bool) { return nil, false }
