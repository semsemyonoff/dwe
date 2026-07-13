package envtest

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/local"
)

func TestBuildLocalOverlaySeedPreserved(t *testing.T) {
	seed := map[string]any{"vars": map[string]any{"kept": "value"}}
	scn := &Scenario{}

	overlay, err := BuildLocalOverlay(seed, scn, "myapp-t-smoke-abc123", nil, nil)
	if err != nil {
		t.Fatalf("BuildLocalOverlay: %v", err)
	}

	vars, ok := overlay["vars"].(map[string]any)
	if !ok || vars["kept"] != "value" {
		t.Errorf("overlay[vars] = %v, want seeded value preserved", overlay["vars"])
	}
	project, ok := overlay["project"].(map[string]any)
	if !ok || project["prefix"] != "myapp-t-smoke-abc123" {
		t.Errorf("overlay[project] = %v, want prefix set", overlay["project"])
	}
	update, ok := overlay["update"].(map[string]any)
	if !ok || update["mode"] != "off" {
		t.Errorf("overlay[update] = %v, want mode off", overlay["update"])
	}
}

func TestBuildLocalOverlayStripsComposeExtra(t *testing.T) {
	seed := map[string]any{
		"compose": map[string]any{"extra": []any{"compose.local.yml"}, "kept": "x"},
		"services": map[string]any{
			"app": map[string]any{
				"compose": map[string]any{"extra": []any{"app.local.yml"}},
				"enabled": true,
			},
		},
	}
	var warnings []string
	warn := func(msg string) { warnings = append(warnings, msg) }

	overlay, err := BuildLocalOverlay(seed, &Scenario{}, "proj-t-s-abc", nil, warn)
	if err != nil {
		t.Fatalf("BuildLocalOverlay: %v", err)
	}

	compose, _ := overlay["compose"].(map[string]any)
	if _, has := compose["extra"]; has {
		t.Errorf("compose.extra survived stripping: %v", compose)
	}
	if compose["kept"] != "x" {
		t.Errorf("compose.kept was dropped, want preserved: %v", compose)
	}

	services, _ := overlay["services"].(map[string]any)
	app, _ := services["app"].(map[string]any)
	appCompose, _ := app["compose"].(map[string]any)
	if _, has := appCompose["extra"]; has {
		t.Errorf("services.app.compose.extra survived stripping: %v", app)
	}
	if app["enabled"] != true {
		t.Errorf("services.app.enabled was dropped: %v", app)
	}

	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want 2 strip warnings", warnings)
	}

	// Original seed map must not be mutated.
	origServices := seed["services"].(map[string]any)
	origApp := origServices["app"].(map[string]any)
	origCompose := origApp["compose"].(map[string]any)
	if _, has := origCompose["extra"]; !has {
		t.Error("BuildLocalOverlay mutated the caller's seed map")
	}
}

func TestBuildLocalOverlayVarDotPathExpansion(t *testing.T) {
	scn := &Scenario{
		Env: ScenarioEnv{
			Vars: map[string]any{
				"app.http_port": 8080,
				"db.name":       "testdb",
			},
		},
	}

	overlay, err := BuildLocalOverlay(nil, scn, "proj-t-s-abc", nil, nil)
	if err != nil {
		t.Fatalf("BuildLocalOverlay: %v", err)
	}

	vars := overlay["vars"].(map[string]any)
	app := vars["app"].(map[string]any)
	if app["http_port"] != 8080 {
		t.Errorf("vars.app.http_port = %v, want 8080", app["http_port"])
	}
	db := vars["db"].(map[string]any)
	if db["name"] != "testdb" {
		t.Errorf("vars.db.name = %v, want testdb", db["name"])
	}
}

// TestBuildLocalOverlayVarNestingCollision pins the collision resolution: a
// seeded scalar at a var path that the scenario wants to descend through as a
// map is discarded — the scenario's map wins (mirrors config's deepMerge).
func TestBuildLocalOverlayVarNestingCollision(t *testing.T) {
	seed := map[string]any{"vars": map[string]any{"app": "scalar-value"}}
	scn := &Scenario{
		Env: ScenarioEnv{
			Vars: map[string]any{"app.http_port": 8080},
		},
	}

	overlay, err := BuildLocalOverlay(seed, scn, "proj-t-s-abc", nil, nil)
	if err != nil {
		t.Fatalf("BuildLocalOverlay: %v", err)
	}

	vars := overlay["vars"].(map[string]any)
	app, ok := vars["app"].(map[string]any)
	if !ok {
		t.Fatalf("vars.app = %v (%T), want map with http_port (scenario map must win)", vars["app"], vars["app"])
	}
	if app["http_port"] != 8080 {
		t.Errorf("vars.app.http_port = %v, want 8080", app["http_port"])
	}
}

func TestBuildLocalOverlayAutoPortReplacement(t *testing.T) {
	scn := &Scenario{
		Env: ScenarioEnv{
			Vars: map[string]any{
				"app.http_port": AutoPortSentinel,
				"app.fixed":     5432,
			},
		},
	}
	ports := map[string]int{"app.http_port": 41234}

	overlay, err := BuildLocalOverlay(nil, scn, "proj-t-s-abc", ports, nil)
	if err != nil {
		t.Fatalf("BuildLocalOverlay: %v", err)
	}

	app := overlay["vars"].(map[string]any)["app"].(map[string]any)
	if app["http_port"] != 41234 {
		t.Errorf("vars.app.http_port = %v, want allocated port 41234", app["http_port"])
	}
	if app["fixed"] != 5432 {
		t.Errorf("vars.app.fixed = %v, want unchanged 5432", app["fixed"])
	}
}

func TestBuildLocalOverlayAutoPortMissingAllocation(t *testing.T) {
	scn := &Scenario{
		Env: ScenarioEnv{Vars: map[string]any{"app.http_port": AutoPortSentinel}},
	}

	if _, err := BuildLocalOverlay(nil, scn, "proj-t-s-abc", nil, nil); err == nil {
		t.Fatal("BuildLocalOverlay() = nil error, want error for unallocated auto port")
	}
}

func TestBuildLocalOverlayServiceToggles(t *testing.T) {
	scn := &Scenario{
		Env: ScenarioEnv{
			Services: ScenarioServices{
				Enable:  []string{"postgres"},
				Disable: []string{"redis"},
			},
		},
	}

	overlay, err := BuildLocalOverlay(nil, scn, "proj-t-s-abc", nil, nil)
	if err != nil {
		t.Fatalf("BuildLocalOverlay: %v", err)
	}

	services := overlay["services"].(map[string]any)
	postgres := services["postgres"].(map[string]any)
	if postgres["enabled"] != true {
		t.Errorf("services.postgres.enabled = %v, want true", postgres["enabled"])
	}
	redis := services["redis"].(map[string]any)
	if redis["enabled"] != false {
		t.Errorf("services.redis.enabled = %v, want false", redis["enabled"])
	}
}

func TestBuildLocalOverlayPrecedenceScenarioOverridesSeed(t *testing.T) {
	seed := map[string]any{"vars": map[string]any{"app": map[string]any{"http_port": 9999}}}
	scn := &Scenario{
		Env: ScenarioEnv{Vars: map[string]any{"app.http_port": 8080}},
	}

	overlay, err := BuildLocalOverlay(seed, scn, "proj-t-s-abc", nil, nil)
	if err != nil {
		t.Fatalf("BuildLocalOverlay: %v", err)
	}

	app := overlay["vars"].(map[string]any)["app"].(map[string]any)
	if app["http_port"] != 8080 {
		t.Errorf("vars.app.http_port = %v, want scenario override 8080", app["http_port"])
	}
}

func TestBuildLocalOverlayIdentityExactYAML(t *testing.T) {
	overlay, err := BuildLocalOverlay(nil, &Scenario{}, "myapp-t-smoke-abc123", nil, nil)
	if err != nil {
		t.Fatalf("BuildLocalOverlay: %v", err)
	}

	out, err := yaml.Marshal(overlay)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	got := string(out)
	want := "project:\n    prefix: myapp-t-smoke-abc123\nupdate:\n    mode: \"off\"\n"
	if got != want {
		t.Errorf("marshalled overlay =\n%s\nwant\n%s", got, want)
	}
}

func TestBuildLocalOverlayNilScenario(t *testing.T) {
	if _, err := BuildLocalOverlay(nil, nil, "proj", nil, nil); err == nil {
		t.Fatal("BuildLocalOverlay(nil scenario) = nil error, want error")
	}
}

func TestLoadSeedLocalYAMLMissingFile(t *testing.T) {
	dir := t.TempDir()
	seed, err := LoadSeedLocalYAML(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadSeedLocalYAML: %v", err)
	}
	if len(seed) != 0 {
		t.Errorf("LoadSeedLocalYAML() = %v, want empty map for missing file", seed)
	}
}

func TestLoadSeedLocalYAMLPresentFile(t *testing.T) {
	dir := t.TempDir()
	workspacePath := filepath.Join(dir, "workspace.yml")
	localPath := config.LocalLayerPath(workspacePath)
	if err := local.WriteLocalYAML(localPath, map[string]any{"vars": map[string]any{"x": "y"}}); err != nil {
		t.Fatalf("WriteLocalYAML: %v", err)
	}

	seed, err := LoadSeedLocalYAML(workspacePath)
	if err != nil {
		t.Fatalf("LoadSeedLocalYAML: %v", err)
	}
	vars, ok := seed["vars"].(map[string]any)
	if !ok || vars["x"] != "y" {
		t.Errorf("LoadSeedLocalYAML() = %v, want vars.x=y", seed)
	}
}

func TestWriteGeneratedLocalYAML(t *testing.T) {
	copyRoot := t.TempDir()
	overlay := map[string]any{
		"project": map[string]any{"prefix": "myapp-t-smoke-abc123"},
		"update":  map[string]any{"mode": "off"},
	}

	if err := WriteGeneratedLocalYAML(copyRoot, overlay); err != nil {
		t.Fatalf("WriteGeneratedLocalYAML: %v", err)
	}

	localPath := filepath.Join(copyRoot, "workspace", "local.yml")
	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var got map[string]any
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, overlay) {
		t.Errorf("written local.yml = %v, want %v", got, overlay)
	}
}
