package statustui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/stack"
	"devbox-cli/internal/ui"
)

// Deps carries the dependencies needed by the statustui model. It mirrors
// statusContext fields without creating a package cycle.
type Deps struct {
	Cfg         *config.DevboxConfig
	State       *journal.ProjectState
	Tracked     []string
	SvcDeploys  map[string]*config.DeployConfig
	ProjectName string
	DockerCfg   *config.DockerConfig
	Topo        map[string][]string
	TopoStatus  map[string]ui.NodeStatus
	IsRunning   stack.ContainerCheckFn
	ProjectRoot string
}

// tab represents one rendered section of the status view.
type tab struct {
	title   string //nolint:unused
	content string //nolint:unused
}

// model is the bubbletea program backing the status TUI. It manages five
// tabs (Services, Deploy, Topology, Git, Daemons) with a shared viewport,
// title bar, tab strip, and status bar.
type model struct {
	deps          Deps
	ctx           context.Context
	tabs          []tab
	active        int
	viewport      viewport.Model
	help          help.Model
	keys          keyMap
	spinner       spinner.Model
	loading       bool
	reloadActive  int    //nolint:unused
	reloadYOffset int    //nolint:unused
	loadGen       uint64 //nolint:unused
	reloadGen     uint64 //nolint:unused
	reloading     bool   //nolint:unused
	width         int
	height        int
	err           error     //nolint:unused
	reloadAt      time.Time //nolint:unused
}

// Compile-time assertion that model implements tea.Model.
var _ tea.Model = (*model)(nil)

// newModel creates a new status TUI model. It initializes the viewport,
// help, spinner, and other UI components.
func newModel(d Deps, ctx context.Context, w, h int) *model {
	vp := viewport.New(viewport.WithWidth(w-2), viewport.WithHeight(h-4))
	hm := help.New()
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return &model{
		deps:     d,
		ctx:      ctx,
		tabs:     []tab{},
		active:   0,
		viewport: vp,
		help:     hm,
		keys:     defaultKeyMap(),
		spinner:  sp,
		loading:  true,
		width:    w,
		height:   h,
	}
}

// Init returns a command to be executed by bubbletea on program start.
func (m *model) Init() tea.Cmd {
	return nil
}

// Update processes messages and returns the updated model and a command.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, nil
}

// View renders the current state as a tea.View with alt-screen enabled.
func (m *model) View() tea.View {
	content := "loading..."
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}
