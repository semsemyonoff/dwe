package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleDockerYML = `
project_name: "${project.prefix}-${project.name}"

args:
  global: ["--ansi", "always", "--progress", "tty"]
  up: ["-d", "--remove-orphans"]
  down: []
  stop: []
  restart: []
  logs: ["-f"]
  ps: []
  exec: []
  run: ["--rm"]

env:
  auto_generate: true
  commands: [up, run, exec]
`

// writeDockerFixture creates devbox/docker.yml and optionally devbox/docker.local.yml
// under a temp directory. Returns the base dir path.
func writeDockerFixture(t *testing.T, docker, local string) string {
	t.Helper()
	dir := t.TempDir()
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("mkdir devbox/: %v", err)
	}
	if err := os.WriteFile(filepath.Join(devboxDir, "docker.yml"), []byte(docker), 0644); err != nil {
		t.Fatalf("write docker.yml: %v", err)
	}
	if local != "" {
		if err := os.WriteFile(filepath.Join(devboxDir, "docker.local.yml"), []byte(local), 0644); err != nil {
			t.Fatalf("write docker.local.yml: %v", err)
		}
	}
	return dir
}

func TestLoadDockerConfig_Basic(t *testing.T) {
	baseDir := writeDockerFixture(t, sampleDockerYML, "")
	cfg := &DevboxConfig{
		Raw: map[string]any{
			"project": map[string]any{
				"name":   "laravel",
				"prefix": "devbox",
			},
		},
	}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}

	if dcfg.ProjectName != "devbox-laravel" {
		t.Errorf("ProjectName = %q, want %q", dcfg.ProjectName, "devbox-laravel")
	}

	// Global args
	wantGlobal := []string{"--ansi", "always", "--progress", "tty"}
	if len(dcfg.Args.Global) != len(wantGlobal) {
		t.Fatalf("Global args len = %d, want %d", len(dcfg.Args.Global), len(wantGlobal))
	}
	for i, v := range wantGlobal {
		if dcfg.Args.Global[i] != v {
			t.Errorf("Global[%d] = %q, want %q", i, dcfg.Args.Global[i], v)
		}
	}

	// Up args
	wantUp := []string{"-d", "--remove-orphans"}
	if len(dcfg.Args.Up) != len(wantUp) {
		t.Fatalf("Up args len = %d, want %d", len(dcfg.Args.Up), len(wantUp))
	}
	for i, v := range wantUp {
		if dcfg.Args.Up[i] != v {
			t.Errorf("Up[%d] = %q, want %q", i, dcfg.Args.Up[i], v)
		}
	}

	// Logs args
	if len(dcfg.Args.Logs) != 1 || dcfg.Args.Logs[0] != "-f" {
		t.Errorf("Logs args = %v, want [-f]", dcfg.Args.Logs)
	}

	// Run args
	if len(dcfg.Args.Run) != 1 || dcfg.Args.Run[0] != "--rm" {
		t.Errorf("Run args = %v, want [--rm]", dcfg.Args.Run)
	}

	// Env config
	if !dcfg.Env.AutoGenerate {
		t.Error("Env.AutoGenerate = false, want true")
	}
	wantCmds := []string{"up", "run", "exec"}
	if len(dcfg.Env.Commands) != len(wantCmds) {
		t.Fatalf("Env.Commands len = %d, want %d", len(dcfg.Env.Commands), len(wantCmds))
	}
	for i, v := range wantCmds {
		if dcfg.Env.Commands[i] != v {
			t.Errorf("Env.Commands[%d] = %q, want %q", i, dcfg.Env.Commands[i], v)
		}
	}
}

func TestLoadDockerConfig_LocalOverride(t *testing.T) {
	localYML := `
args:
  global: ["--ansi", "always"]
  logs: ["-f", "--tail", "100"]
env:
  auto_generate: false
`
	baseDir := writeDockerFixture(t, sampleDockerYML, localYML)
	cfg := &DevboxConfig{
		Raw: map[string]any{
			"project": map[string]any{
				"name":   "laravel",
				"prefix": "devbox",
			},
		},
	}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}

	// Global args overridden (scalar replacement via deepMerge on arrays)
	wantGlobal := []string{"--ansi", "always"}
	if len(dcfg.Args.Global) != len(wantGlobal) {
		t.Fatalf("Global args len = %d, want %d", len(dcfg.Args.Global), len(wantGlobal))
	}
	for i, v := range wantGlobal {
		if dcfg.Args.Global[i] != v {
			t.Errorf("Global[%d] = %q, want %q", i, dcfg.Args.Global[i], v)
		}
	}

	// Logs overridden
	wantLogs := []string{"-f", "--tail", "100"}
	if len(dcfg.Args.Logs) != len(wantLogs) {
		t.Fatalf("Logs args len = %d, want %d", len(dcfg.Args.Logs), len(wantLogs))
	}

	// Up args NOT overridden — should keep base values
	wantUp := []string{"-d", "--remove-orphans"}
	if len(dcfg.Args.Up) != len(wantUp) {
		t.Fatalf("Up args len = %d, want %d", len(dcfg.Args.Up), len(wantUp))
	}

	// Env overridden
	if dcfg.Env.AutoGenerate {
		t.Error("Env.AutoGenerate = true, want false")
	}
}

func TestLoadDockerConfig_ProjectNameResolution(t *testing.T) {
	yml := `project_name: "${project.prefix}-${project.name}"
args: {}
env: {}
`
	baseDir := writeDockerFixture(t, yml, "")
	cfg := &DevboxConfig{
		Raw: map[string]any{
			"project": map[string]any{
				"name":   "myapp",
				"prefix": "dev",
			},
		},
	}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}
	if dcfg.ProjectName != "dev-myapp" {
		t.Errorf("ProjectName = %q, want %q", dcfg.ProjectName, "dev-myapp")
	}
}

func TestLoadDockerConfig_MissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &DevboxConfig{Raw: map[string]any{}}
	_, err := LoadDockerConfig(dir, cfg)
	if err == nil {
		t.Fatal("expected error for missing docker.yml")
	}
}

func TestLoadDockerConfig_NoLocalFile(t *testing.T) {
	// Should succeed with just docker.yml and no local override.
	yml := `project_name: "test"
args: {}
env: {}
`
	baseDir := writeDockerFixture(t, yml, "")
	cfg := &DevboxConfig{Raw: map[string]any{}}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}
	if dcfg.ProjectName != "test" {
		t.Errorf("ProjectName = %q, want %q", dcfg.ProjectName, "test")
	}
}

func TestDockerEnvConfig_ShouldGenerateEnv(t *testing.T) {
	env := &DockerEnvConfig{
		AutoGenerate: true,
		Commands:     []string{"up", "run", "exec"},
	}

	if !env.ShouldGenerateEnv("up") {
		t.Error("ShouldGenerateEnv(up) = false, want true")
	}
	if !env.ShouldGenerateEnv("exec") {
		t.Error("ShouldGenerateEnv(exec) = false, want true")
	}
	if env.ShouldGenerateEnv("down") {
		t.Error("ShouldGenerateEnv(down) = true, want false")
	}
	if env.ShouldGenerateEnv("stop") {
		t.Error("ShouldGenerateEnv(stop) = true, want false")
	}

	// Disabled auto_generate
	envDisabled := &DockerEnvConfig{
		AutoGenerate: false,
		Commands:     []string{"up"},
	}
	if envDisabled.ShouldGenerateEnv("up") {
		t.Error("ShouldGenerateEnv(up) with disabled = true, want false")
	}
}

func TestResolveVarTemplate(t *testing.T) {
	raw := map[string]any{
		"project": map[string]any{
			"name":   "laravel",
			"prefix": "devbox",
		},
	}

	tests := []struct {
		input string
		want  string
	}{
		{"${project.prefix}-${project.name}", "devbox-laravel"},
		{"plain-string", "plain-string"},
		{"${project.name}", "laravel"},
		{"prefix-${project.prefix}", "prefix-devbox"},
	}

	for _, tt := range tests {
		got, err := resolveVarTemplate(tt.input, raw)
		if err != nil {
			t.Errorf("resolveVarTemplate(%q): %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("resolveVarTemplate(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestResolveVarTemplate_Errors(t *testing.T) {
	raw := map[string]any{}

	// Unresolved path
	_, err := resolveVarTemplate("${missing.path}", raw)
	if err == nil {
		t.Error("expected error for unresolved path")
	}

	// Unclosed bracket
	_, err = resolveVarTemplate("${unclosed", raw)
	if err == nil {
		t.Error("expected error for unclosed ${")
	}
}

func TestLoadDockerConfig_PullAndBuildArgs(t *testing.T) {
	yml := `
project_name: "test"
args:
  global: []
  up: []
  pull: ["--policy", "always"]
  build: ["--progress", "plain"]
env: {}
`
	baseDir := writeDockerFixture(t, yml, "")
	cfg := &DevboxConfig{Raw: map[string]any{}}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}

	// Pull args
	wantPull := []string{"--policy", "always"}
	if len(dcfg.Args.Pull) != len(wantPull) {
		t.Fatalf("Pull args len = %d, want %d", len(dcfg.Args.Pull), len(wantPull))
	}
	for i, v := range wantPull {
		if dcfg.Args.Pull[i] != v {
			t.Errorf("Pull[%d] = %q, want %q", i, dcfg.Args.Pull[i], v)
		}
	}

	// Build args
	wantBuild := []string{"--progress", "plain"}
	if len(dcfg.Args.Build) != len(wantBuild) {
		t.Fatalf("Build args len = %d, want %d", len(dcfg.Args.Build), len(wantBuild))
	}
	for i, v := range wantBuild {
		if dcfg.Args.Build[i] != v {
			t.Errorf("Build[%d] = %q, want %q", i, dcfg.Args.Build[i], v)
		}
	}
}

func TestLoadDockerConfig_PullAndBuildLocalOverride(t *testing.T) {
	baseYML := `
project_name: "test"
args:
  global: []
  up: []
  pull: ["--policy", "always"]
  build: ["--progress", "plain"]
env: {}
`
	localYML := `
args:
  pull: ["--policy", "missing"]
  build: []
`
	baseDir := writeDockerFixture(t, baseYML, localYML)
	cfg := &DevboxConfig{Raw: map[string]any{}}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}

	// Pull overridden
	wantPull := []string{"--policy", "missing"}
	if len(dcfg.Args.Pull) != len(wantPull) {
		t.Fatalf("Pull args len = %d, want %d", len(dcfg.Args.Pull), len(wantPull))
	}
	for i, v := range wantPull {
		if dcfg.Args.Pull[i] != v {
			t.Errorf("Pull[%d] = %q, want %q", i, dcfg.Args.Pull[i], v)
		}
	}

	// Build overridden to empty
	if len(dcfg.Args.Build) != 0 {
		t.Errorf("Build args len = %d, want 0", len(dcfg.Args.Build))
	}

	// Non-overridden fields from base should survive the merge.
	if len(dcfg.Args.Global) != 0 {
		t.Errorf("Global args = %v, want [] (base value should survive)", dcfg.Args.Global)
	}
	wantUp := []string{}
	if len(dcfg.Args.Up) != len(wantUp) {
		t.Errorf("Up args = %v, want %v (base value should survive)", dcfg.Args.Up, wantUp)
	}
}
