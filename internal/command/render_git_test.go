package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupGitPack writes a git template pack at devbox/templates/git/<packName>/.
func setupGitPack(t *testing.T, projectRoot, packName string, files map[string]string) {
	t.Helper()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "git", packName)
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

func TestNewRenderGitCmd_happyPath(t *testing.T) {
	projectRoot := t.TempDir()

	devboxYAML := `schema_version: "2"
project:
  name: test-project
services:
  api:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(devboxYAML), 0o644); err != nil {
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

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderGitCmd(flags)
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

func TestNewRenderGitCmd_unknownService(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(`schema_version: "2"
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

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderGitCmd(flags)
	err := cmd.RunE(cmd, []string{"missing"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestNewRenderGitCmd_noGitDirSkippedNonError(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(`schema_version: "2"
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

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderGitCmd(flags)
	if err := cmd.RunE(cmd, []string{"api"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	// No hook should be written.
	if _, err := os.Stat(filepath.Join(projectRoot, "services", "api", "src", ".git", "hooks", "pre-commit")); err == nil {
		t.Fatal("expected no hook to be written")
	}
}

func TestNewRenderGitCmd_dirEscapesProjectRoot(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(`schema_version: "2"
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

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderGitCmd(flags)
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

func TestNewRenderGitCmd_manifestMissingFromFile(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(`schema_version: "2"
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

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderGitCmd(flags)
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

func TestNewRenderGitCmd_noArgIteratesEnabledAppServices(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(`schema_version: "2"
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
    type: db
    dir: services/db
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

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderGitCmd(flags)
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

func TestNewRenderGitCmd_completionReturnsAllServices(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(`schema_version: "2"
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

	flags := &rootFlags{
		configPath:  filepath.Join(projectRoot, "devbox.yml"),
		projectRoot: projectRoot,
	}
	cmd := newRenderGitCmd(flags)
	names, _ := cmd.ValidArgsFunction(cmd, nil, "")
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	if !got["api"] || !got["worker"] {
		t.Errorf("expected both services in completion, got %v", names)
	}
}
