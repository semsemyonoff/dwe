package docstui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// Panel IDs for the two-panel docs browser. The tree (contents) sits on the
// left; the viewport (rendered markdown) on the right. They match the Frame
// focus order: index-0 (tree) is focused at launch.
const (
	panelTree     tui.PanelID = "tree"
	panelViewport tui.PanelID = "viewport"
)

// browser is the docs browser surface on the tui framework. It is a
// [tui.Plugin]: the Frame owns chrome (borders, focus highlight, Tab cycling,
// geometry, the status line) and the browser owns body content and behaviour.
//
// The existing *Model remains untouched and compilable through the Task 6–9
// coexistence window (its Init/Update/View/quit are not changed here). The
// browser embeds *Model to reuse its Tree/Viewport/Filter/DiagramState/
// heading-index/loaded-topic state, mirroring how cmdbrowser's browser held
// its data without deleting the old model first.
type browser struct {
	*Model

	// active is the currently focused panel. Tracks the Frame's focus manager
	// (initial index-0 panel == tree) via FocusChangedMsg so nav and scroll
	// route to the right widget.
	active tui.PanelID

	// body is the overall inner body region cached on Resize.
	body tui.Region

	// treeInner / viewportInner are the per-panel inner regions cached on
	// ViewPanel so mouse translation and re-renders can reuse them.
	treeInner     tui.Region
	viewportInner tui.Region

	tr     i18n.Translator
	locale string
}

// Compile-time guarantee that *browser satisfies the tui.Plugin contract.
var _ tui.Plugin = (*browser)(nil)

// newBrowser builds the plugin from the same inputs as the legacy NewModel
// (passed through as a *Model). Sizes are deferred — the Frame supplies
// geometry through Resize/ViewPanel, so the viewport starts at zero width and
// is sized on the first render pass. Translator and locale are read from the
// model (nil-safe).
func newBrowser(m *Model) *browser {
	tr := m.Translator
	if tr == nil {
		tr = i18n.NopTranslator{}
	}
	return &browser{
		Model:  m,
		active: panelTree,
		tr:     tr,
		locale: m.Locale,
	}
}

// Init implements tui.Plugin. Stub — async lifecycle wires in Task 6.
func (b *browser) Init() tea.Cmd { return nil }

// Close implements tui.Plugin. Stub — teardown wires in Task 6.
func (b *browser) Close() error { return nil }

// Panels implements tui.Plugin. Two static panels: tree (left, weight 1) and
// viewport (right, weight 5). The {1,5} split is the starting ratio validated
// against the 60/79/80/99/100 goldens in Task 11; adjust toward {2,7}/{2,5} if
// the tree inner width is too narrow at the 60-col bucket.
func (b *browser) Panels() []tui.Panel {
	return []tui.Panel{
		{ID: panelTree, Title: "Contents", Weight: 1},
		{ID: panelViewport, Title: "", Weight: 5},
	}
}

// Resize implements tui.Plugin. The Frame owns geometry; the browser caches
// the overall inner body region. Per-panel inner regions arrive separately
// through ViewPanel.
func (b *browser) Resize(body tui.Region) { b.body = body }

// CapturingInput implements tui.Plugin. The browser takes raw input without
// an overlay only while the inline filter is active (Task 7). Returns false
// for now; filter capture wires in Task 7.
func (b *browser) CapturingInput() bool { return false }

// Result implements tui.Plugin. The docs browser is quit-only; docstui.Run
// returns error only.
func (b *browser) Result() any { return nil }

// StatusContext implements tui.Plugin. Returns the middle-zone status string:
// current path + 📊 N/M diagram progress + [lang]. Stub delegates to the
// existing StatusBarWidget; full threading wires in Task 9.
func (b *browser) StatusContext() string {
	if b.Model == nil || b.StatusBar == nil {
		return ""
	}
	return b.StatusBar.View()
}

// Actions implements tui.Plugin. Stub — action registry wires in Task 5.
func (b *browser) Actions(_ *tui.Registry) error { return nil }

// HandleAction implements tui.Plugin. Stub — dispatch wires in Task 5.
func (b *browser) HandleAction(_ tui.Action) (tea.Cmd, bool) { return nil, false }

// PendingOverlay implements tui.Plugin. No overlay for the docs browser yet.
func (b *browser) PendingOverlay() (tui.Overlay, bool) { return tui.Overlay{}, false }

// Update implements tui.Plugin. Stub — message routing wires in Task 6.
func (b *browser) Update(_ tea.Msg) tea.Cmd { return nil }

// ViewPanel implements tui.Plugin. Caches the per-panel inner region and
// renders the panel body. Stub — tree/viewport render wires in Tasks 3/4.
func (b *browser) ViewPanel(id tui.PanelID, inner tui.Region) string {
	switch id {
	case panelTree:
		b.treeInner = inner
	case panelViewport:
		b.viewportInner = inner
	}
	return ""
}
