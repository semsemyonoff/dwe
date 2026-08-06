package statustui

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

func TestNewModel_Defaults(t *testing.T) {
	ctx := context.Background()
	deps := Deps{Cfg: &config.DweConfig{}}

	m := newModel(deps, ctx)

	require.NotNil(t, m)
	require.Same(t, deps.Cfg, m.deps.Cfg)
	require.True(t, m.loading, "loading should be true initially")
	require.Equal(t, 0, m.active, "active tab should be 0 initially")
	require.False(t, m.loaded, "loaded should be false initially")
}

func TestRenderTabStrip(t *testing.T) {
	ctx := context.Background()
	deps := Deps{}
	m := newModel(deps, ctx)
	m.loaded = true

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
			require.Contains(t, tabStrip, tabTitles[tt.activeTab])

			// All tabs should be present
			for _, title := range tabTitles {
				require.Contains(t, tabStrip, title)
			}

			// Check for active tab styling markers
			require.Contains(t, tabStrip, "▌", "should contain left corner for active tab")
			require.Contains(t, tabStrip, "▐", "should contain right corner for active tab")
		})
	}
}

func TestRenderTabStrip_EmptyTabs(t *testing.T) {
	ctx := context.Background()
	deps := Deps{}
	m := newModel(deps, ctx)

	tabStrip := m.renderTabStrip()
	require.Empty(t, strings.TrimSpace(tabStrip), "should return empty for no tabs")
}

// Test Init method
func TestInit_BumpsLoadGen(t *testing.T) {
	ctx := context.Background()
	deps := Deps{}
	m := newModel(deps, ctx)

	initialGen := m.loadGen
	cmd := m.Init()

	// loadGen should be incremented to 1
	require.Equal(t, uint64(1), m.loadGen)
	require.NotEqual(t, initialGen, m.loadGen)
	require.NotNil(t, cmd, "Init should return a command")
}

// Test setActiveTab ignores out-of-range indices (relative to the fixed
// tabTitles) and any switch attempted before the first load completes.
func TestSetActiveTab_OutOfRangeNoop(t *testing.T) {
	ctx := context.Background()
	m := newModel(Deps{}, ctx)
	m.loaded = true
	m.active = 1
	m.reloadGen = 7
	m.loading = false

	// Index past len(tabTitles) is ignored: active and reloadGen unchanged.
	m.setActiveTab(len(tabTitles))
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

// TestSetActiveTab_NoopBeforeLoaded verifies the switch is ignored entirely
// until the first tabsLoadedMsg has been applied, even for an in-range index.
func TestSetActiveTab_NoopBeforeLoaded(t *testing.T) {
	ctx := context.Background()
	m := newModel(Deps{}, ctx)
	m.active = 0

	m.setActiveTab(2)
	require.Equal(t, 0, m.active, "switch before load should be ignored")
}
