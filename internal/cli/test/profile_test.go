package test

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"
)

// TestCostProfile_ScenarioRawDoesNotMutateProjectConfig pins that the Raw
// patch serving exports.env when: evaluation stays on the per-scenario view:
// profiling a scenario that enables a service leaves the project config's
// typed and Raw enabled state untouched.
func TestCostProfile_ScenarioRawDoesNotMutateProjectConfig(t *testing.T) {
	baseDir := t.TempDir()
	writeMinimalProject(t, baseDir)
	writeProjectFile(t, baseDir, "workspace/services/redis/service.yml", "type: infra\ncontainer: redis\n")

	p := newCostProfiler(baseDir, "")
	if p == nil {
		t.Fatal("expected a profiler for a loadable project")
	}
	scn := &envtest.Scenario{Env: envtest.ScenarioEnv{Services: envtest.ScenarioServices{Enable: []string{"redis"}}}}
	if got := p.profile(scn); got == nil || got.EnabledServices != 2 {
		t.Fatalf("profile = %+v, want 2 enabled services", got)
	}

	if p.cfg.Services["redis"].Enabled {
		t.Error("profile mutated the typed project config")
	}
	rawServices, _ := p.cfg.Raw["services"].(map[string]any)
	redis, _ := rawServices["redis"].(map[string]any)
	if redis["enabled"] != false {
		t.Errorf("project Raw services.redis.enabled = %v, want false", redis["enabled"])
	}
}

// TestCostProfile_IgnoresLocalComposeExtra pins that the profile is computed
// on the scenario's view of the project — which drops the per-developer
// compose overlays, exactly as the copy's seeded local.yml does. A build
// service that exists only in such an overlay is not part of what the copy
// would run, so it must not be counted.
func TestCostProfile_IgnoresLocalComposeExtra(t *testing.T) {
	baseDir := t.TempDir()
	writeMinimalProject(t, baseDir)
	writeProjectFile(t, baseDir, "local.compose.yml", "services:\n  extra:\n    build: .\n  cache:\n    image: redis:7\n")
	writeProjectFile(t, baseDir, "workspace/local.yml", "compose:\n  extra:\n    - local.compose.yml\n")

	p := newCostProfiler(baseDir, "")
	if p == nil {
		t.Fatal("expected a profiler for a loadable project")
	}
	if len(p.cfg.Compose.Extra) != 1 {
		t.Fatalf("fixture did not load the overlay: %v", p.cfg.Compose.Extra)
	}

	got := p.profile(&envtest.Scenario{})
	if got == nil {
		t.Fatal("expected a profile")
	}
	if len(got.BuildServices) != 0 {
		t.Errorf("build_services = %v, want none (the overlay is not part of the copy)", got.BuildServices)
	}
	if len(got.ExternalImages) != 0 {
		t.Errorf("external_images = %v, want none (the overlay is not part of the copy)", got.ExternalImages)
	}
}

// TestCostProfile_ComposeAfterTracksOwnerEnabled pins that the profile feeds
// ScenarioView into both scanners (profile.go:188), so a compose_after file's
// build: service is counted only when its owning service is enabled in that
// scenario — unlike TestCostProfile_IgnoresLocalComposeExtra, compose_after
// is a tracked field the view keeps, not a per-developer overlay it strips.
func TestCostProfile_ComposeAfterTracksOwnerEnabled(t *testing.T) {
	baseDir := t.TempDir()
	writeMinimalProject(t, baseDir)
	writeProjectFile(t, baseDir, "workspace/services/otel/service.yml", "type: tool\ncontainer: otel\ncompose_after:\n  - compose/otel-apps.yml\n")
	writeProjectFile(t, baseDir, "compose/otel-apps.yml", "services:\n  worker:\n    build: .\n")

	p := newCostProfiler(baseDir, "")
	if p == nil {
		t.Fatal("expected a profiler for a loadable project")
	}

	disabled := p.profile(&envtest.Scenario{})
	if disabled == nil {
		t.Fatal("expected a profile")
	}
	if len(disabled.BuildServices) != 0 {
		t.Errorf("build_services (otel disabled) = %v, want none", disabled.BuildServices)
	}

	enabled := p.profile(&envtest.Scenario{Env: envtest.ScenarioEnv{Services: envtest.ScenarioServices{Enable: []string{"otel"}}}})
	if enabled == nil {
		t.Fatal("expected a profile")
	}
	if want := []string{"worker"}; len(enabled.BuildServices) != 1 || enabled.BuildServices[0] != want[0] {
		t.Errorf("build_services (otel enabled) = %v, want %v", enabled.BuildServices, want)
	}
}

// TestCostProfile_IsolationFindingsOmitTracedVarPorts pins that a host port
// traced to a vars: path never reaches isolation_findings: the runner remaps
// it with no scenario config, so listing it would be advice with no action.
// An untraced variable in the same compose file still shows up.
func TestCostProfile_IsolationFindingsOmitTracedVarPorts(t *testing.T) {
	baseDir := t.TempDir()
	writeProjectFile(t, baseDir, "workspace.yml", `project:
  name: demo
compose:
  base: compose.yaml
vars:
  ports:
    valkey: 6380
exports:
  env:
    - name: VALKEY_PORT
      from: vars.ports.valkey
`)
	writeProjectFile(t, baseDir, "workspace/services/app/service.yml", "type: app\ncontainer: app\nrequired: true\n")
	writeProjectFile(t, baseDir, "compose.yaml", `services:
  valkey:
    image: valkey/valkey:8
    ports: ["${VALKEY_PORT:-6379}:6379"]
  other:
    image: redis:7
    ports: ["${OTHER_PORT:-6390}:6379"]
`)

	p := newCostProfiler(baseDir, "")
	if p == nil {
		t.Fatal("expected a profiler for a loadable project")
	}
	got := p.profile(&envtest.Scenario{})
	if got == nil {
		t.Fatal("expected a profile")
	}
	var resources []string
	for _, f := range got.IsolationFindings {
		if f.Kind == "interpolated_host_port" {
			resources = append(resources, f.Resource)
		}
	}
	if len(resources) != 1 || resources[0] != "other" {
		t.Errorf("interpolated_host_port findings = %v, want only the untraced [other]", resources)
	}
}
