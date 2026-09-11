package test

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
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

func TestScenarioRaw(t *testing.T) {
	raw := map[string]any{
		"vars":     map[string]any{"x": 1},
		"services": map[string]any{"redis": map[string]any{"enabled": false, "type": "infra"}},
	}
	services := map[string]config.ServiceConfig{
		"redis": {Enabled: true},
		"core":  {Enabled: true, Required: true},
	}

	if got := scenarioRaw(raw, services, &envtest.Scenario{}); !sameMap(got, raw) {
		t.Error("no toggles must return raw itself")
	}

	scn := &envtest.Scenario{Env: envtest.ScenarioEnv{Services: envtest.ScenarioServices{
		Enable:  []string{"redis", "unknown"},
		Disable: []string{"core"},
	}}}
	got := scenarioRaw(raw, services, scn)
	gotServices := got["services"].(map[string]any)
	redis := gotServices["redis"].(map[string]any)
	if redis["enabled"] != true || redis["type"] != "infra" {
		t.Errorf("redis entry = %v, want enabled patched and other keys kept", redis)
	}
	// A required service a scenario disables stays enabled (loader semantics).
	if core := gotServices["core"].(map[string]any); core["enabled"] != true {
		t.Errorf("core entry = %v, want enabled true", core)
	}
	if _, ok := gotServices["unknown"]; ok {
		t.Error("an unknown service must not be added")
	}
	if orig := raw["services"].(map[string]any)["redis"].(map[string]any); orig["enabled"] != false {
		t.Errorf("input raw mutated: %v", orig)
	}
	if _, ok := raw["services"].(map[string]any)["core"]; ok {
		t.Error("input raw services mutated")
	}
}

// sameMap reports whether a and b are the same map value (not merely equal).
func sameMap(a, b map[string]any) bool {
	a["__probe"] = true
	defer delete(a, "__probe")
	_, ok := b["__probe"]
	return ok
}
