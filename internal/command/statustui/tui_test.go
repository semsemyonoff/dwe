package statustui

import (
	"context"
	"errors"
	"strings"
	"testing"

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
