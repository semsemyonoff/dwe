package envtest

import (
	"maps"
	"reflect"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// viewFixture builds a config carrying everything ScenarioView touches: two
// services (one required), per-developer compose overlays in both the typed
// fields and Raw, and a nested vars tree.
func viewFixture() *config.DweConfig {
	return &config.DweConfig{
		Compose: config.ComposeConfig{Base: "compose.yaml", Extra: []string{"local.compose.yml"}},
		Services: map[string]config.ServiceConfig{
			"core":  {Type: config.ServiceTypeApp, Required: true, Enabled: true, Compose: []string{"core.yml"}},
			"redis": {Type: config.ServiceTypeInfra, Enabled: false, Compose: []string{"redis.yml"}, LocalComposeExtra: []string{"redis.local.yml"}},
		},
		Raw: map[string]any{
			"compose": map[string]any{"base": "compose.yaml", "extra": []any{"local.compose.yml"}},
			"services": map[string]any{
				"core":  map[string]any{"enabled": true, "type": "app"},
				"redis": map[string]any{"enabled": false, "compose": map[string]any{"extra": []any{"redis.local.yml"}}},
			},
			"vars": map[string]any{"ports": map[string]any{"valkey": 6379, "http": 8080}},
		},
	}
}

func TestScenarioView_Toggles(t *testing.T) {
	tests := []struct {
		name        string
		env         ScenarioEnv
		wantEnabled map[string]bool
		wantRawEnab map[string]any // expected Raw services.<n>.enabled, missing key = absent
	}{
		{
			name:        "no toggles leaves the enabled state alone",
			env:         ScenarioEnv{},
			wantEnabled: map[string]bool{"core": true, "redis": false},
			wantRawEnab: map[string]any{"core": true, "redis": false},
		},
		{
			name:        "enable flips a disabled service",
			env:         ScenarioEnv{Services: ScenarioServices{Enable: []string{"redis"}}},
			wantEnabled: map[string]bool{"core": true, "redis": true},
			wantRawEnab: map[string]any{"core": true, "redis": true},
		},
		{
			name:        "enable of an unknown service is ignored",
			env:         ScenarioEnv{Services: ScenarioServices{Enable: []string{"ghost"}}},
			wantEnabled: map[string]bool{"core": true, "redis": false},
			wantRawEnab: map[string]any{"core": true, "redis": false},
		},
		{
			name:        "disable of a required service keeps it enabled",
			env:         ScenarioEnv{Services: ScenarioServices{Disable: []string{"core"}}},
			wantEnabled: map[string]bool{"core": true, "redis": false},
			wantRawEnab: map[string]any{"core": true, "redis": false},
		},
		{
			name:        "disable wins over enable for the same service",
			env:         ScenarioEnv{Services: ScenarioServices{Enable: []string{"redis"}, Disable: []string{"redis"}}},
			wantEnabled: map[string]bool{"core": true, "redis": false},
			wantRawEnab: map[string]any{"core": true, "redis": false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := ScenarioView(viewFixture(), tt.env)

			for name, want := range tt.wantEnabled {
				if got := view.Services[name].Enabled; got != want {
					t.Errorf("Services[%q].Enabled = %v, want %v", name, got, want)
				}
			}
			if _, ok := view.Services["ghost"]; ok {
				t.Error("an unknown service must not be added to Services")
			}

			rawServices, _ := view.Raw["services"].(map[string]any)
			for name, want := range tt.wantRawEnab {
				entry, ok := rawServices[name].(map[string]any)
				if !ok {
					t.Fatalf("Raw services.%s = %v, want a map", name, rawServices[name])
				}
				if entry["enabled"] != want {
					t.Errorf("Raw services.%s.enabled = %v, want %v", name, entry["enabled"], want)
				}
			}
			if _, ok := rawServices["ghost"]; ok {
				t.Error("an unknown service must not be added to the Raw mirror")
			}
			// Keys the toggle does not own stay in place.
			if core, _ := rawServices["core"].(map[string]any); core["type"] != "app" {
				t.Errorf("Raw services.core.type = %v, want the existing value kept", core["type"])
			}
		})
	}
}

func TestScenarioView_ComposeOverlaysDropped(t *testing.T) {
	cfg := viewFixture()
	// Enable redis so its own overlay would otherwise reach the -f chain.
	view := ScenarioView(cfg, ScenarioEnv{Services: ScenarioServices{Enable: []string{"redis"}}})

	if view.Compose.Extra != nil {
		t.Errorf("Compose.Extra = %v, want nil", view.Compose.Extra)
	}
	if extra := view.Services["redis"].LocalComposeExtra; extra != nil {
		t.Errorf("redis LocalComposeExtra = %v, want nil", extra)
	}
	for _, f := range view.ComposeFiles() {
		if f == "local.compose.yml" || f == "redis.local.yml" {
			t.Errorf("ComposeFiles() = %v, must not carry a per-developer overlay", view.ComposeFiles())
		}
	}

	if _, ok := config.ResolvePath(view.Raw, "compose.extra"); ok {
		t.Error("Raw compose.extra must be gone from the view")
	}
	if _, ok := config.ResolvePath(view.Raw, "services.redis.compose.extra"); ok {
		t.Error("Raw services.redis.compose.extra must be gone from the view")
	}
	// The rest of the compose block survives: an exports.env when: reading
	// compose.base must still resolve.
	if got, ok := config.ResolvePath(view.Raw, "compose.base"); !ok || got != "compose.yaml" {
		t.Errorf("Raw compose.base = %v (ok=%v), want compose.yaml", got, ok)
	}
	// An exports.env rule gated on a per-developer overlay is inactive on the
	// view, exactly as it would be in the copy.
	if _, ok := config.ResolvePath(cfg.Raw, "compose.extra"); !ok {
		t.Fatal("fixture lost its Raw compose.extra")
	}
}

func TestScenarioView_VarsOverlay(t *testing.T) {
	tests := []struct {
		name string
		vars map[string]any
		want map[string]any // path -> expected resolved value
		gone []string       // paths that must not resolve
	}{
		{
			name: "dot-path merges over an existing nested value",
			vars: map[string]any{"ports.valkey": 6380},
			want: map[string]any{"ports.valkey": 6380, "ports.http": 8080},
		},
		{
			name: "auto becomes the placeholder",
			vars: map[string]any{"ports.valkey": AutoPortSentinel},
			want: map[string]any{"ports.valkey": AutoPortPlaceholder},
		},
		{
			name: "a new branch is created",
			vars: map[string]any{"app.debug": true},
			want: map[string]any{"app.debug": true, "ports.http": 8080},
		},
		{
			name: "an empty path is skipped",
			vars: map[string]any{"": 1},
			want: map[string]any{"ports.http": 8080},
		},
		{
			name: "an empty path segment is skipped",
			vars: map[string]any{"a..b": 1},
			gone: []string{"a.b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := ScenarioView(viewFixture(), ScenarioEnv{Vars: tt.vars})
			for path, want := range tt.want {
				got, ok := config.ResolvePath(view.Raw, "vars."+path)
				if !ok || got != want {
					t.Errorf("vars.%s = %v (ok=%v), want %v", path, got, ok, want)
				}
			}
			for _, path := range tt.gone {
				if got, ok := config.ResolvePath(view.Raw, "vars."+path); ok {
					t.Errorf("vars.%s resolved to %v, want absent", path, got)
				}
			}
		})
	}
}

func TestScenarioView_NilRaw(t *testing.T) {
	cfg := &config.DweConfig{Services: map[string]config.ServiceConfig{"redis": {}}}
	view := ScenarioView(cfg, ScenarioEnv{
		Services: ScenarioServices{Enable: []string{"redis"}},
		Vars:     map[string]any{"ports.valkey": AutoPortSentinel},
	})

	if view.Raw == nil {
		t.Fatal("view Raw must be writable, got nil")
	}
	if got, ok := config.ResolvePath(view.Raw, "services.redis.enabled"); !ok || got != true {
		t.Errorf("services.redis.enabled = %v (ok=%v), want true", got, ok)
	}
	if got, ok := config.ResolvePath(view.Raw, "vars.ports.valkey"); !ok || got != AutoPortPlaceholder {
		t.Errorf("vars.ports.valkey = %v (ok=%v), want the placeholder", got, ok)
	}
	if cfg.Raw != nil {
		t.Error("the input config gained a Raw map")
	}
}

// TestScenarioView_NoRawServicesKeyWithoutToggles pins that a config whose Raw
// carries no services mirror does not gain one when the scenario toggles
// nothing — the view must not invent config the loader never produced.
func TestScenarioView_NoRawServicesKeyWithoutToggles(t *testing.T) {
	cfg := &config.DweConfig{Raw: map[string]any{"vars": map[string]any{"x": 1}}}
	view := ScenarioView(cfg, ScenarioEnv{})
	if _, ok := view.Raw["services"]; ok {
		t.Error("the view invented a Raw services key")
	}
}

// TestScenarioView_DoesNotMutateInput deep-snapshots a config carrying both
// compose overlays on an UNTOGGLED service and builds three views from it: the
// input must survive byte-identical (an entry stripped in place would show up
// here) and no view may carry another's overlay.
func TestScenarioView_DoesNotMutateInput(t *testing.T) {
	cfg := viewFixture()
	snapshot := deepCopyMap(cfg.Raw)
	snapshotServices := maps.Clone(cfg.Services)

	plain := ScenarioView(cfg, ScenarioEnv{})
	varsView := ScenarioView(cfg, ScenarioEnv{Vars: map[string]any{"ports.valkey": 6380}})
	toggleView := ScenarioView(cfg, ScenarioEnv{Services: ScenarioServices{Enable: []string{"redis"}}})

	if !reflect.DeepEqual(cfg.Raw, snapshot) {
		t.Errorf("input Raw mutated:\n got %#v\nwant %#v", cfg.Raw, snapshot)
	}
	if !reflect.DeepEqual(cfg.Services, snapshotServices) {
		t.Errorf("input Services mutated: %#v", cfg.Services)
	}
	if cfg.Compose.Extra == nil {
		t.Error("input Compose.Extra was cleared")
	}

	// No cross-contamination between the views.
	if got, ok := config.ResolvePath(plain.Raw, "vars.ports.valkey"); !ok || got != 6379 {
		t.Errorf("plain view vars.ports.valkey = %v (ok=%v), want the project value", got, ok)
	}
	if got, _ := config.ResolvePath(varsView.Raw, "vars.ports.valkey"); got != 6380 {
		t.Errorf("vars view vars.ports.valkey = %v, want 6380", got)
	}
	if got, _ := config.ResolvePath(plain.Raw, "services.redis.enabled"); got != false {
		t.Errorf("plain view services.redis.enabled = %v, want false", got)
	}
	if got, _ := config.ResolvePath(toggleView.Raw, "services.redis.enabled"); got != true {
		t.Errorf("toggle view services.redis.enabled = %v, want true", got)
	}
	if plain.Services["redis"].Enabled {
		t.Error("the toggle view's enable leaked into the plain view")
	}
}

// TestScenarioView_DoesNotMutateScenarioEnv pins the other half of the
// no-shared-state promise: the env the view is built FROM. expandVarPaths
// descends into an existing map at a prefix, so a nested declaration plus a
// dot-path under it used to make the view's substituted value (the
// AutoPortPlaceholder) land in the caller's own env.vars — a pin the scenario
// author never wrote, on the map the runner reuses for the deploy retry.
func TestScenarioView_DoesNotMutateScenarioEnv(t *testing.T) {
	env := ScenarioEnv{Vars: map[string]any{
		"ports":        map[string]any{"other": 7000},
		"ports.valkey": AutoPortSentinel,
	}}

	view := ScenarioView(viewFixture(), env)

	want := map[string]any{
		"ports":        map[string]any{"other": 7000},
		"ports.valkey": AutoPortSentinel,
	}
	if !reflect.DeepEqual(env.Vars, want) {
		t.Errorf("scenario env.vars = %#v, want unchanged %#v", env.Vars, want)
	}
	// The view itself still carries both, with the sentinel substituted.
	if got, _ := config.ResolvePath(view.Raw, "vars.ports.valkey"); got != AutoPortPlaceholder {
		t.Errorf("view vars.ports.valkey = %v, want the placeholder", got)
	}
	if got, _ := config.ResolvePath(view.Raw, "vars.ports.other"); got != 7000 {
		t.Errorf("view vars.ports.other = %v, want 7000", got)
	}
}
