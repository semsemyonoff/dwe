package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// sampleDevboxYML reflects the lean devbox.yml (project identity only).
const sampleDevboxYML = `
schema_version: "1"
project:
  name: laravel
  prefix: devbox
`

// sampleDefaultsYML mirrors devbox/defaults.yml.
const sampleDefaultsYML = `
schema_version: "1"
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
	if !cfg.Tools.RedisInsight.Enabled {
		t.Error("tools.redis_insight.enabled should be true (from defaults)")
	}
	if cfg.Runtime.Ports.Mailpit != 8025 {
		t.Errorf("runtime.ports.mailpit = %d, want 8025 (from defaults)", cfg.Runtime.Ports.Mailpit)
	}
	if cfg.Runtime.Hosts.Main != "app.localhost" {
		t.Errorf("runtime.hosts.main = %q (from defaults)", cfg.Runtime.Hosts.Main)
	}

	// Overridden by local.yml
	if cfg.Runtime.Ports.App != 8080 {
		t.Errorf("runtime.ports.app = %d, want 8080 (from user)", cfg.Runtime.Ports.App)
	}
	if !cfg.Tools.Adminer.Enabled {
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
		Adminer: ToolConfig{Enabled: true},
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
  overlays:
    adminer: compose/tools/adminer.yml
    redis_insight: compose/tools/redis_insight.yml
    mailpit: compose/tools/mailpit.yml
    debug: compose/services/main/debug.yml
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

func TestLoadConfig_composeOverlaysLoaded(t *testing.T) {
	defaultsWithCompose := sampleDefaultsYML + `
compose:
  base: compose.yaml
  overlays:
    adminer: compose/tools/adminer.yml
    redis_insight: compose/tools/redis_insight.yml
    mailpit: compose/tools/mailpit.yml
    debug: compose/services/main/debug.yml
`
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithCompose, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	want := map[string]string{
		"adminer":       "compose/tools/adminer.yml",
		"redis_insight": "compose/tools/redis_insight.yml",
		"mailpit":       "compose/tools/mailpit.yml",
		"debug":         "compose/services/main/debug.yml",
	}
	for key, wantPath := range want {
		got, ok := cfg.Compose.Overlays[key]
		if !ok {
			t.Errorf("Compose.Overlays[%q] missing", key)
			continue
		}
		if got != wantPath {
			t.Errorf("Compose.Overlays[%q] = %q, want %q", key, got, wantPath)
		}
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
	if len(cfg.Compose.Overlays) != 0 {
		t.Errorf("Compose.Overlays = %v, want empty when section absent", cfg.Compose.Overlays)
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
        run: mkdir -p services/main/{src,configs}
        description: Create service hub directories
      - name: copy-configs
        run: devbox deploy config main
        description: Copy template configs
        when: "{{.Runtime.UseHTTPS}}"
  - name: start
    description: Start containers
    steps:
      - name: up
        command: up
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
	if step.Run == "" {
		t.Error("step.Run should be set for cmd: steps")
	}
	if step.Command != "" {
		t.Error("step.Command should be empty for cmd: steps")
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
	if step.Command == "" {
		t.Error("step.Command should be set for command: steps")
	}
	if step.Run != "" {
		t.Error("step.Run should be empty for command: steps")
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
	if step.When != "{{.Runtime.UseHTTPS}}" {
		t.Errorf("step.When = %q, want {{.Runtime.UseHTTPS}}", step.When)
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

func TestLoadDeployConfig_stepBothCmdAndCommand(t *testing.T) {
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
		t.Fatal("LoadDeployConfig: expected error for step with both cmd and command, got nil")
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
        service_configs_copy: main
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
	if step.Builtin != "service_configs_copy" {
		t.Errorf("Builtin = %q, want service_configs_copy", step.Builtin)
	}
	if step.ServiceConfigsCopy != "" {
		t.Errorf("ServiceConfigsCopy should be empty after normalization, got %q", step.ServiceConfigsCopy)
	}
	if step.Mode != "" {
		t.Errorf("Mode should be empty after normalization, got %q", step.Mode)
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
        command: services.main.migrate
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
	if step.Command != "services.main.migrate" {
		t.Errorf("Command = %q, want services.main.migrate", step.Command)
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

func TestLoadDeployConfig_phaseUIFieldIgnored(t *testing.T) {
	// The ui: field was removed from DeployPhase. YAML with ui: must still load
	// without error (backward compatibility — unknown fields are silently ignored).
	yml := `phases:
  - name: setup
    description: Setup phase
    ui: plain
    steps:
      - name: create-dirs
        run: mkdir -p services/main/src
        description: Create directories
  - name: post-deploy
    description: Post-deploy phase
    steps:
      - name: info
        devbox: "info"
        description: Show info
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
}

func TestLoadDeployConfig_phaseUntrackedDefaultFalseSimple(t *testing.T) {
	// Phases without untracked: default to false.
	yml := `phases:
  - name: start
    description: Start phase
    steps:
      - name: up
        devbox: "docker up"
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
        run: mkdir -p services/main/src
  - name: post-deploy
    description: Post-deploy phase
    ui: plain
    untracked: true
    steps:
      - name: info
        devbox: "info"
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
        devbox: "docker up"
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
        run: mkdir -p services/main/src
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
          devbox: "docker up"
        - name: wait
          devbox: "docker wait"
stop:
  final_message: "Project is stopped. Have a nice day!"
  phases:
    - name: stop
      steps:
        - name: down
          devbox: "docker down"
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
          devbox: "docker up"
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
          devbox: "docker up"
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
          devbox: "docker up"
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
          devbox: "docker up"
stop:
  phases:
    - name: stop
      steps:
        - name: down
          devbox: "docker down"
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
          run: echo hello
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
          devbox: "docker up"
stop:
  final_message: "Custom stop message"
  phases:
    - name: stop
      steps:
        - name: down
          devbox: "docker down"
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
          devbox: "docker up"
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
          devbox: "docker up"
stop:
  phases:
    - name: stop
      steps:
        - name: down
          devbox: "docker down"
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
          devbox: "docker up"
stop:
  log: true
  phases:
    - name: stop
      steps:
        - name: down
          devbox: "docker down"
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
