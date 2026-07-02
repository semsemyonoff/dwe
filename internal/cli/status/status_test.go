package status

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/statustui"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"

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
	root.PersistentFlags().StringVar(&flags.ConfigPath, "config", "", "")
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
	root.SetArgs([]string{"--config", configPath, "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"DWE:", "Apps", "Tools"} {
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
	root.SetArgs([]string{"--config", configPath, "status", "--no-apps", "--no-tools", "--no-infra", "--no-deploy", "--no-topology", "--no-git", "--no-daemons"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "DWE:") {
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
			root.SetArgs([]string{"--config", configPath, "status", tt.flag})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			out := buf.String()
			if strings.Contains(out, tt.section) {
				t.Errorf("%s should suppress %q section:\n%s", tt.flag, tt.section, out)
			}
			if !strings.Contains(out, "DWE:") {
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
	root.SetArgs([]string{"--config", configPath, "status", "apps"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Apps") {
		t.Errorf("expected Apps section title:\n%s", out)
	}
	if strings.Contains(out, "DWE:") {
		t.Errorf("subcommand should NOT print health indicator:\n%s", out)
	}
}

func TestStatusCmd_ToolsSubcommandRendersOnlyTools(t *testing.T) {
	configPath := statusFixture(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", configPath, "status", "tools"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Tools") {
		t.Errorf("expected Tools section title:\n%s", out)
	}
	if strings.Contains(out, "DWE:") {
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
	root.SetArgs([]string{"--config", configPath, "status", "infra"})
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
	root.SetArgs([]string{"--config", configPath, "tools"})
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
	root.SetArgs([]string{"--config", configPath, "status", "daemons"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "DWE:") {
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
	root.SetArgs([]string{"--config", configPath, "status", "deploy", "definitely-not-a-service"})
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
	root.SetArgs([]string{"--config", configPath, "status", "stray"})
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
	root.SetArgs([]string{"--config", configPath, "status", "deploy"})
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
	root.SetArgs([]string{"--config", configPath, "status", "deploy", "a", "b"})
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
	root.SetArgs([]string{"--config", configPath, "status", "topology"})
	if err := root.Execute(); err != nil {
		t.Fatalf("status topology should succeed: %v", err)
	}
	if strings.Contains(buf.String(), "DWE:") {
		t.Errorf("topology subcommand should NOT print health indicator: %s", buf.String())
	}
}

func TestStatusCmd_GitSubcommandRuns(t *testing.T) {
	configPath := statusFixture(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", configPath, "status", "git"})
	if err := root.Execute(); err != nil {
		t.Fatalf("status git should succeed: %v", err)
	}
	if strings.Contains(buf.String(), "DWE:") {
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
	root.SetArgs([]string{"--config", configPath, "status"})
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
	root.SetArgs([]string{"--config", configPath, "status", "apps"})
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
	root.SetArgs([]string{"--config", configPath, "status", "deploy"})
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
	root.SetArgs([]string{"--config", configPath, "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "Pending") {
		t.Errorf("expected no pending banner when no pending state:\n%s", out)
	}
}

// readPromptCacheState extracts the `state:` value from the YAML file at the
// project root. Returns "" when the file is absent or malformed — tests treat
// these as "no write happened" (since promptcache.Write is best-effort).
func readPromptCacheState(t *testing.T, projectRoot string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(projectRoot, ".dwe", "prompt-cache.yml"))
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "state: "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// withStubContainerRunning installs a stub for serviceRunningFn and restores
// it on test cleanup. Must NOT be called from t.Parallel() tests. The stub's
// second argument is the compose service name (svc.Container).
func withStubContainerRunning(t *testing.T, stub func(project, service, bin string, processEnv []string) bool) {
	t.Helper()
	prev := serviceRunningFn
	t.Cleanup(func() { serviceRunningFn = prev })
	serviceRunningFn = stub
}

func TestStatus_TopLevel_PlainPath_WritesAccurateState_Running(t *testing.T) {
	// statusFixture has main (required, container=app-main) and adminer
	// (enabled, container=adminer). Stub all containers as running → HealthRunning.
	withStubContainerRunning(t, func(_, _, _ string, _ []string) bool { return true })

	configPath := statusFixture(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	// --no-tui forces the plain branch (cobra's test root may not have a TTY anyway).
	root.SetArgs([]string{"--config", configPath, "status", "--no-tui"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readPromptCacheState(t, filepath.Dir(configPath))
	if got != "running" {
		t.Errorf("prompt-cache state = %q, want %q", got, "running")
	}
}

func TestStatus_TopLevel_JsonPath_WritesAccurateState_Partial(t *testing.T) {
	// Stub: only "app-main" (main's container) running → main running,
	// adminer stopped → partial.
	withStubContainerRunning(t, func(_, composeService, _ string, _ []string) bool {
		return composeService == "app-main"
	})

	configPath := statusFixture(t)
	flags := &cmdctx.RootFlags{Output: "json"}
	root := buildStatusJSONRoot(flags)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--config", configPath, "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readPromptCacheState(t, filepath.Dir(configPath))
	if got != "partial" {
		t.Errorf("prompt-cache state = %q, want %q", got, "partial")
	}
}

func TestStatus_TopLevel_PlainPath_WritesStopped(t *testing.T) {
	// No stub override → default real Docker probe fails (no Docker in tests) → stopped.
	withStubContainerRunning(t, func(_, _, _ string, _ []string) bool { return false })

	configPath := statusFixture(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", configPath, "status", "--no-tui"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readPromptCacheState(t, filepath.Dir(configPath))
	if got != "stopped" {
		t.Errorf("prompt-cache state = %q, want %q", got, "stopped")
	}
}

func TestStatus_SubCommand_DoesNotWriteCache(t *testing.T) {
	// Must NOT use t.Parallel() — withStubContainerRunning mutates a package-level seam.
	withStubContainerRunning(t, func(_, _, _ string, _ []string) bool { return true })

	subcommands := []string{"apps", "tools", "infra", "deploy", "topology", "git", "daemons"}
	for _, subcmd := range subcommands {
		t.Run(subcmd, func(t *testing.T) {
			configPath := statusFixture(t)
			root := buildStatusTestRoot()
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			root.SetArgs([]string{"--config", configPath, "status", subcmd})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			cachePath := filepath.Join(filepath.Dir(configPath), ".dwe", "prompt-cache.yml")
			if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
				t.Errorf("subcommand `status %s` must not create %s (err=%v)", subcmd, cachePath, err)
			}
		})
	}
}

func TestStatus_CacheWriteFailure_DoesNotFailCommand(t *testing.T) {
	withStubContainerRunning(t, func(_, _, _ string, _ []string) bool { return true })

	configPath := statusFixture(t)
	// Plant a directory at the cache file path so atomic rename inside
	// promptcache.Write fails. The status command must still succeed.
	cachePath := filepath.Join(filepath.Dir(configPath), ".dwe", "prompt-cache.yml")
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatalf("setup: planting directory at cache path: %v", err)
	}
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", configPath, "status", "--no-tui"})
	if err := root.Execute(); err != nil {
		t.Fatalf("status should succeed despite cache write failure: %v", err)
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

// forceTUIPath stubs isTerminalFn/TERM so shouldUseTUI takes the TUI branch,
// and restores both plus runStatusTUIFn on cleanup.
func forceTUIPath(t *testing.T) {
	t.Helper()
	oldIsTerminalFn := isTerminalFn
	oldRunStatusTUIFn := runStatusTUIFn
	oldEnvTERM := os.Getenv("TERM")
	t.Cleanup(func() {
		isTerminalFn = oldIsTerminalFn
		runStatusTUIFn = oldRunStatusTUIFn
		_ = os.Setenv("TERM", oldEnvTERM)
	})
	isTerminalFn = func(fd uintptr) bool { return true }
	if err := os.Setenv("TERM", "xterm-256color"); err != nil {
		t.Fatalf("failed to set TERM: %v", err)
	}
}

// TestStatusCmd_TUI_ErrTooNarrow_FallsBackToPlainText verifies that when the
// TUI launcher returns tui.ErrTooNarrow, the top-level `status` command falls
// back to renderDefaultStatus instead of propagating the error, and that the
// i18n Deps fields are threaded from RootFlags.
func TestStatusCmd_TUI_ErrTooNarrow_FallsBackToPlainText(t *testing.T) {
	forceTUIPath(t)
	var gotDeps statustui.Deps
	runStatusTUIFn = func(ctx context.Context, d statustui.Deps) error {
		gotDeps = d
		return tui.ErrTooNarrow
	}

	store := &i18n.Store{}
	flags := &cmdctx.RootFlags{Locale: "fr-TEST", I18n: store}
	root := &cobra.Command{Use: "dwe", SilenceUsage: true}
	root.PersistentFlags().StringVar(&flags.ConfigPath, "config", "", "")
	root.AddGroup(&cobra.Group{ID: "environment", Title: "Environment Commands:"})
	root.AddCommand(NewCmd("environment", flags))

	configPath := statusFixture(t)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", configPath, "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "DWE:") || !strings.Contains(out, "Apps") {
		t.Errorf("expected plain-text fallback output on ErrTooNarrow, got:\n%s", out)
	}
	if gotDeps.Locale != "fr-TEST" {
		t.Errorf("expected Deps.Locale threaded from RootFlags.Locale, got %q", gotDeps.Locale)
	}
	if gotDeps.Translator != store {
		t.Errorf("expected Deps.Translator threaded from RootFlags.I18n, got %#v", gotDeps.Translator)
	}
}

// TestStatusCmd_TUI_CleanQuit_NoPlainFallback verifies that a nil error from
// the TUI launcher does not trigger the plain-text fallback.
func TestStatusCmd_TUI_CleanQuit_NoPlainFallback(t *testing.T) {
	forceTUIPath(t)
	runStatusTUIFn = func(ctx context.Context, d statustui.Deps) error { return nil }

	configPath := statusFixture(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", configPath, "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "DWE:") || strings.Contains(out, "Apps") {
		t.Errorf("clean TUI quit must not print plain-text fallback, got:\n%s", out)
	}
}

// TestStatusCmd_TUI_OtherError_Propagates verifies that a non-ErrTooNarrow
// error from the TUI launcher is returned unchanged, not swallowed into a
// plain-text fallback.
func TestStatusCmd_TUI_OtherError_Propagates(t *testing.T) {
	forceTUIPath(t)
	sentinel := errors.New("boom")
	runStatusTUIFn = func(ctx context.Context, d statustui.Deps) error { return sentinel }

	configPath := statusFixture(t)
	root := buildStatusTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", configPath, "status"})
	err := root.Execute()
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error to propagate, got: %v", err)
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
			root.SetArgs([]string{"--config", configPath, "status", subcmd})

			err := root.Execute()
			if err != nil {
				t.Errorf("subcommand %q failed: %v", subcmd, err)
			}

			out := buf.String()
			// Verify subcommands executed as plain output (not TUI).
			// The key assertion is that we didn't panic (which would happen if
			// runStatusTUIFn was called). Health indicator should NOT appear in subcommands.
			if strings.Contains(out, "DWE:") {
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
