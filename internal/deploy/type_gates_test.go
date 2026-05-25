package deploy_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
	"devbox-cli/internal/usercommands"
)

// writeMixedTypeFixture builds a project with one app and one infra service.
// Caller may add a deploy file for either service. Used by the type-gate
// tests to verify deploy enumeration filters by IsApp().
func writeMixedTypeFixture(t *testing.T, deployFor string) string {
	t.Helper()
	dir := t.TempDir()
	devboxPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(devboxPath, []byte(`schema_version: "1"
project:
  name: laravel
  prefix: devbox
`), 0o644); err != nil {
		t.Fatal(err)
	}
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"main": "type: app\ncontainer: app-main\nmandatory: true\ndir: ./services/main\n",
		"db":   "type: infra\ncontainer: db\nmandatory: true\n",
	} {
		svcDir := filepath.Join(devboxDir, "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if deployFor != "" {
		deployDir := filepath.Join(devboxDir, "deploy")
		if err := os.MkdirAll(deployDir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `phases:
  - name: setup
    steps:
      - name: noop
        type: shell
        cmd: 'true'
`
		if err := os.WriteFile(filepath.Join(deployDir, deployFor+".yml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return devboxPath
}

// TestResolveServicePlan_rejectsNonApp confirms the --service single-target
// path rejects an infra service with a typed sentinel.
func TestResolveServicePlan_rejectsNonApp(t *testing.T) {
	devboxPath := writeMixedTypeFixture(t, "")
	cfg, err := config.LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	_, err = deploy.ResolveServicePlan(cfg, usercommands.NewEmptyRegistry(), "db")
	if err == nil {
		t.Fatal("expected ErrDeployTargetNotApp")
	}
	if !errors.Is(err, config.ErrDeployTargetNotApp) {
		t.Fatalf("err = %v, want wraps ErrDeployTargetNotApp", err)
	}
}

// TestResolveServicesPlan_filtersOutNonApp confirms ResolveServicesPlan never
// enumerates tool/infra services, even if Enabled. Without any app deploy
// files we expect a nil plan (no infra/tool deploy ever runs).
func TestResolveServicesPlan_filtersOutNonApp(t *testing.T) {
	devboxPath := writeMixedTypeFixture(t, "")
	cfg, err := config.LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	plan, err := deploy.ResolveServicesPlan(cfg, usercommands.NewEmptyRegistry())
	if err != nil {
		t.Fatalf("ResolveServicesPlan: %v", err)
	}
	if plan != nil {
		t.Fatalf("expected nil plan, got %d steps", len(plan))
	}
}

// TestLoadConfig_rejectsDependsOnTool confirms a service whose depends_on
// names a tool fails LoadConfig.
func TestLoadConfig_rejectsDependsOnTool(t *testing.T) {
	dir := t.TempDir()
	devboxPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(devboxPath, []byte(`schema_version: "1"
project:
  name: laravel
  prefix: devbox
`), 0o644); err != nil {
		t.Fatal(err)
	}
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"main":    "type: app\ncontainer: app-main\nmandatory: true\ndir: ./services/main\ndepends_on:\n  - adminer\n",
		"adminer": "type: tool\ncontainer: adminer\n",
	} {
		svcDir := filepath.Join(devboxDir, "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := config.LoadConfig(devboxPath)
	if err == nil {
		t.Fatal("expected ErrDependsOnTool")
	}
	if !errors.Is(err, config.ErrDependsOnTool) {
		t.Fatalf("err = %v, want wraps ErrDependsOnTool", err)
	}
}
