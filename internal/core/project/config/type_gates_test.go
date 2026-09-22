package config

import (
	"errors"
	"slices"
	"testing"
)

// TestValidateDependsOnTypes_rejectsTool confirms the loader-side gate rejects
// any depends_on target that resolves to a tool-typed service. The check runs
// on every LoadConfig path, not just `dwe validate`.
func TestValidateDependsOnTypes_rejectsTool(t *testing.T) {
	services := map[string]ServiceConfig{
		"app":     {Type: ServiceTypeApp, Container: "app", DependsOn: []string{"adminer"}},
		"adminer": {Type: ServiceTypeTool, Container: "adminer"},
	}
	err := validateDependsOnTypes(services)
	if err == nil {
		t.Fatal("expected ErrDependsOnTool")
	}
	if !errors.Is(err, ErrDependsOnTool) {
		t.Fatalf("err = %v, want wraps ErrDependsOnTool", err)
	}
}

// TestValidateDependsOnTypes_allowsAppAndInfra confirms depends_on between apps
// is legal, and depends_on on infra is legal as well.
func TestValidateDependsOnTypes_allowsAppAndInfra(t *testing.T) {
	services := map[string]ServiceConfig{
		"main":   {Type: ServiceTypeApp, Container: "main", DependsOn: []string{"db", "worker"}},
		"db":     {Type: ServiceTypeInfra, Container: "db"},
		"worker": {Type: ServiceTypeApp, Container: "worker"},
	}
	if err := validateDependsOnTypes(services); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateDependsOnTypes_unknownTargetTolerated confirms that unknown
// depends_on targets are NOT this gate's responsibility — TopoSortServices
// surfaces them at plan time.
func TestValidateDependsOnTypes_unknownTargetTolerated(t *testing.T) {
	services := map[string]ServiceConfig{
		"main": {Type: ServiceTypeApp, Container: "main", DependsOn: []string{"missing"}},
	}
	if err := validateDependsOnTypes(services); err != nil {
		t.Fatalf("unknown depends_on target should be tolerated here, got %v", err)
	}
}

// TestComposeFiles_grouped_tool_infra_app verifies the explicit grouping
// order: base → tools (sorted) → infra (sorted) → apps (sorted). This order is
// part of the public surface — overlay precedence depends on it.
func TestComposeFiles_grouped_tool_infra_app(t *testing.T) {
	cfg := &DweConfig{
		Compose: ComposeConfig{Base: "compose.yaml"},
		Services: map[string]ServiceConfig{
			"zzz_tool": {Type: ServiceTypeTool, Enabled: true, Compose: []string{"tool-z.yml"}},
			"aaa_tool": {Type: ServiceTypeTool, Enabled: true, Compose: []string{"tool-a.yml"}},
			"db":       {Type: ServiceTypeInfra, Enabled: true, Compose: []string{"infra-db.yml"}},
			"cache":    {Type: ServiceTypeInfra, Enabled: true, Compose: []string{"infra-cache.yml"}},
			"web":      {Type: ServiceTypeApp, Enabled: true, Compose: []string{"app-web.yml"}},
			"api":      {Type: ServiceTypeApp, Enabled: true, Compose: []string{"app-api.yml"}},
		},
	}
	want := []string{
		"compose.yaml",
		"tool-a.yml",
		"tool-z.yml",
		"infra-cache.yml",
		"infra-db.yml",
		"app-api.yml",
		"app-web.yml",
	}
	got := cfg.ComposeFiles()
	if len(got) != len(want) {
		t.Fatalf("ComposeFiles() len = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// ComposeFilesAll() must produce the same ordering when every service is enabled.
	all := cfg.ComposeFilesAll()
	for i := range want {
		if all[i] != want[i] {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, all[i], want[i])
		}
	}
}

// TestComposeFiles_composeAfterTier pins the compose_after tier: emitted
// after all three service groups, one pass sorted by service name across all
// types, list order kept within a service, gated by the same
// `all || svc.Enabled` rule as compose:.
func TestComposeFiles_composeAfterTier(t *testing.T) {
	baseServices := func() map[string]ServiceConfig {
		return map[string]ServiceConfig{
			"otel": {
				Type: ServiceTypeTool, Enabled: true,
				Compose:      []string{"tool-otel.yml"},
				ComposeAfter: []string{"after-otel-1.yml", "after-otel-2.yml"},
			},
			"cache": {
				Type: ServiceTypeInfra, Enabled: true,
				Compose:      []string{"infra-cache.yml"},
				ComposeAfter: []string{"after-cache.yml"},
			},
			"web": {
				Type: ServiceTypeApp, Enabled: true,
				Compose:      []string{"app-web.yml"},
				ComposeAfter: []string{"after-web.yml"},
			},
		}
	}

	t.Run("all owners enabled", func(t *testing.T) {
		cfg := &DweConfig{
			Compose:  ComposeConfig{Base: "compose.yaml"},
			Services: baseServices(),
		}
		want := []string{
			"compose.yaml",
			"tool-otel.yml",
			"infra-cache.yml",
			"app-web.yml",
			"after-cache.yml",
			"after-otel-1.yml",
			"after-otel-2.yml",
			"after-web.yml",
		}
		if got := cfg.ComposeFiles(); !slices.Equal(got, want) {
			t.Errorf("ComposeFiles() = %v, want %v", got, want)
		}
		if got := cfg.ComposeFilesAll(); !slices.Equal(got, want) {
			t.Errorf("ComposeFilesAll() = %v, want %v", got, want)
		}
	})

	t.Run("owner disabled: absent from ComposeFiles, present in ComposeFilesAll", func(t *testing.T) {
		services := baseServices()
		otel := services["otel"]
		otel.Enabled = false
		services["otel"] = otel
		cfg := &DweConfig{
			Compose:  ComposeConfig{Base: "compose.yaml"},
			Services: services,
		}
		active := []string{
			"compose.yaml",
			"infra-cache.yml",
			"app-web.yml",
			"after-cache.yml",
			"after-web.yml",
		}
		if got := cfg.ComposeFiles(); !slices.Equal(got, active) {
			t.Errorf("ComposeFiles() = %v, want %v", got, active)
		}
		allWant := []string{
			"compose.yaml",
			"tool-otel.yml",
			"infra-cache.yml",
			"app-web.yml",
			"after-cache.yml",
			"after-otel-1.yml",
			"after-otel-2.yml",
			"after-web.yml",
		}
		if got := cfg.ComposeFilesAll(); !slices.Equal(got, allWant) {
			t.Errorf("ComposeFilesAll() = %v, want %v", got, allWant)
		}
	})

	t.Run("required owner is always present", func(t *testing.T) {
		services := baseServices()
		otel := services["otel"]
		otel.Required = true
		otel.Enabled = true
		services["otel"] = otel
		cfg := &DweConfig{
			Compose:  ComposeConfig{Base: "compose.yaml"},
			Services: services,
		}
		want := []string{
			"compose.yaml",
			"tool-otel.yml",
			"infra-cache.yml",
			"app-web.yml",
			"after-cache.yml",
			"after-otel-1.yml",
			"after-otel-2.yml",
			"after-web.yml",
		}
		if got := cfg.ComposeFiles(); !slices.Equal(got, want) {
			t.Errorf("ComposeFiles() = %v, want %v", got, want)
		}
	})

	t.Run("no compose_after anywhere: byte-identical to the grouping-only chain", func(t *testing.T) {
		cfg := &DweConfig{
			Compose: ComposeConfig{Base: "compose.yaml"},
			Services: map[string]ServiceConfig{
				"zzz_tool": {Type: ServiceTypeTool, Enabled: true, Compose: []string{"tool-z.yml"}},
				"aaa_tool": {Type: ServiceTypeTool, Enabled: true, Compose: []string{"tool-a.yml"}},
				"db":       {Type: ServiceTypeInfra, Enabled: true, Compose: []string{"infra-db.yml"}},
				"cache":    {Type: ServiceTypeInfra, Enabled: true, Compose: []string{"infra-cache.yml"}},
				"web":      {Type: ServiceTypeApp, Enabled: true, Compose: []string{"app-web.yml"}},
				"api":      {Type: ServiceTypeApp, Enabled: true, Compose: []string{"app-api.yml"}},
			},
		}
		want := []string{
			"compose.yaml",
			"tool-a.yml",
			"tool-z.yml",
			"infra-cache.yml",
			"infra-db.yml",
			"app-api.yml",
			"app-web.yml",
		}
		if got := cfg.ComposeFiles(); !slices.Equal(got, want) {
			t.Errorf("ComposeFiles() = %v, want %v", got, want)
		}
		if got := cfg.ComposeFilesAll(); !slices.Equal(got, want) {
			t.Errorf("ComposeFilesAll() = %v, want %v", got, want)
		}
	})
}
