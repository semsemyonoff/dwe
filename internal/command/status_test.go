package command

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/deploy/journal"
)

// statusFixture creates a minimal devbox project on disk for end-to-end
// status command tests and returns the devbox.yml path.
// The main service has dir: services/main so CollectGitWorkspace produces rows.
func statusFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	devboxYML := `schema_version: "2"
project:
  name: test
  prefix: devbox
`
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(devboxYML), 0o644); err != nil {
		t.Fatal(err)
	}
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	defaultsYML := `runtime:
  ports:
    adminer: 8080
  hosts:
    adminer: adminer.localhost
`
	if err := os.WriteFile(filepath.Join(devboxDir, "defaults.yml"), []byte(defaultsYML), 0o644); err != nil {
		t.Fatal(err)
	}
	// Services are loaded from devbox/services.yml — not from inline devbox.yml.
	// dir: services/main ensures CollectGitWorkspace returns a row (making --no-git non-vacuous).
	servicesYML := `services:
  main:
    type: app
    container: app-main
    mandatory: true
    dir: services/main
  worker:
    type: worker
    container: app-worker
`
	if err := os.WriteFile(filepath.Join(devboxDir, "services.yml"), []byte(servicesYML), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create the service directory so fillGitRow can stat it (no .git → blank cells, no error).
	if err := os.MkdirAll(filepath.Join(dir, "services", "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	toolsYML := `tools:
  adminer:
    container: adminer
`
	if err := os.WriteFile(filepath.Join(devboxDir, "tools.yml"), []byte(toolsYML), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "devbox.yml")
}

// statusFixtureWithDeploy extends statusFixture with a minimal deploy pipeline
// and a persisted state, so RenderDeployStatus produces output.
func statusFixtureWithDeploy(t *testing.T) string {
	t.Helper()
	configPath := statusFixture(t)
	dir := filepath.Dir(configPath)
	devboxDir := filepath.Join(dir, "devbox")

	// Project-level deploy.yml: a deploy_services phase so main is tracked.
	deployYML := `phases:
  - name: services
    deploy_services: true
`
	if err := os.WriteFile(filepath.Join(devboxDir, "deploy.yml"), []byte(deployYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Per-service deploy pipeline for main.
	deployDir := filepath.Join(devboxDir, "deploy")
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainDeployYML := `phases:
  - name: setup
    steps:
      - name: noop
        type: shell
        cmd: echo hello
`
	if err := os.WriteFile(filepath.Join(deployDir, "main.yml"), []byte(mainDeployYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// State file so RenderDeployStatus produces a non-empty section.
	statePath := filepath.Join(dir, journal.DefaultRelPath)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	state := &journal.ProjectState{
		Services: map[string]*journal.ServiceState{
			"main": {Status: journal.StatusDeployed},
		},
	}
	if err := journal.Save(statePath, state); err != nil {
		t.Fatal(err)
	}

	return configPath
}

func TestStatusCmd_DefaultPrintsHealthAndSections(t *testing.T) {
	configPath := statusFixture(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Devbox:", "Services"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in default status output:\n%s", want, out)
		}
	}
}

func TestStatusCmd_NoServicesFlagSuppressesSection(t *testing.T) {
	configPath := statusFixture(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "--no-services", "--no-tools", "--no-deploy", "--no-topology", "--no-git"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Devbox:") {
		t.Errorf("health line must still appear when sections suppressed: %s", out)
	}
	if strings.Contains(out, "Services") {
		t.Errorf("--no-services should suppress Services title:\n%s", out)
	}
}

func TestStatusCmd_EachNoFlag_SuppressesItsSection(t *testing.T) {
	tests := []struct {
		flag      string
		section   string
		fixtureOn func(*testing.T) string // fixture that produces the section
	}{
		{"--no-services", "Services", statusFixture},
		{"--no-tools", "Tools", statusFixture},
		{"--no-deploy", "Deploy Status", statusFixtureWithDeploy},
		{"--no-topology", "Topology", statusFixture},
		{"--no-git", "Git Workspace", statusFixture},
		{"--no-daemons", "Daemons", statusFixture},
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			configPath := tt.fixtureOn(t)
			root := NewRootCmd()
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			root.SetArgs([]string{"-c", configPath, "status", tt.flag})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			out := buf.String()
			if strings.Contains(out, tt.section) {
				t.Errorf("%s should suppress %q section:\n%s", tt.flag, tt.section, out)
			}
			if !strings.Contains(out, "Devbox:") {
				t.Errorf("health line must still appear with %s:\n%s", tt.flag, out)
			}
		})
	}
}

func TestStatusCmd_ServicesSubcommandRendersOnlyServices(t *testing.T) {
	configPath := statusFixture(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "services"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Services") {
		t.Errorf("expected Services section title:\n%s", out)
	}
	if strings.Contains(out, "Devbox:") {
		t.Errorf("subcommand should NOT print health indicator:\n%s", out)
	}
}

func TestStatusCmd_ToolsSubcommandRendersOnlyTools(t *testing.T) {
	configPath := statusFixture(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "tools"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Tools") {
		t.Errorf("expected Tools section title:\n%s", out)
	}
	if strings.Contains(out, "Devbox:") {
		t.Errorf("subcommand should NOT print health indicator:\n%s", out)
	}
	if strings.Contains(out, "Services") {
		t.Errorf("tools subcommand should NOT print Services section:\n%s", out)
	}
}

func TestStatusCmd_DaemonsSubcommandRuns(t *testing.T) {
	configPath := statusFixture(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "daemons"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "Devbox:") {
		t.Errorf("subcommand should NOT print health indicator:\n%s", out)
	}
}

func TestStatusCmd_DefaultOrderContainsDaemonsAfterTools(t *testing.T) {
	idxTools, idxDaemons, idxDeploy := -1, -1, -1
	for i, s := range defaultSectionOrder {
		switch s {
		case sectionTools:
			idxTools = i
		case sectionDaemons:
			idxDaemons = i
		case sectionDeploy:
			idxDeploy = i
		}
	}
	if idxTools == -1 || idxDaemons == -1 || idxDeploy == -1 {
		t.Fatalf("missing sections; tools=%d daemons=%d deploy=%d", idxTools, idxDaemons, idxDeploy)
	}
	if idxTools >= idxDaemons || idxDaemons >= idxDeploy {
		t.Errorf("expected order tools<daemons<deploy, got tools=%d daemons=%d deploy=%d", idxTools, idxDaemons, idxDeploy)
	}
}

func TestStatusDeployCmd_UnknownService_Errors(t *testing.T) {
	configPath := statusFixture(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "deploy", "definitely-not-a-service"})
	err := root.Execute()
	if err == nil {
		t.Fatalf("expected error for unknown service")
	}
	if !strings.Contains(err.Error(), "not tracked") {
		t.Errorf("expected 'not tracked' in error, got: %v", err)
	}
}

func TestStatusCmd_RejectsPositionalArg_E2E(t *testing.T) {
	configPath := statusFixture(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "stray"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error: status accepts no positional args")
	}
}

func TestStatusDeployCmd_NoArgs_RunsWithoutError(t *testing.T) {
	configPath := statusFixture(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "deploy"})
	if err := root.Execute(); err != nil {
		t.Fatalf("status deploy with no args should succeed: %v", err)
	}
}

func TestStatusDeployCmd_TwoArgs_Rejected(t *testing.T) {
	configPath := statusFixture(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "deploy", "a", "b"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error: status deploy accepts at most 1 arg")
	}
}

func TestStatusCmd_TopologySubcommandRuns(t *testing.T) {
	configPath := statusFixture(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "topology"})
	if err := root.Execute(); err != nil {
		t.Fatalf("status topology should succeed: %v", err)
	}
	if strings.Contains(buf.String(), "Devbox:") {
		t.Errorf("topology subcommand should NOT print health indicator: %s", buf.String())
	}
}

func TestStatusCmd_GitSubcommandRuns(t *testing.T) {
	configPath := statusFixture(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "git"})
	if err := root.Execute(); err != nil {
		t.Fatalf("status git should succeed: %v", err)
	}
	if strings.Contains(buf.String(), "Devbox:") {
		t.Errorf("git subcommand should NOT print health indicator: %s", buf.String())
	}
}
