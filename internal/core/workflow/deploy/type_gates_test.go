package deploy_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/workflow/deploy"
	"devbox-cli/internal/usercommands"
)

// writeMixedTypeFixture builds a project with one app and one infra service.
// The deployFor parameter names which service gets a deploy.yml (empty = none).
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
		"main": "type: app\ncontainer: app-main\nrequired: true\ndir: ./services/main\n",
		"db":   "type: infra\ncontainer: db\nrequired: true\n",
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
		body := `phases:
  - name: setup
    steps:
      - name: noop
        type: shell
        cmd: 'true'
`
		svcDeployPath := filepath.Join(devboxDir, "services", deployFor, "deploy.yml")
		if err := os.WriteFile(svcDeployPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return devboxPath
}

// writeThreeServiceFixture builds a project with app + tool + infra services.
// deployFor lists which services get a deploy.yml; mandatory lists which services
// are marked required: true (and thus always enabled). Services not in mandatory
// are disabled by default (no enabled: true in merged config).
func writeThreeServiceFixture(t *testing.T, deployFor []string, mandatory []string) string {
	t.Helper()
	dir := t.TempDir()
	devboxPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(devboxPath, []byte(`schema_version: "1"
project:
  name: mixed
  prefix: devbox
`), 0o644); err != nil {
		t.Fatal(err)
	}
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}

	mandatorySet := make(map[string]bool)
	for _, n := range mandatory {
		mandatorySet[n] = true
	}

	// Base service content per type.
	services := map[string]string{
		"app":   "type: app\ncontainer: app-main\ndir: ./services/app\n",
		"tool":  "type: tool\ncontainer: tool\n",
		"infra": "type: infra\ncontainer: infra\n",
	}
	for name, content := range services {
		svcDir := filepath.Join(devboxDir, "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		yml := content
		if mandatorySet[name] {
			yml += "required: true\n"
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(yml), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	deployBody := `phases:
  - name: setup
    steps:
      - name: noop
        type: shell
        cmd: 'true'
`
	for _, name := range deployFor {
		svcDeployPath := filepath.Join(devboxDir, "services", name, "deploy.yml")
		if err := os.WriteFile(svcDeployPath, []byte(deployBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return devboxPath
}

// TestResolveServicePlan_infraWithDeploy confirms that ResolveServicePlan accepts
// a non-app (infra) service when it has a deploy.yml.
func TestResolveServicePlan_infraWithDeploy(t *testing.T) {
	devboxPath := writeMixedTypeFixture(t, "db")
	cfg, err := config.LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	steps, err := deploy.ResolveServicePlan(cfg, usercommands.NewEmptyRegistry(), "db")
	if err != nil {
		t.Fatalf("ResolveServicePlan: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("expected at least one step")
	}
}

// TestResolveServicePlan_noDeployFile returns ErrServiceNoDeployFile when the
// named service exists but has no deploy.yml.
func TestResolveServicePlan_noDeployFile(t *testing.T) {
	devboxPath := writeMixedTypeFixture(t, "")
	cfg, err := config.LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	_, err = deploy.ResolveServicePlan(cfg, usercommands.NewEmptyRegistry(), "db")
	if err == nil {
		t.Fatal("expected ErrServiceNoDeployFile")
	}
	if !errors.Is(err, deploy.ErrServiceNoDeployFile) {
		t.Fatalf("err = %v, want wraps ErrServiceNoDeployFile", err)
	}
}

// TestResolveServicesPlan_enumeratesAllEnabledWithDeployFile confirms that full
// deploy enumerates every enabled service that has a deploy.yml, not just apps.
func TestResolveServicesPlan_enumeratesAllEnabledWithDeployFile(t *testing.T) {
	// app + infra both mandatory (enabled) + have deploy.yml; tool has neither.
	devboxPath := writeThreeServiceFixture(t, []string{"app", "infra"}, []string{"app", "infra"})
	cfg, err := config.LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	steps, err := deploy.ResolveServicesPlan(cfg, usercommands.NewEmptyRegistry())
	if err != nil {
		t.Fatalf("ResolveServicesPlan: %v", err)
	}
	found := make(map[string]bool)
	for _, s := range steps {
		found[s.Service] = true
	}
	if !found["app"] {
		t.Errorf("expected app steps in plan, found: %v", found)
	}
	if !found["infra"] {
		t.Errorf("expected infra steps in plan, found: %v", found)
	}
	if found["tool"] {
		t.Errorf("tool has no deploy.yml but appeared in plan: %v", found)
	}
}

// TestResolveServicesPlan_skipsDisabled confirms that non-mandatory services
// (disabled by default) with a deploy.yml are excluded from full deploy.
func TestResolveServicesPlan_skipsDisabled(t *testing.T) {
	// app is mandatory (enabled), infra is not mandatory (disabled); both have deploy.yml.
	devboxPath := writeThreeServiceFixture(t, []string{"app", "infra"}, []string{"app"})
	cfg, err := config.LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	steps, err := deploy.ResolveServicesPlan(cfg, usercommands.NewEmptyRegistry())
	if err != nil {
		t.Fatalf("ResolveServicesPlan: %v", err)
	}
	for _, s := range steps {
		if s.Service == "infra" {
			t.Errorf("disabled infra service appeared in plan steps")
		}
	}
}

// TestResolveServicePlan_toolWithDeployExplicitOverridesEnabled confirms that
// --service <tool> with deploy.yml works whether or not the service is enabled.
func TestResolveServicePlan_toolWithDeployExplicitOverridesEnabled(t *testing.T) {
	// tool is not mandatory (disabled), but has deploy.yml; --service should work.
	devboxPath := writeThreeServiceFixture(t, []string{"tool"}, nil)
	cfg, err := config.LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	steps, err := deploy.ResolveServicePlan(cfg, usercommands.NewEmptyRegistry(), "tool")
	if err != nil {
		t.Fatalf("ResolveServicePlan: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("expected steps for explicit --service tool")
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
		"main":    "type: app\ncontainer: app-main\nrequired: true\ndir: ./services/main\ndepends_on:\n  - adminer\n",
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
