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
`

// writeDockerFixture creates devbox/docker.yml and optionally devbox/docker.local.yml
// under a temp directory. Returns the base dir path.
func writeDockerFixture(t *testing.T, docker, local string) string {
	t.Helper()
	dir := t.TempDir()
	devboxDir := filepath.Join(dir, "workspace")
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
	cfg := &DweConfig{
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
}

func TestLoadDockerConfig_LocalOverride(t *testing.T) {
	localYML := `
args:
  global: ["--ansi", "always"]
  logs: ["-f", "--tail", "100"]
`
	baseDir := writeDockerFixture(t, sampleDockerYML, localYML)
	cfg := &DweConfig{
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
}

func TestLoadDockerConfig_ProjectNameResolution(t *testing.T) {
	yml := `project_name: "${project.prefix}-${project.name}"
args: {}
`
	baseDir := writeDockerFixture(t, yml, "")
	cfg := &DweConfig{
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
	cfg := &DweConfig{Raw: map[string]any{}}
	_, err := LoadDockerConfig(dir, cfg)
	if err == nil {
		t.Fatal("expected error for missing docker.yml")
	}
}

func TestLoadDockerConfig_NoLocalFile(t *testing.T) {
	// Should succeed with just docker.yml and no local override.
	yml := `project_name: "test"
args: {}
`
	baseDir := writeDockerFixture(t, yml, "")
	cfg := &DweConfig{Raw: map[string]any{}}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}
	if dcfg.ProjectName != "test" {
		t.Errorf("ProjectName = %q, want %q", dcfg.ProjectName, "test")
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
`
	baseDir := writeDockerFixture(t, yml, "")
	cfg := &DweConfig{Raw: map[string]any{}}

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
`
	localYML := `
args:
  pull: ["--policy", "missing"]
  build: []
`
	baseDir := writeDockerFixture(t, baseYML, localYML)
	cfg := &DweConfig{Raw: map[string]any{}}

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

// Tests for per-key defaults behavior (Task 8)

func TestLoadDockerConfig_DefaultsAppliedWhenAbsent(t *testing.T) {
	// When args keys are entirely absent, defaults should be applied.
	yml := `
project_name: "test"
args: {}
`
	baseDir := writeDockerFixture(t, yml, "")
	cfg := &DweConfig{Raw: map[string]any{}}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}

	// Verify defaults were applied
	if !slicesEqual(dcfg.Args.Up, []string{"-d", "--remove-orphans"}) {
		t.Errorf("Up args = %v, want [-d, --remove-orphans]", dcfg.Args.Up)
	}
	if !slicesEqual(dcfg.Args.Logs, []string{"-f"}) {
		t.Errorf("Logs args = %v, want [-f]", dcfg.Args.Logs)
	}
	if !slicesEqual(dcfg.Args.Run, []string{"--rm"}) {
		t.Errorf("Run args = %v, want [--rm]", dcfg.Args.Run)
	}
	if !slicesEqual(dcfg.Args.Down, []string{"--remove-orphans"}) {
		t.Errorf("Down args = %v, want [--remove-orphans]", dcfg.Args.Down)
	}

	// Other args should remain nil/empty
	if len(dcfg.Args.Global) != 0 {
		t.Errorf("Global args = %v, want empty", dcfg.Args.Global)
	}
	if len(dcfg.Args.Stop) != 0 {
		t.Errorf("Stop args = %v, want empty", dcfg.Args.Stop)
	}
}

func TestLoadDockerConfig_ExplicitEmptyOptsOutOfDefault(t *testing.T) {
	// When args keys are explicitly set to [], defaults should NOT be applied (opt-out).
	yml := `
project_name: "test"
args:
  up: []
  logs: []
  run: []
  down: []
`
	baseDir := writeDockerFixture(t, yml, "")
	cfg := &DweConfig{Raw: map[string]any{}}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}

	// Verify explicit empty [] is preserved (no default applied)
	if len(dcfg.Args.Up) != 0 {
		t.Errorf("Up args = %v, want empty (explicit [] opts out)", dcfg.Args.Up)
	}
	if len(dcfg.Args.Logs) != 0 {
		t.Errorf("Logs args = %v, want empty (explicit [] opts out)", dcfg.Args.Logs)
	}
	if len(dcfg.Args.Run) != 0 {
		t.Errorf("Run args = %v, want empty (explicit [] opts out)", dcfg.Args.Run)
	}
	if len(dcfg.Args.Down) != 0 {
		t.Errorf("Down args = %v, want empty (explicit [] opts out)", dcfg.Args.Down)
	}
}

func TestLoadDockerConfig_ExplicitValuesUsed(t *testing.T) {
	// When args keys have explicit values, they should be used (no merge with defaults).
	yml := `
project_name: "test"
args:
  up: ["--no-deps"]
  logs: ["-f", "--tail", "50"]
  run: ["-it"]
  down: []
`
	baseDir := writeDockerFixture(t, yml, "")
	cfg := &DweConfig{Raw: map[string]any{}}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}

	// Verify explicit values are used without merging with defaults
	if !slicesEqual(dcfg.Args.Up, []string{"--no-deps"}) {
		t.Errorf("Up args = %v, want [--no-deps]", dcfg.Args.Up)
	}
	if !slicesEqual(dcfg.Args.Logs, []string{"-f", "--tail", "50"}) {
		t.Errorf("Logs args = %v, want [-f, --tail, 50]", dcfg.Args.Logs)
	}
	if !slicesEqual(dcfg.Args.Run, []string{"-it"}) {
		t.Errorf("Run args = %v, want [-it]", dcfg.Args.Run)
	}
	if len(dcfg.Args.Down) != 0 {
		t.Errorf("Down args = %v, want empty", dcfg.Args.Down)
	}
}

func TestLoadDockerConfig_PartialDefaultsApplied(t *testing.T) {
	// Mix of absent (get defaults) and explicit (keep values) args.
	yml := `
project_name: "test"
args:
  up: ["--no-deps"]
  logs: []
`
	baseDir := writeDockerFixture(t, yml, "")
	cfg := &DweConfig{Raw: map[string]any{}}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}

	// up is explicit, logs is explicit-empty, run/down are absent
	if !slicesEqual(dcfg.Args.Up, []string{"--no-deps"}) {
		t.Errorf("Up args = %v, want [--no-deps] (explicit value)", dcfg.Args.Up)
	}
	if len(dcfg.Args.Logs) != 0 {
		t.Errorf("Logs args = %v, want empty (explicit [])", dcfg.Args.Logs)
	}
	if !slicesEqual(dcfg.Args.Run, []string{"--rm"}) {
		t.Errorf("Run args = %v, want [--rm] (default applied)", dcfg.Args.Run)
	}
	if !slicesEqual(dcfg.Args.Down, []string{"--remove-orphans"}) {
		t.Errorf("Down args = %v, want [--remove-orphans] (default applied)", dcfg.Args.Down)
	}
}

func TestLoadDockerConfig_LocalOverrideNoDefaults(t *testing.T) {
	// When local.yml overrides args explicitly, defaults for unset keys in local are still applied.
	baseYML := `
project_name: "test"
args:
  up: ["--no-deps"]
`
	localYML := `
args:
  logs: ["-f", "--tail", "100"]
`
	baseDir := writeDockerFixture(t, baseYML, localYML)
	cfg := &DweConfig{Raw: map[string]any{}}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}

	// Both base and local have explicit values — no defaults
	if !slicesEqual(dcfg.Args.Up, []string{"--no-deps"}) {
		t.Errorf("Up args = %v, want [--no-deps] (from base)", dcfg.Args.Up)
	}
	if !slicesEqual(dcfg.Args.Logs, []string{"-f", "--tail", "100"}) {
		t.Errorf("Logs args = %v, want [-f, --tail, 100] (from local)", dcfg.Args.Logs)
	}
	// run and down are absent from both → defaults applied
	if !slicesEqual(dcfg.Args.Run, []string{"--rm"}) {
		t.Errorf("Run args = %v, want [--rm] (default applied)", dcfg.Args.Run)
	}
	if !slicesEqual(dcfg.Args.Down, []string{"--remove-orphans"}) {
		t.Errorf("Down args = %v, want [--remove-orphans] (default applied)", dcfg.Args.Down)
	}
}
