package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// sampleDevboxYML reflects the lean devbox.yml (project identity only).
const sampleDevboxYML = `
schema_version: "1"
project:
  name: laravel
  prefix: devbox
`

// minimalDefaultsYML has no tools with compose files, for testing base-only scenarios.
const minimalDefaultsYML = `
schema_version: "1"
tools:
  adminer:
    enabled: false
    container: adminer
    host: adminer.localhost
    port: 8080
  redis_insight:
    enabled: false
    container: redis_insight
    host: redis.localhost
    port: 5540
  mailpit:
    enabled: false
    container: mailpit
    host: mail.localhost
    port: 8025
runtime:
  use_https: false
  ports:
    app: 80
    db: 13306
    redis: 6379
  hosts:
    main: app.localhost
  spx:
    path: ""
state: ""
`

// sampleDefaultsYML mirrors devbox/defaults.yml.
const sampleDefaultsYML = `
schema_version: "1"
tools:
  adminer:
    enabled: false
    container: adminer
    host: adminer.localhost
    port: 8080
  redis_insight:
    enabled: true
    container: redis_insight
    host: redis.localhost
    port: 5540
    compose: compose/tools/redis_insight.yml
  mailpit:
    enabled: true
    container: mailpit
    host: mail.localhost
    port: 8025
    compose: compose/tools/mailpit.yml
runtime:
  use_https: false
  ports:
    app: 80
    db: 13306
    redis: 6379
  hosts:
    main: app.localhost
  spx:
    path: ""
state: ""
`

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
func writeLayeredFixture(t *testing.T, devbox, defaults, user string) string {
	t.Helper()
	return writeFullFixture(t, devbox, defaults, user, "")
}

// writeFullFixture creates the complete file layout used by LoadConfig,
// including an optional services.yml.
func writeFullFixture(t *testing.T, devbox, defaults, user, services string) string {
	t.Helper()
	dir := t.TempDir()

	devboxPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(devboxPath, []byte(devbox), 0644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	devboxDir := filepath.Join(dir, "devbox")

	if defaults != "" || services != "" || user != "" {
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

	if services != "" {
		if err := os.WriteFile(filepath.Join(devboxDir, "services.yml"), []byte(services), 0644); err != nil {
			t.Fatalf("write services.yml: %v", err)
		}
	}

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
    container: adminer
    host: adminer.localhost
    port: 8080
  redis_insight:
    enabled: true
    container: redis_insight
    host: redis.localhost
    port: 5540
  mailpit:
    enabled: true
    container: mailpit
    host: mail.localhost
    port: 8025
runtime:
  use_https: false
  ports:
    app: 80
    db: 13306
    redis: 6379
  hosts:
    main: app.localhost
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
runtime:
  ports:
    app: 8080
tools:
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

	// From defaults.yml
	if !cfg.Tools["redis_insight"].Enabled {
		t.Error("tools.redis_insight.enabled should be true (from defaults)")
	}
	if cfg.Tools["mailpit"].Port != 8025 {
		t.Errorf("tools.mailpit.port = %d, want 8025 (from defaults)", cfg.Tools["mailpit"].Port)
	}
	if cfg.Runtime.Hosts["main"] != "app.localhost" {
		t.Errorf("runtime.hosts.main = %q (from defaults)", cfg.Runtime.Hosts["main"])
	}

	// Overridden by local.yml
	if cfg.Runtime.Ports["app"] != 8080 {
		t.Errorf("runtime.ports.app = %d, want 8080 (from user)", cfg.Runtime.Ports["app"])
	}
	if !cfg.Tools["adminer"].Enabled {
		t.Error("tools.adminer.enabled should be true (overridden by user)")
	}
	if cfg.State != "staging" {
		t.Errorf("state = %q, want staging (from user)", cfg.State)
	}
}

func TestLoadConfig_noOptionalFiles(t *testing.T) {
	// Works fine when defaults.yml and local.yml are absent.
	path := writeLayeredFixture(t, sampleDevboxYML, "", "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Project.Name != "laravel" {
		t.Errorf("Project.Name = %q", cfg.Project.Name)
	}
}

func TestLoadConfig_noDefaultsFile(t *testing.T) {
	// local.yml present, defaults.yml absent.
	userYML := `state: demo`
	path := writeLayeredFixture(t, sampleDevboxYML, "", userYML)
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

func TestToolsAnyEnabled_allDisabled(t *testing.T) {
	tools := ToolsConfig{}
	if tools.AnyEnabled() {
		t.Error("AnyEnabled() should be false when all tools disabled")
	}
}

func TestToolsAnyEnabled_oneEnabled(t *testing.T) {
	tools := ToolsConfig{
		"adminer": ToolConfig{Enabled: true, Container: "adminer", Host: "adminer.localhost", Port: 8080},
	}
	if !tools.AnyEnabled() {
		t.Error("AnyEnabled() should be true when at least one tool is enabled")
	}
}

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
		"runtime": map[string]any{
			"ports": map[string]any{
				"app": 8080,
			},
		},
	}
	v, ok := ResolvePath(m, "runtime.ports.app")
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
	v, ok := ResolvePath(cfg.Raw, "runtime.ports.app")
	if !ok {
		t.Error("runtime.ports.app not found in Raw")
	}
	if fmt.Sprintf("%v", v) != "80" {
		t.Errorf("runtime.ports.app = %v, want 80", v)
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
	if cfg.Tools["redis_insight"].Compose != "compose/tools/redis_insight.yml" {
		t.Errorf("Tools[redis_insight].Compose = %q, want compose/tools/redis_insight.yml", cfg.Tools["redis_insight"].Compose)
	}
	if cfg.Tools["mailpit"].Compose != "compose/tools/mailpit.yml" {
		t.Errorf("Tools[mailpit].Compose = %q, want compose/tools/mailpit.yml", cfg.Tools["mailpit"].Compose)
	}
	// Adminer is disabled and has no compose, so check that empty is OK.
	if cfg.Tools["adminer"].Compose != "" {
		t.Errorf("Tools[adminer].Compose = %q, want empty", cfg.Tools["adminer"].Compose)
	}
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

func TestValidateConfigKeys_identifierSafety(t *testing.T) {
	tests := []struct {
		name      string
		toolKey   string
		wantError bool
	}{
		{"valid lowercase", "adminer", false},
		{"valid underscore", "redis_insight", false},
		{"valid leading underscore", "_private", false},
		{"invalid dash", "redis-insight", true},
		{"invalid dot", "redis.insight", true},
		{"invalid leading digit", "1redis", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &DevboxConfig{
				Tools: ToolsConfig{
					tt.toolKey: ToolConfig{
						Enabled:   false,
						Container: "test",
						Host:      "localhost",
						Port:      8080,
					},
				},
				Runtime: RuntimeConfig{
					Ports: RuntimePorts{},
					Hosts: RuntimeHosts{},
				},
			}
			err := validateConfigKeys(cfg)
			if (err != nil) != tt.wantError {
				t.Errorf("validateConfigKeys = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestValidateConfigKeys_toolFieldsRequired(t *testing.T) {
	tests := []struct {
		name    string
		tool    ToolConfig
		wantErr bool
	}{
		{"complete", ToolConfig{Enabled: true, Container: "test", Host: "localhost", Port: 8080}, false},
		{"disabled but complete", ToolConfig{Enabled: false, Container: "test", Host: "localhost", Port: 8080}, false},
		{"missing container", ToolConfig{Enabled: true, Host: "localhost", Port: 8080}, true},
		{"missing host", ToolConfig{Enabled: true, Container: "test", Port: 8080}, true},
		{"zero port", ToolConfig{Enabled: true, Container: "test", Host: "localhost", Port: 0}, true},
		{"negative port", ToolConfig{Enabled: true, Container: "test", Host: "localhost", Port: -1}, true},
		// Disabled tools are validated too — prevents tools enable from flipping a half-defined entry.
		{"disabled missing container", ToolConfig{Enabled: false, Host: "localhost", Port: 8080}, true},
		{"disabled zero port", ToolConfig{Enabled: false, Container: "test", Host: "localhost", Port: 0}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &DevboxConfig{
				Tools: ToolsConfig{"test_tool": tt.tool},
				Runtime: RuntimeConfig{
					Ports: RuntimePorts{},
					Hosts: RuntimeHosts{},
				},
			}
			err := validateConfigKeys(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateConfigKeys = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateConfigKeys_nilMapsAreSafe(t *testing.T) {
	cfg := &DevboxConfig{
		Tools: nil,
		Runtime: RuntimeConfig{
			Ports: nil,
			Hosts: nil,
		},
	}
	err := validateConfigKeys(cfg)
	if err != nil {
		t.Errorf("validateConfigKeys on nil maps = %v, want nil", err)
	}
}

func TestValidateConfigKeys_runtimePortsAndHosts(t *testing.T) {
	validTool := ToolConfig{Enabled: false, Container: "test", Host: "localhost", Port: 8080}
	tests := []struct {
		name      string
		ports     RuntimePorts
		hosts     RuntimeHosts
		wantError bool
	}{
		{"valid port key", RuntimePorts{"app": 3000}, nil, false},
		{"valid host key", nil, RuntimeHosts{"main": "main.localhost"}, false},
		{"invalid port key dash", RuntimePorts{"my-port": 3000}, nil, true},
		{"invalid port key leading digit", RuntimePorts{"1port": 3000}, nil, true},
		{"invalid host key dash", nil, RuntimeHosts{"my-host": "x.localhost"}, true},
		{"invalid host key dot", nil, RuntimeHosts{"my.host": "x.localhost"}, true},
		// tool name duplicated in runtime.ports must be rejected (no backward compat)
		{"port key duplicates tool name", RuntimePorts{"test_tool": 9090}, nil, true},
		// tool name duplicated in runtime.hosts must be rejected
		{"host key duplicates tool name", nil, RuntimeHosts{"test_tool": "test.localhost"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &DevboxConfig{
				Tools:   ToolsConfig{"test_tool": validTool},
				Runtime: RuntimeConfig{Ports: tt.ports, Hosts: tt.hosts},
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
	path := writeLayeredFixture(t, minimalYML, "", "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Check that AnyEnabled doesn't panic and returns false for nil/empty tools.
	// Call without nil guard to verify the nil-safe receiver works correctly.
	if cfg.Tools.AnyEnabled() {
		t.Error("AnyEnabled should return false for nil/empty tools")
	}
}

func TestLoadConfig_arbitraryToolKey(t *testing.T) {
	// An arbitrary new tool (elasticvue) should be preserved through the merge.
	defaultsWithNewTool := `
schema_version: "1"
tools:
  elasticvue:
    enabled: false
    container: elasticvue
    host: elastic.localhost
    port: 9200
    compose: compose/tools/elasticvue.yml
`
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithNewTool, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	tool, ok := cfg.Tools["elasticvue"]
	if !ok {
		t.Error("elasticvue tool not found in merged config")
	} else if tool.Container != "elasticvue" || tool.Port != 9200 {
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
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(sampleServicesYML), 0644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
	services, err := LoadServicesConfig(path)
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
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
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(sampleServicesYML), 0644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
	services, err := LoadServicesConfig(path)
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
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

func TestLoadServicesConfig_extendsUnknownParent(t *testing.T) {
	yml := `
services:
  child:
    type: app
    container: child
    extends: nonexistent
`
	dir := t.TempDir()
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadServicesConfig(path)
	if err == nil {
		t.Fatal("expected error for unknown extends parent")
	}
}

func TestLoadConfig_servicesEnabledFromMerge(t *testing.T) {
	defaults := sampleDefaultsYML + `
services:
  main-debug:
    enabled: false
`
	path := writeFullFixture(t, sampleDevboxYML, defaults, "", sampleServicesYML)
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
	defaults := sampleDefaultsYML + `
services:
  main-debug:
    enabled: false
`
	local := `
services:
  main-debug:
    enabled: true
`
	path := writeFullFixture(t, sampleDevboxYML, defaults, local, sampleServicesYML)
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
	path := writeFullFixture(t, sampleDevboxYML, sampleDefaultsYML, "", sampleServicesYML)
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
	// Without services.yml, cfg.Services should be nil (no error).
	path := writeLayeredFixture(t, sampleDevboxYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Services != nil {
		t.Errorf("Services should be nil when services.yml absent, got %v", cfg.Services)
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

func TestLoadDeployConfig_phasesPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
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

func TestLoadDeployConfig_stepWithCmd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
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

func TestLoadDeployConfig_stepWithCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
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

func TestLoadDeployConfig_stepWithWhen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
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

func TestLoadDeployConfig_strictDecodeStringWhen(t *testing.T) {
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
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Errorf("LoadDeployConfig expected error for string-form when:, got nil")
	} else if !strings.Contains(err.Error(), "when") {
		t.Errorf("error should mention 'when' field: %v", err)
	}
}

func TestLoadDeployConfig_invalidWhenType(t *testing.T) {
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
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Errorf("LoadDeployConfig expected error for invalid when type, got nil")
	} else if !strings.Contains(err.Error(), "when") {
		t.Errorf("error should mention 'when': %v", err)
	}
}

func TestLoadDeployConfig_strictDecodeUnknownField(t *testing.T) {
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
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Errorf("LoadDeployConfig expected error for unknown field, got nil")
	} else if !strings.Contains(err.Error(), "notafield") && !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error should mention 'notafield' or 'unknown': %v", err)
	}
}

func TestLoadDeployConfig_notFound(t *testing.T) {
	_, err := LoadDeployConfig(filepath.Join(t.TempDir(), "nonexistent.yml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}

func TestLoadDeployConfig_invalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte("{ invalid yaml ]["), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadDeployConfig(path)
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

func TestLoadDeployConfig_emptyPhases(t *testing.T) {
	yml := "phases: []\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}
	if len(cfg.Phases) != 0 {
		t.Errorf("Phases len = %d, want 0", len(cfg.Phases))
	}
}

func TestLoadDeployConfig_legacyRunAndCommandFieldsRejected(t *testing.T) {
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
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("LoadDeployConfig: expected strict-decode error for removed legacy fields 'run' and 'command', got nil")
	}
}

func TestLoadDeployConfig_checkBadTypeProducesWrappedError(t *testing.T) {
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
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("LoadDeployConfig: expected validation error for bad check type, got nil")
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

func TestLoadDeployConfig_stepNeitherCmdNorCommand(t *testing.T) {
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
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("LoadDeployConfig: expected error for step with neither cmd nor command, got nil")
	}
}

func TestLoadDeployConfig_stepWithServiceConfigsCopy(t *testing.T) {
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
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
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

func TestLoadDeployConfig_stepWithCommandAndWith(t *testing.T) {
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
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
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

func TestLoadDeployConfig_deployServicesPhase(t *testing.T) {
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
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}
	if !cfg.Phases[0].DeployServices {
		t.Error("expected DeployServices=true")
	}
}

func TestLoadDeployConfig_deployServicesWithStepsError(t *testing.T) {
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
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for deploy_services phase with steps")
	}
}

func TestLoadDeployConfig_deployServicesWithWhenError(t *testing.T) {
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
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for deploy_services phase with when, got nil")
	}
	if !strings.Contains(err.Error(), "does not support when") {
		t.Errorf("error should mention 'does not support when', got: %v", err)
	}
}

func TestLoadDeployConfig_phaseUIFieldRejected(t *testing.T) {
	// The ui: field was removed from DeployPhase. Strict decode rejects unknown fields.
	yml := `phases:
  - name: setup
    description: Setup phase
    ui: plain
    steps:
      - name: create-dirs
        run: mkdir -p services/main/src
        description: Create directories
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatalf("LoadDeployConfig expected error for unknown field ui, got nil")
	}
	if !strings.Contains(err.Error(), "ui") && !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("error should mention ui or unknown field: %v", err)
	}
}

func TestLoadDeployConfig_phaseUntrackedDefaultFalseSimple(t *testing.T) {
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
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}
	if cfg.Phases[0].Untracked {
		t.Error("Phases[0].Untracked = true, want false (default)")
	}
}

func TestLoadDeployConfig_phaseUntrackedField(t *testing.T) {
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
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
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

func TestLoadDeployConfig_phaseUntrackedDefaultFalse(t *testing.T) {
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
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
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
	deployDir := filepath.Join(dir, "devbox", "deploy")
	if err := os.MkdirAll(deployDir, 0755); err != nil {
		t.Fatal(err)
	}
	mainDeploy := `phases:
  - name: setup
    steps:
      - name: create-dirs
        type: shell
        cmd: mkdir -p services/main/src
`
	if err := os.WriteFile(filepath.Join(deployDir, "main.yml"), []byte(mainDeploy), 0644); err != nil {
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

func TestLoadConfig_exportsLoaded(t *testing.T) {
	defaultsWithExports := sampleDefaultsYML + `
exports:
  env:
    - name: APP_PORT
      from: runtime.ports.app
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
	if cfg.Exports.Env[0].From != "runtime.ports.app" {
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
      from: runtime.ports.app
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
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(sampleServicesWithCLIYML), 0644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
	services, err := LoadServicesConfig(path)
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
	}
	main := services["main"]
	if main.CLI.Mode != "auto" {
		t.Errorf("main.CLI.Mode = %q, want auto", main.CLI.Mode)
	}
}

func TestLoadServicesConfig_envField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(sampleServicesWithCLIYML), 0644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
	services, err := LoadServicesConfig(path)
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
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
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(sampleServicesWithCLIYML), 0644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
	services, err := LoadServicesConfig(path)
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
	}
	// main-debug extends main and has no CLI block of its own — inherits mode from parent
	debug := services["main-debug"]
	if debug.CLI.Mode != "auto" {
		t.Errorf("main-debug.CLI.Mode = %q, want auto (inherited from main)", debug.CLI.Mode)
	}
}

func TestLoadServicesConfig_extendsInheritsEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(sampleServicesWithCLIYML), 0644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
	services, err := LoadServicesConfig(path)
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
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
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(sampleServicesWithCLIYML), 0644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
	services, err := LoadServicesConfig(path)
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
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
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(sampleServicesWithDirsYML), 0644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
	services, err := LoadServicesConfig(path)
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
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
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(sampleServicesWithDirsYML), 0644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
	services, err := LoadServicesConfig(path)
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
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
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(sampleServicesWithDirsYML), 0644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
	services, err := LoadServicesConfig(path)
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
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
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(sampleServicesWithDirsYML), 0644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
	services, err := LoadServicesConfig(path)
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
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

// TestLoadDeployConfig_logDefaultEnabled verifies that omitting `log:` in
// deploy.yml defaults to logging enabled.
func TestLoadDeployConfig_logDefaultEnabled(t *testing.T) {
	path := writePipelineFixture(t, "deploy", "phases: []\n")
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}
	if !cfg.LogEnabled() {
		t.Errorf("deploy log should default to enabled")
	}
}

// TestLoadDeployConfig_logExplicitFalse verifies that `log: false` disables it.
func TestLoadDeployConfig_logExplicitFalse(t *testing.T) {
	path := writePipelineFixture(t, "deploy", "log: false\nphases: []\n")
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
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
	rawBin, ok := cfg.Raw["binaries"].(map[string]any)
	if !ok {
		t.Fatal("cfg.Raw[\"binaries\"] is not map[string]any")
	}
	if rawBin["docker"] != "podman" {
		t.Errorf("Raw[binaries][docker] = %v, want podman", rawBin["docker"])
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
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithCompose, "")
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
	// Create a defaults with all tools having compose files, adminer disabled.
	defaultsWithCompose := `
schema_version: "1"
tools:
  adminer:
    enabled: false
    container: adminer
    host: adminer.localhost
    port: 8080
    compose: compose/tools/adminer.yml
  mailpit:
    enabled: true
    container: mailpit
    host: mail.localhost
    port: 8025
    compose: compose/tools/mailpit.yml
  redis_insight:
    enabled: true
    container: redis_insight
    host: redis.localhost
    port: 5540
    compose: compose/tools/redis_insight.yml
runtime:
  use_https: false
  ports:
    app: 80
    db: 13306
    redis: 6379
  hosts:
    main: app.localhost
  spx:
    path: ""
compose:
  base: compose.yaml
`
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithCompose, "")
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
	defaultsWithCompose := minimalDefaultsYML + `
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
    enabled: true
    compose:
      - compose/services/main/base.yml
  worker:
    type: app
    container: app-worker
    mandatory: false
    dir: ./services/worker
    dir_internal: /workspace
    work_dir_internal: /workspace/src
    enabled: false
    compose:
      - compose/services/worker/base.yml
`
	path := writeFullFixture(t, sampleDevboxYML, defaultsWithCompose, "", servicesYML)
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
	path := writeFullFixture(t, sampleDevboxYML, defaultsWithBase, "", sampleServicesYML)
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
	// Use minimalDefaultsYML which has no compose section and no enabled tools with compose files.
	path := writeLayeredFixture(t, sampleDevboxYML, minimalDefaultsYML, "")
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
	path := writeFullFixture(t, sampleDevboxYML, defaultsWithCompose, "", serviceYML)
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
	defaultsWithElasticvue := `
schema_version: "1"
tools:
  elasticvue:
    enabled: true
    container: elasticvue
    host: elastic.localhost
    port: 9200
    compose: compose/tools/elasticvue.yml
  adminer:
    enabled: false
    container: adminer
    host: adminer.localhost
    port: 8080
runtime:
  use_https: false
  ports:
    app: 80
    db: 13306
  hosts:
    main: app.localhost
  spx:
    path: ""
compose:
  base: compose.yaml
`
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithElasticvue, "")
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
	defaultsUnsorted := `
schema_version: "1"
tools:
  zebra:
    enabled: true
    container: zebra
    host: zebra.localhost
    port: 8001
    compose: compose/tools/zebra.yml
  apple:
    enabled: true
    container: apple
    host: apple.localhost
    port: 8002
    compose: compose/tools/apple.yml
  mango:
    enabled: true
    container: mango
    host: mango.localhost
    port: 8003
    compose: compose/tools/mango.yml
runtime:
  use_https: false
  ports:
    app: 80
    db: 13306
  hosts:
    main: app.localhost
  spx:
    path: ""
compose:
  base: compose.yaml
`
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsUnsorted, "")
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
			svc:      ServiceConfig{IDE: ServiceIDEConfig{Enabled: ptr(true)}}, //nolint:modernize
			wantBool: true,
			wantExp:  true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{IDE: ServiceIDEConfig{Enabled: ptr(false)}}, //nolint:modernize
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
			svc:      ServiceConfig{IDE: ServiceIDEConfig{Enabled: ptr(true)}}, //nolint:modernize
			wantBool: true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{IDE: ServiceIDEConfig{Enabled: ptr(false)}}, //nolint:modernize
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
    ide:
      enabled: true
  child-override-template:
    type: app
    container: child-override-template
    mandatory: false
    extends: parent
    ide:
      template: child-tmpl
  child-override-both:
    type: app
    container: child-override-both
    mandatory: false
    extends: parent
    ide:
      enabled: true
      template: both-tmpl
`
	dir := t.TempDir()
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
	services, err := LoadServicesConfig(path)
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
	}

	// Parent has explicit false and template
	parent := services["parent"]
	if parent.IDE.Enabled == nil || *parent.IDE.Enabled != false {
		t.Errorf("parent IDE.Enabled should be false, got %v", parent.IDE.Enabled)
	}
	if parent.IDE.Template != "parent-tmpl" {
		t.Errorf("parent IDE.Template = %q, want parent-tmpl", parent.IDE.Template)
	}

	// Child inherits both parent's enabled and template
	childInh := services["child-inherit"]
	if childInh.IDE.Enabled == nil || *childInh.IDE.Enabled != false {
		t.Errorf("child-inherit IDE.Enabled should inherit false from parent, got %v", childInh.IDE.Enabled)
	}
	if childInh.IDE.Template != "parent-tmpl" {
		t.Errorf("child-inherit IDE.Template should inherit parent-tmpl, got %q", childInh.IDE.Template)
	}

	// Child overrides enabled but inherits template
	childOvrE := services["child-override-enabled"]
	if childOvrE.IDE.Enabled == nil || *childOvrE.IDE.Enabled != true {
		t.Errorf("child-override-enabled IDE.Enabled should be true, got %v", childOvrE.IDE.Enabled)
	}
	if childOvrE.IDE.Template != "parent-tmpl" {
		t.Errorf("child-override-enabled IDE.Template should inherit parent-tmpl, got %q", childOvrE.IDE.Template)
	}

	// Child overrides template but inherits enabled
	childOvrT := services["child-override-template"]
	if childOvrT.IDE.Enabled == nil || *childOvrT.IDE.Enabled != false {
		t.Errorf("child-override-template IDE.Enabled should inherit false from parent, got %v", childOvrT.IDE.Enabled)
	}
	if childOvrT.IDE.Template != "child-tmpl" {
		t.Errorf("child-override-template IDE.Template = %q, want child-tmpl", childOvrT.IDE.Template)
	}

	// Child overrides both
	childOvrB := services["child-override-both"]
	if childOvrB.IDE.Enabled == nil || *childOvrB.IDE.Enabled != true {
		t.Errorf("child-override-both IDE.Enabled should be true, got %v", childOvrB.IDE.Enabled)
	}
	if childOvrB.IDE.Template != "both-tmpl" {
		t.Errorf("child-override-both IDE.Template = %q, want both-tmpl", childOvrB.IDE.Template)
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
			svc:      ServiceConfig{AI: ServiceAIConfig{Enabled: ptr(true)}}, //nolint:modernize
			wantBool: true,
			wantExp:  true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{AI: ServiceAIConfig{Enabled: ptr(false)}}, //nolint:modernize
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
			svc:      ServiceConfig{AI: ServiceAIConfig{Enabled: ptr(true)}}, //nolint:modernize
			wantBool: true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{AI: ServiceAIConfig{Enabled: ptr(false)}}, //nolint:modernize
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
    ai:
      enabled: true
  child-override-template:
    type: app
    container: child-override-template
    mandatory: false
    extends: parent
    ai:
      template: child-tmpl
  child-override-both:
    type: app
    container: child-override-both
    mandatory: false
    extends: parent
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
	path := filepath.Join(dir, "services.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
	services, err := LoadServicesConfig(path)
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
	}

	// Parent has explicit false and template
	parent := services["parent"]
	if parent.AI.Enabled == nil || *parent.AI.Enabled != false {
		t.Errorf("parent AI.Enabled should be false, got %v", parent.AI.Enabled)
	}
	if parent.AI.Template != "parent-tmpl" {
		t.Errorf("parent AI.Template = %q, want parent-tmpl", parent.AI.Template)
	}

	// Child inherits both parent's enabled and template
	childInh := services["child-inherit"]
	if childInh.AI.Enabled == nil || *childInh.AI.Enabled != false {
		t.Errorf("child-inherit AI.Enabled should inherit false from parent, got %v", childInh.AI.Enabled)
	}
	if childInh.AI.Template != "parent-tmpl" {
		t.Errorf("child-inherit AI.Template should inherit parent-tmpl, got %q", childInh.AI.Template)
	}

	// Child overrides enabled but inherits template
	childOvrE := services["child-override-enabled"]
	if childOvrE.AI.Enabled == nil || *childOvrE.AI.Enabled != true {
		t.Errorf("child-override-enabled AI.Enabled should be true, got %v", childOvrE.AI.Enabled)
	}
	if childOvrE.AI.Template != "parent-tmpl" {
		t.Errorf("child-override-enabled AI.Template should inherit parent-tmpl, got %q", childOvrE.AI.Template)
	}

	// Child overrides template but inherits enabled
	childOvrT := services["child-override-template"]
	if childOvrT.AI.Enabled == nil || *childOvrT.AI.Enabled != false {
		t.Errorf("child-override-template AI.Enabled should inherit false from parent, got %v", childOvrT.AI.Enabled)
	}
	if childOvrT.AI.Template != "child-tmpl" {
		t.Errorf("child-override-template AI.Template = %q, want child-tmpl", childOvrT.AI.Template)
	}

	// Child overrides both
	childOvrB := services["child-override-both"]
	if childOvrB.AI.Enabled == nil || *childOvrB.AI.Enabled != true {
		t.Errorf("child-override-both AI.Enabled should be true, got %v", childOvrB.AI.Enabled)
	}
	if childOvrB.AI.Template != "both-tmpl" {
		t.Errorf("child-override-both AI.Template = %q, want both-tmpl", childOvrB.AI.Template)
	}

	// Grandchild (multi-hop): inherits from child-inherit
	grandchild := services["grandchild-multi-hop"]
	if grandchild.AI.Enabled == nil || *grandchild.AI.Enabled != false {
		t.Errorf("grandchild-multi-hop AI.Enabled should inherit false from parent chain, got %v", grandchild.AI.Enabled)
	}
	if grandchild.AI.Template != "parent-tmpl" {
		t.Errorf("grandchild-multi-hop AI.Template should inherit parent-tmpl, got %q", grandchild.AI.Template)
	}
}

// ptr is a helper to create a pointer to a value.
// nolint: unused,modernize // used in test table initialization
func ptr[T any](v T) *T {
	return &v
}
