package statustui

import (
	"context"
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
