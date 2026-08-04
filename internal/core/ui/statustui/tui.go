package statustui

import (
	"context"
	"slices"
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

// tabTitles are the five fixed tab labels, in display order. The tab count
// never varies — buildTabs always collects all five sections (empty ones
// render a placeholder) — so titles are a static array rather than data
// carried in the snapshot.
var tabTitles = [...]string{"Services", "Deploy", "Topology", "Git", "Daemons"}

// model holds the status dashboard's state: five tabs (Services, Deploy,
// Topology, Git, Daemons) with a shared viewport, spinner, and reload
// generations. It is rendered by the plugin (plugin.go) as body content
// inside the shared tui.Frame; it no longer implements tea.Model itself.
type model struct {
	deps            Deps
	ctx             context.Context
	snap            tabSnapshot
	loaded          bool    // true once the first tabsLoadedMsg has been applied
	sectionAnchors  [][]int // per-tab 0-based line offsets of stacked sub-tables
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

	// renderCache memoises the last renderTabFn call so repeated View()
	// calls with an unchanged (loadGen, active, width) do not re-render the
	// active tab's tables on every frame. Valid only while renderCacheValid:
	// tab switches change active and a resize changes width, but a reload
	// needs the explicit renderCacheValid = false in the tabsLoadedMsg branch
	// — loadGen is bumped when the reload starts, not when its snapshot
	// arrives, so the key tuple alone would keep serving pre-reload content.
	renderCacheValid   bool
	renderCacheGen     uint64
	renderCacheTab     int
	renderCacheWidth   int
	renderCacheBody    string
	renderCacheAnchors []int
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

// setActiveTab switches to the tab at idx, resets the pending-reload
// generation, and scrolls the viewport to the top. Out-of-range indices, and
// any switch before the first load completes, are ignored — preserving the
// per-key guard the explicit tab-switch blocks used to carry. Content is not
// set here: renderBody recomputes the active tab's body on the next render
// via renderTab.
func (m *model) setActiveTab(idx int) {
	if !m.loaded || idx < 0 || idx >= len(tabTitles) {
		return
	}
	m.active = idx
	m.reloadGen = 0
	m.viewport.GotoTop()
}

// jumpSection scrolls the viewport to the next (dir > 0) or previous (dir < 0)
// sub-table anchor of the active tab, so ] / [ hop between the stacked tables
// (Apps / Tools / Infra on Services) instead of line-scrolling. A tab with
// fewer than two anchors, or a jump past the first/last table, is a no-op
// (jumps clamp at the ends, matching how ↑/↓ clamp — no wrap-around).
func (m *model) jumpSection(dir int) {
	if m.active < 0 || m.active >= len(m.sectionAnchors) {
		return
	}
	anchors := m.sectionAnchors[m.active]
	if len(anchors) < 2 {
		return
	}
	cur := m.viewport.YOffset()
	target := -1
	if dir > 0 {
		for _, a := range anchors {
			if a > cur {
				target = a
				break
			}
		}
	} else {
		for _, a := range slices.Backward(anchors) {
			if a < cur {
				target = a
				break
			}
		}
	}
	if target < 0 {
		return
	}
	m.viewport.SetYOffset(target)
}

// Tab-strip layout, shared by renderTabStrip (drawing) and mouse.go's
// tabHitZones (click mapping) so the two can never drift on spacing. The active
// tab is bracketed by tabActiveLeft/Right; tabActiveDecoWidth derives their
// combined column width from the glyphs themselves.
const (
	tabStripLeadPad = 1   // leading blank column before the first tab
	tabStripGap     = 3   // blank columns between adjacent tabs
	tabActiveLeft   = "▌" // decoration bracketing the active tab
	tabActiveRight  = "▐"
)

// tabActiveDecoWidth is the combined display width of the active-tab decoration.
func tabActiveDecoWidth() int {
	return lipgloss.Width(tabActiveLeft) + lipgloss.Width(tabActiveRight)
}

// renderTabStrip renders the tab navigation with active tab highlighted.
// Active tab is bracketed by tabActiveLeft/Right with accent styling; inactive
// tabs are dimmed. Layout constants are shared with mouse.go's tabHitZones, so
// click hit-zones match what is drawn here.
func (m *model) renderTabStrip() string {
	if !m.loaded {
		return ""
	}

	var parts []string
	for i, title := range tabTitles {
		if i == m.active {
			// Active tab with accent corners
			parts = append(parts, lipgloss.NewStyle().
				Foreground(lipgloss.Color(styles.ColorAccent())).
				Bold(true).
				Render(tabActiveLeft+title+tabActiveRight))
		} else {
			// Inactive tab, dimmed
			parts = append(parts, lipgloss.NewStyle().
				Foreground(lipgloss.Color(styles.ColorMuted())).
				Render(title))
		}
	}

	strip := strings.Join(parts, strings.Repeat(" ", tabStripGap))
	return strings.Repeat(" ", tabStripLeadPad) + strip
}
