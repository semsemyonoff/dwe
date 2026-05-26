package stack

import (
	"slices"
	"testing"

	"devbox-cli/internal/config"
)

func TestDeployOrder_Empty(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cfg   *config.DevboxConfig
		types []string
		want  []string
	}{
		{
			name:  "nil config",
			cfg:   nil,
			types: []string{"app", "tool"},
			want:  nil,
		},
		{
			name:  "nil services map",
			cfg:   &config.DevboxConfig{},
			types: []string{"app", "tool"},
			want:  nil,
		},
		{
			name:  "empty types",
			cfg:   &config.DevboxConfig{Services: map[string]config.ServiceConfig{}},
			types: []string{},
			want:  nil,
		},
		{
			name:  "empty services",
			cfg:   &config.DevboxConfig{Services: map[string]config.ServiceConfig{}},
			types: []string{"app", "tool", "infra"},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DeployOrder(tt.cfg, tt.types)
			if !slices.Equal(got, tt.want) {
				t.Errorf("DeployOrder() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeployOrder_DisabledSkipped(t *testing.T) {
	t.Parallel()
	// Create a config with both enabled and disabled services.
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"app1": {
				Type:    "app",
				Enabled: true,
			},
			"app2": {
				Type:    "app",
				Enabled: false, // disabled, should be skipped
			},
			"tool1": {
				Type:    "tool",
				Enabled: true,
			},
		},
	}
	got := DeployOrder(cfg, []string{"app", "tool"})
	// Should include app1 (enabled) but NOT app2 (disabled); should include tool1.
	// Order may vary due to map iteration, but disabled must be absent.
	if len(got) != 2 {
		t.Errorf("DeployOrder() returned %d services, want 2 (disabled should be skipped)", len(got))
	}
	if !slices.Contains(got, "app1") {
		t.Errorf("DeployOrder() missing enabled app1: %v", got)
	}
	if slices.Contains(got, "app2") {
		t.Errorf("DeployOrder() included disabled app2: %v", got)
	}
	if !slices.Contains(got, "tool1") {
		t.Errorf("DeployOrder() missing tool1: %v", got)
	}
}

func TestDeployOrder_TypeGrouping(t *testing.T) {
	t.Parallel()
	// Create a config with mixed types.
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"app1": {
				Type:    "app",
				Enabled: true,
			},
			"app2": {
				Type:    "app",
				Enabled: true,
			},
			"tool1": {
				Type:    "tool",
				Enabled: true,
			},
			"infra1": {
				Type:    "infra",
				Enabled: true,
			},
		},
	}
	got := DeployOrder(cfg, []string{"app", "tool", "infra"})
	// All services should be present in the specified type order.
	if len(got) != 4 {
		t.Errorf("DeployOrder() returned %d services, want 4", len(got))
	}
	// Apps should come before tools, tools before infra.
	// Extract indices to verify order.
	appIdx := -1
	toolIdx := -1
	infraIdx := -1
	for i, name := range got {
		if name == "app1" || name == "app2" {
			if appIdx == -1 {
				appIdx = i
			}
		}
		if name == "tool1" {
			toolIdx = i
		}
		if name == "infra1" {
			infraIdx = i
		}
	}
	if appIdx >= toolIdx {
		t.Errorf("DeployOrder() apps should come before tools: %v", got)
	}
	if toolIdx >= infraIdx {
		t.Errorf("DeployOrder() tools should come before infra: %v", got)
	}
}

func TestDeployOrder_DependsOnOrdering(t *testing.T) {
	t.Parallel()
	// Create a config where services have dependencies.
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"app1": {
				Type:      "app",
				Enabled:   true,
				DependsOn: []string{"app2"}, // app1 depends on app2
			},
			"app2": {
				Type:    "app",
				Enabled: true,
				// no dependencies
			},
		},
	}
	got := DeployOrder(cfg, []string{"app"})
	// app2 should come before app1 (since app1 depends on app2).
	if len(got) != 2 {
		t.Errorf("DeployOrder() returned %d services, want 2", len(got))
	}
	idx1 := slices.Index(got, "app1")
	idx2 := slices.Index(got, "app2")
	if idx2 >= idx1 {
		t.Errorf("DeployOrder() dependency ordering failed: app2 should come before app1: %v", got)
	}
}

func TestDeployOrder_AlphabeticFallback(t *testing.T) {
	t.Parallel()
	// Create a config with a circular dependency (app1 -> app2 -> app1).
	// DeployOrder should fall back to alphabetic order silently.
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"app1": {
				Type:      "app",
				Enabled:   true,
				DependsOn: []string{"app2"},
			},
			"app2": {
				Type:      "app",
				Enabled:   true,
				DependsOn: []string{"app1"},
			},
		},
	}
	got := DeployOrder(cfg, []string{"app"})
	// Should return both services in some order (fallback to alphabetic).
	if len(got) != 2 {
		t.Errorf("DeployOrder() returned %d services, want 2", len(got))
	}
	// Should be in alphabetic order (app1, app2) after fallback.
	if got[0] != "app1" || got[1] != "app2" {
		t.Errorf("DeployOrder() circular dependency fallback incorrect: %v, want [app1 app2]", got)
	}
}

func TestDeployOrder_NoDependenciesAlphabetic(t *testing.T) {
	t.Parallel()
	// Create a config with services that have no dependencies.
	// They should be ordered alphabetically (pre-sort before topo-sort).
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"zebra": {
				Type:    "app",
				Enabled: true,
			},
			"apple": {
				Type:    "app",
				Enabled: true,
			},
			"banana": {
				Type:    "app",
				Enabled: true,
			},
		},
	}
	got := DeployOrder(cfg, []string{"app"})
	want := []string{"apple", "banana", "zebra"}
	if !slices.Equal(got, want) {
		t.Errorf("DeployOrder() = %v, want %v", got, want)
	}
}

func TestDeployOrder_MapIterationDeterminism(t *testing.T) {
	t.Parallel()
	// Create a config with many services in random map insertion order.
	// Run multiple times and verify consistent output (proves map iteration
	// doesn't affect the result).
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"z_app": {
				Type:    "app",
				Enabled: true,
			},
			"a_app": {
				Type:    "app",
				Enabled: true,
			},
			"m_app": {
				Type:    "app",
				Enabled: true,
			},
		},
	}
	first := DeployOrder(cfg, []string{"app"})
	// Run again to ensure determinism.
	second := DeployOrder(cfg, []string{"app"})
	if !slices.Equal(first, second) {
		t.Errorf("DeployOrder() not deterministic: got %v then %v", first, second)
	}
	// Verify alphabetic order.
	if !slices.Equal(first, []string{"a_app", "m_app", "z_app"}) {
		t.Errorf("DeployOrder() = %v, want [a_app m_app z_app]", first)
	}
}

func TestDeployOrder_MultipleTypeGroups(t *testing.T) {
	t.Parallel()
	// Create a config with mixed types and dependencies within each type.
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"app_b": {
				Type:      "app",
				Enabled:   true,
				DependsOn: []string{"app_a"},
			},
			"app_a": {
				Type:    "app",
				Enabled: true,
			},
			"tool_b": {
				Type:      "tool",
				Enabled:   true,
				DependsOn: []string{"tool_a"},
			},
			"tool_a": {
				Type:    "tool",
				Enabled: true,
			},
		},
	}
	got := DeployOrder(cfg, []string{"app", "tool"})
	if len(got) != 4 {
		t.Errorf("DeployOrder() returned %d services, want 4", len(got))
	}
	// Verify apps come before tools.
	appIndices := []int{}
	toolIndices := []int{}
	for i, name := range got {
		if name == "app_a" || name == "app_b" {
			appIndices = append(appIndices, i)
		}
		if name == "tool_a" || name == "tool_b" {
			toolIndices = append(toolIndices, i)
		}
	}
	if len(appIndices) > 0 && len(toolIndices) > 0 {
		maxApp := appIndices[len(appIndices)-1]
		minTool := toolIndices[0]
		if maxApp >= minTool {
			t.Errorf("DeployOrder() apps should come before tools: %v", got)
		}
	}
	// Verify within-type ordering.
	idx_app_a := slices.Index(got, "app_a")
	idx_app_b := slices.Index(got, "app_b")
	if idx_app_a >= idx_app_b {
		t.Errorf("DeployOrder() app_a should come before app_b (dependency): %v", got)
	}
	idx_tool_a := slices.Index(got, "tool_a")
	idx_tool_b := slices.Index(got, "tool_b")
	if idx_tool_a >= idx_tool_b {
		t.Errorf("DeployOrder() tool_a should come before tool_b (dependency): %v", got)
	}
}

func TestDeployOrder_OnlySelectedTypes(t *testing.T) {
	t.Parallel()
	// Create a config with multiple types, but only request some.
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"app1": {
				Type:    "app",
				Enabled: true,
			},
			"tool1": {
				Type:    "tool",
				Enabled: true,
			},
			"infra1": {
				Type:    "infra",
				Enabled: true,
			},
		},
	}
	// Only request app and infra, not tool.
	got := DeployOrder(cfg, []string{"app", "infra"})
	if len(got) != 2 {
		t.Errorf("DeployOrder() returned %d services, want 2", len(got))
	}
	if !slices.Contains(got, "app1") {
		t.Errorf("DeployOrder() missing app1: %v", got)
	}
	if !slices.Contains(got, "infra1") {
		t.Errorf("DeployOrder() missing infra1: %v", got)
	}
	if slices.Contains(got, "tool1") {
		t.Errorf("DeployOrder() should not include tool1: %v", got)
	}
}

func TestDeployOrder_EmptyTypesSlice(t *testing.T) {
	t.Parallel()
	// Request with empty types slice.
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"app1": {
				Type:    "app",
				Enabled: true,
			},
		},
	}
	got := DeployOrder(cfg, []string{})
	if len(got) != 0 {
		t.Errorf("DeployOrder() with empty types = %v, want nil", got)
	}
}
