package status

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/statustui"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"

	"github.com/spf13/cobra"
)

// buildStatusTestRoot constructs a minimal cobra root carrying the persistent
// `-c/--config` flag plus the status subcommand. Replaces cli.NewRootCmd() in
// tests so this package does not import its parent (which would form a cycle).
// flags.ConfigPath is the binding target for `-c`, and ProjectRoot falls back
// to filepath.Dir(ConfigPath) — same observable behaviour as the cli root.
func buildStatusTestRoot() *cobra.Command {
	flags := &cmdctx.RootFlags{}
	root := &cobra.Command{Use: "dwe", SilenceUsage: true}
	root.PersistentFlags().StringVarP(&flags.ConfigPath, "config", "c", "", "")
	root.AddGroup(&cobra.Group{ID: "environment", Title: "Environment Commands:"})
	root.AddCommand(NewCmd("environment", flags))
	return root
}

// statusFixture creates a minimal dwe project on disk for end-to-end
// status command tests and returns the workspace.yml path.
// The main service has dir: services/main so CollectGitWorkspace produces rows.
func statusFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfgYAML := `schema_version: "2"
project:
  name: test
  prefix: dwe
`
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	defaultsYML := `services:
  adminer:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "defaults.yml"), []byte(defaultsYML), 0o644); err != nil {
		t.Fatal(err)
	}
	// Services are loaded from per-folder workspace/services/<name>/ — not from inline workspace.yml.
	// dir: services/main ensures CollectGitWorkspace returns a row (making --no-git non-vacuous).
	for name, content := range map[string]string{
		"main":    "type: app\ncontainer: app-main\nrequired: true\ndir: services/main\n",
		"worker":  "type: app\ncontainer: app-worker\n",
		"adminer": "type: tool\ncontainer: adminer\nports:\n  main: 8080\nhosts:\n  main: adminer.localhost\n",
	} {
		svcDir := filepath.Join(workspaceDir, "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Create the service directory so fillGitRow can stat it (no .git → blank cells, no error).
	if err := os.MkdirAll(filepath.Join(dir, "services", "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "workspace.yml")
}

// statusFixtureWithInfra builds a fixture that includes a type=infra service
// so the Infra section produces output.
func statusFixtureWithInfra(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfgYAML := `schema_version: "2"
project:
  name: test
  prefix: dwe
`
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"main": "type: app\ncontainer: app-main\nrequired: true\ndir: services/main\n",
		"db":   "type: infra\ncontainer: db\nrequired: true\n",
	} {
		svcDir := filepath.Join(workspaceDir, "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "services", "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "workspace.yml")
}

// statusFixtureWithDeploy extends statusFixture with a minimal deploy pipeline
// and a persisted state, so DeployStatus produces output.
func statusFixtureWithDeploy(t *testing.T) string {
	t.Helper()
	configPath := statusFixture(t)
	dir := filepath.Dir(configPath)
	workspaceDir := filepath.Join(dir, "workspace")

	// Project-level deploy.yml: a deploy_services phase so main is tracked.
	deployYML := `phases:
  - name: services
    deploy_services: true
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "deploy.yml"), []byte(deployYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Per-service deploy pipeline for main.
	deployDir := filepath.Join(workspaceDir, "deploy")
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

	// State file so DeployStatus produces a non-empty section.
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
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Devbox:", "Apps", "Tools"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in default status output:\n%s", want, out)
		}
	}
}

func TestStatusCmd_NoAppsFlagSuppressesSection(t *testing.T) {
	configPath := statusFixture(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "--no-apps", "--no-tools", "--no-infra", "--no-deploy", "--no-topology", "--no-git", "--no-daemons"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Devbox:") {
		t.Errorf("health line must still appear when sections suppressed: %s", out)
	}
	if strings.Contains(out, "Apps") {
		t.Errorf("--no-apps should suppress Apps title:\n%s", out)
	}
}

func TestStatusCmd_EachNoFlag_SuppressesItsSection(t *testing.T) {
	tests := []struct {
		flag      string
		section   string
		fixtureOn func(*testing.T) string // fixture that produces the section
	}{
		{"--no-apps", "Apps", statusFixture},
		{"--no-tools", "Tools", statusFixture},
		{"--no-infra", "Infra", statusFixtureWithInfra},
		{"--no-deploy", "Deploy Status", statusFixtureWithDeploy},
		{"--no-topology", "Topology", statusFixture},
		{"--no-git", "Git Workspace", statusFixture},
		{"--no-daemons", "Daemons", statusFixture},
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			configPath := tt.fixtureOn(t)
			root := buildStatusTestRoot()
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

func TestStatusCmd_AppsSubcommandRendersOnlyApps(t *testing.T) {
	configPath := statusFixture(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "apps"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Apps") {
		t.Errorf("expected Apps section title:\n%s", out)
	}
	if strings.Contains(out, "Devbox:") {
		t.Errorf("subcommand should NOT print health indicator:\n%s", out)
	}
}

func TestStatusCmd_ToolsSubcommandRendersOnlyTools(t *testing.T) {
	configPath := statusFixture(t)
	root := buildStatusTestRoot()
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
	if strings.Contains(out, "Apps") {
		t.Errorf("tools subcommand should NOT print Apps section:\n%s", out)
	}
}

func TestStatusCmd_InfraSubcommandRendersOnlyInfra(t *testing.T) {
	configPath := statusFixtureWithInfra(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "infra"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Infra") {
		t.Errorf("expected Infra section title:\n%s", out)
	}
	if strings.Contains(out, "Apps") {
		t.Errorf("infra subcommand should NOT print Apps section:\n%s", out)
	}
}

// TestStatusCmd_UnknownToolsCommand_RemovedFromRoot verifies the rename guard:
// `dwe tools` should error as unknown after the unification.
func TestStatusCmd_ToolsRootCmdRemoved(t *testing.T) {
	configPath := statusFixture(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "tools"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error: 'dwe tools' should be unknown command after unification")
	}
}

func TestStatusCmd_DaemonsSubcommandRuns(t *testing.T) {
	configPath := statusFixture(t)
	root := buildStatusTestRoot()
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

func TestStatusCmd_DefaultOrderAppsToolsInfra(t *testing.T) {
	idx := make(map[section]int)
	for i, s := range defaultSectionOrder {
		idx[s] = i + 1 // 1-indexed so 0 means missing
	}
	want := []section{sectionApps, sectionTools, sectionInfra, sectionDeploy, sectionTopology, sectionGit, sectionDaemons}
	for i, s := range want {
		if idx[s] == 0 {
			t.Fatalf("missing section %d in defaultSectionOrder", s)
		}
		if i > 0 && idx[want[i-1]] >= idx[s] {
			t.Errorf("expected section %d before %d", want[i-1], s)
		}
	}
}

func TestStatusDeployCmd_UnknownService_Errors(t *testing.T) {
	configPath := statusFixture(t)
	root := buildStatusTestRoot()
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
	root := buildStatusTestRoot()
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
	root := buildStatusTestRoot()
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
	root := buildStatusTestRoot()
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
	root := buildStatusTestRoot()
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
	root := buildStatusTestRoot()
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

// statusFixtureWithPending builds a fixture that includes a state file with
// pending entries so the pending banner is rendered by status commands.
func statusFixtureWithPending(t *testing.T) string {
	t.Helper()
	configPath := statusFixture(t)
	dir := filepath.Dir(configPath)
	statePath := filepath.Join(dir, journal.DefaultRelPath)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	state := &journal.ProjectState{
		Services: map[string]*journal.ServiceState{},
		Pending: &journal.PendingApply{
			Operations: []journal.PendingOp{
				{Kind: journal.PendingDeploy, Services: []string{"main"}},
				{Kind: journal.PendingRestart},
			},
		},
	}
	if err := journal.Save(statePath, state); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func TestStatusCmd_ShowsPendingBanner_DefaultView(t *testing.T) {
	configPath := statusFixtureWithPending(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Pending") {
		t.Errorf("expected pending banner in default status output:\n%s", out)
	}
	if !strings.Contains(out, "deploy required for: main") {
		t.Errorf("expected deploy pending for 'main' in output:\n%s", out)
	}
	if !strings.Contains(out, "restart required") {
		t.Errorf("expected restart pending in output:\n%s", out)
	}
}

func TestStatusCmd_ShowsPendingBanner_AppsSubcommand(t *testing.T) {
	configPath := statusFixtureWithPending(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "apps"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Pending") {
		t.Errorf("expected pending banner in 'status apps' output:\n%s", out)
	}
}

func TestStatusCmd_ShowsPendingBanner_DeploySubcommand(t *testing.T) {
	configPath := statusFixtureWithPending(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status", "deploy"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Pending") {
		t.Errorf("expected pending banner in 'status deploy' output:\n%s", out)
	}
}

func TestStatusCmd_NoBanner_WhenNoPending(t *testing.T) {
	// statusFixture has no pending entries in the state file (no state file at all)
	configPath := statusFixture(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"-c", configPath, "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "Pending") {
		t.Errorf("expected no pending banner when no pending state:\n%s", out)
	}
}

func TestShouldUseTUI_Matrix(t *testing.T) {
	tests := []struct {
		name      string
		noTUI     bool
		noFlags   *noSectionFlags
		isTTY     bool
		termValue string
		expect    bool
	}{
		// Happy path: TTY, no flags
		{"TTY_NoFlags_True", false, &noSectionFlags{}, true, "xterm-256color", true},
		// --no-tui forces plain output
		{"NoTUI_False", true, &noSectionFlags{}, true, "xterm-256color", false},
		// Each --no-<section> flag forces plain output
		{"NoApps_False", false, &noSectionFlags{noApps: true}, true, "xterm-256color", false},
		{"NoTools_False", false, &noSectionFlags{noTools: true}, true, "xterm-256color", false},
		{"NoInfra_False", false, &noSectionFlags{noInfra: true}, true, "xterm-256color", false},
		{"NoDeploy_False", false, &noSectionFlags{noDeploy: true}, true, "xterm-256color", false},
		{"NoTopology_False", false, &noSectionFlags{noTopology: true}, true, "xterm-256color", false},
		{"NoGit_False", false, &noSectionFlags{noGit: true}, true, "xterm-256color", false},
		{"NoDaemons_False", false, &noSectionFlags{noDaemons: true}, true, "xterm-256color", false},
		// Non-TTY forces plain output
		{"NonTTY_False", false, &noSectionFlags{}, false, "xterm-256color", false},
		// TERM=dumb forces plain output
		{"TermDumb_False", false, &noSectionFlags{}, true, "dumb", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldIsTerminalFn := isTerminalFn
			oldEnvTERM := os.Getenv("TERM")
			defer func() {
				isTerminalFn = oldIsTerminalFn
				_ = os.Setenv("TERM", oldEnvTERM)
			}()

			isTerminalFn = func(fd uintptr) bool { return tt.isTTY }
			if err := os.Setenv("TERM", tt.termValue); err != nil {
				t.Fatalf("failed to set TERM: %v", err)
			}

			result := shouldUseTUI(tt.noTUI, tt.noFlags)
			if result != tt.expect {
				t.Errorf("shouldUseTUI(%v, %+v) = %v, want %v",
					tt.noTUI, tt.noFlags, result, tt.expect)
			}
		})
	}
}

func TestStatusSubcommands_NeverInvokeTUI(t *testing.T) {
	// Must NOT use t.Parallel() — mutates package-level TUI seams
	configPath := statusFixture(t)

	subcommands := []string{"apps", "tools", "infra", "deploy", "topology", "git", "daemons"}

	for _, subcmd := range subcommands {
		t.Run(subcmd, func(t *testing.T) {
			oldRunStatusTUIFn := runStatusTUIFn
			oldIsTerminalFn := isTerminalFn
			oldEnvTERM := os.Getenv("TERM")

			defer func() {
				runStatusTUIFn = oldRunStatusTUIFn
				isTerminalFn = oldIsTerminalFn
				_ = os.Setenv("TERM", oldEnvTERM)
			}()

			// Panicking stub to catch if TUI dispatch is invoked
			runStatusTUIFn = func(ctx context.Context, d statustui.Deps) error {
				t.Fatal("TUI should not be invoked for subcommands")
				return nil
			}
			isTerminalFn = func(fd uintptr) bool { return true }
			if err := os.Setenv("TERM", "xterm-256color"); err != nil {
				t.Fatalf("failed to set TERM: %v", err)
			}

			root := buildStatusTestRoot()
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			root.SetArgs([]string{"-c", configPath, "status", subcmd})

			err := root.Execute()
			if err != nil {
				t.Errorf("subcommand %q failed: %v", subcmd, err)
			}

			out := buf.String()
			// Verify subcommands executed as plain output (not TUI).
			// The key assertion is that we didn't panic (which would happen if
			// runStatusTUIFn was called). Health indicator should NOT appear in subcommands.
			if strings.Contains(out, "Devbox:") {
				// Health indicator should only appear in default view, not subcommands
				t.Errorf("subcommand %q should not print health indicator", subcmd)
			}
			// For subcommands that produce content with the basic fixture, verify
			// that plain rendering ran (not empty/failed).
			switch subcmd {
			case "apps":
				if !strings.Contains(out, "Apps") {
					t.Errorf("subcommand %q should render Apps section, got:\n%s", subcmd, out)
				}
			case "tools":
				if !strings.Contains(out, "Tools") {
					t.Errorf("subcommand %q should render Tools section, got:\n%s", subcmd, out)
				}
			}
		})
	}
}
