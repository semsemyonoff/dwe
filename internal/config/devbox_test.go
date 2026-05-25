package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// sampleDevboxYML reflects the lean devbox.yml (project identity only).
const sampleDevboxYML = `
schema_version: "1"
project:
  name: laravel
  prefix: devbox
`

// sampleToolsYML declares the standard tools as type=tool services with
// compose overlays for two. Post-unification the file is a services.yml
// fragment, not a separate tools.yml. Kept under the legacy name so existing
// call sites stay terse.
const sampleToolsYML = sampleToolsServicesYML

// minimalToolsYML declares the standard tools as type=tool services without
// compose overlays. Post-unification the file is a services.yml fragment.
const minimalToolsYML = `
services:
  adminer:
    type: tool
    container: adminer
    ports:
      main: 8080
    hosts:
      main: adminer.localhost
  redis_insight:
    type: tool
    container: redis_insight
    ports:
      main: 5540
    hosts:
      main: redis.localhost
  mailpit:
    type: tool
    container: mailpit
    ports:
      main: 8025
    hosts:
      main: mail.localhost
`

// sampleToolsServicesYML declares the standard tools as type=tool services
// with per-service ports/hosts. Two of them carry compose overlays.
const sampleToolsServicesYML = `
services:
  adminer:
    type: tool
    container: adminer
    ports:
      main: 8080
    hosts:
      main: adminer.localhost
  redis_insight:
    type: tool
    container: redis_insight
    compose:
      - compose/tools/redis_insight.yml
    ports:
      main: 5540
    hosts:
      main: redis.localhost
  mailpit:
    type: tool
    container: mailpit
    compose:
      - compose/tools/mailpit.yml
    ports:
      main: 8025
    hosts:
      main: mail.localhost
`

// minimalDefaultsYML carries enable overlays for the sample tools, all disabled.
const minimalDefaultsYML = `
schema_version: "1"
services:
  adminer:
    enabled: false
  redis_insight:
    enabled: false
  mailpit:
    enabled: false
runtime:
  use_https: false
  spx:
    path: ""
state: ""
`

// sampleDefaultsYML enables redis_insight and mailpit; disables adminer.
const sampleDefaultsYML = `
schema_version: "1"
services:
  adminer:
    enabled: false
  redis_insight:
    enabled: true
  mailpit:
    enabled: true
runtime:
  use_https: false
  spx:
    path: ""
state: ""
`

// writeServicesDir creates per-folder service files under <baseDir>/devbox/services/
// from a YAML fragment shaped like `services: {name: {...}}`.
func writeServicesDir(t *testing.T, baseDir, servicesYML string) {
	t.Helper()
	if servicesYML == "" {
		return
	}
	type wrap struct {
		Services map[string]any `yaml:"services"`
	}
	var w wrap
	if err := yaml.Unmarshal([]byte(servicesYML), &w); err != nil {
		t.Fatalf("writeServicesDir: parse: %v", err)
	}
	for name, svc := range w.Services {
		dir := filepath.Join(baseDir, "devbox", "services", name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("writeServicesDir: mkdir %s: %v", dir, err)
		}
		data, err := yaml.Marshal(svc)
		if err != nil {
			t.Fatalf("writeServicesDir: marshal %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "service.yml"), data, 0644); err != nil {
			t.Fatalf("writeServicesDir: write %s: %v", name, err)
		}
	}
}

func writeTempYML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "devbox-*.yml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	return f.Name()
}

// writeLayeredFixture creates the file layout used by LoadConfig:
//
//	<tmp>/devbox.yml
//	<tmp>/devbox/defaults.yml   (optional)
//	<tmp>/devbox/local.yml       (optional)
//	<tmp>/devbox/tools.yml       (sampleToolsYML)
//
// Tests that need a non-standard tools.yml should call writeFullFixture with an
// explicit tools argument; tests that want no tools.yml at all should pass
// "<NONE>" sentinel for tools.
func writeLayeredFixture(t *testing.T, devbox, defaults, user string) string {
	t.Helper()
	return writeFullFixture(t, devbox, defaults, user, "", sampleToolsYML)
}

// noToolsYML is a sentinel passed as the tools argument to writeFullFixture to
// suppress creation of devbox/tools.yml entirely.
const noToolsYML = "<NONE>"

// writeFullFixture creates the complete file layout used by LoadConfig,
// including optional per-folder services and tool services.
//
// Pass tools=noToolsYML to suppress creating tool service folders. Empty
// string falls back to sampleToolsYML so existing layered tests stay terse.
func writeFullFixture(t *testing.T, devbox, defaults, user, services, tools string) string {
	t.Helper()
	dir := t.TempDir()

	devboxPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(devboxPath, []byte(devbox), 0644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	devboxDir := filepath.Join(dir, "devbox")

	writeTools := tools != noToolsYML
	toolsContent := ""
	if writeTools {
		toolsContent = tools
		if toolsContent == "" {
			toolsContent = sampleToolsYML
		}
	}

	if defaults != "" || user != "" {
		if err := os.MkdirAll(devboxDir, 0755); err != nil {
			t.Fatalf("mkdir devbox/: %v", err)
		}
	}

	if defaults != "" {
		if err := os.WriteFile(filepath.Join(devboxDir, "defaults.yml"), []byte(defaults), 0644); err != nil {
			t.Fatalf("write defaults.yml: %v", err)
		}
	}

	if user != "" {
		if err := os.WriteFile(filepath.Join(devboxDir, "local.yml"), []byte(user), 0644); err != nil {
			t.Fatalf("write local.yml: %v", err)
		}
	}

	// Write per-folder services (services and tools are independent fragments).
	writeServicesDir(t, dir, services)
	writeServicesDir(t, dir, toolsContent)

	return devboxPath
}

// --- LoadDevboxConfig (single-file loader) ---

// fullSingleYML is a self-contained devbox.yml with all fields, used to test
// LoadDevboxConfig in isolation.
const fullSingleYML = `
schema_version: "1"
project:
  name: laravel
  prefix: devbox
tools:
  adminer:
    enabled: false
  redis_insight:
    enabled: true
  mailpit:
    enabled: true
runtime:
  use_https: false
  ports:
    app: 80
    db: 13306
    redis: 6379
    adminer: 8080
    redis_insight: 5540
    mailpit: 8025
  hosts:
    main: app.localhost
    adminer: adminer.localhost
    redis_insight: redis.localhost
    mailpit: mail.localhost
  spx:
    path: ""
state: ""
`

func TestLoadDevboxConfig(t *testing.T) {
	path := writeTempYML(t, fullSingleYML)
	cfg, err := LoadDevboxConfig(path)
	if err != nil {
		t.Fatalf("LoadDevboxConfig: %v", err)
	}

	if cfg.SchemaVersion != "1" {
		t.Errorf("SchemaVersion = %q, want 1", cfg.SchemaVersion)
	}
	if cfg.Project.Name != "laravel" {
		t.Errorf("Project.Name = %q", cfg.Project.Name)
	}
	if cfg.Project.FullName() != "devbox-laravel" {
		t.Errorf("FullName = %q, want devbox-laravel", cfg.Project.FullName())
	}
}

func TestLoadDevboxConfig_notFound(t *testing.T) {
	_, err := LoadDevboxConfig(filepath.Join(t.TempDir(), "nonexistent.yml"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadDevboxConfig_invalidYAML(t *testing.T) {
	path := writeTempYML(t, "{ invalid yaml ][")
	_, err := LoadDevboxConfig(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

// --- LoadConfig (layered loader) ---

func TestLoadConfig_mergesDefaultsAndUser(t *testing.T) {
	userYML := `
services:
  adminer:
    enabled: true
state: staging
`
	path := writeLayeredFixture(t, sampleDevboxYML, sampleDefaultsYML, userYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// From devbox.yml
	if cfg.Project.Name != "laravel" {
		t.Errorf("Project.Name = %q", cfg.Project.Name)
	}

	// From defaults.yml: redis_insight enabled; mailpit's per-service port is 8025
	if !cfg.Services["redis_insight"].Enabled {
		t.Error("services.redis_insight.enabled should be true (from defaults)")
	}
	if cfg.Services["mailpit"].Port("main") != 8025 {
		t.Errorf("services.mailpit.ports.main = %d, want 8025", cfg.Services["mailpit"].Port("main"))
	}

	// Overridden by local.yml: adminer flipped enabled=true
	if !cfg.Services["adminer"].Enabled {
		t.Error("services.adminer.enabled should be true (overridden by user)")
	}
	if cfg.State != "staging" {
		t.Errorf("state = %q, want staging (from user)", cfg.State)
	}
}

func TestLoadConfig_noOptionalFiles(t *testing.T) {
	// Works fine when defaults.yml, local.yml, and tools.yml are absent.
	path := writeFullFixture(t, sampleDevboxYML, "", "", "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Project.Name != "laravel" {
		t.Errorf("Project.Name = %q", cfg.Project.Name)
	}
}

func TestLoadConfig_noDefaultsFile(t *testing.T) {
	// local.yml present, defaults.yml absent. Skip tools.yml to avoid requiring
	// a runtime block solely to satisfy tool host/port validation.
	userYML := `state: demo`
	path := writeFullFixture(t, sampleDevboxYML, "", userYML, "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.State != "demo" {
		t.Errorf("state = %q, want demo", cfg.State)
	}
}

func TestLoadConfig_notFound(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "nonexistent.yml"))
	if err == nil {
		t.Error("expected error for missing devbox.yml")
	}
}

// --- helpers ---

func TestProjectFullName_noPrefix(t *testing.T) {
	p := ProjectConfig{Name: "myapp"}
	if p.FullName() != "myapp" {
		t.Errorf("FullName = %q, want myapp", p.FullName())
	}
}

// Tools no longer have a dedicated AnyEnabled helper; the equivalent is to
// walk cfg.Services and filter by IsTool().

func TestDeepMerge_nonConflicting(t *testing.T) {
	dst := map[string]any{"a": 1}
	src := map[string]any{"b": 2}
	deepMerge(dst, src)
	if dst["a"] != 1 || dst["b"] != 2 {
		t.Errorf("unexpected result: %v", dst)
	}
}

func TestDeepMerge_srcWins(t *testing.T) {
	dst := map[string]any{"a": 1}
	src := map[string]any{"a": 99}
	deepMerge(dst, src)
	if dst["a"] != 99 {
		t.Errorf("expected src to win: got %v", dst["a"])
	}
}

func TestDeepMerge_recursiveMaps(t *testing.T) {
	dst := map[string]any{
		"m": map[string]any{"x": 1, "y": 2},
	}
	src := map[string]any{
		"m": map[string]any{"y": 99, "z": 3},
	}
	deepMerge(dst, src)
	m := dst["m"].(map[string]any)
	if m["x"] != 1 || m["y"] != 99 || m["z"] != 3 {
		t.Errorf("unexpected nested merge result: %v", m)
	}
}

// --- ResolvePath ---

func TestResolvePath_topLevel(t *testing.T) {
	m := map[string]any{"state": "staging"}
	v, ok := ResolvePath(m, "state")
	if !ok || v != "staging" {
		t.Errorf("got %v, %v", v, ok)
	}
}

func TestResolvePath_nested(t *testing.T) {
	m := map[string]any{
		"services": map[string]any{
			"app": map[string]any{
				"ports": map[string]any{"http": 8080},
			},
		},
	}
	v, ok := ResolvePath(m, "services.app.ports.http")
	if !ok || v != 8080 {
		t.Errorf("got %v, %v", v, ok)
	}
}

func TestResolvePath_missing(t *testing.T) {
	m := map[string]any{"a": 1}
	_, ok := ResolvePath(m, "a.b.c")
	if ok {
		t.Error("expected not found for non-map intermediate")
	}
}

func TestResolvePath_emptyPath(t *testing.T) {
	m := map[string]any{"a": 1}
	_, ok := ResolvePath(m, "")
	if ok {
		t.Error("expected not found for empty path")
	}
}

func TestResolvePath_nilMap(t *testing.T) {
	_, ok := ResolvePath(nil, "a")
	if ok {
		t.Error("expected not found for nil map")
	}
}

// --- LookupDotPath ---

func TestLookupDotPath_stringLeaf(t *testing.T) {
	cfg := &DevboxConfig{Raw: map[string]any{
		"services": map[string]any{
			"main": map[string]any{"work_dir_internal": "/var/www"},
		},
	}}
	v, err := LookupDotPath(cfg, "services.main.work_dir_internal")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "/var/www" {
		t.Errorf("got %v, want /var/www", v)
	}
}

func TestLookupDotPath_missingReturnsNil(t *testing.T) {
	cfg := &DevboxConfig{Raw: map[string]any{}}
	v, err := LookupDotPath(cfg, "a.b.c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != nil {
		t.Errorf("got %v, want nil", v)
	}
}

func TestLookupDotPath_nonStringErrors(t *testing.T) {
	cfg := &DevboxConfig{Raw: map[string]any{"port": 8080}}
	_, err := LookupDotPath(cfg, "port")
	if err == nil {
		t.Fatal("expected error for non-string leaf")
	}
}

func TestLookupDotPath_nilConfig(t *testing.T) {
	v, err := LookupDotPath(nil, "any.path")
	if err != nil || v != nil {
		t.Errorf("got v=%v, err=%v; want nil, nil", v, err)
	}
}

// --- LoadConfig populates Raw ---

func TestLoadConfig_rawPopulated(t *testing.T) {
	path := writeLayeredFixture(t, sampleDevboxYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Raw == nil {
		t.Fatal("cfg.Raw should be populated")
	}
	v, ok := ResolvePath(cfg.Raw, "services.adminer.ports.main")
	if !ok {
		t.Error("services.adminer.ports.main not found in Raw")
	}
	if fmt.Sprintf("%v", v) != "8080" {
		t.Errorf("services.adminer.ports.main = %v, want 8080", v)
	}
}

// --- ComposeConfig loading ---

func TestLoadConfig_composeBasePresent(t *testing.T) {
	defaultsWithCompose := sampleDefaultsYML + `
compose:
  base: compose.yaml
`
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithCompose, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Compose.Base != "compose.yaml" {
		t.Errorf("Compose.Base = %q, want compose.yaml", cfg.Compose.Base)
	}
}

func TestLoadConfig_toolComposesLoaded(t *testing.T) {
	path := writeLayeredFixture(t, sampleDevboxYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Check that tool compose files are loaded from tool entries.
	if want := []string{"compose/tools/redis_insight.yml"}; !slicesEqual(cfg.Services["redis_insight"].Compose, want) {
		t.Errorf("Services[redis_insight].Compose = %v, want %v", cfg.Services["redis_insight"].Compose, want)
	}
	if want := []string{"compose/tools/mailpit.yml"}; !slicesEqual(cfg.Services["mailpit"].Compose, want) {
		t.Errorf("Services[mailpit].Compose = %v, want %v", cfg.Services["mailpit"].Compose, want)
	}
	// Adminer is disabled and has no compose, so check that empty is OK.
	if len(cfg.Services["adminer"].Compose) != 0 {
		t.Errorf("Services[adminer].Compose = %v, want empty", cfg.Services["adminer"].Compose)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLoadConfig_composeAbsent(t *testing.T) {
	// When compose section is absent, fields are zero values.
	path := writeLayeredFixture(t, sampleDevboxYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Compose.Base != "" {
		t.Errorf("Compose.Base = %q, want empty when section absent", cfg.Compose.Base)
	}
}

// --- Config Validation ---

func TestValidateConfigKeys_nilMapsAreSafe(t *testing.T) {
	cfg := &DevboxConfig{Services: nil}
	err := validateConfigKeys(cfg)
	if err != nil {
		t.Errorf("validateConfigKeys on nil maps = %v, want nil", err)
	}
}

func TestValidateConfigKeys_servicePortsHostsIdentifierSafety(t *testing.T) {
	tests := []struct {
		name      string
		ports     map[string]int
		hosts     map[string]string
		wantError bool
	}{
		{"valid port key", map[string]int{"http": 3000}, nil, false},
		{"valid host key", nil, map[string]string{"main": "main.localhost"}, false},
		{"invalid port key dash", map[string]int{"my-port": 3000}, nil, true},
		{"invalid port key leading digit", map[string]int{"1port": 3000}, nil, true},
		{"invalid host key dash", nil, map[string]string{"my-host": "x.localhost"}, true},
		{"invalid host key dot", nil, map[string]string{"my.host": "x.localhost"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &DevboxConfig{
				Services: map[string]ServiceConfig{
					"svc": {Type: ServiceTypeTool, Container: "test", Ports: tt.ports, Hosts: tt.hosts},
				},
			}
			err := validateConfigKeys(cfg)
			if (err != nil) != tt.wantError {
				t.Errorf("validateConfigKeys = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestDetectLegacyComposeOverlays_rejectsOldFormat(t *testing.T) {
	raw := map[string]any{
		"compose": map[string]any{
			"overlays": map[string]any{
				"adminer": "compose/tools/adminer.yml",
			},
		},
	}
	err := detectLegacyComposeOverlays(raw)
	if err == nil {
		t.Error("detectLegacyComposeOverlays should reject legacy compose.overlays")
	}
}

func TestDetectLegacyComposeOverlays_rejectsEmptyOverlays(t *testing.T) {
	// An empty compose.overlays: {} is still a legacy key and must be rejected.
	raw := map[string]any{
		"compose": map[string]any{
			"overlays": map[string]any{},
		},
	}
	err := detectLegacyComposeOverlays(raw)
	if err == nil {
		t.Error("detectLegacyComposeOverlays should reject empty compose.overlays block")
	}
}

func TestDetectLegacyComposeOverlays_allowsNewFormat(t *testing.T) {
	raw := map[string]any{
		"compose": map[string]any{
			"base": "compose.yaml",
		},
	}
	err := detectLegacyComposeOverlays(raw)
	if err != nil {
		t.Errorf("detectLegacyComposeOverlays on new format = %v, want nil", err)
	}
}

func TestLoadConfig_nilSafety(t *testing.T) {
	// A minimal devbox.yml with no tools, runtime, or services should load without panic.
	minimalYML := `
schema_version: "1"
project:
  name: test
`
	path := writeFullFixture(t, minimalYML, "", "", "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// With no services declared, cfg.Services should be empty / nil-safe.
	if len(cfg.Services) != 0 {
		t.Errorf("expected empty services, got %v", cfg.Services)
	}
}

func TestLoadConfig_arbitraryToolKey(t *testing.T) {
	// An arbitrary new tool (elasticvue) declared in services.yml is preserved.
	toolsWithNewTool := `
services:
  elasticvue:
    type: tool
    container: elasticvue
    compose:
      - compose/tools/elasticvue.yml
    ports:
      main: 9200
    hosts:
      main: elastic.localhost
`
	defaultsWithNewTool := `
schema_version: "1"
services:
  elasticvue:
    enabled: false
`
	path := writeFullFixture(t, sampleDevboxYML, defaultsWithNewTool, "", "", toolsWithNewTool)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	tool, ok := cfg.Services["elasticvue"]
	if !ok {
		t.Error("elasticvue tool not found in merged config")
	} else if tool.Container != "elasticvue" || tool.Port("main") != 9200 || tool.Host("main") != "elastic.localhost" {
		t.Errorf("elasticvue tool definition incorrect: %+v", tool)
	}
}

// --- Services (loaded from services.yml) ---

const sampleServicesYML = `
services:
  main:
    type: app
    container: app-main
    mandatory: true
    dir: ./services/main
    dir_internal: /workspace
    work_dir_internal: /workspace/src
    configs:
      - .env
  main-debug:
    type: app
    container: app-main-debug
    mandatory: false
    extends: main
    compose:
      - compose/services/main/debug.yml
`

func TestLoadServicesConfig_basic(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
	main := services["main"]
	if main.Container != "app-main" {
		t.Errorf("main.Container = %q, want app-main", main.Container)
	}
	if !main.Mandatory {
		t.Error("main.Mandatory should be true")
	}
	if main.Dir != "./services/main" {
		t.Errorf("main.Dir = %q, want ./services/main", main.Dir)
	}
	if len(main.Configs) != 1 || main.Configs[0].File != ".env" {
		t.Errorf("main.Configs = %v, want [{File:.env}]", main.Configs)
	}
}

func TestLoadServicesConfig_extendsResolved(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	debug := services["main-debug"]
	if debug.Container != "app-main-debug" {
		t.Errorf("main-debug.Container = %q, want app-main-debug", debug.Container)
	}
	// Inherited from main via extends
	if debug.Dir != "./services/main" {
		t.Errorf("main-debug.Dir = %q, want ./services/main (inherited)", debug.Dir)
	}
	if debug.DirInternal != "/workspace" {
		t.Errorf("main-debug.DirInternal = %q, want /workspace (inherited)", debug.DirInternal)
	}
	if debug.WorkDirInternal != "/workspace/src" {
		t.Errorf("main-debug.WorkDirInternal = %q, want /workspace/src (inherited)", debug.WorkDirInternal)
	}
	if len(debug.Configs) != 1 || debug.Configs[0].File != ".env" {
		t.Errorf("main-debug.Configs = %v, want [{File:.env}] (inherited)", debug.Configs)
	}
	if len(debug.Compose) != 1 || debug.Compose[0] != "compose/services/main/debug.yml" {
		t.Errorf("main-debug.Compose = %v, want [compose/services/main/debug.yml]", debug.Compose)
	}
}

func TestLoadServicesConfig_extendsInheritsContainer(t *testing.T) {
	yml := `
services:
  parent:
    type: app
    container: parent-ctr
    dir: ./services/parent
  child-no-container:
    type: app
    extends: parent
  child-own-container:
    type: app
    container: child-ctr
    extends: parent
`
	dir := t.TempDir()
	writeServicesDir(t, dir, yml)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// child with no container should inherit parent's container
	if got := services["child-no-container"].Container; got != "parent-ctr" {
		t.Errorf("child-no-container.Container = %q, want parent-ctr (inherited from parent)", got)
	}
	// child with its own container should keep it
	if got := services["child-own-container"].Container; got != "child-ctr" {
		t.Errorf("child-own-container.Container = %q, want child-ctr (own value)", got)
	}
}

func TestLoadServicesConfig_extendsUnknownParent(t *testing.T) {
	yml := `
services:
  child:
    type: app
    container: child
    extends: nonexistent
`
	dir := t.TempDir()
	writeServicesDir(t, dir, yml)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected error for unknown extends parent")
	}
}

func TestLoadConfig_servicesEnabledFromMerge(t *testing.T) {
	defaults := `
schema_version: "1"
services:
  adminer:
    enabled: false
  redis_insight:
    enabled: true
  mailpit:
    enabled: true
  main-debug:
    enabled: false
`
	path := writeFullFixture(t, sampleDevboxYML, defaults, "", sampleServicesYML, sampleToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	main := cfg.Services["main"]
	if !main.Enabled {
		t.Error("main should be enabled (mandatory)")
	}
	debug := cfg.Services["main-debug"]
	if debug.Enabled {
		t.Error("main-debug should be disabled (defaults say false)")
	}
}

func TestLoadConfig_servicesEnabledByLocal(t *testing.T) {
	defaults := `
schema_version: "1"
services:
  adminer:
    enabled: false
  redis_insight:
    enabled: true
  mailpit:
    enabled: true
  main-debug:
    enabled: false
`
	local := `
services:
  main-debug:
    enabled: true
`
	path := writeFullFixture(t, sampleDevboxYML, defaults, local, sampleServicesYML, sampleToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	debug := cfg.Services["main-debug"]
	if !debug.Enabled {
		t.Error("main-debug should be enabled (local override)")
	}
}

func TestLoadConfig_servicesInjectedIntoRaw(t *testing.T) {
	path := writeFullFixture(t, sampleDevboxYML, sampleDefaultsYML, "", sampleServicesYML, sampleToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Export rules resolve from raw map — verify service data is there.
	val, ok := ResolvePath(cfg.Raw, "services.main.container")
	if !ok {
		t.Fatal("services.main.container not found in raw map")
	}
	if val != "app-main" {
		t.Errorf("services.main.container = %v, want app-main", val)
	}
}

func TestLoadConfig_noServicesFile(t *testing.T) {
	// Without services.yml, cfg.Services should be empty/nil-safe (no error).
	path := writeFullFixture(t, sampleDevboxYML, "", "", "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Services) != 0 {
		t.Errorf("Services should be empty when services.yml absent, got %v", cfg.Services)
	}
}

// --- ExportRule loading ---

// --- DeployConfig loading ---

const sampleDeployYML = `
phases:
  - name: setup
    description: Prepare directories
    steps:
      - name: create-dirs
        type: shell
        cmd: mkdir -p services/main/{src,configs}
        description: Create service hub directories
      - name: copy-configs
        type: shell
        cmd: devbox deploy config main
        description: Copy template configs
        when:
          type: template
          expr: "{{.Runtime.UseHTTPS}}"
  - name: start
    description: Start containers
    steps:
      - name: up
        type: command
        cmd: up
        description: Start all containers
`

func writeDeployFixture(t *testing.T, deployYML string) string {
	t.Helper()
	dir := t.TempDir()
	devboxPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(devboxPath, []byte(sampleDevboxYML), 0644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("mkdir devbox/: %v", err)
	}
	if deployYML != "" {
		if err := os.WriteFile(filepath.Join(devboxDir, "deploy.yml"), []byte(deployYML), 0644); err != nil {
			t.Fatalf("write deploy.yml: %v", err)
		}
	}
	return devboxPath
}

func TestLoadServiceDeployConfig_phasesPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if len(cfg.Phases) != 2 {
		t.Fatalf("Phases len = %d, want 2", len(cfg.Phases))
	}
	if cfg.Phases[0].Name != "setup" {
		t.Errorf("Phases[0].Name = %q, want setup", cfg.Phases[0].Name)
	}
	if cfg.Phases[1].Name != "start" {
		t.Errorf("Phases[1].Name = %q, want start", cfg.Phases[1].Name)
	}
}

func TestLoadServiceDeployConfig_stepWithCmd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[0].Steps[0]
	if step.Name != "create-dirs" {
		t.Errorf("step.Name = %q, want create-dirs", step.Name)
	}
	if step.Cmd == "" {
		t.Error("step.Cmd should be set for shell: steps")
	}
	if step.Type != "shell" {
		t.Error("step.Type should be 'shell' for cmd: steps")
	}
}

func TestLoadServiceDeployConfig_stepWithCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[1].Steps[0]
	if step.Name != "up" {
		t.Errorf("step.Name = %q, want up", step.Name)
	}
	if step.Cmd == "" {
		t.Error("step.Cmd should be set for command: steps")
	}
	if step.Type != "command" {
		t.Error("step.Type should be 'command' for command: steps")
	}
}

func TestLoadServiceDeployConfig_stepWithWhen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[0].Steps[1]
	if step.When == nil {
		t.Fatalf("step.When is nil, want a Condition")
	}
	if step.When.Type != "template" {
		t.Errorf("step.When.Type = %q, want template", step.When.Type)
	}
	if step.When.Expr != "{{.Runtime.UseHTTPS}}" {
		t.Errorf("step.When.Expr = %q, want {{.Runtime.UseHTTPS}}", step.When.Expr)
	}
}

func TestLoadServiceDeployConfig_strictDecodeStringWhen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	invalidYAML := `
phases:
  - name: setup
    steps:
      - name: test
        type: shell
        cmd: echo hello
        when: "{{.Runtime.UseHTTPS}}"
`
	if err := os.WriteFile(path, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Errorf("LoadServiceDeployConfig expected error for string-form when:, got nil")
	} else if !strings.Contains(err.Error(), "when") {
		t.Errorf("error should mention 'when' field: %v", err)
	}
}

func TestLoadServiceDeployConfig_invalidWhenType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	invalidYAML := `
phases:
  - name: setup
    steps:
      - name: test
        type: shell
        cmd: echo hello
        when:
          type: bogus
          cmd: something
`
	if err := os.WriteFile(path, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Errorf("LoadServiceDeployConfig expected error for invalid when type, got nil")
	} else if !strings.Contains(err.Error(), "when") {
		t.Errorf("error should mention 'when': %v", err)
	}
}

func TestLoadServiceDeployConfig_strictDecodeUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	invalidYAML := `
phases:
  - name: setup
    steps:
      - name: test
        type: shell
        cmd: echo hello
        notafield: value
`
	if err := os.WriteFile(path, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Errorf("LoadServiceDeployConfig expected error for unknown field, got nil")
	} else if !strings.Contains(err.Error(), "notafield") && !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error should mention 'notafield' or 'unknown': %v", err)
	}
}

func TestLoadServiceDeployConfig_notFound(t *testing.T) {
	_, err := LoadServiceDeployConfig(filepath.Join(t.TempDir(), "nonexistent.yml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}

func TestLoadServiceDeployConfig_invalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte("{ invalid yaml ]["), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestLoadConfig_deployLoaded(t *testing.T) {
	devboxPath := writeDeployFixture(t, sampleDeployYML)
	cfg, err := LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Deploy.Phases) != 2 {
		t.Fatalf("Deploy.Phases len = %d, want 2", len(cfg.Deploy.Phases))
	}
	if cfg.Deploy.Phases[0].Name != "setup" {
		t.Errorf("Deploy.Phases[0].Name = %q, want setup", cfg.Deploy.Phases[0].Name)
	}
}

func TestLoadConfig_deployAbsent(t *testing.T) {
	// No deploy.yml — Deploy field should be zero value (no error).
	devboxPath := writeDeployFixture(t, "")
	cfg, err := LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Deploy.Phases) != 0 {
		t.Errorf("Deploy.Phases should be empty when deploy.yml absent, got %d phases", len(cfg.Deploy.Phases))
	}
}

func TestLoadServiceDeployConfig_emptyPhases(t *testing.T) {
	yml := "phases: []\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if len(cfg.Phases) != 0 {
		t.Errorf("Phases len = %d, want 0", len(cfg.Phases))
	}
}

func TestLoadServiceDeployConfig_legacyRunAndCommandFieldsRejected(t *testing.T) {
	yml := `phases:
  - name: setup
    steps:
      - name: bad-step
        run: echo hi
        command: services.main.migrate
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("LoadServiceDeployConfig: expected strict-decode error for removed legacy fields 'run' and 'command', got nil")
	}
}

func TestLoadServiceDeployConfig_checkBadTypeProducesWrappedError(t *testing.T) {
	yml := `phases:
  - name: setup
    steps:
      - name: my-step
        type: shell
        cmd: echo hi
        check:
          type: badtype
          cmd: some-check
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("LoadServiceDeployConfig: expected validation error for bad check type, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "my-step") {
		t.Errorf("error %q does not contain step name 'my-step'", msg)
	}
	if !strings.Contains(msg, "setup") {
		t.Errorf("error %q does not contain phase name 'setup'", msg)
	}
	if !strings.Contains(msg, "check") {
		t.Errorf("error %q does not contain 'check'", msg)
	}
}

func TestLoadServiceDeployConfig_stepNeitherCmdNorCommand(t *testing.T) {
	yml := `phases:
  - name: setup
    steps:
      - name: empty-step
        description: no cmd or command
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("LoadServiceDeployConfig: expected error for step with neither cmd nor command, got nil")
	}
}

func TestLoadServiceDeployConfig_stepWithServiceConfigsCopy(t *testing.T) {
	yml := `phases:
  - name: setup
    steps:
      - name: copy-configs
        type: builtin
        cmd: service_configs_copy
        with:
          service: main
          mode: replace
        description: Copy configs
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[0].Steps[0]
	// Legacy service_configs_copy is converted to builtin at load time.
	if step.Type != "builtin" {
		t.Errorf("Type = %q, want builtin", step.Type)
	}
	if step.Cmd != "service_configs_copy" {
		t.Errorf("Cmd = %q, want service_configs_copy", step.Cmd)
	}
	service, _ := step.With["service"].(string)
	if service != "main" {
		t.Errorf("With[service] = %q, want main", service)
	}
	mode, _ := step.With["mode"].(string)
	if mode != "replace" {
		t.Errorf("With[mode] = %q, want replace", mode)
	}
}

func TestLoadServiceDeployConfig_stepWithCommandAndWith(t *testing.T) {
	yml := `phases:
  - name: init
    steps:
      - name: migrate
        type: command
        cmd: services.main.migrate
        with:
          db: mydb
          env: testing
        description: Run migrations
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[0].Steps[0]
	if step.Type != "command" {
		t.Errorf("Type = %q, want command", step.Type)
	}
	if step.Cmd != "services.main.migrate" {
		t.Errorf("Cmd = %q, want services.main.migrate", step.Cmd)
	}
	if step.With["db"] != "mydb" {
		t.Errorf("With[db] = %q, want mydb", step.With["db"])
	}
	if step.With["env"] != "testing" {
		t.Errorf("With[env] = %q, want testing", step.With["env"])
	}
}

// --- DeployServices marker validation ---

func TestLoadServiceDeployConfig_deployServicesPhase(t *testing.T) {
	yml := `phases:
  - name: services
    deploy_services: true
    description: Deploy all services
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if !cfg.Phases[0].DeployServices {
		t.Error("expected DeployServices=true")
	}
}

func TestLoadServiceDeployConfig_deployServicesWithStepsError(t *testing.T) {
	yml := `phases:
  - name: services
    deploy_services: true
    steps:
      - name: bad
        cmd: echo bad
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for deploy_services phase with steps")
	}
}

func TestLoadServiceDeployConfig_deployServicesWithWhenError(t *testing.T) {
	yml := `phases:
  - name: services
    deploy_services: true
    when:
      type: shell
      cmd: "test -f marker"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for deploy_services phase with when, got nil")
	}
	if !strings.Contains(err.Error(), "does not support when") {
		t.Errorf("error should mention 'does not support when', got: %v", err)
	}
}

func TestLoadServiceDeployConfig_phaseUIFieldRejected(t *testing.T) {
	// The ui: field was removed from DeployPhase. Strict decode rejects unknown fields.
	yml := `phases:
  - name: setup
    description: Setup phase
    ui: plain
    steps:
      - name: create-dirs
        type: shell
        cmd: mkdir -p services/main/src
        description: Create directories
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatalf("LoadServiceDeployConfig expected error for unknown field ui, got nil")
	}
	if !strings.Contains(err.Error(), "ui") && !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("error should mention ui or unknown field: %v", err)
	}
}

func TestLoadServiceDeployConfig_phaseUntrackedDefaultFalseSimple(t *testing.T) {
	// Phases without untracked: default to false.
	yml := `phases:
  - name: start
    description: Start phase
    steps:
      - name: up
        type: devbox
        cmd: "docker up"
        description: Start containers
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if cfg.Phases[0].Untracked {
		t.Error("Phases[0].Untracked = true, want false (default)")
	}
}

func TestLoadServiceDeployConfig_phaseUntrackedField(t *testing.T) {
	yml := `phases:
  - name: setup
    description: Setup phase
    steps:
      - name: create-dirs
        type: shell
        cmd: mkdir -p services/main/src
  - name: post-deploy
    description: Post-deploy phase
    untracked: true
    steps:
      - name: info
        type: devbox
        cmd: "info"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if len(cfg.Phases) != 2 {
		t.Fatalf("Phases len = %d, want 2", len(cfg.Phases))
	}
	if cfg.Phases[0].Untracked {
		t.Error("Phases[0].Untracked = true, want false (default)")
	}
	if !cfg.Phases[1].Untracked {
		t.Error("Phases[1].Untracked = false, want true")
	}
}

func TestLoadServiceDeployConfig_phaseUntrackedDefaultFalse(t *testing.T) {
	yml := `phases:
  - name: start
    description: Start phase
    steps:
      - name: up
        type: devbox
        cmd: "docker up"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if cfg.Phases[0].Untracked {
		t.Error("Phases[0].Untracked = true, want false (zero value default)")
	}
}

// --- TopoSortServices ---

func TestTopoSortServices_noDeps(t *testing.T) {
	services := map[string]ServiceConfig{
		"a": {},
		"b": {},
	}
	sorted, err := TopoSortServices([]string{"b", "a"}, services)
	if err != nil {
		t.Fatalf("TopoSortServices: %v", err)
	}
	if len(sorted) != 2 {
		t.Fatalf("want 2, got %d", len(sorted))
	}
}

func TestTopoSortServices_withDeps(t *testing.T) {
	services := map[string]ServiceConfig{
		"main":   {},
		"worker": {DependsOn: []string{"main"}},
	}
	sorted, err := TopoSortServices([]string{"worker", "main"}, services)
	if err != nil {
		t.Fatalf("TopoSortServices: %v", err)
	}
	// main must come before worker
	mainIdx, workerIdx := -1, -1
	for i, name := range sorted {
		if name == "main" {
			mainIdx = i
		}
		if name == "worker" {
			workerIdx = i
		}
	}
	if mainIdx >= workerIdx {
		t.Errorf("main (idx %d) should come before worker (idx %d)", mainIdx, workerIdx)
	}
}

func TestTopoSortServices_circularError(t *testing.T) {
	services := map[string]ServiceConfig{
		"a": {DependsOn: []string{"b"}},
		"b": {DependsOn: []string{"a"}},
	}
	_, err := TopoSortServices([]string{"a", "b"}, services)
	if err == nil {
		t.Fatal("expected circular dependency error")
	}
}

func TestTopoSortServices_unknownDepError(t *testing.T) {
	services := map[string]ServiceConfig{
		"a": {DependsOn: []string{"nonexistent"}},
	}
	_, err := TopoSortServices([]string{"a"}, services)
	if err == nil {
		t.Fatal("expected unknown dependency error")
	}
}

func TestTopoSortServices_depNotInSetSkipped(t *testing.T) {
	// worker depends on main, but main is not in the set being sorted.
	// main exists in services though — should not error, just skip.
	services := map[string]ServiceConfig{
		"main":   {},
		"worker": {DependsOn: []string{"main"}},
	}
	sorted, err := TopoSortServices([]string{"worker"}, services)
	if err != nil {
		t.Fatalf("TopoSortServices: %v", err)
	}
	if len(sorted) != 1 || sorted[0] != "worker" {
		t.Errorf("got %v, want [worker]", sorted)
	}
}

// --- LoadServiceDeployConfigs ---

func TestLoadServiceDeployConfigs_loadsExisting(t *testing.T) {
	dir := t.TempDir()
	mainDeployDir := filepath.Join(dir, "devbox", "services", "main")
	if err := os.MkdirAll(mainDeployDir, 0755); err != nil {
		t.Fatal(err)
	}
	mainDeploy := `phases:
  - name: setup
    steps:
      - name: create-dirs
        type: shell
        cmd: mkdir -p services/main/src
`
	if err := os.WriteFile(filepath.Join(mainDeployDir, "deploy.yml"), []byte(mainDeploy), 0644); err != nil {
		t.Fatal(err)
	}
	services := map[string]ServiceConfig{
		"main":  {},
		"other": {},
	}
	result, err := LoadServiceDeployConfigs(dir, services)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfigs: %v", err)
	}
	if _, ok := result["main"]; !ok {
		t.Error("expected main deploy config to be loaded")
	}
	if _, ok := result["other"]; ok {
		t.Error("other should not be loaded (no deploy file)")
	}
}

// --- Loader split: LoadProjectDeployConfig / LoadServiceDeployConfig ---

func TestLoadProjectDeployConfig_rejectsAfterField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	yml := `after:
  - somesvc
phases:
  - name: setup
    steps:
      - name: noop
        type: shell
        cmd: 'true'
`
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadProjectDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for after: in project deploy.yml, got nil")
	}
	if !errors.Is(err, ErrAfterFieldNotAllowed) {
		t.Errorf("err = %v, want wraps ErrAfterFieldNotAllowed", err)
	}
}

func TestLoadProjectDeployConfig_acceptsNoAfterField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadProjectDeployConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Phases) != 2 {
		t.Errorf("Phases len = %d, want 2", len(cfg.Phases))
	}
}

func TestLoadServiceDeployConfig_permitsAfterField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	yml := `after:
  - othersvc
phases:
  - name: setup
    steps:
      - name: noop
        type: shell
        cmd: 'true'
`
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.After) != 1 || cfg.After[0] != "othersvc" {
		t.Errorf("After = %v, want [othersvc]", cfg.After)
	}
}

func TestLoadResetConfig_rejectsAfterField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reset.yml")
	yml := `after:
  - somesvc
phases:
  - name: setup
    steps:
      - name: noop
        type: shell
        cmd: 'true'
`
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadResetConfig(path)
	if err == nil {
		t.Fatal("expected error for after: in reset.yml, got nil")
	}
	if !errors.Is(err, ErrAfterFieldNotAllowed) {
		t.Errorf("err = %v, want wraps ErrAfterFieldNotAllowed", err)
	}
}

// --- LoadServiceResetConfig / LoadServiceResetConfigs ---

func TestLoadServiceResetConfig_presentFile(t *testing.T) {
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "devbox", "services", "mydb")
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		t.Fatal(err)
	}
	yml := `phases:
  - name: wipe
    steps:
      - name: drop
        type: shell
        cmd: 'echo drop'
`
	if err := os.WriteFile(filepath.Join(svcDir, "reset.yml"), []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadServiceResetConfig(dir, "mydb")
	if err != nil {
		t.Fatalf("LoadServiceResetConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config, got nil")
	}
	if len(cfg.Phases) != 1 || cfg.Phases[0].Name != "wipe" {
		t.Errorf("phases = %v, want [{wipe ...}]", cfg.Phases)
	}
}

func TestLoadServiceResetConfig_missingFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadServiceResetConfig(dir, "ghost")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config for missing file, got: %+v", cfg)
	}
}

func TestLoadServiceResetConfig_unknownFieldRejected(t *testing.T) {
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "devbox", "services", "svc")
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		t.Fatal(err)
	}
	yml := `phases: []
bogus_field: true
`
	if err := os.WriteFile(filepath.Join(svcDir, "reset.yml"), []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadServiceResetConfig(dir, "svc")
	if err == nil {
		t.Fatal("expected strict-decode error for unknown field, got nil")
	}
}

func TestLoadServiceResetConfig_rejectsAfterField(t *testing.T) {
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "devbox", "services", "svc")
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		t.Fatal(err)
	}
	yml := `after:
  - other
phases: []
`
	if err := os.WriteFile(filepath.Join(svcDir, "reset.yml"), []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadServiceResetConfig(dir, "svc")
	if err == nil {
		t.Fatal("expected error for after: in service reset.yml, got nil")
	}
	if !errors.Is(err, ErrAfterFieldNotAllowed) {
		t.Errorf("err = %v, want wraps ErrAfterFieldNotAllowed", err)
	}
}

func TestLoadServiceResetConfigs_loadsPresent(t *testing.T) {
	dir := t.TempDir()
	for _, svc := range []string{"db", "cache"} {
		d := filepath.Join(dir, "devbox", "services", svc)
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
		yml := "phases:\n  - name: wipe\n    steps:\n      - name: s\n        type: shell\n        cmd: 'true'\n"
		if err := os.WriteFile(filepath.Join(d, "reset.yml"), []byte(yml), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	// third service without reset.yml — should be omitted silently
	noReset := filepath.Join(dir, "devbox", "services", "noreset")
	if err := os.MkdirAll(noReset, 0755); err != nil {
		t.Fatal(err)
	}

	result, err := LoadServiceResetConfigs(dir)
	if err != nil {
		t.Fatalf("LoadServiceResetConfigs: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("want 2 entries (db, cache), got %d: %v", len(result), result)
	}
	for _, key := range []string{"db", "cache"} {
		if result[key] == nil {
			t.Errorf("expected non-nil entry for %q", key)
		}
	}
	if result["noreset"] != nil {
		t.Errorf("expected no entry for noreset, got %+v", result["noreset"])
	}
}

func TestLoadServiceResetConfigs_missingServicesDir(t *testing.T) {
	dir := t.TempDir()
	result, err := LoadServiceResetConfigs(dir)
	if err != nil {
		t.Fatalf("expected nil error for absent services dir, got: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}

func TestLoadServiceResetConfigs_collectsErrors(t *testing.T) {
	dir := t.TempDir()
	// service with invalid reset.yml (unknown field)
	svcDir := filepath.Join(dir, "devbox", "services", "bad")
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "reset.yml"), []byte("phases: []\nbad_key: x\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadServiceResetConfigs(dir)
	if err == nil {
		t.Fatal("expected error for invalid service reset.yml, got nil")
	}
}

func TestDeployConfigAfter_decodePresent(t *testing.T) {
	yml := `after:
  - svc-a
  - svc-b
phases: []
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.After) != 2 || cfg.After[0] != "svc-a" || cfg.After[1] != "svc-b" {
		t.Errorf("After = %v, want [svc-a svc-b]", cfg.After)
	}
}

func TestDeployConfigAfter_decodeAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte("phases: []\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.After) != 0 {
		t.Errorf("After = %v, want empty", cfg.After)
	}
}

// --- LoadServiceDeployConfigs new path ---

func TestLoadServiceDeployConfigs_strictDecoderRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "devbox", "services", "main")
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		t.Fatal(err)
	}
	badYML := `phases:
  - name: setup
    steps:
      - name: noop
        type: shell
        cmd: 'true'
        notafield: boom
`
	if err := os.WriteFile(filepath.Join(svcDir, "deploy.yml"), []byte(badYML), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadServiceDeployConfigs(dir, map[string]ServiceConfig{"main": {}})
	if err == nil {
		t.Fatal("expected strict-decode error for unknown field, got nil")
	}
}

func TestLoadServiceDeployConfigs_onlyServicesWithDeployFile(t *testing.T) {
	dir := t.TempDir()
	mainDir := filepath.Join(dir, "devbox", "services", "main")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainDir, "deploy.yml"), []byte("phases: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// "other" has no deploy.yml
	otherDir := filepath.Join(dir, "devbox", "services", "other")
	if err := os.MkdirAll(otherDir, 0755); err != nil {
		t.Fatal(err)
	}
	services := map[string]ServiceConfig{"main": {}, "other": {}}
	result, err := LoadServiceDeployConfigs(dir, services)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfigs: %v", err)
	}
	if _, ok := result["main"]; !ok {
		t.Error("main should be loaded")
	}
	if _, ok := result["other"]; ok {
		t.Error("other should not be loaded (no deploy.yml)")
	}
}

func TestLoadConfig_exportsLoaded(t *testing.T) {
	defaultsWithExports := sampleDefaultsYML + `
exports:
  env:
    - name: APP_PORT
      from: services.app.ports.http
      format: int
    - name: STATE
      from: state
`
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithExports, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Exports.Env) != 2 {
		t.Fatalf("Exports.Env len = %d, want 2", len(cfg.Exports.Env))
	}
	if cfg.Exports.Env[0].Name != "APP_PORT" {
		t.Errorf("rule[0].Name = %q", cfg.Exports.Env[0].Name)
	}
	if cfg.Exports.Env[0].From != "services.app.ports.http" {
		t.Errorf("rule[0].From = %q", cfg.Exports.Env[0].From)
	}
	if cfg.Exports.Env[0].Format != "int" {
		t.Errorf("rule[0].Format = %q", cfg.Exports.Env[0].Format)
	}
}

func TestLoadConfig_reservedExportNameRejected(t *testing.T) {
	for _, name := range ReservedExportNames {
		t.Run(name, func(t *testing.T) {
			defaultsWithReservedRule := sampleDefaultsYML + `
exports:
  env:
    - name: ` + name + `
      from: services.app.ports.http
`
			path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithReservedRule, "")
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatalf("LoadConfig: expected error for reserved name %q, got nil", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q should mention reserved name %q", err.Error(), name)
			}
			if !strings.Contains(err.Error(), "reserved") {
				t.Errorf("error %q should explain that the name is reserved", err.Error())
			}
		})
	}
}

func TestIsReservedExportName(t *testing.T) {
	for _, name := range ReservedExportNames {
		if !IsReservedExportName(name) {
			t.Errorf("IsReservedExportName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "APP_PORT", "project", "uid", "gid", "PROJECT_NAME"} {
		if IsReservedExportName(name) {
			t.Errorf("IsReservedExportName(%q) = true, want false", name)
		}
	}
}

// --- ServiceCLIConfig: Mode and Env fields ---

const sampleServicesWithCLIYML = `
services:
  main:
    type: app
    container: app-main
    mandatory: true
    dir: ./services/main
    dir_internal: /workspace
    work_dir_internal: /workspace/src
    configs:
      - .env
    cli:
      mode: auto
      shell: bash
      user: www-data
      workdir: /workspace/src
      env:
        APP_ENV: local
        DEBUG: "true"
  main-debug:
    type: app
    container: app-main-debug
    mandatory: false
    extends: main
    compose:
      - compose/services/main/debug.yml
  main-run:
    type: app
    container: app-main-run
    mandatory: false
    extends: main
    cli:
      mode: run
`

func TestLoadServicesConfig_modeField(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithCLIYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	main := services["main"]
	if main.CLI.Mode != "auto" {
		t.Errorf("main.CLI.Mode = %q, want auto", main.CLI.Mode)
	}
}

func TestLoadServicesConfig_envField(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithCLIYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	main := services["main"]
	if len(main.CLI.Env) != 2 {
		t.Fatalf("main.CLI.Env len = %d, want 2", len(main.CLI.Env))
	}
	if main.CLI.Env["APP_ENV"] != "local" {
		t.Errorf("main.CLI.Env[APP_ENV] = %q, want local", main.CLI.Env["APP_ENV"])
	}
	if main.CLI.Env["DEBUG"] != "true" {
		t.Errorf("main.CLI.Env[DEBUG] = %q, want true", main.CLI.Env["DEBUG"])
	}
}

func TestLoadServicesConfig_extendsInheritsMode(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithCLIYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// main-debug extends main and has no CLI block of its own — inherits mode from parent
	debug := services["main-debug"]
	if debug.CLI.Mode != "auto" {
		t.Errorf("main-debug.CLI.Mode = %q, want auto (inherited from main)", debug.CLI.Mode)
	}
}

func TestLoadServicesConfig_extendsInheritsEnv(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithCLIYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// main-debug extends main and has no CLI.Env of its own — inherits env from parent
	debug := services["main-debug"]
	if len(debug.CLI.Env) != 2 {
		t.Fatalf("main-debug.CLI.Env len = %d, want 2 (inherited from main)", len(debug.CLI.Env))
	}
	if debug.CLI.Env["APP_ENV"] != "local" {
		t.Errorf("main-debug.CLI.Env[APP_ENV] = %q, want local (inherited)", debug.CLI.Env["APP_ENV"])
	}
}

func TestLoadServicesConfig_extendsOverridesMode(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithCLIYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// main-run extends main but sets its own mode: run — should NOT inherit parent mode
	run := services["main-run"]
	if run.CLI.Mode != "run" {
		t.Errorf("main-run.CLI.Mode = %q, want run (own value, not inherited)", run.CLI.Mode)
	}
}

// --- ServiceConfig.Dirs field ---

const sampleServicesWithDirsYML = `
services:
  base:
    type: app
    container: app-base
    mandatory: true
    dir: ./services/base
    dirs:
      - logs
      - home
      - runtime
  child:
    type: app
    container: app-child
    mandatory: false
    extends: base
    dirs:
      - extra
  child-nodir:
    type: app
    container: app-child-nodir
    mandatory: false
    extends: base
  child-overlap:
    type: app
    container: app-child-overlap
    mandatory: false
    extends: base
    dirs:
      - logs
      - home
      - custom
`

func TestLoadServicesConfig_dirsField(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithDirsYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	base := services["base"]
	want := []string{"logs", "home", "runtime"}
	if len(base.Dirs) != len(want) {
		t.Fatalf("base.Dirs = %v, want %v", base.Dirs, want)
	}
	for i, d := range want {
		if base.Dirs[i] != d {
			t.Errorf("base.Dirs[%d] = %q, want %q", i, base.Dirs[i], d)
		}
	}
}

func TestLoadServicesConfig_dirsInheritedAndMerged(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithDirsYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// child extends base (dirs: [logs, home, runtime]) and adds dirs: [extra]
	// expected: parent first, then child additions
	child := services["child"]
	want := []string{"logs", "home", "runtime", "extra"}
	if len(child.Dirs) != len(want) {
		t.Fatalf("child.Dirs = %v, want %v", child.Dirs, want)
	}
	for i, d := range want {
		if child.Dirs[i] != d {
			t.Errorf("child.Dirs[%d] = %q, want %q", i, child.Dirs[i], d)
		}
	}
}

func TestLoadServicesConfig_dirsInheritedWhenChildEmpty(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithDirsYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// child-nodir extends base and has no dirs of its own — should inherit parent dirs
	child := services["child-nodir"]
	want := []string{"logs", "home", "runtime"}
	if len(child.Dirs) != len(want) {
		t.Fatalf("child-nodir.Dirs = %v, want %v", child.Dirs, want)
	}
	for i, d := range want {
		if child.Dirs[i] != d {
			t.Errorf("child-nodir.Dirs[%d] = %q, want %q", i, child.Dirs[i], d)
		}
	}
}

func TestLoadServicesConfig_dirsDeduplicated(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithDirsYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// child-overlap extends base (logs, home, runtime) and adds (logs, home, custom)
	// duplicate logs and home must appear only once; parent order preserved
	child := services["child-overlap"]
	want := []string{"logs", "home", "runtime", "custom"}
	if len(child.Dirs) != len(want) {
		t.Fatalf("child-overlap.Dirs = %v, want %v (deduplicated)", child.Dirs, want)
	}
	for i, d := range want {
		if child.Dirs[i] != d {
			t.Errorf("child-overlap.Dirs[%d] = %q, want %q", i, child.Dirs[i], d)
		}
	}
}

// --- LoadLifecycleConfig ---

const sampleLifecycleYML = `
run:
  update:
    enabled: true
    mode: prompt
  show_info: true
  final_message: "Project is ready for work!"
  phases:
    - name: start
      steps:
        - name: up
          type: devbox
          cmd: "docker up"
        - name: wait
          type: devbox
          cmd: "docker wait"
stop:
  final_message: "Project is stopped. Have a nice day!"
  phases:
    - name: stop
      steps:
        - name: down
          type: devbox
          cmd: "docker down"
`

func writeLifecycleFixture(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "lifecycle.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write lifecycle.yml: %v", err)
	}
	return path
}

func TestLoadLifecycleConfig_happyPath(t *testing.T) {
	path := writeLifecycleFixture(t, sampleLifecycleYML)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if cfg.Run == nil {
		t.Fatal("cfg.Run should not be nil")
	}
	if cfg.Run.Update == nil {
		t.Fatal("cfg.Run.Update should not be nil")
	}
	if cfg.Run.Update.Enabled == nil || !*cfg.Run.Update.Enabled {
		t.Error("cfg.Run.Update.Enabled should be true")
	}
	if cfg.Run.Update.Mode != "prompt" {
		t.Errorf("cfg.Run.Update.Mode = %q, want prompt", cfg.Run.Update.Mode)
	}
	if !cfg.Run.ShowInfo {
		t.Error("cfg.Run.ShowInfo should be true")
	}
	if cfg.Run.FinalMessage != "Project is ready for work!" {
		t.Errorf("cfg.Run.FinalMessage = %q", cfg.Run.FinalMessage)
	}
	if len(cfg.Run.Phases) != 1 || cfg.Run.Phases[0].Name != "start" {
		t.Errorf("cfg.Run.Phases = %v", cfg.Run.Phases)
	}
	if cfg.Stop == nil {
		t.Fatal("cfg.Stop should not be nil")
	}
	if cfg.Stop.FinalMessage != "Project is stopped. Have a nice day!" {
		t.Errorf("cfg.Stop.FinalMessage = %q", cfg.Stop.FinalMessage)
	}
	if len(cfg.Stop.Phases) != 1 || cfg.Stop.Phases[0].Name != "stop" {
		t.Errorf("cfg.Stop.Phases = %v", cfg.Stop.Phases)
	}
}

func TestLoadLifecycleConfig_notFound(t *testing.T) {
	_, err := LoadLifecycleConfig(filepath.Join(t.TempDir(), "nonexistent.yml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}

func TestLoadLifecycleConfig_invalidStep(t *testing.T) {
	yml := `
run:
  phases:
    - name: start
      steps:
        - name: bad
          type: devbox
          cmd: "docker up"
          run: echo hello
`
	path := writeLifecycleFixture(t, yml)
	_, err := LoadLifecycleConfig(path)
	if err == nil {
		t.Fatal("expected error for step with two action fields, got nil")
	}
}

func TestLoadLifecycleConfig_rejectsDeployServicesPhase(t *testing.T) {
	yml := `
run:
  phases:
    - name: services
      deploy_services: true
`
	path := writeLifecycleFixture(t, yml)
	_, err := LoadLifecycleConfig(path)
	if err == nil {
		t.Fatal("expected error for deploy_services phase in lifecycle, got nil")
	}
}

func TestLoadLifecycleConfig_defaultMode(t *testing.T) {
	// update block present but mode omitted — EffectiveMode should default to "prompt".
	yml := `
run:
  update:
    enabled: true
  phases:
    - name: start
      steps:
        - name: up
          type: devbox
          cmd: "docker up"
`
	path := writeLifecycleFixture(t, yml)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if cfg.Run.EffectiveMode() != "prompt" {
		t.Errorf("EffectiveMode() = %q, want prompt when mode omitted with enabled:true", cfg.Run.EffectiveMode())
	}
}

func TestLoadLifecycleConfig_invalidUpdateMode(t *testing.T) {
	yml := `
run:
  update:
    enabled: true
    mode: invalid-mode
  phases:
    - name: start
      steps:
        - name: up
          type: devbox
          cmd: "docker up"
`
	path := writeLifecycleFixture(t, yml)
	_, err := LoadLifecycleConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid update.mode, got nil")
	}
}

func TestLoadLifecycleConfig_defaultFinalMessages(t *testing.T) {
	// final_message omitted in both run and stop — defaults should be applied by loader.
	yml := `
run:
  phases:
    - name: start
      steps:
        - name: up
          type: devbox
          cmd: "docker up"
stop:
  phases:
    - name: stop
      steps:
        - name: down
          type: devbox
          cmd: "docker down"
`
	path := writeLifecycleFixture(t, yml)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if cfg.Run.FinalMessage != "Project is ready for work!" {
		t.Errorf("Run.FinalMessage = %q, want default", cfg.Run.FinalMessage)
	}
	if cfg.Stop.FinalMessage != "Project is stopped. Have a nice day!" {
		t.Errorf("Stop.FinalMessage = %q, want default", cfg.Stop.FinalMessage)
	}
}

func TestLoadLifecycleConfig_ContinueOnError_YAML(t *testing.T) {
	// Verify that continue_on_error: true in YAML is correctly parsed by the loader.
	yml := `
run:
  phases:
    - name: hooks
      steps:
        - name: optional-hook
          type: shell
          cmd: echo hello
          continue_on_error: true
`
	path := writeLifecycleFixture(t, yml)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if len(cfg.Run.Phases) == 0 || len(cfg.Run.Phases[0].Steps) == 0 {
		t.Fatal("expected at least one phase with one step")
	}
	step := cfg.Run.Phases[0].Steps[0]
	if !step.ContinueOnError {
		t.Errorf("step.ContinueOnError = false, want true (continue_on_error: true in YAML)")
	}
}

func TestLoadLifecycleConfig_explicitFinalMessagesPreserved(t *testing.T) {
	yml := `
run:
  final_message: "Custom run message"
  phases:
    - name: start
      steps:
        - name: up
          type: devbox
          cmd: "docker up"
stop:
  final_message: "Custom stop message"
  phases:
    - name: stop
      steps:
        - name: down
          type: devbox
          cmd: "docker down"
`
	path := writeLifecycleFixture(t, yml)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if cfg.Run.FinalMessage != "Custom run message" {
		t.Errorf("Run.FinalMessage = %q, want preserved value", cfg.Run.FinalMessage)
	}
	if cfg.Stop.FinalMessage != "Custom stop message" {
		t.Errorf("Stop.FinalMessage = %q, want preserved value", cfg.Stop.FinalMessage)
	}
}

func TestLoadLifecycleConfig_updateBlockPresentEnabledOmitted(t *testing.T) {
	// Writing the update: block without enabled: → loader sets Enabled to &true.
	yml := `
run:
  update:
    mode: auto
  phases:
    - name: start
      steps:
        - name: up
          type: devbox
          cmd: "docker up"
`
	path := writeLifecycleFixture(t, yml)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if cfg.Run.Update.Enabled == nil {
		t.Fatal("Update.Enabled should be set by loader when block is present")
	}
	if !*cfg.Run.Update.Enabled {
		t.Error("Update.Enabled should be true (block presence = opt-in)")
	}
}

// --- EffectiveMode ---

func TestEffectiveMode(t *testing.T) {
	trueVal := true
	falseVal := false

	cases := []struct {
		name string
		cfg  *LifecycleRunConfig
		want string
	}{
		{
			name: "update block omitted",
			cfg:  &LifecycleRunConfig{Update: nil},
			want: "off",
		},
		{
			name: "block present enabled omitted (nil)",
			cfg:  &LifecycleRunConfig{Update: &LifecycleUpdate{Enabled: nil}},
			want: "off",
		},
		{
			name: "enabled true mode auto",
			cfg:  &LifecycleRunConfig{Update: &LifecycleUpdate{Enabled: &trueVal, Mode: "auto"}},
			want: "auto",
		},
		{
			name: "enabled false mode auto",
			cfg:  &LifecycleRunConfig{Update: &LifecycleUpdate{Enabled: &falseVal, Mode: "auto"}},
			want: "off",
		},
		{
			name: "enabled true mode omitted",
			cfg:  &LifecycleRunConfig{Update: &LifecycleUpdate{Enabled: &trueVal, Mode: ""}},
			want: "prompt",
		},
		{
			name: "enabled true mode check",
			cfg:  &LifecycleRunConfig{Update: &LifecycleUpdate{Enabled: &trueVal, Mode: "check"}},
			want: "check",
		},
		{
			name: "enabled true mode off",
			cfg:  &LifecycleRunConfig{Update: &LifecycleUpdate{Enabled: &trueVal, Mode: "off"}},
			want: "off",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.EffectiveMode()
			if got != tc.want {
				t.Errorf("EffectiveMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- mergeDeduplicatedStrings ---

func TestMergeDeduplicatedStrings_basic(t *testing.T) {
	got := mergeDeduplicatedStrings([]string{"a", "b"}, []string{"c"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("[%d] got %q, want %q", i, got[i], v)
		}
	}
}

func TestMergeDeduplicatedStrings_deduplicates(t *testing.T) {
	got := mergeDeduplicatedStrings([]string{"a", "b", "c"}, []string{"b", "d"})
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("[%d] got %q, want %q", i, got[i], v)
		}
	}
}

func TestMergeDeduplicatedStrings_emptyA(t *testing.T) {
	got := mergeDeduplicatedStrings(nil, []string{"x", "y"})
	want := []string{"x", "y"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergeDeduplicatedStrings_emptyB(t *testing.T) {
	got := mergeDeduplicatedStrings([]string{"x", "y"}, nil)
	want := []string{"x", "y"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergeDeduplicatedStrings_bothEmpty(t *testing.T) {
	got := mergeDeduplicatedStrings(nil, nil)
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestMergeDeduplicatedStrings_allDuplicates(t *testing.T) {
	got := mergeDeduplicatedStrings([]string{"a", "b"}, []string{"a", "b"})
	want := []string{"a", "b"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// writePipelineFixture writes content to a temporary <name>.yml and returns the path.
func writePipelineFixture(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name+".yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestLoadServiceDeployConfig_logDefaultEnabled verifies that omitting `log:` in
// deploy.yml defaults to logging enabled.
func TestLoadServiceDeployConfig_logDefaultEnabled(t *testing.T) {
	path := writePipelineFixture(t, "deploy", "phases: []\n")
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if !cfg.LogEnabled() {
		t.Errorf("deploy log should default to enabled")
	}
}

// TestLoadServiceDeployConfig_logExplicitFalse verifies that `log: false` disables it.
func TestLoadServiceDeployConfig_logExplicitFalse(t *testing.T) {
	path := writePipelineFixture(t, "deploy", "log: false\nphases: []\n")
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if cfg.LogEnabled() {
		t.Errorf("deploy log should be disabled when log: false")
	}
}

// TestLoadResetConfig_logDefaultDisabled verifies that omitting `log:` in
// reset.yml defaults to logging disabled.
func TestLoadResetConfig_logDefaultDisabled(t *testing.T) {
	path := writePipelineFixture(t, "reset", "phases: []\n")
	cfg, err := LoadResetConfig(path)
	if err != nil {
		t.Fatalf("LoadResetConfig: %v", err)
	}
	if cfg.LogEnabled() {
		t.Errorf("reset log should default to disabled")
	}
}

// TestLoadResetConfig_logExplicitTrue verifies that `log: true` enables it.
func TestLoadResetConfig_logExplicitTrue(t *testing.T) {
	path := writePipelineFixture(t, "reset", "log: true\nphases: []\n")
	cfg, err := LoadResetConfig(path)
	if err != nil {
		t.Fatalf("LoadResetConfig: %v", err)
	}
	if !cfg.LogEnabled() {
		t.Errorf("reset log should be enabled when log: true")
	}
}

// TestLoadLifecycleConfig_logDefaults verifies run/stop logging defaults to
// disabled when the `log:` field is omitted.
func TestLoadLifecycleConfig_logDefaults(t *testing.T) {
	yml := `
run:
  phases:
    - name: start
      steps:
        - name: up
          type: devbox
          cmd: "docker up"
stop:
  phases:
    - name: stop
      steps:
        - name: down
          type: devbox
          cmd: "docker down"
`
	path := writeLifecycleFixture(t, yml)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if cfg.Run.LogEnabled() {
		t.Errorf("run log should default to disabled")
	}
	if cfg.Stop.LogEnabled() {
		t.Errorf("stop log should default to disabled")
	}
}

// --- FilesGate ---

func TestLoadServiceDeployConfig_stepWithFilesGateShortForm(t *testing.T) {
	yml := `
phases:
  - name: setup
    steps:
      - name: dump-deploy
        type: command
        cmd: db-dump-deploy
        files_gate: readable
`
	path := writePipelineFixture(t, "deploy", yml)
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[0].Steps[0]
	if step.FilesGate == nil {
		t.Fatal("FilesGate should be set")
	}
	if step.FilesGate.State != "readable" {
		t.Errorf("FilesGate.State = %q, want readable", step.FilesGate.State)
	}
	if step.FilesGate.Command != "" {
		t.Errorf("FilesGate.Command should be empty (default to step.cmd), got %q", step.FilesGate.Command)
	}
}

func TestLoadServiceDeployConfig_stepWithFilesGateLongForm(t *testing.T) {
	yml := `
phases:
  - name: setup
    steps:
      - name: dump-deploy
        type: command
        cmd: db-dump-deploy
        files_gate:
          state: missing
          require: [dump]
`
	path := writePipelineFixture(t, "deploy", yml)
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[0].Steps[0]
	if step.FilesGate == nil {
		t.Fatal("FilesGate should be set")
	}
	if step.FilesGate.State != "missing" {
		t.Errorf("FilesGate.State = %q, want missing", step.FilesGate.State)
	}
}

func TestLoadServiceDeployConfig_stepWithFilesGateUnknownField(t *testing.T) {
	yml := `
phases:
  - name: setup
    steps:
      - name: dump-deploy
        type: command
        cmd: db-dump-deploy
        files_gate:
          state: readable
          unknown_field: value
`
	path := writePipelineFixture(t, "deploy", yml)
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("LoadServiceDeployConfig: expected error for unknown field in files_gate")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("LoadServiceDeployConfig error should mention unknown field, got: %v", err)
	}
}

// --- BinariesConfig ---

func TestLoadConfig_binariesAllDefaulted(t *testing.T) {
	// No binaries: block in any layer — all three fields get built-in defaults.
	path := writeLayeredFixture(t, sampleDevboxYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Binaries.Devbox != "devbox" {
		t.Errorf("Binaries.Devbox = %q, want devbox", cfg.Binaries.Devbox)
	}
	if cfg.Binaries.Docker != "docker" {
		t.Errorf("Binaries.Docker = %q, want docker", cfg.Binaries.Docker)
	}
	if cfg.Binaries.Shell != "sh" {
		t.Errorf("Binaries.Shell = %q, want sh", cfg.Binaries.Shell)
	}
	if cfg.Binaries.Git != "git" {
		t.Errorf("Binaries.Git = %q, want git", cfg.Binaries.Git)
	}
	// Raw map must also reflect defaults.
	rawBin, ok := cfg.Raw["binaries"].(map[string]any)
	if !ok {
		t.Fatal("cfg.Raw[\"binaries\"] is not map[string]any")
	}
	if rawBin["devbox"] != "devbox" {
		t.Errorf("Raw[binaries][devbox] = %v, want devbox", rawBin["devbox"])
	}
	if rawBin["docker"] != "docker" {
		t.Errorf("Raw[binaries][docker] = %v, want docker", rawBin["docker"])
	}
	if rawBin["shell"] != "sh" {
		t.Errorf("Raw[binaries][shell] = %v, want sh", rawBin["shell"])
	}
	if rawBin["git"] != "git" {
		t.Errorf("Raw[binaries][git] = %v, want git", rawBin["git"])
	}
}

func TestLoadConfig_binariesExplicitOverrides(t *testing.T) {
	devboxYML := `
schema_version: "1"
project:
  name: laravel
  prefix: devbox
binaries:
  devbox: my-devbox
  docker: podman
  shell: bash
  git: /opt/git/bin/git
`
	path := writeLayeredFixture(t, devboxYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Binaries.Devbox != "my-devbox" {
		t.Errorf("Binaries.Devbox = %q, want my-devbox", cfg.Binaries.Devbox)
	}
	if cfg.Binaries.Docker != "podman" {
		t.Errorf("Binaries.Docker = %q, want podman", cfg.Binaries.Docker)
	}
	if cfg.Binaries.Shell != "bash" {
		t.Errorf("Binaries.Shell = %q, want bash", cfg.Binaries.Shell)
	}
	if cfg.Binaries.Git != "/opt/git/bin/git" {
		t.Errorf("Binaries.Git = %q, want /opt/git/bin/git", cfg.Binaries.Git)
	}
	rawBin, ok := cfg.Raw["binaries"].(map[string]any)
	if !ok {
		t.Fatal("cfg.Raw[\"binaries\"] is not map[string]any")
	}
	if rawBin["docker"] != "podman" {
		t.Errorf("Raw[binaries][docker] = %v, want podman", rawBin["docker"])
	}
	if rawBin["git"] != "/opt/git/bin/git" {
		t.Errorf("Raw[binaries][git] = %v, want /opt/git/bin/git", rawBin["git"])
	}
}

func TestLoadConfig_binariesIgnoredFromDefaultsLayer(t *testing.T) {
	// Even when defaults.yml sets binaries.docker: podman, the resulting value
	// comes from top-level devbox.yml only (or the built-in default when absent).
	defaultsWithBinaries := sampleDefaultsYML + `
binaries:
  docker: podman
`
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithBinaries, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// devbox.yml has no binaries block, so the default "docker" must win over defaults.yml.
	if cfg.Binaries.Docker != "docker" {
		t.Errorf("Binaries.Docker = %q, want docker (defaults.yml binaries must be ignored)", cfg.Binaries.Docker)
	}
	rawBin, _ := cfg.Raw["binaries"].(map[string]any)
	if rawBin["docker"] != "docker" {
		t.Errorf("Raw[binaries][docker] = %v, want docker", rawBin["docker"])
	}
}

func TestLoadConfig_binariesIgnoredFromLocalLayer(t *testing.T) {
	// local.yml binaries block must also be ignored.
	localWithBinaries := `
binaries:
  docker: nerdctl
`
	path := writeLayeredFixture(t, sampleDevboxYML, sampleDefaultsYML, localWithBinaries)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Binaries.Docker != "docker" {
		t.Errorf("Binaries.Docker = %q, want docker (local.yml binaries must be ignored)", cfg.Binaries.Docker)
	}
}

func TestLoadConfig_binariesPartialOverride(t *testing.T) {
	// Only docker: set in top-level — other two get built-in defaults.
	devboxYML := `
schema_version: "1"
project:
  name: laravel
  prefix: devbox
binaries:
  docker: podman
`
	path := writeLayeredFixture(t, devboxYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Binaries.Devbox != "devbox" {
		t.Errorf("Binaries.Devbox = %q, want devbox (default)", cfg.Binaries.Devbox)
	}
	if cfg.Binaries.Docker != "podman" {
		t.Errorf("Binaries.Docker = %q, want podman", cfg.Binaries.Docker)
	}
	if cfg.Binaries.Shell != "sh" {
		t.Errorf("Binaries.Shell = %q, want sh (default)", cfg.Binaries.Shell)
	}
	rawBin, _ := cfg.Raw["binaries"].(map[string]any)
	if rawBin["devbox"] != "devbox" {
		t.Errorf("Raw[binaries][devbox] = %v, want devbox", rawBin["devbox"])
	}
	if rawBin["docker"] != "podman" {
		t.Errorf("Raw[binaries][docker] = %v, want podman", rawBin["docker"])
	}
	if rawBin["shell"] != "sh" {
		t.Errorf("Raw[binaries][shell] = %v, want sh", rawBin["shell"])
	}
}

func TestBinariesAccessors(t *testing.T) {
	// DevboxBin(nil) == "devbox"
	if got := DevboxBin(nil); got != "devbox" {
		t.Errorf("DevboxBin(nil) = %q, want devbox", got)
	}
	// DockerBin(&DevboxConfig{}) == "docker"
	if got := DockerBin(&DevboxConfig{}); got != "docker" {
		t.Errorf("DockerBin(&DevboxConfig{}) = %q, want docker", got)
	}
	// ShellBin(nil) == "sh"
	if got := ShellBin(nil); got != "sh" {
		t.Errorf("ShellBin(nil) = %q, want sh", got)
	}
	// ShellBin with explicit value
	cfg := &DevboxConfig{Binaries: BinariesConfig{Shell: "bash"}}
	if got := ShellBin(cfg); got != "bash" {
		t.Errorf("ShellBin(cfg) = %q, want bash", got)
	}
	// DevboxBin with explicit value
	cfg2 := &DevboxConfig{Binaries: BinariesConfig{Devbox: "my-devbox"}}
	if got := DevboxBin(cfg2); got != "my-devbox" {
		t.Errorf("DevboxBin(cfg2) = %q, want my-devbox", got)
	}
	// DockerBin with explicit value
	cfg3 := &DevboxConfig{Binaries: BinariesConfig{Docker: "podman"}}
	if got := DockerBin(cfg3); got != "podman" {
		t.Errorf("DockerBin(cfg3) = %q, want podman", got)
	}
	// GitBin(nil) == "git"
	if got := GitBin(nil); got != "git" {
		t.Errorf("GitBin(nil) = %q, want git", got)
	}
	// GitBin(&DevboxConfig{}) == "git"
	if got := GitBin(&DevboxConfig{}); got != "git" {
		t.Errorf("GitBin(&DevboxConfig{}) = %q, want git", got)
	}
	// GitBin with explicit value
	cfg4 := &DevboxConfig{Binaries: BinariesConfig{Git: "/opt/git/bin/git"}}
	if got := GitBin(cfg4); got != "/opt/git/bin/git" {
		t.Errorf("GitBin(cfg4) = %q, want /opt/git/bin/git", got)
	}
}

// TestLoadConfig_noTopLevelIDEField verifies that the top-level IDE config
// has been removed from DevboxConfig. The IDE field is no longer part of the
// typed configuration and cfg.IDE does not exist.
func TestLoadConfig_noTopLevelIDEField(t *testing.T) {
	// Verify that the DevboxConfig struct does not carry top-level IDE state.
	// Reflection check: IDE field should not exist in the struct.
	cfgStructType := reflect.TypeFor[DevboxConfig]()
	if _, ok := cfgStructType.FieldByName("IDE"); ok {
		t.Error("DevboxConfig should not have an IDE field")
	}
}

// TestLoadLifecycleConfig_logExplicit verifies that `log: true` is respected
// for both run and stop.
func TestLoadLifecycleConfig_logExplicit(t *testing.T) {
	yml := `
run:
  log: true
  phases:
    - name: start
      steps:
        - name: up
          type: devbox
          cmd: "docker up"
stop:
  log: true
  phases:
    - name: stop
      steps:
        - name: down
          type: devbox
          cmd: "docker down"
`
	path := writeLifecycleFixture(t, yml)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if !cfg.Run.LogEnabled() {
		t.Errorf("run log should be enabled when log: true")
	}
	if !cfg.Stop.LogEnabled() {
		t.Errorf("stop log should be enabled when log: true")
	}
}

// --- ComposeFilesAll ---

func TestComposeFilesAll_baseOnly(t *testing.T) {
	// Only base file, no tool overlays or service composes.
	defaultsWithCompose := minimalDefaultsYML + `
compose:
  base: compose.yaml
`
	path := writeFullFixture(t, sampleDevboxYML, defaultsWithCompose, "", "", minimalToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got := cfg.ComposeFilesAll()
	want := []string{"compose.yaml"}
	if len(got) != len(want) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(got), len(want), got)
	}
	if got[0] != want[0] {
		t.Errorf("ComposeFilesAll[0] = %q, want %q", got[0], want[0])
	}
}

func TestComposeFilesAll_baseAndDisabledToolOverlay(t *testing.T) {
	// Base + disabled tool overlay. ComposeFilesAll must include disabled overlays.
	// Create a tools.yml with all 3 tools having compose files; defaults disables adminer.
	customTools := `
services:
  adminer:
    type: tool
    container: adminer
    compose:
      - compose/tools/adminer.yml
  mailpit:
    type: tool
    container: mailpit
    compose:
      - compose/tools/mailpit.yml
  redis_insight:
    type: tool
    container: redis_insight
    compose:
      - compose/tools/redis_insight.yml
`
	defaultsWithCompose := `
schema_version: "1"
services:
  adminer:
    enabled: false
  mailpit:
    enabled: true
  redis_insight:
    enabled: true
runtime:
  use_https: false
  spx:
    path: ""
compose:
  base: compose.yaml
`
	path := writeFullFixture(t, sampleDevboxYML, defaultsWithCompose, "", "", customTools)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Note: adminer is disabled, redis_insight and mailpit enabled.
	// ComposeFilesAll must include all, ComposeFiles only includes enabled.
	allFiles := cfg.ComposeFilesAll()
	activeFiles := cfg.ComposeFiles()

	// Verify ComposeFilesAll includes all overlays in sorted key order.
	want := []string{
		"compose.yaml",
		"compose/tools/adminer.yml",       // sorted first, even though disabled
		"compose/tools/mailpit.yml",       // sorted
		"compose/tools/redis_insight.yml", // sorted
	}
	if len(allFiles) != len(want) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(allFiles), len(want), allFiles)
	}
	for i, w := range want {
		if allFiles[i] != w {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, allFiles[i], w)
		}
	}

	// Verify ComposeFiles only includes enabled overlays (redis_insight, mailpit, but not adminer).
	wantActive := []string{
		"compose.yaml",
		"compose/tools/mailpit.yml",       // sorted
		"compose/tools/redis_insight.yml", // sorted
	}
	if len(activeFiles) != len(wantActive) {
		t.Fatalf("ComposeFiles len = %d, want %d: %v", len(activeFiles), len(wantActive), activeFiles)
	}
	for i, w := range wantActive {
		if activeFiles[i] != w {
			t.Errorf("ComposeFiles[%d] = %q, want %q", i, activeFiles[i], w)
		}
	}
}

func TestComposeFilesAll_baseAndDisabledServiceOverlay(t *testing.T) {
	// Base + service composes, with mixed enabled/disabled services.
	// Use minimalDefaultsYML to avoid tool compose files.
	defaultsWithCompose := `
schema_version: "1"
services:
  adminer:
    enabled: false
  redis_insight:
    enabled: false
  mailpit:
    enabled: false
  worker:
    enabled: false
runtime:
  use_https: false
  spx:
    path: ""
state: ""
compose:
  base: compose.yaml
`
	servicesYML := `
services:
  main:
    type: app
    container: app-main
    mandatory: true
    dir: ./services/main
    dir_internal: /workspace
    work_dir_internal: /workspace/src
    compose:
      - compose/services/main/base.yml
  worker:
    type: app
    container: app-worker
    mandatory: false
    dir: ./services/worker
    dir_internal: /workspace
    work_dir_internal: /workspace/src
    compose:
      - compose/services/worker/base.yml
`
	path := writeFullFixture(t, sampleDevboxYML, defaultsWithCompose, "", servicesYML, minimalToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// ComposeFilesAll includes all services' composes, regardless of enabled state.
	allFiles := cfg.ComposeFilesAll()
	want := []string{
		"compose.yaml",
		"compose/services/main/base.yml",
		"compose/services/worker/base.yml",
	}
	if len(allFiles) != len(want) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(allFiles), len(want), allFiles)
	}
	for i, w := range want {
		if allFiles[i] != w {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, allFiles[i], w)
		}
	}

	// ComposeFiles only includes enabled services.
	activeFiles := cfg.ComposeFiles()
	wantActive := []string{
		"compose.yaml",
		"compose/services/main/base.yml",
	}
	if len(activeFiles) != len(wantActive) {
		t.Fatalf("ComposeFiles len = %d, want %d: %v", len(activeFiles), len(wantActive), activeFiles)
	}
	for i, w := range wantActive {
		if activeFiles[i] != w {
			t.Errorf("ComposeFiles[%d] = %q, want %q", i, activeFiles[i], w)
		}
	}
}

func TestComposeFilesAll_fullMixedScenario(t *testing.T) {
	// Base + tool overlays (some disabled) + service composes (some disabled).
	// Verify all are included in ComposeFilesAll, in correct order.
	// Use sampleDefaultsYML which already has tool compose files.
	defaultsWithBase := sampleDefaultsYML + `
compose:
  base: compose.yaml
`
	path := writeFullFixture(t, sampleDevboxYML, defaultsWithBase, "", sampleServicesYML, sampleToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	allFiles := cfg.ComposeFilesAll()

	// Expected: base, all tool overlays in sorted key order, all service composes in sorted name order.
	// From sampleDefaultsYML: adminer (disabled, no compose), mailpit (enabled), redis_insight (enabled)
	want := []string{
		"compose.yaml",
		// Tool overlays in sorted key order (mailpit, redis_insight)
		"compose/tools/mailpit.yml",
		"compose/tools/redis_insight.yml",
		// Service composes in sorted service name order (main, main-debug)
		"compose/services/main/debug.yml", // from main-debug service in sampleServicesYML
	}
	if len(allFiles) != len(want) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(allFiles), len(want), allFiles)
	}
	for i, w := range want {
		if allFiles[i] != w {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, allFiles[i], w)
		}
	}
}

func TestComposeFilesAll_noBase(t *testing.T) {
	// Tool overlays without a base file. ComposeFilesAll must still work.
	// Use sampleDefaultsYML which has mailpit and redis_insight enabled with compose files.
	path := writeLayeredFixture(t, sampleDevboxYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	allFiles := cfg.ComposeFilesAll()
	// sampleDefaultsYML doesn't have compose.base, only tool compose files (sorted).
	want := []string{
		"compose/tools/mailpit.yml",
		"compose/tools/redis_insight.yml",
	}
	if len(allFiles) != len(want) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(allFiles), len(want), allFiles)
	}
	for i, w := range want {
		if allFiles[i] != w {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, allFiles[i], w)
		}
	}
}

func TestComposeFilesAll_empty(t *testing.T) {
	// No compose section at all, and no tools with compose files.
	// Use minimalDefaultsYML/minimalToolsYML which have no compose section and no tool compose files.
	path := writeFullFixture(t, sampleDevboxYML, minimalDefaultsYML, "", "", minimalToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	allFiles := cfg.ComposeFilesAll()
	if len(allFiles) != 0 {
		t.Errorf("ComposeFilesAll should be empty when no compose section and no tool composes, got %v", allFiles)
	}
}

func TestComposeFilesAll_serviceMultipleComposeFiles(t *testing.T) {
	// Service with multiple compose files. They should all be included, in order.
	// Use minimalDefaultsYML to focus on service compose files, not tool ones.
	serviceYML := `
services:
  multi:
    type: app
    container: multi
    mandatory: true
    dir: ./services/multi
    dir_internal: /workspace
    work_dir_internal: /workspace/src
    compose:
      - compose/services/multi/base.yml
      - compose/services/multi/debug.yml
      - compose/services/multi/test.yml
`
	defaultsWithCompose := minimalDefaultsYML + `
compose:
  base: compose.yaml
`
	path := writeFullFixture(t, sampleDevboxYML, defaultsWithCompose, "", serviceYML, minimalToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	allFiles := cfg.ComposeFilesAll()
	want := []string{
		"compose.yaml",
		"compose/services/multi/base.yml",
		"compose/services/multi/debug.yml",
		"compose/services/multi/test.yml",
	}
	if len(allFiles) != len(want) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(allFiles), len(want), allFiles)
	}
	for i, w := range want {
		if allFiles[i] != w {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, allFiles[i], w)
		}
	}
}

func TestComposeFilesAll_unknownToolWithCompose(t *testing.T) {
	// An unknown/new tool (elasticvue) with compose file should be included
	// when enabled. This confirms the data-driven path works for any tool key.
	customTools := `
services:
  elasticvue:
    type: tool
    container: elasticvue
    compose:
      - compose/tools/elasticvue.yml
  adminer:
    type: tool
    container: adminer
`
	defaultsWithElasticvue := `
schema_version: "1"
services:
  elasticvue:
    enabled: true
  adminer:
    enabled: false
runtime:
  use_https: false
  spx:
    path: ""
compose:
  base: compose.yaml
`
	path := writeFullFixture(t, sampleDevboxYML, defaultsWithElasticvue, "", "", customTools)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	allFiles := cfg.ComposeFilesAll()
	activeFiles := cfg.ComposeFiles()

	// ComposeFilesAll should include all tools (adminer disabled, but still listed).
	// ComposeFilesAll includes all tools with compose files (enabled or disabled).
	// adminer has no compose field, so it's not included.
	wantAll := []string{
		"compose.yaml",
		"compose/tools/elasticvue.yml", // enabled, included
	}

	if len(allFiles) != len(wantAll) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(allFiles), len(wantAll), allFiles)
	}
	for i, w := range wantAll {
		if allFiles[i] != w {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, allFiles[i], w)
		}
	}

	// ComposeFiles only includes enabled tools with compose files.
	wantActive := []string{
		"compose.yaml",
		"compose/tools/elasticvue.yml", // enabled, included
	}
	if len(activeFiles) != len(wantActive) {
		t.Fatalf("ComposeFiles len = %d, want %d: %v", len(activeFiles), len(wantActive), activeFiles)
	}
	for i, w := range wantActive {
		if activeFiles[i] != w {
			t.Errorf("ComposeFiles[%d] = %q, want %q", i, activeFiles[i], w)
		}
	}
}

func TestComposeFilesAll_determinismSortedOrder(t *testing.T) {
	// Verify that ComposeFiles returns tools in sorted key order, not declaration order.
	// Build a config with 3+ tools whose keys sort differently from declaration order.
	// Run 100 times to catch any non-determinism due to Go's randomized map iteration.
	customTools := `
services:
  zebra:
    type: tool
    container: zebra
    compose:
      - compose/tools/zebra.yml
  apple:
    type: tool
    container: apple
    compose:
      - compose/tools/apple.yml
  mango:
    type: tool
    container: mango
    compose:
      - compose/tools/mango.yml
`
	defaultsUnsorted := `
schema_version: "1"
services:
  zebra:
    enabled: true
  apple:
    enabled: true
  mango:
    enabled: true
runtime:
  use_https: false
  spx:
    path: ""
compose:
  base: compose.yaml
`
	path := writeFullFixture(t, sampleDevboxYML, defaultsUnsorted, "", "", customTools)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Expected order: sorted by key (apple, mango, zebra), not declaration order (zebra, apple, mango).
	wantSorted := []string{
		"compose.yaml",
		"compose/tools/apple.yml",
		"compose/tools/mango.yml",
		"compose/tools/zebra.yml",
	}

	// Run ComposeFiles 100 times and verify order is always the same.
	for i := range 100 {
		got := cfg.ComposeFiles()
		if len(got) != len(wantSorted) {
			t.Fatalf("iteration %d: ComposeFiles len = %d, want %d: %v", i, len(got), len(wantSorted), got)
		}
		for j, w := range wantSorted {
			if got[j] != w {
				t.Errorf("iteration %d: ComposeFiles[%d] = %q, want %q", i, j, got[j], w)
			}
		}
	}
}

// --- ServiceConfig.IDE field ---

// TestServiceConfig_IDERenderEnabledExplicit tests the tristate logic for IDE rendering.
func TestServiceConfig_IDERenderEnabledExplicit(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
		wantExp  bool // wantExp indicates whether the value is explicit
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Render: ServiceRenderConfig{IDE: ServiceIDEConfig{Enabled: ptr(true)}}}, //nolint:modernize
			wantBool: true,
			wantExp:  true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{Render: ServiceRenderConfig{IDE: ServiceIDEConfig{Enabled: ptr(false)}}}, //nolint:modernize
			wantBool: false,
			wantExp:  true,
		},
		{
			name:     "omitted on app type",
			svc:      ServiceConfig{Type: "app"},
			wantBool: true,
			wantExp:  false,
		},
		{
			name:     "omitted on db type",
			svc:      ServiceConfig{Type: "db"},
			wantBool: false,
			wantExp:  false,
		},
		{
			name:     "omitted on empty type",
			svc:      ServiceConfig{Type: ""},
			wantBool: false,
			wantExp:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotExp := tt.svc.IDERenderEnabledExplicit()
			if got != tt.wantBool {
				t.Errorf("IDERenderEnabledExplicit() bool = %v, want %v", got, tt.wantBool)
			}
			if gotExp != tt.wantExp {
				t.Errorf("IDERenderEnabledExplicit() explicit = %v, want %v", gotExp, tt.wantExp)
			}
		})
	}
}

// TestServiceConfig_IDERenderEnabled tests the simple bool wrapper.
func TestServiceConfig_IDERenderEnabled(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Render: ServiceRenderConfig{IDE: ServiceIDEConfig{Enabled: ptr(true)}}}, //nolint:modernize
			wantBool: true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{Render: ServiceRenderConfig{IDE: ServiceIDEConfig{Enabled: ptr(false)}}}, //nolint:modernize
			wantBool: false,
		},
		{
			name:     "app default true",
			svc:      ServiceConfig{Type: "app"},
			wantBool: true,
		},
		{
			name:     "db default false",
			svc:      ServiceConfig{Type: "db"},
			wantBool: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.svc.IDERenderEnabled(); got != tt.wantBool {
				t.Errorf("IDERenderEnabled() = %v, want %v", got, tt.wantBool)
			}
		})
	}
}

// TestLoadServicesConfig_IDEEnabled tests IDE block inheritance.
func TestLoadServicesConfig_IDEEnabled(t *testing.T) {
	yml := `
services:
  parent:
    type: app
    container: parent
    mandatory: true
    dir: ./services/parent
    render:
      ide:
        enabled: false
        template: parent-tmpl
  child-inherit:
    type: app
    container: child-inherit
    mandatory: false
    extends: parent
  child-override-enabled:
    type: app
    container: child-override-enabled
    mandatory: false
    extends: parent
    render:
      ide:
        enabled: true
  child-override-template:
    type: app
    container: child-override-template
    mandatory: false
    extends: parent
    render:
      ide:
        template: child-tmpl
  child-override-both:
    type: app
    container: child-override-both
    mandatory: false
    extends: parent
    render:
      ide:
        enabled: true
        template: both-tmpl
`
	dir := t.TempDir()
	writeServicesDir(t, dir, yml)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}

	// Parent has explicit false and template
	parent := services["parent"]
	if parent.Render.IDE.Enabled == nil || *parent.Render.IDE.Enabled != false {
		t.Errorf("parent Render.IDE.Enabled should be false, got %v", parent.Render.IDE.Enabled)
	}
	if parent.Render.IDE.Template != "parent-tmpl" {
		t.Errorf("parent Render.IDE.Template = %q, want parent-tmpl", parent.Render.IDE.Template)
	}

	// Child inherits both parent's enabled and template
	childInh := services["child-inherit"]
	if childInh.Render.IDE.Enabled == nil || *childInh.Render.IDE.Enabled != false {
		t.Errorf("child-inherit Render.IDE.Enabled should inherit false from parent, got %v", childInh.Render.IDE.Enabled)
	}
	if childInh.Render.IDE.Template != "parent-tmpl" {
		t.Errorf("child-inherit Render.IDE.Template should inherit parent-tmpl, got %q", childInh.Render.IDE.Template)
	}

	// Child overrides enabled but inherits template
	childOvrE := services["child-override-enabled"]
	if childOvrE.Render.IDE.Enabled == nil || *childOvrE.Render.IDE.Enabled != true {
		t.Errorf("child-override-enabled Render.IDE.Enabled should be true, got %v", childOvrE.Render.IDE.Enabled)
	}
	if childOvrE.Render.IDE.Template != "parent-tmpl" {
		t.Errorf("child-override-enabled Render.IDE.Template should inherit parent-tmpl, got %q", childOvrE.Render.IDE.Template)
	}

	// Child overrides template but inherits enabled
	childOvrT := services["child-override-template"]
	if childOvrT.Render.IDE.Enabled == nil || *childOvrT.Render.IDE.Enabled != false {
		t.Errorf("child-override-template Render.IDE.Enabled should inherit false from parent, got %v", childOvrT.Render.IDE.Enabled)
	}
	if childOvrT.Render.IDE.Template != "child-tmpl" {
		t.Errorf("child-override-template Render.IDE.Template = %q, want child-tmpl", childOvrT.Render.IDE.Template)
	}

	// Child overrides both
	childOvrB := services["child-override-both"]
	if childOvrB.Render.IDE.Enabled == nil || *childOvrB.Render.IDE.Enabled != true {
		t.Errorf("child-override-both Render.IDE.Enabled should be true, got %v", childOvrB.Render.IDE.Enabled)
	}
	if childOvrB.Render.IDE.Template != "both-tmpl" {
		t.Errorf("child-override-both Render.IDE.Template = %q, want both-tmpl", childOvrB.Render.IDE.Template)
	}
}

// TestServiceConfig_AIRenderEnabledExplicit tests the tristate logic for AI docs rendering.
func TestServiceConfig_AIRenderEnabledExplicit(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
		wantExp  bool // wantExp indicates whether the value is explicit
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Render: ServiceRenderConfig{AI: ServiceAIConfig{Enabled: ptr(true)}}}, //nolint:modernize
			wantBool: true,
			wantExp:  true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{Render: ServiceRenderConfig{AI: ServiceAIConfig{Enabled: ptr(false)}}}, //nolint:modernize
			wantBool: false,
			wantExp:  true,
		},
		{
			name:     "omitted on app type",
			svc:      ServiceConfig{Type: "app"},
			wantBool: true,
			wantExp:  false,
		},
		{
			name:     "omitted on db type",
			svc:      ServiceConfig{Type: "db"},
			wantBool: true,
			wantExp:  false,
		},
		{
			name:     "omitted on empty type",
			svc:      ServiceConfig{Type: ""},
			wantBool: true,
			wantExp:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotExp := tt.svc.AIRenderEnabledExplicit()
			if got != tt.wantBool {
				t.Errorf("AIRenderEnabledExplicit() bool = %v, want %v", got, tt.wantBool)
			}
			if gotExp != tt.wantExp {
				t.Errorf("AIRenderEnabledExplicit() explicit = %v, want %v", gotExp, tt.wantExp)
			}
		})
	}
}

// TestServiceConfig_AIRenderEnabled tests the simple bool wrapper.
func TestServiceConfig_AIRenderEnabled(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Render: ServiceRenderConfig{AI: ServiceAIConfig{Enabled: ptr(true)}}}, //nolint:modernize
			wantBool: true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{Render: ServiceRenderConfig{AI: ServiceAIConfig{Enabled: ptr(false)}}}, //nolint:modernize
			wantBool: false,
		},
		{
			name:     "app default true",
			svc:      ServiceConfig{Type: "app"},
			wantBool: true,
		},
		{
			name:     "db default true",
			svc:      ServiceConfig{Type: "db"},
			wantBool: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.svc.AIRenderEnabled(); got != tt.wantBool {
				t.Errorf("AIRenderEnabled() = %v, want %v", got, tt.wantBool)
			}
		})
	}
}

// TestLoadServicesConfig_AIEnabled tests AI docs block inheritance.
func TestLoadServicesConfig_AIEnabled(t *testing.T) {
	yml := `
services:
  parent:
    type: app
    container: parent
    mandatory: true
    dir: ./services/parent
    render:
      ai:
        enabled: false
        template: parent-tmpl
  child-inherit:
    type: app
    container: child-inherit
    mandatory: false
    extends: parent
  child-override-enabled:
    type: app
    container: child-override-enabled
    mandatory: false
    extends: parent
    render:
      ai:
        enabled: true
  child-override-template:
    type: app
    container: child-override-template
    mandatory: false
    extends: parent
    render:
      ai:
        template: child-tmpl
  child-override-both:
    type: app
    container: child-override-both
    mandatory: false
    extends: parent
    render:
      ai:
        enabled: true
        template: both-tmpl
  grandchild-multi-hop:
    type: app
    container: grandchild
    mandatory: false
    extends: child-inherit
`
	dir := t.TempDir()
	writeServicesDir(t, dir, yml)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}

	// Parent has explicit false and template
	parent := services["parent"]
	if parent.Render.AI.Enabled == nil || *parent.Render.AI.Enabled != false {
		t.Errorf("parent Render.AI.Enabled should be false, got %v", parent.Render.AI.Enabled)
	}
	if parent.Render.AI.Template != "parent-tmpl" {
		t.Errorf("parent Render.AI.Template = %q, want parent-tmpl", parent.Render.AI.Template)
	}

	// Child inherits both parent's enabled and template
	childInh := services["child-inherit"]
	if childInh.Render.AI.Enabled == nil || *childInh.Render.AI.Enabled != false {
		t.Errorf("child-inherit Render.AI.Enabled should inherit false from parent, got %v", childInh.Render.AI.Enabled)
	}
	if childInh.Render.AI.Template != "parent-tmpl" {
		t.Errorf("child-inherit Render.AI.Template should inherit parent-tmpl, got %q", childInh.Render.AI.Template)
	}

	// Child overrides enabled but inherits template
	childOvrE := services["child-override-enabled"]
	if childOvrE.Render.AI.Enabled == nil || *childOvrE.Render.AI.Enabled != true {
		t.Errorf("child-override-enabled Render.AI.Enabled should be true, got %v", childOvrE.Render.AI.Enabled)
	}
	if childOvrE.Render.AI.Template != "parent-tmpl" {
		t.Errorf("child-override-enabled Render.AI.Template should inherit parent-tmpl, got %q", childOvrE.Render.AI.Template)
	}

	// Child overrides template but inherits enabled
	childOvrT := services["child-override-template"]
	if childOvrT.Render.AI.Enabled == nil || *childOvrT.Render.AI.Enabled != false {
		t.Errorf("child-override-template Render.AI.Enabled should inherit false from parent, got %v", childOvrT.Render.AI.Enabled)
	}
	if childOvrT.Render.AI.Template != "child-tmpl" {
		t.Errorf("child-override-template Render.AI.Template = %q, want child-tmpl", childOvrT.Render.AI.Template)
	}

	// Child overrides both
	childOvrB := services["child-override-both"]
	if childOvrB.Render.AI.Enabled == nil || *childOvrB.Render.AI.Enabled != true {
		t.Errorf("child-override-both Render.AI.Enabled should be true, got %v", childOvrB.Render.AI.Enabled)
	}
	if childOvrB.Render.AI.Template != "both-tmpl" {
		t.Errorf("child-override-both Render.AI.Template = %q, want both-tmpl", childOvrB.Render.AI.Template)
	}

	// Grandchild (multi-hop): inherits from child-inherit
	grandchild := services["grandchild-multi-hop"]
	if grandchild.Render.AI.Enabled == nil || *grandchild.Render.AI.Enabled != false {
		t.Errorf("grandchild-multi-hop Render.AI.Enabled should inherit false from parent chain, got %v", grandchild.Render.AI.Enabled)
	}
	if grandchild.Render.AI.Template != "parent-tmpl" {
		t.Errorf("grandchild-multi-hop Render.AI.Template should inherit parent-tmpl, got %q", grandchild.Render.AI.Template)
	}
}

// TestServiceConfig_GitRenderEnabledExplicit tests the tristate logic for git-hooks rendering.
func TestServiceConfig_GitRenderEnabledExplicit(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
		wantExp  bool
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Render: ServiceRenderConfig{Git: ServiceGitHooksConfig{Enabled: ptr(true)}}}, //nolint:modernize
			wantBool: true,
			wantExp:  true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{Render: ServiceRenderConfig{Git: ServiceGitHooksConfig{Enabled: ptr(false)}}}, //nolint:modernize
			wantBool: false,
			wantExp:  true,
		},
		{
			name:     "omitted on app type",
			svc:      ServiceConfig{Type: "app"},
			wantBool: true,
			wantExp:  false,
		},
		{
			name:     "omitted on db type",
			svc:      ServiceConfig{Type: "db"},
			wantBool: false,
			wantExp:  false,
		},
		{
			name:     "omitted on empty type",
			svc:      ServiceConfig{Type: ""},
			wantBool: false,
			wantExp:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotExp := tt.svc.GitRenderEnabledExplicit()
			if got != tt.wantBool {
				t.Errorf("GitRenderEnabledExplicit() bool = %v, want %v", got, tt.wantBool)
			}
			if gotExp != tt.wantExp {
				t.Errorf("GitRenderEnabledExplicit() explicit = %v, want %v", gotExp, tt.wantExp)
			}
		})
	}
}

// TestServiceConfig_GitRenderEnabled tests the simple bool wrapper.
func TestServiceConfig_GitRenderEnabled(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Render: ServiceRenderConfig{Git: ServiceGitHooksConfig{Enabled: ptr(true)}}}, //nolint:modernize
			wantBool: true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{Render: ServiceRenderConfig{Git: ServiceGitHooksConfig{Enabled: ptr(false)}}}, //nolint:modernize
			wantBool: false,
		},
		{
			name:     "app default true",
			svc:      ServiceConfig{Type: "app"},
			wantBool: true,
		},
		{
			name:     "db default false",
			svc:      ServiceConfig{Type: "db"},
			wantBool: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.svc.GitRenderEnabled(); got != tt.wantBool {
				t.Errorf("GitRenderEnabled() = %v, want %v", got, tt.wantBool)
			}
		})
	}
}

// TestLoadServicesConfig_GitEnabled tests git-hooks block inheritance through extends.
func TestLoadServicesConfig_GitEnabled(t *testing.T) {
	yml := `
services:
  parent:
    type: app
    container: parent
    mandatory: true
    dir: ./services/parent
    render:
      git:
        enabled: false
        template: parent-tmpl
  child-inherit:
    type: app
    container: child-inherit
    mandatory: false
    extends: parent
  child-override-enabled:
    type: app
    container: child-override-enabled
    mandatory: false
    extends: parent
    render:
      git:
        enabled: true
  child-override-template:
    type: app
    container: child-override-template
    mandatory: false
    extends: parent
    render:
      git:
        template: child-tmpl
  child-override-both:
    type: app
    container: child-override-both
    mandatory: false
    extends: parent
    render:
      git:
        enabled: true
        template: both-tmpl
  grandchild-multi-hop:
    type: app
    container: grandchild
    mandatory: false
    extends: child-inherit
`
	dir := t.TempDir()
	writeServicesDir(t, dir, yml)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}

	parent := services["parent"]
	if parent.Render.Git.Enabled == nil || *parent.Render.Git.Enabled != false {
		t.Errorf("parent Render.Git.Enabled should be false, got %v", parent.Render.Git.Enabled)
	}
	if parent.Render.Git.Template != "parent-tmpl" {
		t.Errorf("parent Render.Git.Template = %q, want parent-tmpl", parent.Render.Git.Template)
	}

	childInh := services["child-inherit"]
	if childInh.Render.Git.Enabled == nil || *childInh.Render.Git.Enabled != false {
		t.Errorf("child-inherit Render.Git.Enabled should inherit false from parent, got %v", childInh.Render.Git.Enabled)
	}
	if childInh.Render.Git.Template != "parent-tmpl" {
		t.Errorf("child-inherit Render.Git.Template should inherit parent-tmpl, got %q", childInh.Render.Git.Template)
	}

	childOvrE := services["child-override-enabled"]
	if childOvrE.Render.Git.Enabled == nil || *childOvrE.Render.Git.Enabled != true {
		t.Errorf("child-override-enabled Render.Git.Enabled should be true, got %v", childOvrE.Render.Git.Enabled)
	}
	if childOvrE.Render.Git.Template != "parent-tmpl" {
		t.Errorf("child-override-enabled Render.Git.Template should inherit parent-tmpl, got %q", childOvrE.Render.Git.Template)
	}

	childOvrT := services["child-override-template"]
	if childOvrT.Render.Git.Enabled == nil || *childOvrT.Render.Git.Enabled != false {
		t.Errorf("child-override-template Render.Git.Enabled should inherit false from parent, got %v", childOvrT.Render.Git.Enabled)
	}
	if childOvrT.Render.Git.Template != "child-tmpl" {
		t.Errorf("child-override-template Render.Git.Template = %q, want child-tmpl", childOvrT.Render.Git.Template)
	}

	childOvrB := services["child-override-both"]
	if childOvrB.Render.Git.Enabled == nil || *childOvrB.Render.Git.Enabled != true {
		t.Errorf("child-override-both Render.Git.Enabled should be true, got %v", childOvrB.Render.Git.Enabled)
	}
	if childOvrB.Render.Git.Template != "both-tmpl" {
		t.Errorf("child-override-both Render.Git.Template = %q, want both-tmpl", childOvrB.Render.Git.Template)
	}

	grandchild := services["grandchild-multi-hop"]
	if grandchild.Render.Git.Enabled == nil || *grandchild.Render.Git.Enabled != false {
		t.Errorf("grandchild-multi-hop Render.Git.Enabled should inherit false from parent chain, got %v", grandchild.Render.Git.Enabled)
	}
	if grandchild.Render.Git.Template != "parent-tmpl" {
		t.Errorf("grandchild-multi-hop Render.Git.Template should inherit parent-tmpl, got %q", grandchild.Render.Git.Template)
	}
}

// TestLoadConfig_GitNotInjectedIntoRaw confirms git.* is not injected into raw["services"]
// for dot-path expressions (parity with ide/ai).
func TestLoadConfig_GitNotInjectedIntoRaw(t *testing.T) {
	svcYml := `services:
  main:
    type: app
    container: main
    mandatory: true
    dir: ./services/main
    render:
      git:
        enabled: true
        template: custom
`
	path := writeFullFixture(t, sampleDevboxYML, sampleDefaultsYML, "", svcYml, sampleToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Services["main"].Render.Git.Template != "custom" {
		t.Fatalf("expected Render.Git.Template to be loaded, got %q", cfg.Services["main"].Render.Git.Template)
	}
	svcMap, ok := cfg.Raw["services"].(map[string]any)
	if !ok {
		t.Fatalf("raw services not a map: %T", cfg.Raw["services"])
	}
	main, ok := svcMap["main"].(map[string]any)
	if !ok {
		t.Fatalf("raw services.main not a map: %T", svcMap["main"])
	}
	if _, present := main["git"]; present {
		t.Errorf("git.* should NOT be injected into raw[services][main]; got: %v", main["git"])
	}
}

// ptr is a helper to create a pointer to a value.
// nolint: unused,modernize // used in test table initialization
func ptr[T any](v T) *T {
	return &v
}

// TestDeployStep_UnmarshalYAML covers the custom unmarshaller's allow-list
// and the parallel-vs-leaf mutual exclusion rules.
func TestDeployStep_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "leaf step parses",
			yaml: `
name: hello
type: shell
cmd: echo hi
`,
		},
		{
			name: "pure parallel parses",
			yaml: `
name: group
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
    - {name: b, type: shell, cmd: echo b}
`,
		},
		{
			name: "parallel + type rejected",
			yaml: `
name: g
type: shell
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "type",
		},
		{
			name: "parallel + cmd rejected",
			yaml: `
name: g
cmd: x
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "cmd",
		},
		{
			name: "parallel + with rejected",
			yaml: `
name: g
with: {x: 1}
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "with",
		},
		{
			name: "parallel + check rejected",
			yaml: `
name: g
check: {type: shell, cmd: echo ok}
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "check",
		},
		{
			name: "parallel + files_gate rejected",
			yaml: `
name: g
files_gate:
  files: [foo]
  required:
    state: readable
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "files_gate",
		},
		{
			name: "parallel + continue_on_error rejected",
			yaml: `
name: g
continue_on_error: true
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "continue_on_error",
		},
		{
			name: "parallel + when accepted",
			yaml: `
name: g
when: {type: shell, cmd: "true"}
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
		},
		{
			name: "parallel + skip_confirm accepted",
			yaml: `
name: g
skip_confirm: true
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
		},
		{
			name: "parallel + description + name accepted",
			yaml: `
name: g
description: hello
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
		},
		{
			name: "empty parallel.steps parses at loader (length checked later)",
			yaml: `
name: g
parallel:
  steps: []
`,
		},
		{
			name: "single-element parallel.steps parses at loader",
			yaml: `
name: g
parallel:
  steps:
    - {name: only, type: shell, cmd: echo a}
`,
		},
		{
			name: "unknown field on DeployStep rejected",
			yaml: `
name: g
typo_field: hello
type: shell
cmd: echo a
`,
			wantErr:   true,
			errSubstr: "typo_field",
		},
		{
			name: "unknown field on ParallelGroup rejected",
			yaml: `
name: g
parallel:
  max_concurent: 2
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "max_concurent",
		},
		{
			name: "nested parallel parses (validated at plan time)",
			yaml: `
name: outer
parallel:
  steps:
    - name: inner
      parallel:
        steps:
          - {name: a, type: shell, cmd: echo a}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var step DeployStep
			err := yamlUnmarshalForTest(tt.yaml, &step)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errSubstr)
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestParallelGroup_FailFastTristate verifies the *bool round-trips nil/true/false.
func TestParallelGroup_FailFastTristate(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want *bool
	}{
		{name: "absent", yaml: `
name: g
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`, want: nil},
		{name: "true", yaml: `
name: g
parallel:
  fail_fast: true
  steps:
    - {name: a, type: shell, cmd: echo a}
`, want: new(true)},
		{name: "false", yaml: `
name: g
parallel:
  fail_fast: false
  steps:
    - {name: a, type: shell, cmd: echo a}
`, want: new(false)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var step DeployStep
			if err := yamlUnmarshalForTest(tt.yaml, &step); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if step.Parallel == nil {
				t.Fatalf("expected non-nil Parallel")
			}
			got := step.Parallel.FailFast
			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("expected nil, got %v", *got)
			case tt.want != nil && got == nil:
				t.Fatalf("expected %v, got nil", *tt.want)
			case tt.want != nil && got != nil && *tt.want != *got:
				t.Fatalf("expected %v, got %v", *tt.want, *got)
			}
		})
	}
}

func yamlUnmarshalForTest(src string, out any) error {
	return yaml.Unmarshal([]byte(src), out)
}

// Tests below this point were heavily tied to the legacy tools.yml /
// runtime.ports / runtime.hosts shape. They are kept as no-op stubs here so
// the file's structure remains unchanged; the new behaviour is exercised by
// the dedicated services-overlay/injection tests added in services_overlay_test.go.
var _ = sampleToolsServicesYML

// Legacy tools.yml shape removed in the unified-services-schema refactor.
// New behaviour is exercised by services_overlay_test.go.
