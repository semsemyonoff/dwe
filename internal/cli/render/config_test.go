package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/shared/generatedstore"
)

// setupConfigPack writes a config template pack (manifest + sources) under
// <projectRoot>/workspace/templates/config/<packName>/.
func setupConfigPack(t *testing.T, projectRoot, packName string, files map[string]string) {
	t.Helper()
	packDir := filepath.Join(projectRoot, "workspace", "templates", "config", packName)
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create config pack dir: %v", err)
	}
	for rel, content := range files {
		path := filepath.Join(packDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create pack source dir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write pack source %s: %v", rel, err)
		}
	}
}

// TestNewConfigCmd_rendersExpectedFiles renders a single service's config pack
// and verifies the file is written under the hub dir with ${...} resolved.
func TestNewConfigCmd_rendersExpectedFiles(t *testing.T) {
	projectRoot := t.TempDir()

	cfgYAML := `schema_version: "2"
project:
  name: test-project
services:
  main:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  main:
    type: app
    dir: services/main
    container: test-main
`)
	setupConfigPack(t, projectRoot, "default", map[string]string{
		"manifest.yml": "render:\n  - from: env.tmpl\n    to: src/.env\n",
		"env.tmpl":     "APP=${project.name}\n",
	})
	if err := os.MkdirAll(filepath.Join(projectRoot, "services", "main"), 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newConfigCmd(flags)
	if err := cmd.RunE(cmd, []string{"main"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	got := mustReadFile(t, filepath.Join(projectRoot, "services", "main", "src", ".env"))
	if got != "APP=test-project\n" {
		t.Errorf("rendered content = %q, want %q", got, "APP=test-project\n")
	}
}

// TestNewConfigCmd_replaysGeneratedFromStore verifies that a render replays a
// ${generated.<name>} value already present in .dwe/generated.yml.
func TestNewConfigCmd_replaysGeneratedFromStore(t *testing.T) {
	projectRoot := t.TempDir()

	cfgYAML := `schema_version: "2"
project:
  name: test-project
services:
  main:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  main:
    type: app
    dir: services/main
    container: test-main
`)
	setupConfigPack(t, projectRoot, "default", map[string]string{
		"manifest.yml": "render:\n  - from: env.tmpl\n    to: src/.env\n",
		"env.tmpl":     "APP_KEY=${generated.app_key}\n",
	})
	if err := os.MkdirAll(filepath.Join(projectRoot, "services", "main"), 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}

	// Pre-seed the store with a harvested value.
	store := generatedstore.New()
	store.SetIfAbsent("main", "app_key", "base64:secret==")
	if err := generatedstore.Save(filepath.Join(projectRoot, generatedstore.DefaultRelPath), store); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newConfigCmd(flags)
	if err := cmd.RunE(cmd, []string{"main"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	got := mustReadFile(t, filepath.Join(projectRoot, "services", "main", "src", ".env"))
	if got != "APP_KEY=base64:secret==\n" {
		t.Errorf("rendered content = %q, want replayed app_key", got)
	}
}

// TestNewConfigCmd_allServicesSelection renders every enabled service that
// resolves a config pack when no explicit service argument is given.
func TestNewConfigCmd_allServicesSelection(t *testing.T) {
	projectRoot := t.TempDir()

	cfgYAML := `schema_version: "2"
project:
  name: test-project
services:
  alpha:
    enabled: true
  zebra:
    enabled: true
  off-svc:
    enabled: false
`
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  alpha:
    type: app
    dir: services/alpha
    container: test-alpha
  zebra:
    type: app
    dir: services/zebra
    container: test-zebra
  off-svc:
    type: app
    dir: services/off
    container: test-off
`)
	// A shared "default" pack resolves for every service via the implicit chain.
	setupConfigPack(t, projectRoot, "default", map[string]string{
		"manifest.yml": "render:\n  - from: env.tmpl\n    to: src/.env\n",
		"env.tmpl":     "ok\n",
	})
	for _, d := range []string{"services/alpha", "services/zebra", "services/off"} {
		if err := os.MkdirAll(filepath.Join(projectRoot, d), 0o755); err != nil {
			t.Fatalf("create dir %s: %v", d, err)
		}
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newConfigCmd(flags)
	if err := cmd.RunE(cmd, []string{}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	for _, name := range []string{"alpha", "zebra"} {
		if _, err := os.Stat(filepath.Join(projectRoot, "services", name, "src", ".env")); err != nil {
			t.Errorf("expected rendered .env for %s: %v", name, err)
		}
	}
	// Disabled service must be skipped (DeployOrder excludes it).
	if _, err := os.Stat(filepath.Join(projectRoot, "services", "off", "src", ".env")); err == nil {
		t.Error("expected no rendered .env for disabled service")
	}
}

// TestNewConfigCmd_harvestPopulatesStore verifies --harvest reads on-disk values
// into the store WITHOUT rendering any template.
func TestNewConfigCmd_harvestPopulatesStore(t *testing.T) {
	projectRoot := t.TempDir()

	cfgYAML := `schema_version: "2"
project:
  name: test-project
services:
  main:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  main:
    type: app
    dir: services/main
    container: test-main
    generated:
      app_key:
        file: configs/.env
        pattern: '^APP_KEY=(.*)$'
`)
	// A config pack exists, but --harvest must NOT render it.
	setupConfigPack(t, projectRoot, "default", map[string]string{
		"manifest.yml": "render:\n  - from: env.tmpl\n    to: src/.env\n",
		"env.tmpl":     "rendered-should-not-appear\n",
	})

	// Write the on-disk file holding the already-committed value to harvest.
	confDir := filepath.Join(projectRoot, "services", "main", "configs")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatalf("create configs dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, ".env"), []byte("APP_KEY=base64:harvested==\n"), 0o644); err != nil {
		t.Fatalf("write source .env: %v", err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newConfigCmd(flags)
	cmd.SetArgs([]string{"main", "--harvest"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// The store must now hold the harvested value.
	store, err := generatedstore.Load(filepath.Join(projectRoot, generatedstore.DefaultRelPath))
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	if got := store.Get("main", "app_key"); got != "base64:harvested==" {
		t.Errorf("store app_key = %q, want harvested value", got)
	}

	// --harvest must NOT have rendered the config pack.
	if _, err := os.Stat(filepath.Join(projectRoot, "services", "main", "src", ".env")); err == nil {
		t.Error("--harvest must not render config templates")
	}
}

// TestNewConfigCmd_explicitServiceNotFound errors on an unknown service.
func TestNewConfigCmd_explicitServiceNotFound(t *testing.T) {
	projectRoot := t.TempDir()

	cfgYAML := `schema_version: "2"
project:
  name: test-project
services:
  main:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  main:
    type: app
    dir: services/main
    container: test-main
`)

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newConfigCmd(flags)
	err := cmd.RunE(cmd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

// TestNewConfigCmd_explicitServiceDisabled errors on a disabled service.
func TestNewConfigCmd_explicitServiceDisabled(t *testing.T) {
	projectRoot := t.TempDir()

	cfgYAML := `schema_version: "2"
project:
  name: test-project
services:
  main:
    enabled: false
`
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  main:
    type: app
    dir: services/main
    container: test-main
`)

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newConfigCmd(flags)
	err := cmd.RunE(cmd, []string{"main"})
	if err == nil {
		t.Fatal("expected error for disabled service")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error should mention 'disabled': %v", err)
	}
}

// TestNewConfigCmd_explicitMissingPackSkips verifies an explicit service with no
// config pack is a non-error skip (config rendering is opt-in).
func TestNewConfigCmd_explicitMissingPackSkips(t *testing.T) {
	projectRoot := t.TempDir()

	cfgYAML := `schema_version: "2"
project:
  name: test-project
services:
  main:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	setupServicesConfig(t, projectRoot, `
services:
  main:
    type: app
    dir: services/main
    container: test-main
`)
	if err := os.MkdirAll(filepath.Join(projectRoot, "services", "main"), 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "workspace.yml")}
	cmd := newConfigCmd(flags)
	if err := cmd.RunE(cmd, []string{"main"}); err != nil {
		t.Fatalf("expected missing pack to warn and skip, got error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "services", "main", "src", ".env")); err == nil {
		t.Error("expected no rendered file when no pack resolves")
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
