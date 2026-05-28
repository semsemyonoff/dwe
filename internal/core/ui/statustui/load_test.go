package statustui

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"devbox-cli/internal/core/project/config"
)

// testTool mirrors the one in internal/core/project/stack/testhelpers_test.go for test config building.
type testTool struct {
	Enabled   bool
	Container string
	Host      string
	Port      int
}

// makeServicesCfg builds a minimal DevboxConfig for tests.
func makeServicesCfg(services map[string]config.ServiceConfig, tools map[string]testTool) *config.DevboxConfig {
	merged := make(map[string]config.ServiceConfig, len(services)+len(tools))
	for k, v := range services {
		if v.Type == "" {
			v.Type = config.ServiceTypeApp
		}
		merged[k] = v
	}
	for k, v := range tools {
		svc := config.ServiceConfig{
			Type:      config.ServiceTypeTool,
			Container: v.Container,
			Enabled:   v.Enabled,
		}
		if v.Port != 0 {
			svc.Ports = map[string]int{"main": v.Port}
		}
		if v.Host != "" {
			svc.Hosts = map[string]string{"main": v.Host}
		}
		merged[k] = svc
	}
	cfg := &config.DevboxConfig{Services: merged}
	cfg.Project.Name = "test-project"
	cfg.Project.Prefix = "devbox"
	return cfg
}

func TestJoinNonEmpty(t *testing.T) {
	tests := []struct {
		name     string
		parts    []string
		expected string
	}{
		{
			name:     "all empty",
			parts:    []string{"", "", ""},
			expected: "",
		},
		{
			name:     "single non-empty",
			parts:    []string{"hello"},
			expected: "hello",
		},
		{
			name:     "multiple non-empty",
			parts:    []string{"hello", "world"},
			expected: "hello\nworld",
		},
		{
			name:     "mixed with empties",
			parts:    []string{"", "hello", "", "world", ""},
			expected: "hello\nworld",
		},
		{
			name:     "whitespace treated as empty",
			parts:    []string{" ", "\t", "hello", "  \n  "},
			expected: "hello",
		},
		{
			name:     "no trailing/leading newlines",
			parts:    []string{"hello", "world"},
			expected: "hello\nworld",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := joinNonEmpty(tt.parts...)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildTabs_AllRunning(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: config.ServiceTypeApp, Container: "app-main", Required: true},
		},
		nil,
	)
	alwaysRunning := func(_, _ string) bool { return true }

	deps := Deps{
		Cfg:         cfg,
		IsRunning:   alwaysRunning,
		ProjectName: "test",
	}

	tabs, _ := buildTabs(context.Background(), deps)

	require.Equal(t, 5, len(tabs), "expected 5 tabs")
	require.Equal(t, "Services", tabs[0].title)
	require.Equal(t, "Deploy", tabs[1].title)
	require.Equal(t, "Topology", tabs[2].title)
	require.Equal(t, "Git", tabs[3].title)
	require.Equal(t, "Daemons", tabs[4].title)

	// Services tab should contain the app name and not be a placeholder
	require.NotEqual(t, "no services configured", tabs[0].content)
	require.Contains(t, tabs[0].content, "main")
}

func TestBuildTabs_AllStopped(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: config.ServiceTypeApp, Container: "app-main", Required: true},
		},
		nil,
	)
	neverRunning := func(_, _ string) bool { return false }

	deps := Deps{
		Cfg:         cfg,
		IsRunning:   neverRunning,
		ProjectName: "test",
	}

	tabs, _ := buildTabs(context.Background(), deps)

	require.Equal(t, 5, len(tabs))
	// Content should still show the app, just not running
	require.Contains(t, tabs[0].content, "main")
}

func TestBuildTabs_Partial(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main":   {Type: config.ServiceTypeApp, Container: "app-main", Required: true},
			"second": {Type: config.ServiceTypeApp, Container: "app-second", Enabled: true},
		},
		nil,
	)
	partialRunning := func(_, container string) bool {
		return container == "app-main"
	}

	deps := Deps{
		Cfg:         cfg,
		IsRunning:   partialRunning,
		ProjectName: "test",
	}

	tabs, _ := buildTabs(context.Background(), deps)

	require.Equal(t, 5, len(tabs))
	require.Contains(t, tabs[0].content, "main")
	require.Contains(t, tabs[0].content, "second")
}

func TestBuildTabs_EmptyService(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{},
		nil,
	)

	deps := Deps{
		Cfg:         cfg,
		IsRunning:   func(_, _ string) bool { return false },
		ProjectName: "test",
	}

	tabs, _ := buildTabs(context.Background(), deps)

	require.Equal(t, 5, len(tabs))
	// Services tab should show placeholder when no services configured
	require.Equal(t, "no services configured", tabs[0].content)
}

func TestBuildTabs_WithNilState(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: config.ServiceTypeApp, Container: "app-main", Required: true},
		},
		nil,
	)

	deps := Deps{
		Cfg:         cfg,
		IsRunning:   func(_, _ string) bool { return false },
		ProjectName: "test",
		State:       nil, // No deploy state
	}

	tabs, _ := buildTabs(context.Background(), deps)

	// Deploy tab should show placeholder when no state
	require.Equal(t, "no deploy status", tabs[1].content)
}

func TestBuildTabs_PrependsWarningOnRenderError(t *testing.T) {
	// Create a service with a broken template expression that will trigger an error
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {
				Type:      config.ServiceTypeApp,
				Container: "app-main",
				Required:  true,
				Status: []config.StatusColumn{
					{Name: "X", Value: "{{ this is broken syntax"},
				},
			},
		},
		nil,
	)

	deps := Deps{
		Cfg:         cfg,
		IsRunning:   func(_, _ string) bool { return false },
		ProjectName: "test",
	}

	tabs, _ := buildTabs(context.Background(), deps)

	// Services tab should have a warning prefix because RenderApps will return an error
	require.Contains(t, tabs[0].content, "⚠", "expected warning symbol in services tab when render error occurs")
}

func TestWarningPrefix(t *testing.T) {
	tests := []struct {
		name    string
		count   int
		hasText string
	}{
		{
			name:    "zero errors",
			count:   0,
			hasText: "",
		},
		{
			name:    "one error",
			count:   1,
			hasText: "1 expression failed",
		},
		{
			name:    "multiple errors",
			count:   3,
			hasText: "3 expression(s) failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := warningPrefix(tt.count)
			if tt.count == 0 {
				require.Equal(t, "", result)
			} else {
				require.Contains(t, result, "⚠")
				if tt.hasText != "" {
					require.Contains(t, result, tt.hasText)
				}
			}
		})
	}
}

func TestNormaliseDocker(t *testing.T) {
	t.Run("nil returns empty config", func(t *testing.T) {
		result := normaliseDocker(nil)
		require.NotNil(t, result)
		require.Equal(t, &config.DockerConfig{}, result)
	})

	t.Run("non-nil returns input", func(t *testing.T) {
		input := &config.DockerConfig{ProjectName: "test"}
		result := normaliseDocker(input)
		require.Equal(t, input, result)
	})
}
