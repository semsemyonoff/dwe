package deploy_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
)

// writeServiceDeployFixture creates the full file layout for service deploy tests.
func writeServiceDeployFixture(t *testing.T, orchestratorYML, serviceDeployYML string) string {
	t.Helper()
	dir := t.TempDir()
	devboxPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(devboxPath, []byte(`schema_version: "1"
project:
  name: laravel
  prefix: devbox
`), 0o644); err != nil {
		t.Fatal(err)
	}

	devboxDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}

	svcDir := filepath.Join(devboxDir, "services", "main")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte("type: app\ncontainer: app-main\nrequired: true\ndir: ./services/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if orchestratorYML != "" {
		if err := os.WriteFile(filepath.Join(devboxDir, "deploy.yml"), []byte(orchestratorYML), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if serviceDeployYML != "" {
		if err := os.WriteFile(filepath.Join(svcDir, "deploy.yml"), []byte(serviceDeployYML), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return devboxPath
}

func TestResolvePlan_deployServicesInlines(t *testing.T) {
	orchestrator := `phases:
  - name: services
    deploy_services: true
    description: Deploy services
  - name: start
    description: Start
    steps:
      - name: up
        type: shell
        cmd: make up
`
	serviceDeploy := `phases:
  - name: setup
    description: Setup main
    steps:
      - name: create-dirs
        type: shell
        cmd: mkdir -p services/main/src
  - name: init
    description: Init main
    steps:
      - name: migrate
        type: command
        cmd: services.main.migrate
`
	devboxPath := writeServiceDeployFixture(t, orchestrator, serviceDeploy)
	cfg, err := config.LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	steps, err := deploy.ResolvePlan(cfg, usercommands.NewEmptyRegistry())
	if err != nil {
		t.Fatalf("ResolvePlan: %v", err)
	}

	if len(steps) != 4 {
		var names []string
		for _, s := range steps {
			names = append(names, s.StepAddress())
		}
		t.Fatalf("want 4 steps, got %d: %v", len(steps), names)
	}

	if steps[1].Service != "main" {
		t.Errorf("steps[1].Service = %q, want main", steps[1].Service)
	}
	if steps[1].StepAddress() != "main/setup/create-dirs" {
		t.Errorf("steps[1] address = %q, want main/setup/create-dirs", steps[1].StepAddress())
	}
	if steps[2].StepAddress() != "main/init/migrate" {
		t.Errorf("steps[2] address = %q, want main/init/migrate", steps[2].StepAddress())
	}
	if steps[3].Service != "" {
		t.Errorf("steps[3].Service = %q, want empty", steps[3].Service)
	}
	if steps[3].StepAddress() != "start/up" {
		t.Errorf("steps[3] address = %q, want start/up", steps[3].StepAddress())
	}
}

func TestResolveServicePlan_singleService(t *testing.T) {
	serviceDeploy := `phases:
  - name: setup
    steps:
      - name: create-dirs
        type: shell
        cmd: mkdir -p services/main/src
  - name: init
    steps:
      - name: migrate
        type: command
        cmd: services.main.migrate
`
	devboxPath := writeServiceDeployFixture(t, "", serviceDeploy)
	cfg, err := config.LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	steps, err := deploy.ResolveServicePlan(cfg, usercommands.NewEmptyRegistry(), "main")
	if err != nil {
		t.Fatalf("ResolveServicePlan: %v", err)
	}

	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(steps))
	}
	if steps[1].Service != "main" {
		t.Errorf("steps[1].Service = %q, want main", steps[1].Service)
	}
}

func TestFindStep_threePartAddress(t *testing.T) {
	serviceDeploy := `phases:
  - name: init
    steps:
      - name: migrate
        type: command
        cmd: services.main.migrate
        description: Run migrations
`
	devboxPath := writeServiceDeployFixture(t, "", serviceDeploy)
	cfg, err := config.LoadConfig(devboxPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	phase, step, err := deploy.FindStep(cfg, "main/init/migrate")
	if err != nil {
		t.Fatalf("FindStep: %v", err)
	}
	if phase.Name != "init" {
		t.Errorf("phase.Name = %q, want init", phase.Name)
	}
	if step.Type != "command" || step.Cmd != "services.main.migrate" {
		t.Errorf("step type/cmd = %q/%q, want command/services.main.migrate", step.Type, step.Cmd)
	}
}

// helpers for ResolveServicesPlanSubset tests

func makeSubsetCfg(services map[string]config.ServiceConfig) *config.DweConfig {
	return &config.DweConfig{
		Services: services,
		Raw:      map[string]any{"__configPath": "/tmp/devbox.yml"},
	}
}

func shellDeploy(phases ...config.DeployPhase) *config.ServiceDeployConfig {
	return &config.ServiceDeployConfig{Phases: phases}
}

func shellDeployAfter(after []string, phases ...config.DeployPhase) *config.ServiceDeployConfig {
	return &config.ServiceDeployConfig{After: after, Phases: phases}
}

func svcEntry(name string) config.ServiceConfig {
	return config.ServiceConfig{Type: config.ServiceTypeApp, Container: name, Enabled: true}
}

func deployPhase(svcName, phaseName string) config.DeployPhase {
	return config.DeployPhase{
		Name: phaseName,
		Steps: []config.DeployStep{
			{Name: "step", Type: "shell", Cmd: "echo " + svcName},
		},
	}
}

// TestResolveServicesPlanSubset_OneEnvStep verifies exactly one ImplicitEnvStep is
// prepended when resolving multiple services.
func TestResolveServicesPlanSubset_OneEnvStep(t *testing.T) {
	cfg := makeSubsetCfg(map[string]config.ServiceConfig{
		"a": svcEntry("a"),
		"b": svcEntry("b"),
	})
	deploys := map[string]*config.ServiceDeployConfig{
		"a": shellDeploy(deployPhase("a", "setup")),
		"b": shellDeploy(deployPhase("b", "setup")),
	}
	steps, err := deploy.ResolveServicesPlanSubset(cfg, usercommands.NewEmptyRegistry(), deploys, []string{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: 1 env step + 1 step for a + 1 step for b = 3 total
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(steps))
	}
	if steps[0].Phase.Name != "env" {
		t.Errorf("steps[0] should be env phase, got %q", steps[0].Phase.Name)
	}
	// Verify only one env step
	envCount := 0
	for _, s := range steps {
		if s.Phase.Name == "env" {
			envCount++
		}
	}
	if envCount != 1 {
		t.Errorf("want exactly 1 env step, got %d", envCount)
	}
}

// TestResolveServicesPlanSubset_AfterOrdering verifies that after: ordering is
// respected within the subset.
func TestResolveServicesPlanSubset_AfterOrdering(t *testing.T) {
	cfg := makeSubsetCfg(map[string]config.ServiceConfig{
		"a": svcEntry("a"),
		"b": svcEntry("b"),
	})
	// b after [a] → a must come first
	deploys := map[string]*config.ServiceDeployConfig{
		"a": shellDeploy(deployPhase("a", "setup")),
		"b": shellDeployAfter([]string{"a"}, deployPhase("b", "setup")),
	}
	// Pass names in reverse order to verify topo sort overrides input order.
	steps, err := deploy.ResolveServicesPlanSubset(cfg, usercommands.NewEmptyRegistry(), deploys, []string{"b", "a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect env + a/setup/step + b/setup/step = 3
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d: %v", len(steps), steps)
	}
	if steps[1].Service != "a" {
		t.Errorf("steps[1].Service = %q, want a (a must precede b)", steps[1].Service)
	}
	if steps[2].Service != "b" {
		t.Errorf("steps[2].Service = %q, want b", steps[2].Service)
	}
}

// TestResolveServicesPlanSubset_IgnoresAfterOutsideSubset verifies that after:
// references to services not in the subset are silently dropped (no cascade).
func TestResolveServicesPlanSubset_IgnoresAfterOutsideSubset(t *testing.T) {
	cfg := makeSubsetCfg(map[string]config.ServiceConfig{
		"a":    svcEntry("a"),
		"b":    svcEntry("b"),
		"deps": svcEntry("deps"), // exists in services but not in the subset
	})
	deploys := map[string]*config.ServiceDeployConfig{
		"a":    shellDeployAfter([]string{"deps"}, deployPhase("a", "setup")), // deps outside subset
		"b":    shellDeploy(deployPhase("b", "setup")),
		"deps": shellDeploy(deployPhase("deps", "setup")), // in deploys map but not requested
	}
	// Only request a and b — deps should not be auto-included
	steps, err := deploy.ResolveServicesPlanSubset(cfg, usercommands.NewEmptyRegistry(), deploys, []string{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Env + a + b = 3 (deps not included)
	if len(steps) != 3 {
		t.Fatalf("want 3 steps (deps excluded), got %d", len(steps))
	}
	for _, s := range steps {
		if s.Service == "deps" {
			t.Errorf("deps service should not appear in subset result")
		}
	}
}

// TestResolveServicesPlanSubset_MissingDeployFile verifies ErrServiceNoDeployFile.
func TestResolveServicesPlanSubset_MissingDeployFile(t *testing.T) {
	cfg := makeSubsetCfg(map[string]config.ServiceConfig{
		"a": svcEntry("a"),
		"b": svcEntry("b"),
	})
	deploys := map[string]*config.ServiceDeployConfig{
		"a": shellDeploy(deployPhase("a", "setup")),
		// b intentionally missing
	}
	_, err := deploy.ResolveServicesPlanSubset(cfg, usercommands.NewEmptyRegistry(), deploys, []string{"a", "b"})
	if err == nil {
		t.Fatal("expected error for missing deploy.yml, got nil")
	}
	if !errors.Is(err, deploy.ErrServiceNoDeployFile) {
		t.Errorf("want ErrServiceNoDeployFile, got: %v", err)
	}
}
