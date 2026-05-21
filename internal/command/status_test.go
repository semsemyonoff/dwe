package command

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// statusFixture creates a minimal devbox project on disk for end-to-end
// status command tests and returns the devbox.yml path.
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
	// Services are loaded from devbox/services.yml — not from inline devbox.yml.
	servicesYML := `services:
  main:
    type: app
    container: app-main
    mandatory: true
  worker:
    type: worker
    container: app-worker
`
	if err := os.WriteFile(filepath.Join(devboxDir, "services.yml"), []byte(servicesYML), 0o644); err != nil {
		t.Fatal(err)
	}
	toolsYML := `tools:
  adminer:
    container: adminer
    host: adminer.localhost
    port: 8080
`
	if err := os.WriteFile(filepath.Join(devboxDir, "tools.yml"), []byte(toolsYML), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "devbox.yml")
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
		flag    string
		section string
	}{
		{"--no-services", "Services"},
		{"--no-tools", "Tools"},
		{"--no-deploy", "Deploy Status"},
		{"--no-topology", "Topology"},
		{"--no-git", "Git Workspace"},
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			configPath := statusFixture(t)
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
