package statustui

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/stack"
	"devbox-cli/internal/ui"
)

// Test seams for TTY detection and terminal size queries.
// Tests override these via t.Cleanup to avoid actual terminal calls.
var (
	isTerminalFn = func(fd uintptr) bool {
		return term.IsTerminal(fd)
	}

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
		m.viewport.SetHeight(m.height - 4)
		m.help.SetWidth(m.width)
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
		m.err = msg.err
		m.reloadAt = msg.loadedAt
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
			return m, nil

		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
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
	text := ui.LogoMarkPlain() + " devbox · " + m.deps.ProjectName + " · Status"
	return lipgloss.NewStyle().
		Width(m.width).
		Padding(0, 1).
		Foreground(lipgloss.Color(ui.ColorAccent())).
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
				Foreground(lipgloss.Color(ui.ColorAccent())).
				Bold(true).
				Render("▌"+t.title+"▐"))
		} else {
			// Inactive tab, dimmed
			parts = append(parts, lipgloss.NewStyle().
				Foreground(lipgloss.Color(ui.ColorMuted())).
				Render(t.title))
		}
	}

	strip := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	// Pad the strip and add a left padding
	return " " + strip
}

// renderStatusBar renders the bottom status bar with health indicator on the left
// and help text on the right.
func (m *model) renderStatusBar() string {
	// Build left side: health indicator + loaded timestamp
	var leftParts []string
	if m.loading {
		leftParts = append(leftParts, "·", "loading…")
	} else if m.err != nil {
		leftParts = append(leftParts, "·", "error")
	} else if m.reloading {
		leftParts = append(leftParts, "·", "reloading…")
	} else if len(m.tabs) > 0 && m.deps.Cfg != nil {
		// Get health indicator (only if Cfg is available)
		in := stack.StatusInput{
			Cfg:        m.deps.Cfg,
			IsRunning:  m.deps.IsRunning,
			Topo:       m.deps.Topo,
			TopoStatus: m.deps.TopoStatus,
			State:      m.deps.State,
		}
		indicator := stack.HealthIndicator(in)
		leftParts = append(leftParts, indicator)
		if !m.reloadAt.IsZero() {
			elapsed := time.Since(m.reloadAt)
			leftParts = append(leftParts, fmt.Sprintf("loaded %v ago", elapsed.Round(time.Second)))
		}
	}

	leftSide := lipgloss.JoinHorizontal(lipgloss.Top, leftParts...)

	// Build right side: help text
	m.help.SetWidth(m.width - 10) // Account for left side + padding
	rightSide := m.help.View(m.keys)

	// Combine with padding
	status := lipgloss.JoinHorizontal(lipgloss.Top,
		leftSide,
		lipgloss.NewStyle().Width(m.width - lipgloss.Width(leftSide) - lipgloss.Width(rightSide)).Render(""),
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
		centered := lipgloss.NewStyle().
			Width(m.width).
			Height(m.height - 4).
			Align(lipgloss.Center).
			AlignVertical(lipgloss.Center).
			Render(spinnerView)
		titleBar := m.renderTitleBar()
		content := lipgloss.JoinVertical(lipgloss.Top, titleBar, centered)
		v := tea.NewView(content)
		v.AltScreen = true
		return v
	}

	// Show error state
	if m.err != nil {
		errorMsg := fmt.Sprintf("Error: %v\n\nPress r to retry, q to quit", m.err)
		errorView := lipgloss.NewStyle().
			Width(m.width - 4).
			Padding(1, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(ui.ColorDanger())).
			Render(errorMsg)
		centered := lipgloss.NewStyle().
			Width(m.width).
			Height(m.height - 4).
			Align(lipgloss.Center).
			AlignVertical(lipgloss.Center).
			Render(errorView)
		titleBar := m.renderTitleBar()
		content := lipgloss.JoinVertical(lipgloss.Top, titleBar, centered)
		v := tea.NewView(content)
		v.AltScreen = true
		return v
	}

	// Normal view: title / tabs / divider / viewport / status bar
	titleBar := m.renderTitleBar()
	tabStrip := m.renderTabStrip()

	// Set viewport dimensions to account for: title (1) + tabs (1) + divider (1) + status bar (1)
	viewportHeight := m.height - 4
	m.viewport.SetWidth(m.width - 2)
	m.viewport.SetHeight(viewportHeight)

	// Set viewport content if tabs are loaded
	if len(m.tabs) > m.active {
		m.viewport.SetContent(m.tabs[m.active].content)
	}

	// Render divider line - simple horizontal separator
	dividerLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ui.ColorMuted())).
		Render(lipgloss.NewStyle().Width(m.width - 2).Render("─"))

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
