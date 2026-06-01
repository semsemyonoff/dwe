package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// setupGitPack writes a git template pack at devbox/templates/git/<packName>/.
func setupGitPack(t *testing.T, projectRoot, packName string, files map[string]string) {
	t.Helper()
	packDir := filepath.Join(projectRoot, "workspace", "templates", "git", packName)
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}
	for rel, content := range files {
		path := filepath.Join(packDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// mkGitDir creates an empty <hub>/src/.git directory so git rendering proceeds.
func mkGitDir(t *testing.T, hubDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(hubDir, "src", ".git", "hooks"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
}

// makeGitCfg returns a DweConfig configured for git rendering tests.
func makeGitCfg(name string) *config.DweConfig {
	return &config.DweConfig{
		Project: config.ProjectConfig{Name: "test", Prefix: "devbox"},
		Services: map[string]config.ServiceConfig{
			name: {
				Type:      "app",
				Enabled:   true,
				Dir:       filepath.Join("services", name),
				Container: "c",
			},
		},
		Raw: map[string]any{},
	}
}

// TestRenderGitHooksForService_implicitPackMissing verifies that a missing implicit
// pack emits a warning and returns no error.
func TestRenderGitHooksForService_implicitPackMissing(t *testing.T) {
	projectRoot := t.TempDir()
	cfg := makeGitCfg("api")
	svc := cfg.Services["api"]

	hubDir := filepath.Join(projectRoot, "services", "api")
	mkGitDir(t, hubDir)

	var buf strings.Builder
	w := render.NewWriter(&buf)
	err := renderGitHooksForService(projectRoot, "api", svc, cfg, w)
	if err != nil {
		t.Fatalf("expected implicit missing pack to warn and skip, got error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "skipped") || !strings.Contains(output, "no template pack found") {
		t.Errorf("expected warning about missing template pack in output, got: %q", output)
	}
}

func TestNewGitCmd_happyPath(t *testing.T) {
	projectRoot := t.TempDir()

	devboxYAML := `schema_version: "2"
project:
  name: test-project
services:
  api:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(devboxYAML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: test-api
`)

	setupGitPack(t, projectRoot, "default", map[string]string{
		"manifest.yml":    "render:\n  - from: pre-commit.tmpl\n    to: pre-commit\n",
		"pre-commit.tmpl": "#!/bin/sh\necho {{ .Service }}\n",
	})

	hubDir := filepath.Join(projectRoot, "services", "api")
	mkGitDir(t, hubDir)

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newGitCmd(flags)
	if err := cmd.RunE(cmd, []string{"api"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	hookPath := filepath.Join(hubDir, "src", ".git", "hooks", "pre-commit")
	content, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	if !strings.Contains(string(content), "echo api") {
		t.Errorf("hook content unexpected: %s", content)
	}
	fi, err := os.Stat(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("hook mode = %o want 0755", fi.Mode().Perm())
	}
}

func TestNewGitCmd_unknownService(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(`schema_version: "2"
project:
  name: p
services:
  api:
    enabled: true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: c
`)

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newGitCmd(flags)
	err := cmd.RunE(cmd, []string{"missing"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestNewGitCmd_noGitDirSkippedNonError(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(`schema_version: "2"
project:
  name: p
services:
  api:
    enabled: true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: c
`)
	setupGitPack(t, projectRoot, "default", map[string]string{
		"manifest.yml":    "render:\n  - from: pre-commit.tmpl\n    to: pre-commit\n",
		"pre-commit.tmpl": "#!/bin/sh\n",
	})
	// Service hub exists but src/.git does not.
	if err := os.MkdirAll(filepath.Join(projectRoot, "services", "api"), 0o755); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newGitCmd(flags)
	if err := cmd.RunE(cmd, []string{"api"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	// No hook should be written.
	if _, err := os.Stat(filepath.Join(projectRoot, "services", "api", "src", ".git", "hooks", "pre-commit")); err == nil {
		t.Fatal("expected no hook to be written")
	}
}

func TestNewGitCmd_dirEscapesProjectRoot(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(`schema_version: "2"
project:
  name: p
services:
  api:
    enabled: true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: ../outside
    container: c
`)
	setupGitPack(t, projectRoot, "default", map[string]string{
		"manifest.yml":    "render:\n  - from: pre-commit.tmpl\n    to: pre-commit\n",
		"pre-commit.tmpl": "#!/bin/sh\n",
	})

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newGitCmd(flags)
	err := cmd.RunE(cmd, []string{"api"})
	if err == nil {
		t.Fatal("expected error for escaping dir")
	}
	if !strings.Contains(err.Error(), "escape") {
		t.Errorf("expected escape error, got %v", err)
	}
	// Ensure nothing was written outside the project root.
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(projectRoot), "outside")); statErr == nil {
		t.Fatal("MkdirAll should not have created the outside dir")
	}
}

func TestNewGitCmd_manifestMissingFromFile(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(`schema_version: "2"
project:
  name: p
services:
  api:
    enabled: true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: c
`)
	// Manifest references a `from` file that does not exist.
	setupGitPack(t, projectRoot, "default", map[string]string{
		"manifest.yml": "render:\n  - from: missing.tmpl\n    to: pre-commit\n",
	})
	hubDir := filepath.Join(projectRoot, "services", "api")
	mkGitDir(t, hubDir)

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newGitCmd(flags)
	err := cmd.RunE(cmd, []string{"api"})
	if err == nil {
		t.Fatal("expected validation error")
	}
	// Hook directory should be empty.
	entries, _ := os.ReadDir(filepath.Join(hubDir, "src", ".git", "hooks"))
	if len(entries) != 0 {
		t.Errorf("expected no hooks written, got %d entries", len(entries))
	}
}

func TestNewGitCmd_implicitPackMissing(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(`schema_version: "2"
project:
  name: p
services:
  api:
    enabled: true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: c
`)
	hubDir := filepath.Join(projectRoot, "services", "api")
	mkGitDir(t, hubDir)

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newGitCmd(flags)
	err := cmd.RunE(cmd, []string{"api"})
	if err != nil {
		t.Fatalf("expected implicit missing pack to warn and skip, got error: %v", err)
	}
}

func TestNewGitCmd_explicitPackMissing(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(`schema_version: "2"
project:
  name: p
services:
  api:
    enabled: true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: c
    render:
      git:
        template: nonexistent
`)
	hubDir := filepath.Join(projectRoot, "services", "api")
	mkGitDir(t, hubDir)

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newGitCmd(flags)
	err := cmd.RunE(cmd, []string{"api"})
	if err == nil {
		t.Fatal("expected error for explicit missing template pack")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

func TestNewGitCmd_explicitPackMissingWithoutGitDir(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(`schema_version: "2"
project:
  name: p
services:
  api:
    enabled: true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: c
    render:
      git:
        template: nonexistent
`)
	hubDir := filepath.Join(projectRoot, "services", "api")
	// Do NOT create .git directory
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newGitCmd(flags)
	err := cmd.RunE(cmd, []string{"api"})
	if err == nil {
		t.Fatal("expected error for explicit missing template pack")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found' (explicit error wins over .git-missing): %v", err)
	}
}

func TestNewGitCmd_noArgIteratesEnabledAppServices(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(`schema_version: "2"
project:
  name: p
services:
  app1:
    enabled: true
  app2:
    enabled: true
  dbsvc:
    enabled: true
  off:
    enabled: false
`), 0o644); err != nil {
		t.Fatal(err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  app1:
    type: app
    dir: services/app1
    container: c1
  app2:
    type: app
    dir: services/app2
    container: c2
  dbsvc:
    type: infra
    container: c3
  off:
    type: app
    dir: services/off
    container: c4
`)
	setupGitPack(t, projectRoot, "default", map[string]string{
		"manifest.yml":    "render:\n  - from: pre-commit.tmpl\n    to: pre-commit\n",
		"pre-commit.tmpl": "#!/bin/sh\n# {{ .Service }}\n",
	})
	for _, d := range []string{"services/app1", "services/app2", "services/db", "services/off"} {
		mkGitDir(t, filepath.Join(projectRoot, d))
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newGitCmd(flags)
	if err := cmd.RunE(cmd, []string{}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	// app1, app2 → hook present
	for _, svc := range []string{"app1", "app2"} {
		if _, err := os.Stat(filepath.Join(projectRoot, "services", svc, "src", ".git", "hooks", "pre-commit")); err != nil {
			t.Errorf("expected hook for %s: %v", svc, err)
		}
	}
	// db (non-app default-disabled) and off (project-disabled) → no hook
	for _, svc := range []string{"db", "off"} {
		if _, err := os.Stat(filepath.Join(projectRoot, "services", svc, "src", ".git", "hooks", "pre-commit")); err == nil {
			t.Errorf("expected no hook for %s", svc)
		}
	}
}

func TestNewGitCmd_completionReturnsAllServices(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(`schema_version: "2"
project:
  name: p
services:
  api:
    enabled: true
  worker:
    enabled: false
`), 0o644); err != nil {
		t.Fatal(err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: c
  worker:
    type: app
    dir: services/worker
    container: w
`)

	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(projectRoot, "workspace.yml"),
		Root:       projectRoot,
	}
	cmd := newGitCmd(flags)
	names, _ := cmd.ValidArgsFunction(cmd, nil, "")
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	if !got["api"] || !got["worker"] {
		t.Errorf("expected both services in completion, got %v", names)
	}
}
