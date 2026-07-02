package statustui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/stack"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// Deps carries the dependencies needed by the statustui model. It mirrors
// statusContext fields without creating a package cycle.
type Deps struct {
	Cfg         *config.DweConfig
	State       *journal.ProjectState
	Tracked     []string
	SvcDeploys  map[string]*config.ServiceDeployConfig
	ProjectName string
	DockerCfg   *config.DockerConfig
	Topo        map[string][]string
	TopoStatus  map[string]render.NodeStatus
	IsRunning   stack.ContainerCheckFn
	ProjectRoot string

	// Translator / Locale resolve the Frame's help-modal display strings. A
	// nil Translator falls back to i18n.NopTranslator; an empty Locale falls
	// back to "en" (both handled by tui.Run).
	Translator i18n.Translator
	Locale     string
}

// tab represents one rendered section of the status view.
type tab struct {
	title   string
	content string
}

// model holds the status dashboard's state: five tabs (Services, Deploy,
// Topology, Git, Daemons) with a shared viewport, spinner, and reload
// generations. It is rendered by the plugin (plugin.go) as body content
// inside the shared tui.Frame; it no longer implements tea.Model itself.
type model struct {
	deps            Deps
	ctx             context.Context
	tabs            []tab
	active          int
	viewport        viewport.Model
	spinner         spinner.Model
	loading         bool
	reloadActive    int
	reloadYOffset   int
	loadGen         uint64
	reloadGen       uint64
	reloading       bool
	reloadAt        time.Time
	healthIndicator string // cached; recomputed only on tab reload
}

// newModel creates a new status dashboard model. It initializes the viewport
// and spinner; the Frame owns geometry and resizes the viewport on every
// render via Plugin.ViewPanel.
func newModel(d Deps, ctx context.Context) *model {
	vp := viewport.New()
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return &model{
		deps:     d,
		ctx:      ctx,
		tabs:     []tab{},
		active:   0,
		viewport: vp,
		spinner:  sp,
		loading:  true,
	}
}

// Init returns a command to be executed by bubbletea on program start.
func (m *model) Init() tea.Cmd {
	m.loadGen++
	return tea.Batch(m.spinner.Tick, buildTabsCmd(m.ctx, m.deps, m.loadGen))
}

// setActiveTab switches to the tab at idx, resets the pending-reload generation,
// and scrolls the viewport to the top. Out-of-range indices are ignored, which
// preserves the per-key guard the explicit tab-switch blocks used to carry.
func (m *model) setActiveTab(idx int) {
	if idx < 0 || idx >= len(m.tabs) {
		return
	}
	m.active = idx
	m.reloadGen = 0
	m.viewport.SetContent(m.tabs[m.active].content)
	m.viewport.GotoTop()
}

// renderTabStrip renders the tab navigation with active tab highlighted.
// Active tab is wrapped in ▌ ▐ with accent styling; inactive tabs are dimmed.
// Shared verbatim with mouse.go's tabHitZones, so click hit-zones match what
// is drawn here.
func (m *model) renderTabStrip() string {
	if len(m.tabs) == 0 {
		return ""
	}

	var parts []string
	for i, t := range m.tabs {
		if i == m.active {
			// Active tab with accent corners
			parts = append(parts, lipgloss.NewStyle().
				Foreground(lipgloss.Color(styles.ColorAccent())).
				Bold(true).
				Render("▌"+t.title+"▐"))
		} else {
			// Inactive tab, dimmed
			parts = append(parts, lipgloss.NewStyle().
				Foreground(lipgloss.Color(styles.ColorMuted())).
				Render(t.title))
		}
	}

	strip := strings.Join(parts, "   ")
	// Pad the strip and add a left padding
	return " " + strip
}
