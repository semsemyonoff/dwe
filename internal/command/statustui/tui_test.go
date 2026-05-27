package statustui

import (
	"context"
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
	m.loadGen = 1

	// Load tabs via Update so the viewport content is set properly.
	newM, _ := m.Update(tabsLoadedMsg{
		gen: 1,
		tabs: []tab{
			{"Services", "apps table\nwith multiple lines"},
			{"Deploy", "deploy info"},
		},
		loadedAt: time.Now(),
	})
	m = newM.(*model)

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
	m.loadGen = 1

	tabs := []tab{
		{"Services", "services content"},
		{"Deploy", "deploy content"},
		{"Topology", "topology content"},
		{"Git", "git content"},
		{"Daemons", "daemons content"},
	}

	// Load tabs via Update so viewport content is set for the first tab.
	newM, _ := m.Update(tabsLoadedMsg{gen: 1, tabs: tabs, loadedAt: time.Now()})
	m = newM.(*model)

	for i := range tabs {
		// Switch to each tab via Update so viewport content is updated.
		if i > 0 {
			newM, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			m = newM.(*model)
		}
		view := m.View()
		content := view.Content

		// Verify current tab content is visible
		require.Contains(t, content, tabs[i].content, "should show content for tab %d", i)

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

	// NextTab: 0 → 1 → 2 → wrap to 0
	newM, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = newM.(*model)
	require.Equal(t, 1, m.active)

	newM, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = newM.(*model)
	require.Equal(t, 2, m.active)

	newM, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = newM.(*model)
	require.Equal(t, 0, m.active, "should wrap around to first tab")

	// PrevTab: 0 → wrap to 2
	newM, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = newM.(*model)
	require.Equal(t, 2, m.active, "prev tab should wrap to last")
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
	m.loading = false

	// Key "3" jumps to index 2
	newM, _ := m.Update(tea.KeyPressMsg{Code: '3', Text: "3"})
	require.Equal(t, 2, newM.(*model).active)

	// Key "5" jumps to index 4
	newM, _ = m.Update(tea.KeyPressMsg{Code: '5', Text: "5"})
	require.Equal(t, 4, newM.(*model).active)

	// Key "1" jumps to index 0
	newM, _ = m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	require.Equal(t, 0, newM.(*model).active)
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

	initialGen := m.loadGen
	newM, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	updated := newM.(*model)

	require.True(t, updated.reloading)
	require.Greater(t, updated.loadGen, initialGen, "loadGen should increment on reload")
	require.NotNil(t, cmd, "reload should fire a command")
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

	// Start a reload on tab 0 via Update
	reloadedM, _ := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = reloadedM.(*model)
	savedReloadGen := m.reloadGen
	require.Greater(t, savedReloadGen, uint64(0), "reloadGen should be set after reload")

	// Switch to tab 1 via Update — this should clear reloadGen
	switchedM, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = switchedM.(*model)
	require.Equal(t, 1, m.active)
	require.Equal(t, uint64(0), m.reloadGen, "tab switch should clear reloadGen")

	// Simulate the old reload completing — offset should NOT be restored
	staleMsg := tabsLoadedMsg{
		gen:      savedReloadGen,
		tabs:     m.tabs,
		loadedAt: time.Now(),
	}
	// The stale msg gen != m.loadGen (which was bumped by reload), so it's dropped
	finalM, _ := m.Update(staleMsg)
	require.Equal(t, 1, finalM.(*model).active, "active tab should remain unchanged")
}

// Test quit returns tea.Quit
func TestUpdate_QuitReturnsTeaQuit(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.tabs = []tab{{"Services", "content"}}
	m.loading = false

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	require.NotNil(t, cmd, "quit key should return a command")
	// Execute the command and verify it is tea.Quit
	msg := cmd()
	require.IsType(t, tea.QuitMsg{}, msg, "quit command should return QuitMsg")
}

// Test help toggle
func TestUpdate_HelpToggle(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx, 100, 30)
	m.tabs = []tab{{"Services", "content"}}
	m.loading = false

	initialState := m.help.ShowAll
	newM, _ := m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	require.NotEqual(t, initialState, newM.(*model).help.ShowAll, "help toggle should flip ShowAll")
}
