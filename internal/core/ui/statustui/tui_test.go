package statustui

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewModel_Defaults(t *testing.T) {
	ctx := context.Background()
	deps := Deps{
		ProjectName: "test-project",
	}

	m := newModel(deps, ctx)

	require.NotNil(t, m)
	require.Equal(t, deps.ProjectName, m.deps.ProjectName)
	require.True(t, m.loading, "loading should be true initially")
	require.Equal(t, 0, m.active, "active tab should be 0 initially")
	require.Empty(t, m.tabs, "tabs should be empty initially")
}

func TestRenderTabStrip(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx)
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
	m := newModel(deps, ctx)

	tabStrip := m.renderTabStrip()
	require.Empty(t, strings.TrimSpace(tabStrip), "should return empty for no tabs")
}

// Test Init method
func TestInit_BumpsLoadGen(t *testing.T) {
	ctx := context.Background()
	deps := Deps{ProjectName: "test"}
	m := newModel(deps, ctx)

	initialGen := m.loadGen
	cmd := m.Init()

	// loadGen should be incremented to 1
	require.Equal(t, uint64(1), m.loadGen)
	require.NotEqual(t, initialGen, m.loadGen)
	require.NotNil(t, cmd, "Init should return a command")
}

// Test setActiveTab ignores out-of-range indices, preserving the per-key guard
// the explicit Tab1–Tab5 blocks used to carry (e.g. Tab5 with only 3 tabs).
func TestSetActiveTab_OutOfRangeNoop(t *testing.T) {
	ctx := context.Background()
	m := newModel(Deps{ProjectName: "test"}, ctx)
	m.tabs = []tab{
		{"Services", "content1"},
		{"Deploy", "content2"},
		{"Topology", "content3"},
	}
	m.active = 1
	m.reloadGen = 7
	m.loading = false

	// Index past the tab count is ignored: active and reloadGen unchanged.
	m.setActiveTab(4)
	require.Equal(t, 1, m.active, "out-of-range index should not change active")
	require.Equal(t, uint64(7), m.reloadGen, "out-of-range index should leave reloadGen untouched")

	// Negative index is ignored too.
	m.setActiveTab(-1)
	require.Equal(t, 1, m.active)

	// In-range index switches and resets the pending-reload generation.
	m.setActiveTab(2)
	require.Equal(t, 2, m.active)
	require.Equal(t, uint64(0), m.reloadGen, "valid switch should reset reloadGen")
}
