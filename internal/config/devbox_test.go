package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// sampleDevboxYML reflects the new lean devbox.yml (structure only, no runtime/tools/state).
const sampleDevboxYML = `
schema_version: "1"
project:
  name: laravel
  prefix: devbox
services:
  main:
    type: app
    dir: ./services/main
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
	dir := t.TempDir()

	devboxPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(devboxPath, []byte(devbox), 0644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	if defaults != "" {
		devboxDir := filepath.Join(dir, "devbox")
		if err := os.MkdirAll(devboxDir, 0755); err != nil {
			t.Fatalf("mkdir devbox/: %v", err)
		}
		if err := os.WriteFile(filepath.Join(devboxDir, "defaults.yml"), []byte(defaults), 0644); err != nil {
			t.Fatalf("write defaults.yml: %v", err)
		}
	}

	if user != "" {
		devboxDir := filepath.Join(dir, "devbox")
		if err := os.MkdirAll(devboxDir, 0755); err != nil {
			t.Fatalf("mkdir devbox/: %v", err)
		}
		if err := os.WriteFile(filepath.Join(devboxDir, "local.yml"), []byte(user), 0644); err != nil {
			t.Fatalf("write local.yml: %v", err)
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
services:
  main:
    type: app
    dir: ./services/main
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
	if _, ok := cfg.Services["main"]; !ok {
		t.Error("services.main not found")
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
	if _, ok := cfg.Services["main"]; !ok {
		t.Error("services.main not found")
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

// --- ServiceConfig extended fields ---

func TestLoadConfig_serviceContainerAndDirInternal(t *testing.T) {
	defaultsWithService := sampleDefaultsYML + `
services:
  main:
    type: app
    dir: ./services/main
    container: app-main
    dir_internal: /var/www/app
    configs:
      - src: configs/app/main/.env
        dest: .env
        mode: replace
`
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithService, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	svc, ok := cfg.Services["main"]
	if !ok {
		t.Fatal("services.main not found")
	}
	if svc.Container != "app-main" {
		t.Errorf("Container = %q, want app-main", svc.Container)
	}
	if svc.DirInternal != "/var/www/app" {
		t.Errorf("DirInternal = %q, want /var/www/app", svc.DirInternal)
	}
}

func TestLoadConfig_serviceConfigsLoaded(t *testing.T) {
	defaultsWithService := sampleDefaultsYML + `
services:
  main:
    type: app
    dir: ./services/main
    container: app-main
    dir_internal: /var/www/app
    configs:
      - src: configs/app/main/.env
        dest: .env
        mode: replace
`
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithService, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	svc, ok := cfg.Services["main"]
	if !ok {
		t.Fatal("services.main not found")
	}
	if len(svc.Configs) != 1 {
		t.Fatalf("Configs len = %d, want 1", len(svc.Configs))
	}
	cf := svc.Configs[0]
	if cf.Src != "configs/app/main/.env" {
		t.Errorf("Configs[0].Src = %q, want configs/app/main/.env", cf.Src)
	}
	if cf.Dest != ".env" {
		t.Errorf("Configs[0].Dest = %q, want .env", cf.Dest)
	}
	if cf.Mode != "replace" {
		t.Errorf("Configs[0].Mode = %q, want replace", cf.Mode)
	}
}

func TestLoadConfig_serviceLocalOverride(t *testing.T) {
	defaultsWithService := sampleDefaultsYML + `
services:
  main:
    type: app
    dir: ./services/main
    container: app-main
    dir_internal: /var/www/app
    configs:
      - src: configs/app/main/.env
        dest: .env
        mode: replace
`
	// Local override changes container name
	localYML := `
services:
  main:
    container: app-main-custom
`
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithService, localYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	svc, ok := cfg.Services["main"]
	if !ok {
		t.Fatal("services.main not found")
	}
	if svc.Container != "app-main-custom" {
		t.Errorf("Container = %q, want app-main-custom (overridden by local)", svc.Container)
	}
	// DirInternal should be preserved from defaults
	if svc.DirInternal != "/var/www/app" {
		t.Errorf("DirInternal = %q, want /var/www/app (from defaults)", svc.DirInternal)
	}
}

func TestLoadConfig_serviceInstallerImage(t *testing.T) {
	defaultsWithService := sampleDefaultsYML + `
services:
  main:
    type: app
    dir: ./services/main
    container: app-main
    dir_internal: /var/www/app
    installer_image: composer:2
    configs:
      - src: configs/app/main/.env
        dest: .env
        mode: replace
`
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithService, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	svc, ok := cfg.Services["main"]
	if !ok {
		t.Fatal("services.main not found")
	}
	if svc.InstallerImage != "composer:2" {
		t.Errorf("InstallerImage = %q, want composer:2", svc.InstallerImage)
	}
}

func TestLoadConfig_serviceInstallerImageOverride(t *testing.T) {
	defaultsWithService := sampleDefaultsYML + `
services:
  main:
    type: app
    dir: ./services/main
    container: app-main
    dir_internal: /var/www/app
    installer_image: composer:2
`
	localYML := `
services:
  main:
    installer_image: composer:2.7
`
	path := writeLayeredFixture(t, sampleDevboxYML, defaultsWithService, localYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	svc, ok := cfg.Services["main"]
	if !ok {
		t.Fatal("services.main not found")
	}
	if svc.InstallerImage != "composer:2.7" {
		t.Errorf("InstallerImage = %q, want composer:2.7 (overridden by local)", svc.InstallerImage)
	}
}

func TestLoadConfig_serviceNoExtendedFields(t *testing.T) {
	// When service only has type/dir (no container/configs), fields are zero values.
	path := writeLayeredFixture(t, sampleDevboxYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	svc, ok := cfg.Services["main"]
	if !ok {
		t.Fatal("services.main not found")
	}
	if svc.Container != "" {
		t.Errorf("Container = %q, want empty when not set", svc.Container)
	}
	if svc.DirInternal != "" {
		t.Errorf("DirInternal = %q, want empty when not set", svc.DirInternal)
	}
	if len(svc.Configs) != 0 {
		t.Errorf("Configs = %v, want empty when not set", svc.Configs)
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
        cmd: mkdir -p services/main/{src,configs}
        description: Create service hub directories
      - name: copy-configs
        cmd: devbox deploy config main
        description: Copy template configs
        when: "{{.Runtime.Debug.Enabled}}"
  - name: start
    description: Start containers
    steps:
      - name: up
        make: up
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
		t.Error("step.Cmd should be set for cmd: steps")
	}
	if step.Make != "" {
		t.Error("step.Make should be empty for cmd: steps")
	}
}

func TestLoadDeployConfig_stepWithMake(t *testing.T) {
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
	if step.Make == "" {
		t.Error("step.Make should be set for make: steps")
	}
	if step.Cmd != "" {
		t.Error("step.Cmd should be empty for make: steps")
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
	if step.When != "{{.Runtime.Debug.Enabled}}" {
		t.Errorf("step.When = %q, want {{.Runtime.Debug.Enabled}}", step.When)
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

func TestLoadDeployConfig_stepBothCmdAndMake(t *testing.T) {
	yml := `phases:
  - name: setup
    steps:
      - name: bad-step
        cmd: echo hi
        make: up
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("LoadDeployConfig: expected error for step with both cmd and make, got nil")
	}
}

func TestLoadDeployConfig_stepNeitherCmdNorMake(t *testing.T) {
	yml := `phases:
  - name: setup
    steps:
      - name: empty-step
        description: no cmd or make
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("LoadDeployConfig: expected error for step with neither cmd nor make, got nil")
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
