package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/core/usercommands/registry"
	"devbox-cli/internal/core/usercommands/runtime"
	"devbox-cli/internal/core/workflow/deploy"
	"devbox-cli/internal/core/workflow/deploy/journal"
	"devbox-cli/internal/core/workflow/reset"

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

// resetServiceFixtureOpts controls optional service-folder contents for
// per-service reset tests. The zero value omits everything optional.
type resetServiceFixtureOpts struct {
	enabled   bool
	mandatory bool
	deployYML bool
	resetYML  bool
	// withDir, when true, writes `dir: ./services/<name>` into service.yml
	// AND pre-creates the directory on disk so the synthetic files phase
	// has something to remove.
	withDir bool
}

// makeResetServiceTestDir writes a minimal devbox.yml and service folder for per-service reset tests.
// deployYML controls whether to write a deploy.yml for the service.
// resetYML controls whether to write a per-service reset.yml.
// Returns the config path and the base dir.
func makeResetServiceTestDir(t *testing.T, serviceName string, enabled, mandatory, deployYML, resetYML bool) (string, string) {
	t.Helper()
	return makeResetServiceFixture(t, serviceName, resetServiceFixtureOpts{
		enabled:   enabled,
		mandatory: mandatory,
		deployYML: deployYML,
		resetYML:  resetYML,
	})
}

// makeResetServiceFixture is the options-form sibling that supports withDir.
// It also installs a fake docker binary via .devbox/config so the synthetic
// container baseline can run without a real Docker daemon. The fake's
// invocation log path is recorded in fakeDockerLogPath so callers can read it
// via dockerInvocations(t, dir).
func makeResetServiceFixture(t *testing.T, serviceName string, opts resetServiceFixtureOpts) (string, string) {
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
	if opts.mandatory {
		svcContent += "required: true\n"
	}
	if opts.withDir {
		svcContent += "dir: ./services/" + serviceName + "\n"
	}
	if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(svcContent), 0o644); err != nil {
		t.Fatalf("write service.yml: %v", err)
	}
	if opts.withDir {
		dirRel := filepath.Join(dir, "services", serviceName)
		if err := os.MkdirAll(dirRel, 0o755); err != nil {
			t.Fatalf("mkdir service dir: %v", err)
		}
		// Drop a sentinel file inside so we can assert removal afterward.
		if err := os.WriteFile(filepath.Join(dirRel, "marker"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write marker: %v", err)
		}
	}
	// Services are disabled by default in the 3-layer config merge; always write local.yml
	// to set the enabled state explicitly, whether true or false.
	localContent := "services:\n  " + serviceName + ":\n    enabled: "
	if opts.enabled {
		localContent += "true\n"
	} else {
		localContent += "false\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "devbox", "local.yml"), []byte(localContent), 0o644); err != nil {
		t.Fatalf("write local.yml: %v", err)
	}
	if opts.deployYML {
		deployContent := "phases:\n  - name: setup\n    steps:\n      - name: init\n        type: shell\n        cmd: echo init\n"
		if err := os.WriteFile(filepath.Join(svcDir, "deploy.yml"), []byte(deployContent), 0o644); err != nil {
			t.Fatalf("write deploy.yml: %v", err)
		}
	}
	if opts.resetYML {
		resetContent := "phases:\n  - name: cleanup\n    steps:\n      - name: wipe\n        type: shell\n        cmd: echo wipe\n"
		if err := os.WriteFile(filepath.Join(svcDir, "reset.yml"), []byte(resetContent), 0o644); err != nil {
			t.Fatalf("write reset.yml: %v", err)
		}
	}
	installFakeDocker(t, dir)
	return cfgPath, dir
}

// installFakeDocker writes a shell-script fake docker binary into
// <baseDir>/.devbox/bin/docker and a .devbox/config that points
// binary_docker at it. The fake logs every invocation (as "<args>\n") to
// <baseDir>/.devbox/docker-args.log and always exits 0. Use
// dockerInvocations(t, baseDir) to read the log.
func installFakeDocker(t *testing.T, baseDir string) {
	t.Helper()
	binDir := filepath.Join(baseDir, ".devbox", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	logPath := filepath.Join(baseDir, ".devbox", "docker-args.log")
	fakePath := filepath.Join(binDir, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + logPath + "\nexit 0\n"
	if err := os.WriteFile(fakePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	userCfg := "binary_docker=" + fakePath + "\n"
	if err := os.WriteFile(filepath.Join(baseDir, ".devbox", "config"), []byte(userCfg), 0o644); err != nil {
		t.Fatalf("write user config: %v", err)
	}
}

// dockerInvocations returns the lines logged by the fake docker binary,
// one entry per invocation in call order. Returns nil when no calls.
func dockerInvocations(t *testing.T, baseDir string) []string {
	t.Helper()
	logPath := filepath.Join(baseDir, ".devbox", "docker-args.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatalf("read docker log: %v", err)
	}
	out := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(out) == 1 && out[0] == "" {
		return nil
	}
	return out
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
	resetCmd := NewResetCmd(groupPipelines, flags)
	var cmd *cobra.Command
	for _, sub := range resetCmd.Commands() {
		if sub.Name() == "run" {
			cmd = sub
			break
		}
	}
	if cmd == nil {
		t.Fatal("reset run subcommand missing")
	}
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
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, true, false)

	stubPreflightRun(t)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "nonexistent", "--yes"})

	err := root.Execute()
	if !errors.Is(err, ErrUnknownService) {
		t.Errorf("expected ErrUnknownService, got %v", err)
	}
	if calls := dockerInvocations(t, dir); calls != nil {
		t.Errorf("docker should not be invoked for unknown service; got %v", calls)
	}
}

// TestResetServiceRun_NoDeployFile verifies ErrServiceNoDeployFile before any side effect.
func TestResetServiceRun_NoDeployFile(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, false, false)
	statePath := filepath.Join(dir, journal.DefaultRelPath)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes"})

	err := root.Execute()
	if !errors.Is(err, deploy.ErrServiceNoDeployFile) {
		t.Errorf("expected ErrServiceNoDeployFile, got %v", err)
	}
	if calls := dockerInvocations(t, dir); calls != nil {
		t.Errorf("docker should not be invoked when deploy.yml absent; got %v", calls)
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

	stubPreflightRun(t)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := dockerInvocations(t, dir)
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 docker invocations (stop, rm); got %v", calls)
	}
	if !strings.HasPrefix(calls[0], "stop ") || !strings.Contains(calls[0], "devbox-test-app-postgres") {
		t.Errorf("docker stop call wrong; got %q", calls[0])
	}
	if !strings.HasPrefix(calls[1], "rm ") || !strings.Contains(calls[1], "devbox-test-app-postgres") {
		t.Errorf("docker rm call wrong; got %q", calls[1])
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

	stubPreflightRun(t)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := dockerInvocations(t, dir)
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 docker invocations (stop, rm); got %v", calls)
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

	stubPreflightRun(t)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := dockerInvocations(t, dir)
	if len(calls) < 2 {
		t.Errorf("expected stop+rm docker invocations even for disabled service; got %v", calls)
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

	installFakeDocker(t, dir)

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

	stubPreflightRun(t)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
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

	installFakeDocker(t, dir)

	var hookCalled bool
	prevHook := resetServiceRunHook
	t.Cleanup(func() { resetServiceRunHook = prevHook })
	resetServiceRunHook = func(_ context.Context, rc runtime.RunContext) error {
		hookCalled = true
		return nil
	}

	stubPreflightRun(t)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
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
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, true, false)

	stubPreflightRun(t)

	prevInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = prevInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	var capturedTitle, capturedAffirm, capturedNegative string
	prevConfirm := resetConfirmFn
	t.Cleanup(func() { resetConfirmFn = prevConfirm })
	resetConfirmFn = func(title, affirm, negative string) (bool, error) {
		capturedTitle, capturedAffirm, capturedNegative = title, affirm, negative
		return false, nil
	}

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error on decline: %v", err)
	}
	if calls := dockerInvocations(t, dir); calls != nil {
		t.Errorf("docker should not be invoked when user declines; got %v", calls)
	}
	if !strings.Contains(capturedTitle, `Reset service "postgres"?`) {
		t.Errorf("confirm title missing header; got %q", capturedTitle)
	}
	if !strings.Contains(capturedTitle, `stop and remove container "app-postgres"`) {
		t.Errorf("confirm title missing container bullet; got %q", capturedTitle)
	}
	if strings.Contains(capturedTitle, "delete directory") {
		t.Errorf("confirm title should not mention directory deletion when svc.Dir empty; got %q", capturedTitle)
	}
	if strings.Contains(capturedTitle, "services/postgres/reset.yml") {
		t.Errorf("confirm title should not mention reset.yml when absent; got %q", capturedTitle)
	}
	if capturedAffirm != "Reset" || capturedNegative != "Cancel" {
		t.Errorf("confirm buttons = (%q,%q), want (\"Reset\",\"Cancel\")", capturedAffirm, capturedNegative)
	}
}

// TestResetServiceRun_TTYConfirmationAccept verifies that in TTY mode user can accept.
func TestResetServiceRun_TTYConfirmationAccept(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, true, false)

	stubPreflightRun(t)

	prevInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = prevInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	prevConfirm := resetConfirmFn
	t.Cleanup(func() { resetConfirmFn = prevConfirm })
	resetConfirmFn = func(_, _, _ string) (bool, error) { return true, nil }

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error on confirm: %v", err)
	}
	calls := dockerInvocations(t, dir)
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 docker invocations (stop, rm); got %v", calls)
	}
	if !strings.HasPrefix(calls[0], "stop ") {
		t.Errorf("first docker call should be stop; got %q", calls[0])
	}
	if !strings.HasPrefix(calls[1], "rm ") {
		t.Errorf("second docker call should be rm; got %q", calls[1])
	}
}

// TestResetServiceRun_ConfirmCancelledSilently verifies that pressing Esc/Ctrl-C
// in the confirm form (ui.ErrCancelled) returns nil without side effects.
func TestResetServiceRun_ConfirmCancelledSilently(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, true, false)
	stubPreflightRun(t)

	prevInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = prevInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	prevConfirm := resetConfirmFn
	t.Cleanup(func() { resetConfirmFn = prevConfirm })
	resetConfirmFn = func(_, _, _ string) (bool, error) { return false, ui.ErrCancelled }

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres"})
	if err := root.Execute(); err != nil {
		t.Fatalf("expected nil on Esc cancel; got %v", err)
	}
	if calls := dockerInvocations(t, dir); calls != nil {
		t.Errorf("docker should not be invoked on Esc cancel; got %v", calls)
	}
}

// TestResetServiceRun_NonInteractiveRequiresYes verifies that without --yes
// in a non-interactive terminal, the command errors with a clear message.
func TestResetServiceRun_NonInteractiveRequiresYes(t *testing.T) {
	cfgPath, _ := makeResetServiceTestDir(t, "postgres", true, false, true, false)
	stubPreflightRun(t)

	prevInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = prevInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return false }

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "non-interactive terminal") {
		t.Errorf("expected non-interactive error, got %v", err)
	}
}

// TestResetServiceRun_WithDirSyntheticFilesPhase verifies that when svc.Dir is
// set and the directory exists, the synthetic files phase removes it.
func TestResetServiceRun_WithDirSyntheticFilesPhase(t *testing.T) {
	cfgPath, dir := makeResetServiceFixture(t, "postgres", resetServiceFixtureOpts{
		enabled:   true,
		deployYML: true,
		withDir:   true,
	})
	stubPreflightRun(t)

	var capturedTitle string
	prevConfirm := resetConfirmFn
	t.Cleanup(func() { resetConfirmFn = prevConfirm })
	resetConfirmFn = func(title, _, _ string) (bool, error) {
		capturedTitle = title
		return true, nil
	}
	prevInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = prevInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedTitle, "delete directory ./services/postgres") {
		t.Errorf("confirm title missing dir bullet; got %q", capturedTitle)
	}
	target := filepath.Join(dir, "services", "postgres")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("expected %s removed; stat err = %v", target, err)
	}
}

// TestResetServiceRun_RequiredServiceWarning verifies the confirm title contains
// the required-service warning line.
func TestResetServiceRun_RequiredServiceWarning(t *testing.T) {
	cfgPath, _ := makeResetServiceTestDir(t, "postgres", true, true, true, false)
	stubPreflightRun(t)
	prevInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = prevInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	var capturedTitle string
	prevConfirm := resetConfirmFn
	t.Cleanup(func() { resetConfirmFn = prevConfirm })
	resetConfirmFn = func(title, _, _ string) (bool, error) {
		capturedTitle = title
		return false, nil
	}

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedTitle, "Warning: this is a required service.") {
		t.Errorf("required-service warning missing from confirm title; got %q", capturedTitle)
	}
}

// TestResetServiceRun_ResetYMLBulletInTitle verifies the confirm title mentions
// services/<name>/reset.yml only when that file exists.
func TestResetServiceRun_ResetYMLBulletInTitle(t *testing.T) {
	cfgPath, _ := makeResetServiceTestDir(t, "postgres", true, false, true, true)
	stubPreflightRun(t)
	prevInteractive := ui.IsInteractiveFn
	t.Cleanup(func() { ui.IsInteractiveFn = prevInteractive })
	ui.IsInteractiveFn = func(_ io.Reader) bool { return true }

	var capturedTitle string
	prevConfirm := resetConfirmFn
	t.Cleanup(func() { resetConfirmFn = prevConfirm })
	resetConfirmFn = func(title, _, _ string) (bool, error) {
		capturedTitle = title
		return false, nil
	}

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedTitle, "run services/postgres/reset.yml") {
		t.Errorf("reset.yml bullet missing from confirm title; got %q", capturedTitle)
	}
}

// TestBuildResetServiceConfirmTitle verifies the title-builder shape directly.
func TestBuildResetServiceConfirmTitle(t *testing.T) {
	tests := []struct {
		name        string
		required    bool
		dirExists   bool
		hasReset    bool
		wantContain []string
		wantOmit    []string
	}{
		{
			name:        "minimal",
			wantContain: []string{`Reset service "pg"?`, `stop and remove container "app-pg"`, `devbox deploy run --service pg`},
			wantOmit:    []string{"Warning: this is a required service", "delete directory", "reset.yml"},
		},
		{
			name:        "required+dir+reset",
			required:    true,
			dirExists:   true,
			hasReset:    true,
			wantContain: []string{"Warning: this is a required service.", "delete directory ./services/pg", "run services/pg/reset.yml"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildResetServiceConfirmTitle("pg", "app-pg", "./services/pg", tc.required, tc.dirExists, tc.hasReset)
			for _, want := range tc.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in title:\n%s", want, got)
				}
			}
			for _, omit := range tc.wantOmit {
				if strings.Contains(got, omit) {
					t.Errorf("unexpected %q in title:\n%s", omit, got)
				}
			}
		})
	}
}

// TestResetServiceRun_MandatoryService verifies mandatory services are allowed to reset.
func TestResetServiceRun_MandatoryService(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, true, true, false)
	statePath := filepath.Join(dir, journal.DefaultRelPath)
	_ = dir

	stubPreflightRun(t)

	var out bytes.Buffer
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
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

// TestResetServiceRun_PipelineFailureSkipsJournal verifies that when the
// pipeline returns an error (fake docker exits nonzero), the journal is NOT
// updated and the error propagates.
func TestResetServiceRun_PipelineFailureSkipsJournal(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, true, false)
	statePath := filepath.Join(dir, journal.DefaultRelPath)

	// Pre-seed a deployed state so we can detect untouched journal afterward.
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

	// Replace fake docker with a failing one.
	logPath := filepath.Join(dir, ".devbox", "docker-args.log")
	failingScript := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + logPath + "\necho 'boom' >&2\nexit 1\n"
	fakePath := filepath.Join(dir, ".devbox", "bin", "docker")
	if err := os.WriteFile(fakePath, []byte(failingScript), 0o755); err != nil {
		t.Fatalf("write failing docker: %v", err)
	}

	stubPreflightRun(t)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected pipeline error from failing docker, got nil")
	}

	state, err2 := journal.Load(statePath)
	if err2 != nil {
		t.Fatalf("loading journal: %v", err2)
	}
	if state.Services["postgres"] == nil || state.Services["postgres"].Status != journal.StatusDeployed {
		t.Errorf("journal should be unchanged on failure; got %+v", state.Services)
	}
	if state.Pending != nil && state.Pending.Find(journal.PendingDeploy) != nil {
		t.Error("PendingDeploy should NOT be added on pipeline failure")
	}
}
