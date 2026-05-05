package deploy_test

import (
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
)

// writeServiceDeployFixture creates the full file layout for service deploy tests.
func writeServiceDeployFixture(t *testing.T, orchestratorYML, serviceDeployYML string) string {
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

	servicesYML := `services:
  main:
    type: app
    container: app-main
    mandatory: true
    dir: ./services/main
`
	if err := os.WriteFile(filepath.Join(devboxDir, "services.yml"), []byte(servicesYML), 0o644); err != nil {
		t.Fatal(err)
	}

	if orchestratorYML != "" {
		if err := os.WriteFile(filepath.Join(devboxDir, "deploy.yml"), []byte(orchestratorYML), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if serviceDeployYML != "" {
		deployDir := filepath.Join(devboxDir, "deploy")
		if err := os.MkdirAll(deployDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(deployDir, "main.yml"), []byte(serviceDeployYML), 0o644); err != nil {
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

	steps, err := deploy.ResolvePlan(cfg)
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

	steps, err := deploy.ResolveServicePlan(cfg, "main")
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
