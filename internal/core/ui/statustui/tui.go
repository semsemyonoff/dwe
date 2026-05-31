package statustui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/core/project/stack"
	"github.com/semsemyonoff/devbox/internal/core/ui/render"
	"github.com/semsemyonoff/devbox/internal/core/ui/styles"
	"github.com/semsemyonoff/devbox/internal/core/workflow/deploy/journal"
)

// Test seams for TTY detection and terminal size queries.
// Tests override these via t.Cleanup to avoid actual terminal calls.
var (
	isTerminalFn = term.IsTerminal

	terminalSizeFn = func() (w, h int, err error) {
		return term.GetSize(1) // stdout is typically fd 1
	}
)

// Deps carries the dependencies needed by the statustui model. It mirrors
// statusContext fields without creating a package cycle.
type Deps struct {
	Cfg         *config.DevboxConfig
	State       *journal.ProjectState
	Tracked     []string
	SvcDeploys  map[string]*config.ServiceDeployConfig
	ProjectName string
	DockerCfg   *config.DockerConfig
	Topo        map[string][]string
	TopoStatus  map[string]render.NodeStatus
	IsRunning   stack.ContainerCheckFn
	ProjectRoot string
}

// tab represents one rendered section of the status view.
type tab struct {
	title   string
	content string
}

// model is the bubbletea program backing the status TUI. It manages five
// tabs (Services, Deploy, Topology, Git, Daemons) with a shared viewport,
// title bar, tab strip, and status bar.
type model struct {
	deps            Deps
	ctx             context.Context
	tabs            []tab
	active          int
	viewport        viewport.Model
	help            help.Model
	keys            keyMap
	spinner         spinner.Model
	loading         bool
	reloadActive    int
	reloadYOffset   int
	loadGen         uint64
	reloadGen       uint64
	reloading       bool
	width           int
	height          int
	reloadAt        time.Time
	healthIndicator string // cached; recomputed only on tab reload
}

// Compile-time assertion that model implements tea.Model.
var _ tea.Model = (*model)(nil)

// viewportHeight returns the viewport height for the current terminal size and
// help state. The layout is: titleBar(1) + tabStrip(1) + divider(1) + viewport
// + statusBar. statusBar grows when help is expanded (ShowAll=true).
func (m *model) viewportHeight() int {
	// 3 fixed chrome rows above the viewport.
	fixed := 3
	// Measure the actual rendered status bar height so that any overflow
	// (leftSide + helpText > available width) is accounted for correctly.
	statusRows := lipgloss.Height(m.renderStatusBar())
	return max(0, m.height-fixed-statusRows)
}

// newModel creates a new status TUI model. It initializes the viewport,
// help, spinner, and other UI components.
func newModel(d Deps, ctx context.Context, w, h int) *model {
	// Initial viewport height: h - 3 chrome rows - 1 compact help row = h-4.
	vp := viewport.New(viewport.WithWidth(w-2), viewport.WithHeight(h-4))
	hm := help.New()
	hm.SetWidth(w)
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
	m.loadGen++
	return tea.Batch(m.spinner.Tick, buildTabsCmd(m.ctx, m.deps, m.loadGen))
}

// Update processes messages and returns the updated model and a command.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.SetWidth(m.width - 2)
		m.viewport.SetHeight(m.viewportHeight())
		if len(m.tabs) > m.active {
			m.viewport.SetContent(m.tabs[m.active].content)
		}
		return m, nil

	case tabsLoadedMsg:
		// Drop stale messages from older reloads
		if msg.gen != m.loadGen {
			return m, nil
		}
		m.tabs = msg.tabs
		m.reloadAt = msg.loadedAt
		m.healthIndicator = msg.healthIndicator
		m.loading = false
		m.reloading = false

		// Restore YOffset if this is a reload that matches the active tab
		if m.reloadGen == msg.gen && m.reloadActive == m.active && len(m.tabs) > m.active {
			m.viewport.SetContent(m.tabs[m.active].content)
			m.viewport.SetYOffset(m.reloadYOffset)
		} else if len(m.tabs) > m.active {
			m.viewport.SetContent(m.tabs[m.active].content)
			m.viewport.GotoTop()
		}
		m.reloadGen = 0
		return m, nil

	case tea.KeyPressMsg:
		// Always allow quit, even while loading.
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		// Guard against tab navigation before tabs are loaded.
		if len(m.tabs) == 0 {
			return m, nil
		}
		// Handle tab navigation
		switch {
		case key.Matches(msg, m.keys.NextTab):
			m.active = (m.active + 1) % len(m.tabs)
			m.reloadGen = 0
			if len(m.tabs) > m.active {
				m.viewport.SetContent(m.tabs[m.active].content)
				m.viewport.GotoTop()
			}
			return m, nil

		case key.Matches(msg, m.keys.PrevTab):
			m.active = (m.active - 1 + len(m.tabs)) % len(m.tabs)
			m.reloadGen = 0
			if len(m.tabs) > m.active {
				m.viewport.SetContent(m.tabs[m.active].content)
				m.viewport.GotoTop()
			}
			return m, nil

		case key.Matches(msg, m.keys.Tab1):
			if 0 < len(m.tabs) {
				m.active = 0
				m.reloadGen = 0
				m.viewport.SetContent(m.tabs[m.active].content)
				m.viewport.GotoTop()
			}
			return m, nil

		case key.Matches(msg, m.keys.Tab2):
			if 1 < len(m.tabs) {
				m.active = 1
				m.reloadGen = 0
				m.viewport.SetContent(m.tabs[m.active].content)
				m.viewport.GotoTop()
			}
			return m, nil

		case key.Matches(msg, m.keys.Tab3):
			if 2 < len(m.tabs) {
				m.active = 2
				m.reloadGen = 0
				m.viewport.SetContent(m.tabs[m.active].content)
				m.viewport.GotoTop()
			}
			return m, nil

		case key.Matches(msg, m.keys.Tab4):
			if 3 < len(m.tabs) {
				m.active = 3
				m.reloadGen = 0
				m.viewport.SetContent(m.tabs[m.active].content)
				m.viewport.GotoTop()
			}
			return m, nil

		case key.Matches(msg, m.keys.Tab5):
			if 4 < len(m.tabs) {
				m.active = 4
				m.reloadGen = 0
				m.viewport.SetContent(m.tabs[m.active].content)
				m.viewport.GotoTop()
			}
			return m, nil

		case key.Matches(msg, m.keys.Reload):
			m.loadGen++
			m.reloadActive = m.active
			m.reloadYOffset = m.viewport.YOffset()
			m.reloadGen = m.loadGen
			m.reloading = true
			return m, buildTabsCmd(m.ctx, m.deps, m.loadGen)

		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			m.viewport.SetHeight(m.viewportHeight())
			return m, nil
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	// Delegate unmatched messages to viewport for scroll handling
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// renderTitleBar renders the branded title bar — `{▪} devbox · <project> · Status`
// in accent+bold, wrapped in lipgloss with padding.
func (m *model) renderTitleBar() string {
	text := render.LogoMarkPlain() + " devbox · " + m.deps.ProjectName + " · Status"
	return lipgloss.NewStyle().
		Width(m.width).
		Padding(0, 1).
		Foreground(lipgloss.Color(styles.ColorAccent())).
		Bold(true).
		Render(text)
}

// renderTabStrip renders the tab navigation with active tab highlighted.
// Active tab is wrapped in ▌ ▐ with accent styling; inactive tabs are dimmed.
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

// renderStatusBar renders the bottom status bar with health indicator on the left
// and help text on the right.
func (m *model) renderStatusBar() string {
	// Build left side: health indicator + loaded timestamp
	var leftParts []string
	switch {
	case m.loading:
		leftParts = append(leftParts, "·", "loading…")
	case m.reloading:
		leftParts = append(leftParts, "·", "reloading…")
	case len(m.tabs) > 0 && m.deps.Cfg != nil:
		leftParts = append(leftParts, m.healthIndicator)
		if !m.reloadAt.IsZero() {
			elapsed := time.Since(m.reloadAt)
			leftParts = append(leftParts, fmt.Sprintf("loaded %v ago", elapsed.Round(time.Second)))
		}
	}

	leftSide := strings.Join(leftParts, "  ")

	// Build right side: help text, constrained to the remaining available
	// width so leftSide + rightSide never exceeds the content area.
	availHelp := max(0, m.width-2-lipgloss.Width(leftSide))
	helpModel := m.help
	helpModel.SetWidth(availHelp)
	rightSide := helpModel.View(m.keys)

	// Padding(0,1) adds 1 col each side = 2 total; subtract from available content width.
	spacerW := max(0, m.width-2-lipgloss.Width(leftSide)-lipgloss.Width(rightSide))
	status := lipgloss.JoinHorizontal(lipgloss.Top,
		leftSide,
		lipgloss.NewStyle().Width(spacerW).Render(""),
		rightSide)

	return lipgloss.NewStyle().
		Width(m.width).
		Padding(0, 1).
		Render(status)
}

// View renders the current state as a tea.View with alt-screen enabled.
func (m *model) View() tea.View {
	// Terminal too small check
	if m.width < 60 || m.height < 16 {
		msg := "terminal too small (need 60×16)"
		centered := lipgloss.NewStyle().
			Width(m.width).
			Height(m.height).
			Align(lipgloss.Center).
			AlignVertical(lipgloss.Center).
			Render(msg)
		v := tea.NewView(centered)
		v.AltScreen = true
		return v
	}

	// Show loading state
	if m.loading {
		spinnerView := m.spinner.View()
		// Loading view only has a title bar (1 row); spinner fills the rest.
		centered := lipgloss.NewStyle().
			Width(m.width).
			Height(m.height - 1).
			Align(lipgloss.Center).
			AlignVertical(lipgloss.Center).
			Render(spinnerView)
		titleBar := m.renderTitleBar()
		content := lipgloss.JoinVertical(lipgloss.Top, titleBar, centered)
		v := tea.NewView(content)
		v.AltScreen = true
		return v
	}

	// Normal view: title / tabs / divider / viewport / status bar
	titleBar := m.renderTitleBar()
	tabStrip := m.renderTabStrip()

	// Render divider line — a full-width horizontal separator.
	dividerLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(styles.ColorMuted())).
		Render(strings.Repeat("─", m.width-2))

	statusBar := m.renderStatusBar()
	viewportContent := m.viewport.View()

	content := lipgloss.JoinVertical(lipgloss.Top,
		titleBar,
		tabStrip,
		dividerLine,
		viewportContent,
		statusBar)

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}
