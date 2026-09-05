package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// countAssignments returns how many lines of out assign name.
//
// Line-prefix, never strings.Count on name+"=": "PROJECT=" is a substring of
// "COMPOSE_PROJECT_NAME=", so a substring count reports two hits for PROJECT
// on an output that carries one line of each.
func countAssignments(out, name string) int {
	n := 0
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, name+"=") {
			n++
		}
	}
	return n
}

// writeDockerYAML writes workspace/<file> under baseDir with a project_name.
func writeDockerYAML(t *testing.T, baseDir, file, projectName string) {
	t.Helper()
	dir := filepath.Join(baseDir, "workspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := "project_name: " + projectName + "\n"
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
}

// TestBuildContent_composeProjectName pins the fourth system variable against
// the single resolver (config.ResolveComposeProjectName): docker.yml and
// docker.local.yml precedence, the unconditional lowercasing, and the
// omit-when-empty rule that mirrors the compose wrapper omitting -p.
func TestBuildContent_composeProjectName(t *testing.T) {
	cases := []struct {
		name    string
		project config.ProjectConfig
		// dockerYML / dockerLocalYML are the project_name values written to
		// workspace/docker.yml and workspace/docker.local.yml; empty means the
		// file is not created at all.
		dockerYML      string
		dockerLocalYML string
		wantProject    string
		wantCompose    string // "" means: no COMPOSE_PROJECT_NAME line at all
	}{
		{
			name:        "no docker.yml falls back to prefix-name",
			project:     config.ProjectConfig{Name: "laravel", Prefix: "dwe"},
			wantProject: "laravel",
			wantCompose: "dwe-laravel",
		},
		{
			name:        "uppercase project name is lowercased for compose only",
			project:     config.ProjectConfig{Name: "Laravel", Prefix: "DWE"},
			wantProject: "Laravel",
			wantCompose: "dwe-laravel",
		},
		{
			name:        "docker.yml project_name wins over prefix-name",
			project:     config.ProjectConfig{Name: "laravel", Prefix: "dwe"},
			dockerYML:   "Custom_Scope",
			wantProject: "laravel",
			wantCompose: "custom_scope",
		},
		{
			name:           "docker.local.yml project_name wins over docker.yml",
			project:        config.ProjectConfig{Name: "laravel", Prefix: "dwe"},
			dockerYML:      "custom_scope",
			dockerLocalYML: "dwe-test-abc123",
			wantProject:    "laravel",
			wantCompose:    "dwe-test-abc123",
		},
		{
			name:        "prefix-only project still resolves to a non-empty name",
			project:     config.ProjectConfig{Prefix: "dwe"},
			wantProject: "",
			wantCompose: "dwe-",
		},
		{
			name:        "empty name and prefix omit the line",
			project:     config.ProjectConfig{},
			wantProject: "",
			wantCompose: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseDir := t.TempDir()
			if tc.dockerYML != "" {
				writeDockerYAML(t, baseDir, "docker.yml", tc.dockerYML)
			}
			if tc.dockerLocalYML != "" {
				writeDockerYAML(t, baseDir, "docker.local.yml", tc.dockerLocalYML)
			}
			cfg := &config.DweConfig{Project: tc.project, Raw: map[string]any{}}

			out, err := BuildContent(cfg, baseDir)
			if err != nil {
				t.Fatalf("BuildContent: %v", err)
			}
			if got := countAssignments(out, "PROJECT"); got != 1 {
				t.Errorf("PROJECT assignments = %d, want 1:\n%s", got, out)
			}
			if !strings.Contains(out, "PROJECT="+tc.wantProject+"\n") {
				t.Errorf("expected PROJECT=%q, got:\n%s", tc.wantProject, out)
			}
			if tc.wantCompose == "" {
				if n := countAssignments(out, "COMPOSE_PROJECT_NAME"); n != 0 {
					t.Errorf("expected no COMPOSE_PROJECT_NAME line, got:\n%s", out)
				}
				return
			}
			if !strings.Contains(out, "COMPOSE_PROJECT_NAME="+tc.wantCompose+"\n") {
				t.Errorf("expected COMPOSE_PROJECT_NAME=%q, got:\n%s", tc.wantCompose, out)
			}
		})
	}
}

// TestBuildContent_composeProjectNameIsLastSystemVariable pins the emission
// order: ReservedExportNames order is the .env line order, and the compose name
// closes the system block ahead of the user rules.
func TestBuildContent_composeProjectNameIsLastSystemVariable(t *testing.T) {
	cfg := &config.DweConfig{
		Project: config.ProjectConfig{Name: "laravel", Prefix: "dwe"},
		Exports: config.ExportsConfig{Env: []config.ExportRule{
			{Name: "MY_VAR", From: "vars.thing"},
		}},
		Raw: map[string]any{"vars": map[string]any{"thing": "value"}},
	}

	out, err := BuildContent(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("BuildContent: %v", err)
	}

	var assigned []string
	for line := range strings.SplitSeq(out, "\n") {
		if name, _, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			assigned = append(assigned, name)
		}
	}
	want := []string{"PROJECT", "UID", "GID", "COMPOSE_PROJECT_NAME", "MY_VAR"}
	if len(assigned) != len(want) {
		t.Fatalf("assignments = %v, want %v\n%s", assigned, want, out)
	}
	for i, name := range want {
		if assigned[i] != name {
			t.Fatalf("assignments = %v, want %v\n%s", assigned, want, out)
		}
	}
}

// TestBuildContent_composeProjectNameResolutionError pins the third rule: a
// project_name whose template cannot be resolved fails the render rather than
// writing a guessed name — a wrong COMPOSE_PROJECT_NAME in .env is exactly the
// split-brain the export exists to remove.
func TestBuildContent_composeProjectNameResolutionError(t *testing.T) {
	baseDir := t.TempDir()
	writeDockerYAML(t, baseDir, "docker.yml", "${project.naem}")
	cfg := &config.DweConfig{
		Project: config.ProjectConfig{Name: "laravel", Prefix: "dwe"},
		Raw:     map[string]any{"project": map[string]any{"name": "laravel"}},
	}

	out, err := BuildContent(cfg, baseDir)
	if err == nil {
		t.Fatalf("expected an error, got output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "compose project name") {
		t.Errorf("error %q does not mention the compose project name", err)
	}
	if out != "" {
		t.Errorf("expected no output on refusal, got:\n%s", out)
	}
}

// TestRegenerate_writesComposeProjectName covers the whole-file path a
// lifecycle command takes: Regenerate derives baseDir from the config path and
// the line lands in the written .env.
func TestRegenerate_writesComposeProjectName(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "workspace.yml")
	yml := "project:\n  name: Testproject\n  prefix: dwe\n"
	if err := os.WriteFile(configPath, []byte(yml), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	writeDockerYAML(t, dir, "docker.yml", "${project.prefix}_${project.name}")

	envPath, err := Regenerate(configPath)
	if err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if !strings.Contains(string(data), "COMPOSE_PROJECT_NAME=dwe_testproject\n") {
		t.Errorf("expected COMPOSE_PROJECT_NAME=dwe_testproject, got:\n%s", data)
	}
	if !strings.Contains(string(data), "PROJECT=Testproject\n") {
		t.Errorf("PROJECT must keep the verbatim project.name, got:\n%s", data)
	}
}
