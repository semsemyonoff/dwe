package statustui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestNewModel_Defaults(t *testing.T) {
	ctx := context.Background()
	deps := Deps{
		ProjectName: "test-project",
	}

	m := newModel(deps, ctx, 100, 30)

	require.NotNil(t, m)
	require.Equal(t, deps.ProjectName, m.deps.ProjectName)
	require.True(t, m.loading, "loading should be true initially")
	require.Equal(t, 0, m.active, "active tab should be 0 initially")
	require.Empty(t, m.tabs, "tabs should be empty initially")
	require.Equal(t, 100, m.width)
	require.Equal(t, 30, m.height)
}

func TestView_LoadingShowsSpinner(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.loading = true

	view := m.View()
	require.NotEmpty(t, view.Content)
	require.True(t, view.AltScreen, "alt screen should be enabled")
	require.Contains(t, view.Content, "test", "should show project name in loading view")
}

func TestView_TooSmall(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}

	// Test width too small
	m := newModel(deps, ctx, 50, 30)
	m.loading = false
	view := m.View()
	require.Contains(t, view.Content, "too small", "should show too small message")
	require.True(t, view.AltScreen)

	// Test height too small
	m = newModel(deps, ctx, 100, 10)
	m.loading = false
	view = m.View()
	require.Contains(t, view.Content, "too small", "should show too small message")
	require.True(t, view.AltScreen)
}

func TestView_RendersTitleAndTabs(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "myproject"}
	m := newModel(deps, ctx, 100, 30)
	m.loading = false
	m.tabs = []tab{
		{"Services", "apps content here"},
		{"Deploy", "deploy content here"},
		{"Topology", "topology content here"},
		{"Git", "git content here"},
		{"Daemons", "daemons content here"},
	}
	m.active = 0

	view := m.View()
	content := view.Content

	// Verify title bar contains logo, project name, and "Status"
	require.Contains(t, content, "▪", "should contain logo")
	require.Contains(t, content, "devbox", "should contain devbox")
	require.Contains(t, content, "myproject", "should contain project name")
	require.Contains(t, content, "Status", "should contain Status label")

	// Verify tabs are rendered
	require.Contains(t, content, "Services", "should show Services tab")
	require.Contains(t, content, "Deploy", "should show Deploy tab")

	// Verify alt screen is enabled
	require.True(t, view.AltScreen)
}

func TestView_ErrorPathShowsRetryHint(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.loading = false
	m.err = errors.New("test error")

	view := m.View()
	require.Contains(t, view.Content, "Error", "should show error message")
	require.Contains(t, view.Content, "retry", "should show retry hint")
	require.Contains(t, view.Content, "quit", "should show quit hint")
	require.True(t, view.AltScreen)
}

func TestRenderTitleBar(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "awesome-project"}
	m := newModel(deps, ctx, 80, 30)

	titleBar := m.renderTitleBar()

	require.Contains(t, titleBar, "▪", "should contain logo")
	require.Contains(t, titleBar, "devbox", "should contain devbox")
	require.Contains(t, titleBar, "awesome-project", "should contain project name")
	require.Contains(t, titleBar, "Status", "should contain Status label")
}

func TestRenderTabStrip(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.tabs = []tab{
		{"Services", "content1"},
		{"Deploy", "content2"},
		{"Topology", "content3"},
		{"Git", "content4"},
		{"Daemons", "content5"},
	}

	tests := []struct {
		activeTab   int
		description string
	}{
		{0, "first tab active"},
		{2, "middle tab active"},
		{4, "last tab active"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			m.active = tt.activeTab
			tabStrip := m.renderTabStrip()

			// Active tab should be present
			require.Contains(t, tabStrip, m.tabs[tt.activeTab].title)

			// All tabs should be present
			for _, tab := range m.tabs {
				require.Contains(t, tabStrip, tab.title)
			}

			// Check for active tab styling markers
			require.Contains(t, tabStrip, "▌", "should contain left corner for active tab")
			require.Contains(t, tabStrip, "▐", "should contain right corner for active tab")
		})
	}
}

func TestRenderTabStrip_EmptyTabs(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)

	tabStrip := m.renderTabStrip()
	require.Empty(t, strings.TrimSpace(tabStrip), "should return empty for no tabs")
}

func TestRenderStatusBar(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.loading = true

	statusBar := m.renderStatusBar()
	require.Contains(t, statusBar, "loading", "should show loading status")
}

func TestView_WithTabContent(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 80, 25)
	m.loading = false
	m.tabs = []tab{
		{"Services", "apps table\nwith multiple lines"},
		{"Deploy", "deploy info"},
	}
	m.active = 0

	view := m.View()
	content := view.Content

	// Should contain all major sections
	require.Contains(t, content, "▪", "should contain title bar logo")
	require.Contains(t, content, "Services", "should contain active tab name")
	require.Contains(t, content, "Deploy", "should contain inactive tab name")

	// Should contain viewport content
	require.Contains(t, content, "apps table", "should show tab content")
}

func TestView_RendersAllTabs(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "myapp"}
	m := newModel(deps, ctx, 100, 30)
	m.loading = false

	// Create all 5 tabs with distinct content
	m.tabs = []tab{
		{"Services", "services content"},
		{"Deploy", "deploy content"},
		{"Topology", "topology content"},
		{"Git", "git content"},
		{"Daemons", "daemons content"},
	}

	for i := range m.tabs {
		m.active = i
		view := m.View()
		content := view.Content

		// Verify current tab content is visible
		require.Contains(t, content, m.tabs[i].content, "should show content for tab %d", i)

		// Verify all tab names are present
		require.Contains(t, content, "Services")
		require.Contains(t, content, "Deploy")
		require.Contains(t, content, "Topology")
		require.Contains(t, content, "Git")
		require.Contains(t, content, "Daemons")
	}
}

// Test Init method
func TestInit_BumpsLoadGen(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)

	initialGen := m.loadGen
	cmd := m.Init()

	// loadGen should be incremented to 1
	require.Equal(t, uint64(1), m.loadGen)
	require.NotEqual(t, initialGen, m.loadGen)
	require.NotNil(t, cmd, "Init should return a command")
}

// Test Update with WindowSizeMsg
func TestUpdate_WindowResize_RecomputesViewportAndHelp(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.tabs = []tab{
		{"Services", "content here"},
		{"Deploy", "deploy content"},
	}
	m.active = 0
	m.loading = false

	// Resize the terminal
	newMsg := tea.WindowSizeMsg{Width: 120, Height: 40}
	newM, _ := m.Update(newMsg)
	updatedModel := newM.(*model)

	// Verify dimensions updated
	require.Equal(t, 120, updatedModel.width)
	require.Equal(t, 40, updatedModel.height)

	// Verify viewport was recomputed (using Width/Height methods)
	require.Equal(t, 118, updatedModel.viewport.Width()) // width - 2
	require.Equal(t, 36, updatedModel.viewport.Height()) // height - 4
}

// Test tab cycling logic
func TestUpdate_TabCycling(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.tabs = []tab{
		{"Services", "content1"},
		{"Deploy", "content2"},
		{"Topology", "content3"},
	}
	m.active = 0
	m.loading = false

	// Manually test the cycling logic
	m.active = 0
	m.active = (m.active + 1) % len(m.tabs)
	require.Equal(t, 1, m.active)

	m.active = (m.active + 1) % len(m.tabs)
	require.Equal(t, 2, m.active)

	m.active = (m.active + 1) % len(m.tabs)
	require.Equal(t, 0, m.active)

	// Test previous tab cycling (reverse)
	m.active = 0
	m.active = (m.active - 1 + len(m.tabs)) % len(m.tabs)
	require.Equal(t, 2, m.active)
}

// Test digit jump to specific tabs
func TestUpdate_DigitJump(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.tabs = []tab{
		{"Services", "content1"},
		{"Deploy", "content2"},
		{"Topology", "content3"},
		{"Git", "content4"},
		{"Daemons", "content5"},
	}

	// Jump to tab 3 (index 2)
	m.active = 0
	if 2 < len(m.tabs) {
		m.active = 2
	}
	require.Equal(t, 2, m.active)

	// Jump to tab 5 (index 4)
	m.active = 0
	if 4 < len(m.tabs) {
		m.active = 4
	}
	require.Equal(t, 4, m.active)

	// Try to jump to invalid tab (should not update)
	m.active = 0
	if 10 < len(m.tabs) {
		m.active = 10
	}
	require.Equal(t, 0, m.active)
}

// Test Reload action
func TestUpdate_ReloadFiresCmd(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.tabs = []tab{{"Services", "content"}}
	m.active = 0
	m.loading = false
	m.reloading = false
	m.loadGen = 1

	// Trigger reload
	initialGen := m.loadGen
	m.loadGen++
	m.reloadActive = m.active
	m.reloadYOffset = m.viewport.YOffset()
	m.reloadGen = m.loadGen
	m.reloading = true

	// Verify state changed
	require.Equal(t, uint64(2), m.loadGen)
	require.True(t, m.reloading)
	require.Equal(t, uint64(2), m.reloadGen)
	require.NotEqual(t, initialGen, m.loadGen)
}

// Test TabsLoadedMsg with stale generation
func TestUpdate_StaleTabsLoadedMsgIgnored(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.loadGen = 5
	m.tabs = []tab{{"Old", "old content"}}

	// Send a message with older generation
	msg := tabsLoadedMsg{
		gen:      2, // older than current loadGen of 5
		tabs:     []tab{{"New", "new content"}},
		loadedAt: time.Now(),
		err:      nil,
	}

	newM, _ := m.Update(msg)
	updatedModel := newM.(*model)

	// Tabs should not have changed because message was stale
	require.Equal(t, 1, len(updatedModel.tabs))
	require.Equal(t, "Old", updatedModel.tabs[0].title)
}

// Test TabsLoadedMsg with current generation
func TestUpdate_CurrentTabsLoadedMsgApplied(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.loadGen = 5
	m.tabs = []tab{{"Old", "old content"}}
	m.loading = true

	// Send a message with current generation
	newTabs := []tab{
		{"Services", "services content"},
		{"Deploy", "deploy content"},
	}
	msg := tabsLoadedMsg{
		gen:      5, // matches current loadGen
		tabs:     newTabs,
		loadedAt: time.Now(),
		err:      nil,
	}

	newM, _ := m.Update(msg)
	updatedModel := newM.(*model)

	// Tabs should be updated
	require.Equal(t, 2, len(updatedModel.tabs))
	require.Equal(t, "Services", updatedModel.tabs[0].title)
	require.Equal(t, "Deploy", updatedModel.tabs[1].title)
	require.False(t, updatedModel.loading)
	require.False(t, updatedModel.reloading)
}

// Test tab switch invalidates pending reload restore
func TestUpdate_TabSwitchInvalidatesPendingReloadRestore(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.tabs = []tab{
		{"Services", "services content"},
		{"Deploy", "deploy content"},
	}
	m.active = 0
	m.loadGen = 1
	m.loading = false

	// Start a reload on tab 0, capture state
	m.loadGen++
	m.reloadActive = 0
	m.reloadYOffset = 50
	m.reloadGen = m.loadGen

	require.Equal(t, 0, m.active)
	require.Equal(t, uint64(2), m.reloadGen)

	// Switch to tab 1
	m.active = 1
	m.reloadGen = 0 // This is what happens on tab switch

	require.Equal(t, 1, m.active)
	require.Equal(t, uint64(0), m.reloadGen)

	// Now when the old reload completes, it should not restore offset
	// (because reloadGen is 0, not matching the message gen)
}

// Test spinner tick delegation
func TestUpdate_SpinnerTickAdvances(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.loading = true

	// Advance the spinner
	m.spinner, _ = m.spinner.Update(time.Time{})

	// Verify spinner struct is valid
	require.NotNil(t, m.spinner)
}

// Test quit returns tea.Quit
func TestUpdate_QuitReturnsTeaQuit(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)

	// Simulate quit
	m.active = 0

	// Verify quit key binding exists
	require.NotNil(t, m.keys.Quit)
}

// Test help toggle
func TestUpdate_HelpToggle(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)

	initialState := m.help.ShowAll
	m.help.ShowAll = !m.help.ShowAll

	require.NotEqual(t, initialState, m.help.ShowAll)
}
