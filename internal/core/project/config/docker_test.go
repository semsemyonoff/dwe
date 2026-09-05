package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// writeDockerFixture creates workspace/docker.yml and optionally workspace/docker.local.yml
// under a temp directory. Returns the base dir path.
func writeDockerFixture(t *testing.T, docker, local string) string {
	t.Helper()
	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("mkdir workspace/: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "docker.yml"), []byte(docker), 0644); err != nil {
		t.Fatalf("write docker.yml: %v", err)
	}
	if local != "" {
		if err := os.WriteFile(filepath.Join(workspaceDir, "docker.local.yml"), []byte(local), 0644); err != nil {
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
				"prefix": "dwe",
			},
		},
	}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}

	if dcfg.ProjectName != "dwe-laravel" {
		t.Errorf("ProjectName = %q, want %q", dcfg.ProjectName, "dwe-laravel")
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
				"prefix": "dwe",
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

func TestResolveComposeProjectName(t *testing.T) {
	cfg := func() *DweConfig {
		c := &DweConfig{
			Raw: map[string]any{
				"project": map[string]any{
					"name":   "tbm",
					"prefix": "dwe",
				},
			},
		}
		c.Project.Name = "tbm"
		c.Project.Prefix = "dwe"
		return c
	}

	t.Run("empty_baseDir_returns_FullName", func(t *testing.T) {
		got, err := ResolveComposeProjectName("", cfg())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "dwe-tbm" {
			t.Errorf("got %q, want %q", got, "dwe-tbm")
		}
	})

	t.Run("missing_docker_yml_falls_back_to_FullName", func(t *testing.T) {
		baseDir := t.TempDir()
		got, err := ResolveComposeProjectName(baseDir, cfg())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "dwe-tbm" {
			t.Errorf("got %q, want %q", got, "dwe-tbm")
		}
	})

	t.Run("docker_yml_with_template_takes_precedence", func(t *testing.T) {
		baseDir := writeDockerFixture(t, "project_name: \"${project.prefix}_${project.name}\"\n", "")
		got, err := ResolveComposeProjectName(baseDir, cfg())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "dwe_tbm" {
			t.Errorf("got %q, want %q", got, "dwe_tbm")
		}
	})

	t.Run("empty_project_name_falls_back_to_FullName", func(t *testing.T) {
		baseDir := writeDockerFixture(t, "args: {}\n", "")
		got, err := ResolveComposeProjectName(baseDir, cfg())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "dwe-tbm" {
			t.Errorf("got %q, want %q", got, "dwe-tbm")
		}
	})

	t.Run("unresolved_template_returns_error", func(t *testing.T) {
		baseDir := writeDockerFixture(t, "project_name: \"${project.naem}\"\n", "")
		_, err := ResolveComposeProjectName(baseDir, cfg())
		if err == nil {
			t.Fatal("expected error from unresolved ${...} reference, got nil")
		}
	})

	// The marker check must run BEFORE the lowercasing, or it can never fire:
	// normalizeComposeProjectName turns ENC[age:…] into enc[age:…], which
	// secrets.ContainsMarker does not match. Both precedence branches are
	// covered — docker.yml's project_name and the FullName() fallback.
	t.Run("undecrypted_marker_in_project_name_returns_error", func(t *testing.T) {
		baseDir := writeDockerFixture(t, "project_name: \"dwe-${vars.scope}\"\n", "")
		c := cfg()
		c.Raw["vars"] = map[string]any{"scope": "ENC[age:YWdlLWVuY3J5cHRpb24ub3JnL3Yx]"}
		_, err := ResolveComposeProjectName(baseDir, c)
		if err == nil {
			t.Fatal("expected an error for a project_name carrying an undecrypted marker")
		}
		if !strings.Contains(err.Error(), "undecrypted secret") {
			t.Errorf("error = %q, want it to name the undecrypted secret", err)
		}
	})

	t.Run("undecrypted_marker_in_project_prefix_returns_error", func(t *testing.T) {
		c := cfg()
		c.Project.Prefix = "ENC[age:YWdlLWVuY3J5cHRpb24ub3JnL3Yx]"
		_, err := ResolveComposeProjectName("", c)
		if err == nil {
			t.Fatal("expected an error for a FullName() carrying an undecrypted marker")
		}
		if !strings.Contains(err.Error(), "undecrypted secret") {
			t.Errorf("error = %q, want it to name the undecrypted secret", err)
		}
	})

	// Regression: malformed schema in unrelated fields (e.g. args.up given a
	// string instead of a sequence) must NOT prevent project_name resolution.
	// Per-service stop/restart/logs only need project_name and should not
	// fail because of a typo elsewhere in docker.yml.
	t.Run("malformed_unrelated_schema_does_not_break_resolution", func(t *testing.T) {
		yml := `project_name: "${project.prefix}_${project.name}"
args:
  up: "not-a-list-this-should-be-a-sequence"
`
		baseDir := writeDockerFixture(t, yml, "")
		got, err := ResolveComposeProjectName(baseDir, cfg())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "dwe_tbm" {
			t.Errorf("got %q, want %q", got, "dwe_tbm")
		}
	})

	// docker.local.yml override of project_name takes precedence and is also
	// only-project_name (no full schema decode), so a malformed args block in
	// local overlay does not break resolution either.
	t.Run("local_override_takes_precedence", func(t *testing.T) {
		base := "project_name: \"base-name\"\n"
		local := "project_name: \"override-name\"\n"
		baseDir := writeDockerFixture(t, base, local)
		got, err := ResolveComposeProjectName(baseDir, cfg())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "override-name" {
			t.Errorf("got %q, want %q", got, "override-name")
		}
	})

	// project.name with mixed case must be normalized: Docker Compose rejects
	// uppercase in project names.
	t.Run("mixed_case_project_name_normalized_to_lowercase", func(t *testing.T) {
		c := &DweConfig{}
		c.Project.Name = "cueBreaker"
		c.Project.Prefix = "dwe"
		got, err := ResolveComposeProjectName("", c)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "dwe-cuebreaker" {
			t.Errorf("got %q, want %q", got, "dwe-cuebreaker")
		}
	})

	// An already-lowercase docker.yml project_name is passed through unchanged.
	t.Run("already_lowercase_docker_yml_project_name_unchanged", func(t *testing.T) {
		baseDir := writeDockerFixture(t, "project_name: \"dwe-tbm\"\n", "")
		got, err := ResolveComposeProjectName(baseDir, cfg())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "dwe-tbm" {
			t.Errorf("got %q, want %q", got, "dwe-tbm")
		}
	})

	// A mixed-case explicit docker.yml project_name is normalized to lowercase.
	t.Run("mixed_case_docker_yml_project_name_normalized", func(t *testing.T) {
		baseDir := writeDockerFixture(t, "project_name: \"dwe-CueBreaker\"\n", "")
		got, err := ResolveComposeProjectName(baseDir, cfg())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "dwe-cuebreaker" {
			t.Errorf("got %q, want %q", got, "dwe-cuebreaker")
		}
	})
}

func TestComposeProjectName(t *testing.T) {
	cfg := &DweConfig{}
	cfg.Project.Name = "tbm"
	cfg.Project.Prefix = "dwe"

	mkCfg := func(name, prefix string) *DweConfig {
		c := &DweConfig{}
		c.Project.Name = name
		c.Project.Prefix = prefix
		return c
	}

	tests := []struct {
		name      string
		dockerCfg *DockerConfig
		cfg       *DweConfig
		want      string
	}{
		{"docker_yml_project_name_wins", &DockerConfig{ProjectName: "dwe_tbm"}, cfg, "dwe_tbm"},
		{"empty_project_name_falls_back_to_FullName", &DockerConfig{}, cfg, "dwe-tbm"},
		{"nil_dockerCfg_falls_back_to_FullName", nil, cfg, "dwe-tbm"},
		{"nil_cfg_returns_empty", &DockerConfig{}, nil, ""},
		{"both_nil_returns_empty", nil, nil, ""},
		{"docker_yml_project_name_normalized_to_lowercase", &DockerConfig{ProjectName: "Dwe_Tbm"}, cfg, "dwe_tbm"},
		{"fullname_normalized_to_lowercase", &DockerConfig{}, mkCfg("Tbm", "dwe"), "dwe-tbm"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ComposeProjectName(tc.dockerCfg, tc.cfg); got != tc.want {
				t.Errorf("ComposeProjectName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestComposeProjectNameCandidates(t *testing.T) {
	mk := func(name, prefix string) *DweConfig {
		c := &DweConfig{}
		c.Project.Name = name
		c.Project.Prefix = prefix
		return c
	}
	tests := []struct {
		name      string
		dockerCfg *DockerConfig
		cfg       *DweConfig
		want      []string
	}{
		{"override_differs_yields_both", &DockerConfig{ProjectName: "dwe_tbm"}, mk("tbm", "dwe"), []string{"dwe_tbm", "dwe-tbm"}},
		{"no_override_single", &DockerConfig{}, mk("tbm", "dwe"), []string{"dwe-tbm"}},
		{"override_equals_fullname_single", &DockerConfig{ProjectName: "dwe-tbm"}, mk("tbm", "dwe"), []string{"dwe-tbm"}},
		{"nil_dockerCfg_single", nil, mk("tbm", "dwe"), []string{"dwe-tbm"}},
		{"both_empty_returns_empty", &DockerConfig{}, &DweConfig{}, nil},
		// Case-only difference between the normalized primary and the
		// pre-normalization docker.yml project_name keeps BOTH candidates,
		// canonical (normalized) first — containers created under the old
		// spelling before normalization landed must still be found.
		{"case_only_docker_yml_override_kept_as_legacy_candidate", &DockerConfig{ProjectName: "Dwe-Tbm"}, mk("tbm", "dwe"), []string{"dwe-tbm", "Dwe-Tbm"}},
		// Case-only difference between the normalized primary and FullName()
		// (no docker.yml override) also keeps both candidates.
		{"case_only_fullname_kept_as_legacy_candidate", nil, mk("Tbm", "dwe"), []string{"dwe-tbm", "dwe-Tbm"}},
		// All three sources distinct: normalized primary, pre-normalization
		// docker.yml override, and legacy FullName all survive as separate
		// candidates, canonical first.
		{"all_three_candidates_distinct", &DockerConfig{ProjectName: "Dwe_Tbm"}, mk("tbm", "legacy"), []string{"dwe_tbm", "Dwe_Tbm", "legacy-tbm"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ComposeProjectNameCandidates(tc.dockerCfg, tc.cfg)
			if len(got) != len(tc.want) {
				t.Fatalf("candidates = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("candidates[%d] = %q, want %q (full: %v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func TestResolveVarTemplate(t *testing.T) {
	raw := map[string]any{
		"project": map[string]any{
			"name":   "laravel",
			"prefix": "dwe",
		},
	}

	tests := []struct {
		input string
		want  string
	}{
		{"${project.prefix}-${project.name}", "dwe-laravel"},
		{"plain-string", "plain-string"},
		{"${project.name}", "laravel"},
		{"prefix-${project.prefix}", "prefix-dwe"},
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

// Tests for per-key defaults behavior.

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

// TestLoadDockerConfigOrEmpty_MissingFile verifies that a missing docker.yml
// returns an empty DockerConfig and nil error (not an os.ErrNotExist error).
func TestLoadDockerConfigOrEmpty_MissingFile(t *testing.T) {
	dir := t.TempDir()
	// Create workspace dir but no docker.yml
	if err := os.MkdirAll(filepath.Join(dir, "workspace"), 0755); err != nil {
		t.Fatalf("mkdir workspace/: %v", err)
	}
	cfg := &DweConfig{Raw: map[string]any{}}

	dcfg, err := LoadDockerConfigOrEmpty(dir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfigOrEmpty missing file: want nil error, got %v", err)
	}
	if dcfg == nil {
		t.Fatal("LoadDockerConfigOrEmpty: want non-nil DockerConfig, got nil")
	}
	// Returned config should be zero-value (empty project name, no args)
	if dcfg.ProjectName != "" {
		t.Errorf("ProjectName = %q, want empty", dcfg.ProjectName)
	}
	if len(dcfg.Args.Up) != 0 {
		t.Errorf("Args.Up = %v, want empty", dcfg.Args.Up)
	}
}

// TestLoadDockerConfigOrEmpty_MalformedYAML verifies that a malformed docker.yml
// returns an error with the "loading docker config:" prefix.
func TestLoadDockerConfigOrEmpty_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("mkdir workspace/: %v", err)
	}
	// Write invalid YAML (tabs are not allowed as indentation in YAML)
	malformed := "project_name: ok\nargs:\n\tup: [bad"
	if err := os.WriteFile(filepath.Join(workspaceDir, "docker.yml"), []byte(malformed), 0644); err != nil {
		t.Fatalf("write docker.yml: %v", err)
	}
	cfg := &DweConfig{Raw: map[string]any{}}

	_, err := LoadDockerConfigOrEmpty(dir, cfg)
	if err == nil {
		t.Fatal("LoadDockerConfigOrEmpty malformed YAML: want error, got nil")
	}
	if !strings.Contains(err.Error(), "loading docker config:") {
		t.Errorf("error %q does not contain %q", err.Error(), "loading docker config:")
	}
}

// TestLoadDockerConfig_BuildPrepullBases verifies that build.prepull_bases: true
// in docker.yml loads as Build.PrepullBases == true.
func TestLoadDockerConfig_BuildPrepullBases(t *testing.T) {
	yml := `
build:
  prepull_bases: true
`
	baseDir := writeDockerFixture(t, yml, "")
	cfg := &DweConfig{Raw: map[string]any{}}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}
	if !dcfg.Build.PrepullBases {
		t.Errorf("Build.PrepullBases = false, want true")
	}
}

// TestLoadDockerConfig_BuildAbsentDefaultsFalse verifies that an absent build:
// block defaults Build.PrepullBases to the zero value (false).
func TestLoadDockerConfig_BuildAbsentDefaultsFalse(t *testing.T) {
	baseDir := writeDockerFixture(t, sampleDockerYML, "")
	cfg := &DweConfig{
		Raw: map[string]any{
			"project": map[string]any{
				"name":   "laravel",
				"prefix": "dwe",
			},
		},
	}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}
	if dcfg.Build.PrepullBases {
		t.Errorf("Build.PrepullBases = true, want false (absent build: block)")
	}
}

// TestLoadDockerConfig_BuildPrepullBasesLocalOverride verifies that
// docker.local.yml overriding build.prepull_bases wins over the base layer
// (deepMerge semantics) in both directions — a developer can both enable and
// disable the opt-in locally.
func TestLoadDockerConfig_BuildPrepullBasesLocalOverride(t *testing.T) {
	tests := []struct {
		name  string
		base  bool
		local bool
		want  bool
	}{
		{name: "local enables over disabled base", base: false, local: true, want: true},
		{name: "local disables over enabled base", base: true, local: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseYML := fmt.Sprintf("build:\n  prepull_bases: %t\n", tt.base)
			localYML := fmt.Sprintf("build:\n  prepull_bases: %t\n", tt.local)
			baseDir := writeDockerFixture(t, baseYML, localYML)
			cfg := &DweConfig{Raw: map[string]any{}}

			dcfg, err := LoadDockerConfig(baseDir, cfg)
			if err != nil {
				t.Fatalf("LoadDockerConfig: %v", err)
			}
			if dcfg.Build.PrepullBases != tt.want {
				t.Errorf("Build.PrepullBases = %t, want %t (local override should win)", dcfg.Build.PrepullBases, tt.want)
			}
		})
	}
}

// TestLoadDockerConfig_AllCommentBaseWithLocalOverride pins the nil-map guard in
// loadRawYAML. The init scaffold ships workspace/docker.yml fully commented, and
// envtest stamps a workspace/docker.local.yml into every test copy — so the base
// layer is an empty document merged into by a non-empty local one. A nil base map
// panicked ("assignment to entry in nil map") inside deepMerge, taking down
// `dwe validate`, `dwe test run` and `dwe test clean` on a fresh project.
func TestLoadDockerConfig_AllCommentBaseWithLocalOverride(t *testing.T) {
	baseDir := writeDockerFixture(t, "# only comments\n# nothing declared\n", "project_name: demo-t-smoke-abc\n")
	cfg := &DweConfig{Raw: map[string]any{}}

	dcfg, err := LoadDockerConfig(baseDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig: %v", err)
	}
	if dcfg.ProjectName != "demo-t-smoke-abc" {
		t.Errorf("ProjectName = %q, want %q", dcfg.ProjectName, "demo-t-smoke-abc")
	}

	name, err := readDockerProjectName(baseDir, cfg)
	if err != nil {
		t.Fatalf("readDockerProjectName: %v", err)
	}
	if name != "demo-t-smoke-abc" {
		t.Errorf("readDockerProjectName = %q, want %q", name, "demo-t-smoke-abc")
	}
}
