package docs

import (
	"testing"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/shared/i18n"
)

func TestCollectServiceSummaries_NilCfg(t *testing.T) {
	result := collectServiceSummaries(nil, i18n.NopTranslator{}, "en")
	if len(result) != 0 {
		t.Errorf("expected empty result for nil cfg, got %d items", len(result))
	}
}

func TestCollectServiceSummaries_Empty(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{},
	}
	result := collectServiceSummaries(cfg, i18n.NopTranslator{}, "en")
	if len(result) != 0 {
		t.Errorf("expected empty result for no services, got %d items", len(result))
	}
}

func TestCollectServiceSummaries_DeployOrder(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"api": {
				Type:    config.ServiceTypeApp,
				Enabled: true,
				Info:    config.ServiceInfoBlock{Title: "API Server"},
			},
			"db": {
				Type:    config.ServiceTypeInfra,
				Enabled: true,
			},
		},
	}
	result := collectServiceSummaries(cfg, i18n.NopTranslator{}, "en")
	// DeployOrder with ["app", "tool", "infra"] puts apps before infra.
	if len(result) != 2 {
		t.Fatalf("expected 2 services, got %d", len(result))
	}
	if result[0].Name != "api" {
		t.Errorf("expected api first (app type), got %s", result[0].Name)
	}
	if result[0].Type != "app" {
		t.Errorf("expected type app, got %s", result[0].Type)
	}
	if result[0].Title != "API Server" {
		t.Errorf("expected title 'API Server', got %q", result[0].Title)
	}
	if result[1].Name != "db" {
		t.Errorf("expected db second (infra type), got %s", result[1].Name)
	}
	if result[1].Title != "" {
		t.Errorf("expected empty title for db, got %q", result[1].Title)
	}
}

func TestCollectServiceSummaries_DisabledExcluded(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"enabled":  {Type: config.ServiceTypeApp, Enabled: true},
			"disabled": {Type: config.ServiceTypeApp, Enabled: false},
		},
	}
	result := collectServiceSummaries(cfg, i18n.NopTranslator{}, "en")
	if len(result) != 1 {
		t.Fatalf("expected 1 service (disabled excluded), got %d", len(result))
	}
	if result[0].Name != "enabled" {
		t.Errorf("expected 'enabled', got %s", result[0].Name)
	}
}

func TestCollectCommandSummaries_NilRegistry(t *testing.T) {
	result := collectCommandSummaries(nil, i18n.NopTranslator{}, "en")
	if len(result) != 0 {
		t.Errorf("expected empty result for nil registry, got %d items", len(result))
	}
}

func TestCollectCommandSummaries_Empty(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()
	result := collectCommandSummaries(reg, i18n.NopTranslator{}, "en")
	if len(result) != 0 {
		t.Errorf("expected empty result for empty registry, got %d items", len(result))
	}
}

func TestCollectCommandSummaries_PrivateExcluded(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:          "build",
		Description: "build the project",
		Type:        usercommands.CommandTypeShell,
	})
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:          "test",
		Description: "run tests",
		Type:        usercommands.CommandTypeShell,
	})
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:          "internal",
		Description: "private command",
		Type:        usercommands.CommandTypeShell,
		Private:     true,
	})

	result := collectCommandSummaries(reg, i18n.NopTranslator{}, "en")
	if len(result) != 2 {
		t.Fatalf("expected 2 commands (private excluded), got %d", len(result))
	}
	// Registry.List sorts by ID.
	if result[0].ID != "build" {
		t.Errorf("expected build first, got %s", result[0].ID)
	}
	if result[0].Description != "build the project" {
		t.Errorf("expected description 'build the project', got %q", result[0].Description)
	}
	if result[1].ID != "test" {
		t.Errorf("expected test second, got %s", result[1].ID)
	}
}

func TestCollectCommandSummaries_TranslatorUsed(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:          "deploy",
		Description: "deploy the project",
		Type:        usercommands.CommandTypeShell,
	})

	// NopTranslator returns the fallback — so description should pass through unchanged.
	result := collectCommandSummaries(reg, i18n.NopTranslator{}, "en")
	if len(result) != 1 {
		t.Fatalf("expected 1 command, got %d", len(result))
	}
	if result[0].Description != "deploy the project" {
		t.Errorf("expected 'deploy the project', got %q", result[0].Description)
	}
}

func TestCollectInfoSummary_NilCfg(t *testing.T) {
	result := collectInfoSummary(nil, "")
	if result != nil {
		t.Errorf("expected nil for nil cfg, got %+v", result)
	}
}

func TestCollectInfoSummary_EmptyProject(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{},
	}
	result := collectInfoSummary(cfg, "")
	// No name, no URLs, no hosts -> nil.
	if result != nil {
		t.Errorf("expected nil for empty project with no name, got %+v", result)
	}
}

func TestCollectInfoSummary_WithProjectName(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project:  config.ProjectConfig{Name: "my-project"},
		Services: map[string]config.ServiceConfig{},
	}
	result := collectInfoSummary(cfg, "")
	if result == nil {
		t.Fatal("expected non-nil result when project has a name")
	}
	if result.Title != "my-project" {
		t.Errorf("expected title 'my-project', got %q", result.Title)
	}
	if len(result.URLs) != 0 {
		t.Errorf("expected no URLs, got %v", result.URLs)
	}
	if len(result.Hosts) != 0 {
		t.Errorf("expected no Hosts, got %v", result.Hosts)
	}
}

func TestCollectInfoSummary_ServicesProvideURLsAndHosts(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "my-project"},
		Services: map[string]config.ServiceConfig{
			"api": {
				Type:    config.ServiceTypeApp,
				Enabled: true,
				Info:    config.ServiceInfoBlock{PrimaryHost: "api.local"},
				Hosts:   map[string]string{"main": "api.local"},
			},
		},
	}
	result := collectInfoSummary(cfg, "")
	if result == nil {
		t.Fatal("expected non-nil info summary")
	}
	if result.Title != "my-project" {
		t.Errorf("expected title 'my-project', got %q", result.Title)
	}
	if len(result.URLs) != 1 || result.URLs[0] != "http://api.local" {
		t.Errorf("expected ['http://api.local'], got %v", result.URLs)
	}
	if len(result.Hosts) != 1 || result.Hosts[0] != "api.local" {
		t.Errorf("expected ['api.local'], got %v", result.Hosts)
	}
}

func TestCollectInfoSummary_HostsSortedDeterministically(t *testing.T) {
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "p"},
		Services: map[string]config.ServiceConfig{
			"svc": {
				Type:    config.ServiceTypeApp,
				Enabled: true,
				Hosts: map[string]string{
					"z-host": "z.local",
					"a-host": "a.local",
					"m-host": "m.local",
				},
			},
		},
	}
	// Run multiple times to catch map iteration non-determinism.
	var first []string
	for i := range 10 {
		result := collectInfoSummary(cfg, "")
		if result == nil {
			t.Fatal("expected non-nil summary")
		}
		if i == 0 {
			first = result.Hosts
		} else {
			if len(result.Hosts) != len(first) {
				t.Fatalf("hosts length changed: %v vs %v", result.Hosts, first)
			}
			for j := range first {
				if result.Hosts[j] != first[j] {
					t.Errorf("non-deterministic hosts order: run %d got %v, run 0 got %v", i, result.Hosts, first)
					break
				}
			}
		}
	}
}
