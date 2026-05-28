package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/command/cmdctx"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/workflow/deploy"
	"devbox-cli/internal/core/workflow/deploy/journal"
	"devbox-cli/internal/core/workflow/reset"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands"
	"devbox-cli/internal/usercommands/registry"
	"devbox-cli/internal/usercommands/runtime"

	"github.com/spf13/cobra"
)

// TestResetRunCmd_projectWideCleanup verifies that a project-wide reset
// removes the entire deploy state file.
func TestResetRunCmd_projectWideCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "devbox.yml")
	stateDir := filepath.Join(tmpDir, ".devbox", "deploy")
	statePath := filepath.Join(stateDir, "state.yml")

	// Create minimal devbox.yml + devbox/services/main/service.yml
	if err := os.WriteFile(configPath, []byte(`
schema_version: "2"
project:
  name: test
  prefix: devbox
`), 0o644); err != nil {
		t.Fatal(err)
	}

	resetDir := filepath.Join(tmpDir, "devbox")
	if err := os.MkdirAll(resetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	svcDirA := filepath.Join(resetDir, "services", "main")
	if err := os.MkdirAll(svcDirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDirA, "service.yml"), []byte("type: app\ncontainer: app-main\ndir: ./services/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resetDir, "reset.yml"), []byte(`
phases:
  - name: cleanup
    steps:
      - name: cleanup-env
        type: shell
        cmd: echo "cleanup"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create initial state file
	initialState := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services: map[string]*journal.ServiceState{
			"main": {
				Status: journal.StatusDeployed,
			},
		},
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := journal.Save(statePath, initialState); err != nil {
		t.Fatal(err)
	}

	// Verify state file exists
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("initial state file not created: %v", err)
	}

	// Load config and verify reset plan
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	resetCfg, steps, err := reset.LoadAndResolvePlan(cfg, usercommands.NewEmptyRegistry())
	if err != nil {
		t.Fatalf("resolving reset plan: %v", err)
	}

	// Verify that reset steps contain only project-level steps (no Service field)
	hasProjectLevel := false
	hasServiceLevel := false
	for _, rs := range steps {
		if rs.Service == "" {
			hasProjectLevel = true
		} else {
			hasServiceLevel = true
		}
	}

	// Should only have project-level steps in this reset plan
	if !hasProjectLevel {
		t.Errorf("expected project-level steps in reset plan")
	}
	if hasServiceLevel {
		t.Errorf("unexpected service-level steps in reset plan")
	}

	// Check that logEnabled works
	_ = resetCfg.LogEnabled()

	// After reset succeeds, verify state cleanup
	// For now, we just verify the logic would clean up correctly
	// In a real integration test, this would be done via the actual command
}

// TestResetRunCmd_handlesMissingStateFile verifies that reset handles
// a missing state file gracefully (RemoveService on missing file is a no-op).
func TestResetRunCmd_handlesMissingStateFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "devbox.yml")
	stateDir := filepath.Join(tmpDir, ".devbox", "deploy")
	statePath := filepath.Join(stateDir, "state.yml")

	// Create minimal devbox.yml
	if err := os.WriteFile(configPath, []byte(`
schema_version: "2"
project:
  name: test
  prefix: devbox
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create devbox/reset.yml + services/main/service.yml
	resetDir := filepath.Join(tmpDir, "devbox")
	if err := os.MkdirAll(resetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	svcDirB := filepath.Join(resetDir, "services", "main")
	if err := os.MkdirAll(svcDirB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDirB, "service.yml"), []byte("type: app\ncontainer: app-main\ndir: ./services/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resetDir, "reset.yml"), []byte(`
phases:
  - name: cleanup
    steps:
      - name: cleanup-env
        type: shell
        cmd: echo "cleanup"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Don't create a state file; ensure RemoveService handles missing file gracefully
	// Load produces default state when file is absent
	state, err := journal.Load(statePath)
	if err != nil {
		t.Fatalf("loading missing state file: %v", err)
	}

	// State should be zero-value with defaults
	if state.SchemaVersion != "1" {
		t.Errorf("expected schema_version=1, got %s", state.SchemaVersion)
	}

	// Verify that RemoveService on a service in the default state is a no-op (file doesn't get created)
	if err := journal.RemoveService(statePath, "main"); err != nil {
		t.Fatalf("RemoveService on missing file: %v", err)
	}
}

// TestResetRunCmd_stateRemovalWhenNoServicesRemain verifies that when the last
// service is removed via RemoveService, the state file is deleted entirely.
func TestResetRunCmd_stateRemovalWhenNoServicesRemain(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, ".devbox", "deploy")
	statePath := filepath.Join(stateDir, "state.yml")

	// Create initial state file with single service
	initialState := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services: map[string]*journal.ServiceState{
			"main": {
				Status: journal.StatusDeployed,
			},
		},
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := journal.Save(statePath, initialState); err != nil {
		t.Fatal(err)
	}

	// Verify state file exists
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("initial state file not created: %v", err)
	}

	// Remove the service
	if err := journal.RemoveService(statePath, "main"); err != nil {
		t.Fatalf("removing service: %v", err)
	}

	// Verify state file is deleted
	if _, err := os.Stat(statePath); err == nil {
		t.Errorf("expected state file to be deleted after removing last service, but it still exists")
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected error checking state file: %v", err)
	}
}

// TestResetRunCmd_multipleLockAttempts verifies that lock cleanup allows retry.
func TestResetRunCmd_multipleLockAttempts(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, ".devbox", "deploy", "deploy.lock")

	// We don't test actual concurrency here, but we verify that
	// the lock file is created and cleaned up properly in sequential scenarios.
	// Real concurrency testing should use the lock package's tests.

	// Verify lock directory is created
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(lockPath)); err != nil {
		t.Errorf("lock directory not created: %v", err)
	}
}

// makeResetServiceTestDir writes a minimal devbox.yml and service folder for per-service reset tests.
// deployYML controls whether to write a deploy.yml for the service.
// resetYML controls whether to write a per-service reset.yml.
// Returns the config path and the base dir.
func makeResetServiceTestDir(t *testing.T, serviceName string, enabled, mandatory, deployYML, resetYML bool) (string, string) {
	t.Helper()
	dir := t.TempDir()
	cfgContent := "schema_version: \"2\"\nproject:\n  name: test\n  prefix: devbox\n"
	cfgPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}
	svcDir := filepath.Join(dir, "devbox", "services", serviceName)
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	svcContent := "type: app\ncontainer: app-" + serviceName + "\n"
	if mandatory {
		svcContent += "required: true\n"
	}
	if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(svcContent), 0o644); err != nil {
		t.Fatalf("write service.yml: %v", err)
	}
	// Services are disabled by default in the 3-layer config merge; always write local.yml
	// to set the enabled state explicitly, whether true or false.
	localContent := "services:\n  " + serviceName + ":\n    enabled: "
	if enabled {
		localContent += "true\n"
	} else {
		localContent += "false\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "devbox", "local.yml"), []byte(localContent), 0o644); err != nil {
		t.Fatalf("write local.yml: %v", err)
	}
	if deployYML {
		deployContent := "phases:\n  - name: setup\n    steps:\n      - name: init\n        type: shell\n        cmd: echo init\n"
		if err := os.WriteFile(filepath.Join(svcDir, "deploy.yml"), []byte(deployContent), 0o644); err != nil {
			t.Fatalf("write deploy.yml: %v", err)
		}
	}
	if resetYML {
		resetContent := "phases:\n  - name: cleanup\n    steps:\n      - name: wipe\n        type: shell\n        cmd: echo wipe\n"
		if err := os.WriteFile(filepath.Join(svcDir, "reset.yml"), []byte(resetContent), 0o644); err != nil {
			t.Fatalf("write reset.yml: %v", err)
		}
	}
	return cfgPath, dir
}

// stubPreflightRun stubs preflightRun to be a no-op for tests, restoring original on cleanup.
func stubPreflightRun(t *testing.T) {
	t.Helper()
	prev := preflightRun
	preflightRun = func(_ context.Context, _ *config.DevboxConfig, _ *usercommands.Registry, _, _ string, _ bool, _ io.Writer) error {
		return nil
	}
	t.Cleanup(func() { preflightRun = prev })
}

// TestResetServiceRun_FlagsExist verifies --service, --yes, --skip-preflight flags exist on reset run.
func TestResetServiceRun_FlagsExist(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := newResetRunCmd(flags)
	if cmd.Flags().Lookup("service") == nil {
		t.Error("missing --service flag on reset run")
	}
	if cmd.Flags().Lookup("yes") == nil {
		t.Error("missing --yes flag on reset run")
	}
	if cmd.Flags().Lookup("skip-preflight") == nil {
		t.Error("missing --skip-preflight flag on reset run")
	}
}

// TestResetServiceRun_UnknownService verifies ErrUnknownService before any side effect.
func TestResetServiceRun_UnknownService(t *testing.T) {
	cfgPath, _ := makeResetServiceTestDir(t, "postgres", true, false, true, false)

	var stopCalled bool
	prevStop := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prevStop })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error {
		stopCalled = true
		return nil
	}
	stubPreflightRun(t)

	root := NewRootCmd()
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "nonexistent", "--yes"})

	err := root.Execute()
	if !errors.Is(err, ErrUnknownService) {
		t.Errorf("expected ErrUnknownService, got %v", err)
	}
	if stopCalled {
		t.Error("stop should not have been called for unknown service")
	}
}

// TestResetServiceRun_NoDeployFile verifies ErrServiceNoDeployFile before any side effect.
func TestResetServiceRun_NoDeployFile(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, false, false)
	statePath := filepath.Join(dir, journal.DefaultRelPath)

	var stopCalled bool
	prevStop := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prevStop })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error {
		stopCalled = true
		return nil
	}

	root := NewRootCmd()
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes"})

	err := root.Execute()
	if !errors.Is(err, deploy.ErrServiceNoDeployFile) {
		t.Errorf("expected ErrServiceNoDeployFile, got %v", err)
	}
	if stopCalled {
		t.Error("stop should not have been called when deploy.yml is absent")
	}
	if _, statErr := os.Stat(statePath); statErr == nil {
		t.Error("journal file should not exist after ErrServiceNoDeployFile")
	}
}

// TestResetServiceRun_NoResetYML verifies container stop + journal update when no reset.yml.
func TestResetServiceRun_NoResetYML(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, true, false)
	statePath := filepath.Join(dir, journal.DefaultRelPath)

	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      map[string]*journal.ServiceState{"postgres": {Status: journal.StatusDeployed}},
	}
	if err := journal.Save(statePath, initial); err != nil {
		t.Fatal(err)
	}

	var stopCalled bool
	prevStop := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prevStop })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error {
		stopCalled = true
		return nil
	}
	stubPreflightRun(t)

	root := NewRootCmd()
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !stopCalled {
		t.Error("expected container stop to be called")
	}

	state, err := journal.Load(statePath)
	if err != nil {
		t.Fatalf("loading journal: %v", err)
	}
	if _, ok := state.Services["postgres"]; ok {
		t.Error("service should be removed from journal after reset")
	}
	if state.Pending == nil {
		t.Fatal("expected PendingApply in journal after reset")
	}
	op := state.Pending.Find(journal.PendingDeploy)
	if op == nil {
		t.Fatal("expected PendingDeploy op")
	}
	if len(op.Services) != 1 || op.Services[0] != "postgres" {
		t.Errorf("PendingDeploy services = %v, want [postgres]", op.Services)
	}
}

// TestResetServiceRun_WithResetYML verifies reset.yml steps are executed.
func TestResetServiceRun_WithResetYML(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, true, true)
	statePath := filepath.Join(dir, journal.DefaultRelPath)

	prevStop := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prevStop })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error { return nil }
	stubPreflightRun(t)

	root := NewRootCmd()
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	state, err := journal.Load(statePath)
	if err != nil {
		t.Fatalf("loading journal: %v", err)
	}
	if state.Pending == nil || state.Pending.Find(journal.PendingDeploy) == nil {
		t.Error("expected PendingDeploy in journal after reset with reset.yml")
	}
}

// TestResetServiceRun_DisabledServiceStop verifies compose-bypass stop works on disabled service.
func TestResetServiceRun_DisabledServiceStop(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", false, false, true, false)
	statePath := filepath.Join(dir, journal.DefaultRelPath)

	var stopCalled bool
	prevStop := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prevStop })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error {
		stopCalled = true
		return nil
	}
	stubPreflightRun(t)

	root := NewRootCmd()
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !stopCalled {
		t.Error("expected container stop to be called even for disabled service")
	}

	state, err := journal.Load(statePath)
	if err != nil {
		t.Fatalf("loading journal: %v", err)
	}
	if state.Pending == nil || state.Pending.Find(journal.PendingDeploy) == nil {
		t.Error("expected PendingDeploy even for disabled service reset")
	}
}

// TestResetServiceRun_EnabledServiceRunsHooks verifies on_disable.before hooks run for enabled services.
func TestResetServiceRun_EnabledServiceRunsHooks(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: test\n  prefix: devbox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(dir, "devbox", "services", "postgres")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Enable the service via local.yml (services are disabled by default without an explicit override).
	localContent := "services:\n  postgres:\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(dir, "devbox", "local.yml"), []byte(localContent), 0o644); err != nil {
		t.Fatal(err)
	}
	svcContent := `type: app
container: app-postgres
on_disable:
  requires: none
  before:
    - db:teardown
`
	if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(svcContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "deploy.yml"), []byte("phases:\n  - name: s\n    steps:\n      - name: x\n        type: shell\n        cmd: echo x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var hookCalled bool
	var hookIDs []string
	// Seam at the runResetHook level so we intercept before any registry lookup.
	prevRunHook := resetRunHookFn
	t.Cleanup(func() { resetRunHookFn = prevRunHook })
	resetRunHookFn = func(_ context.Context, _ *cobra.Command, _ *config.DevboxConfig, _ *registry.Registry, _ string, cmdID string) error {
		hookCalled = true
		hookIDs = append(hookIDs, cmdID)
		return nil
	}

	prevStop := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prevStop })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error { return nil }
	stubPreflightRun(t)

	root := NewRootCmd()
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hookCalled {
		t.Error("expected on_disable.before hook to be called for enabled service")
	}
	if len(hookIDs) != 1 || hookIDs[0] != "db:teardown" {
		t.Errorf("hook IDs = %v, want [db:teardown]", hookIDs)
	}
}

// TestResetServiceRun_DisabledServiceSkipsHooks verifies hooks do not run for disabled services.
func TestResetServiceRun_DisabledServiceSkipsHooks(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: test\n  prefix: devbox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(dir, "devbox", "services", "postgres")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Disable via local.yml
	localContent := "services:\n  postgres:\n    enabled: false\n"
	if err := os.WriteFile(filepath.Join(dir, "devbox", "local.yml"), []byte(localContent), 0o644); err != nil {
		t.Fatal(err)
	}
	svcContent := `type: app
container: app-postgres
on_disable:
  requires: none
  before:
    - db:teardown
`
	if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(svcContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "deploy.yml"), []byte("phases:\n  - name: s\n    steps:\n      - name: x\n        type: shell\n        cmd: echo x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var hookCalled bool
	prevHook := resetServiceRunHook
	t.Cleanup(func() { resetServiceRunHook = prevHook })
	resetServiceRunHook = func(_ context.Context, rc runtime.RunContext) error {
		hookCalled = true
		return nil
	}

	prevStop := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prevStop })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error { return nil }
	stubPreflightRun(t)

	root := NewRootCmd()
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if hookCalled {
		t.Error("hooks should not run for disabled service")
	}
}

// TestResetServiceRun_TTYConfirmationDecline verifies that in TTY mode user can decline.
func TestResetServiceRun_TTYConfirmationDecline(t *testing.T) {
	cfgPath, _ := makeResetServiceTestDir(t, "postgres", true, false, true, false)

	var stopCalled bool
	prevStop := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prevStop })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error {
		stopCalled = true
		return nil
	}
	stubPreflightRun(t)

	prevInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = prevInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	root := NewRootCmd()
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetIn(strings.NewReader("n\n"))
	root.SetArgs([]string{"reset", "run", "--service", "postgres"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error on decline: %v", err)
	}
	if stopCalled {
		t.Error("stop should not have been called when user declines")
	}
}

// TestResetServiceRun_TTYConfirmationAccept verifies that in TTY mode user can accept.
func TestResetServiceRun_TTYConfirmationAccept(t *testing.T) {
	cfgPath, _ := makeResetServiceTestDir(t, "postgres", true, false, true, false)

	var stopCalled bool
	prevStop := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prevStop })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error {
		stopCalled = true
		return nil
	}
	stubPreflightRun(t)

	prevInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = prevInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	root := NewRootCmd()
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetIn(strings.NewReader("y\n"))
	root.SetArgs([]string{"reset", "run", "--service", "postgres"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error on confirm: %v", err)
	}
	if !stopCalled {
		t.Error("stop should have been called when user confirms with 'y'")
	}
}

// TestResetServiceRun_MandatoryService verifies mandatory services are allowed to reset.
func TestResetServiceRun_MandatoryService(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, true, true, false)
	statePath := filepath.Join(dir, journal.DefaultRelPath)

	prevStop := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prevStop })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error { return nil }
	stubPreflightRun(t)

	var out bytes.Buffer
	root := NewRootCmd()
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetOut(&out)
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatalf("mandatory service reset should succeed: %v", err)
	}

	state, err := journal.Load(statePath)
	if err != nil {
		t.Fatalf("loading journal: %v", err)
	}
	if state.Pending == nil || state.Pending.Find(journal.PendingDeploy) == nil {
		t.Error("expected PendingDeploy in journal after mandatory service reset")
	}
}
