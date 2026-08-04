package statustui

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// testTool mirrors the one in internal/core/project/stack/testhelpers_test.go for test config building.
type testTool struct {
	Enabled   bool
	Container string
	Host      string
	Port      int
}

// makeServicesCfg builds a minimal DweConfig for tests.
func makeServicesCfg(services map[string]config.ServiceConfig, tools map[string]testTool) *config.DweConfig {
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
			svc.Ports = map[string]config.ServicePortSpec{"main": {Port: v.Port}}
		}
		if v.Host != "" {
			svc.Hosts = map[string]string{"main": v.Host}
		}
		merged[k] = svc
	}
	cfg := &config.DweConfig{Services: merged}
	cfg.Project.Name = "test-project"
	cfg.Project.Prefix = "dwe"
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
	alwaysRunning := func(_ string) bool { return true }

	deps := Deps{
		Cfg:       cfg,
		IsRunning: alwaysRunning,
	}

	snap, _ := buildTabs(context.Background(), deps)

	require.Equal(t, 5, len(tabTitles), "expected 5 tabs")
	require.Equal(t, "Services", tabTitles[0])
	require.Equal(t, "Deploy", tabTitles[1])
	require.Equal(t, "Topology", tabTitles[2])
	require.Equal(t, "Git", tabTitles[3])
	require.Equal(t, "Daemons", tabTitles[4])

	servicesBody, _ := renderTab(snap, 0, 0)
	// Services tab should contain the app name and not be a placeholder
	require.NotEqual(t, "no services configured", servicesBody)
	require.Contains(t, servicesBody, "main")
}

func TestBuildTabs_AllStopped(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: config.ServiceTypeApp, Container: "app-main", Required: true},
		},
		nil,
	)
	neverRunning := func(_ string) bool { return false }

	deps := Deps{
		Cfg:       cfg,
		IsRunning: neverRunning,
	}

	snap, _ := buildTabs(context.Background(), deps)

	servicesBody, _ := renderTab(snap, 0, 0)
	// Content should still show the app, just not running
	require.Contains(t, servicesBody, "main")
}

func TestBuildTabs_Partial(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main":   {Type: config.ServiceTypeApp, Container: "app-main", Required: true},
			"second": {Type: config.ServiceTypeApp, Container: "app-second", Enabled: true},
		},
		nil,
	)
	partialRunning := func(container string) bool {
		return container == "app-main"
	}

	deps := Deps{
		Cfg:       cfg,
		IsRunning: partialRunning,
	}

	snap, _ := buildTabs(context.Background(), deps)

	servicesBody, _ := renderTab(snap, 0, 0)
	require.Contains(t, servicesBody, "main")
	require.Contains(t, servicesBody, "second")
}

func TestBuildTabs_EmptyService(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{},
		nil,
	)

	deps := Deps{
		Cfg:       cfg,
		IsRunning: func(_ string) bool { return false },
	}

	snap, _ := buildTabs(context.Background(), deps)

	// Services tab should show placeholder when no services configured
	servicesBody, _ := renderTab(snap, 0, 0)
	require.Equal(t, "no services configured", servicesBody)
}

func TestBuildTabs_WithNilState(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: config.ServiceTypeApp, Container: "app-main", Required: true},
		},
		nil,
	)

	deps := Deps{
		Cfg:       cfg,
		IsRunning: func(_ string) bool { return false },
		State:     nil, // No deploy state
	}

	snap, _ := buildTabs(context.Background(), deps)

	// Deploy tab should show placeholder when no state
	deployBody, _ := renderTab(snap, 1, 0)
	require.Equal(t, "no deploy status", deployBody)
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
		Cfg:       cfg,
		IsRunning: func(_ string) bool { return false },
	}

	snap, _ := buildTabs(context.Background(), deps)

	// Services tab should have a warning prefix because RenderApps will return an error
	servicesBody, _ := renderTab(snap, 0, 0)
	require.Contains(t, servicesBody, "⚠", "expected warning symbol in services tab when render error occurs")
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
