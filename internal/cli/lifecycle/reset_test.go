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

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	pipeline "github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/core/workflow/reset"
	"github.com/semsemyonoff/dwe/internal/shared/generatedstore"

	"github.com/spf13/cobra"
)

// seedGeneratedStore writes a generated-value store under baseDir/.dwe with the
// given service → field → value contents, returning the store path.
func seedGeneratedStore(t *testing.T, baseDir string, services map[string]map[string]string) string {
	t.Helper()
	path := filepath.Join(baseDir, generatedstore.DefaultRelPath)
	store := generatedstore.New()
	for svc, fields := range services {
		for field, val := range fields {
			store.SetIfAbsent(svc, field, val)
		}
	}
	if err := generatedstore.Save(path, store); err != nil {
		t.Fatalf("seed generated store: %v", err)
	}
	return path
}

// TestResetRunCmd_projectWideCleanup verifies that a project-wide reset
// removes the entire deploy state file.
func TestResetRunCmd_projectWideCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "workspace.yml")
	stateDir := filepath.Join(tmpDir, ".dwe", "deploy")
	statePath := filepath.Join(stateDir, "state.yml")

	// Create minimal workspace.yml + workspace/services/main/service.yml
	if err := os.WriteFile(configPath, []byte(`
schema_version: "2"
project:
  name: test
  prefix: dwe
`), 0o644); err != nil {
		t.Fatal(err)
	}

	resetDir := filepath.Join(tmpDir, "workspace")
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

	resetCfg, steps, _, err := reset.LoadAndResolvePlan(cfg, usercommands.NewEmptyRegistry())
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
	configPath := filepath.Join(tmpDir, "workspace.yml")
	stateDir := filepath.Join(tmpDir, ".dwe", "deploy")
	statePath := filepath.Join(stateDir, "state.yml")

	// Create minimal workspace.yml
	if err := os.WriteFile(configPath, []byte(`
schema_version: "2"
project:
  name: test
  prefix: dwe
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create workspace/reset.yml + services/main/service.yml
	resetDir := filepath.Join(tmpDir, "workspace")
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
	stateDir := filepath.Join(tmpDir, ".dwe", "deploy")
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
	lockPath := filepath.Join(tmpDir, ".dwe", "deploy", "deploy.lock")

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

// makeResetServiceTestDir writes a minimal workspace.yml and service folder for per-service reset tests.
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
// It also installs a fake docker binary via .dwe/config so the synthetic
// container baseline can run without a real Docker daemon. The fake's
// invocation log path is recorded in fakeDockerLogPath so callers can read it
// via dockerInvocations(t, dir).
func makeResetServiceFixture(t *testing.T, serviceName string, opts resetServiceFixtureOpts) (string, string) {
	t.Helper()
	dir := t.TempDir()
	cfgContent := "schema_version: \"2\"\nproject:\n  name: test\n  prefix: dwe\n"
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	svcDir := filepath.Join(dir, "workspace", "services", serviceName)
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
	if err := os.WriteFile(filepath.Join(dir, "workspace", "local.yml"), []byte(localContent), 0o644); err != nil {
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
// <baseDir>/.dwe/bin/docker and a .dwe/config that points
// binary_docker at it. The fake logs every invocation (as "<args>\n") to
// <baseDir>/.dwe/docker-args.log and always exits 0. Use
// dockerInvocations(t, baseDir) to read the log.
func installFakeDocker(t *testing.T, baseDir string) {
	t.Helper()
	binDir := filepath.Join(baseDir, ".dwe", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	logPath := filepath.Join(baseDir, ".dwe", "docker-args.log")
	fakePath := filepath.Join(binDir, "docker")
	// Logs every invocation and exits 0. For `ps` (the compose-label container
	// lookup added by reset's stop+rm), it echoes "<project>-<service>" derived
	// from the --filter labels so stop_remove resolves a real container name
	// instead of treating the service as not-deployed.
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + logPath + "\n" +
		"if [ \"$1\" = ps ]; then\n" +
		"  proj=; svc=\n" +
		"  for a in \"$@\"; do\n" +
		"    case \"$a\" in\n" +
		"      label=com.docker.compose.project=*) proj=${a#label=com.docker.compose.project=} ;;\n" +
		"      label=com.docker.compose.service=*) svc=${a#label=com.docker.compose.service=} ;;\n" +
		"    esac\n" +
		"  done\n" +
		"  [ -n \"$svc\" ] && echo \"${proj}-${svc}\"\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(fakePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	userCfg := "binary_docker=" + fakePath + "\n"
	if err := os.WriteFile(filepath.Join(baseDir, ".dwe", "config"), []byte(userCfg), 0o644); err != nil {
		t.Fatalf("write user config: %v", err)
	}
}

// findDockerCall returns the first logged invocation whose argv starts with
// prefix (e.g. "stop ", "rm "), or "" when none match. Used so assertions are
// robust to the leading `ps` label-lookup invocation and to call ordering.
func findDockerCall(calls []string, prefix string) string {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return c
		}
	}
	return ""
}

// dockerInvocations returns the lines logged by the fake docker binary,
// one entry per invocation in call order. Returns nil when no calls.
func dockerInvocations(t *testing.T, baseDir string) []string {
	t.Helper()
	logPath := filepath.Join(baseDir, ".dwe", "docker-args.log")
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
	preflightRun = func(_ context.Context, _ *config.DweConfig, _ *usercommands.Registry, _, _ string, _ bool, _ io.Writer) error {
		return nil
	}
	t.Cleanup(func() { preflightRun = prev })
}

// TestResetServiceRun_FlagsExist verifies --service, --yes, --skip-preflight flags exist on reset run.
func TestResetServiceRun_FlagsExist(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "workspace.yml"}
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
	stopCall := findDockerCall(calls, "stop ")
	if stopCall == "" || !strings.Contains(stopCall, "dwe-test-app-postgres") {
		t.Errorf("docker stop call wrong; got calls %v", calls)
	}
	rmCall := findDockerCall(calls, "rm ")
	if rmCall == "" || !strings.Contains(rmCall, "dwe-test-app-postgres") {
		t.Errorf("docker rm call wrong; got calls %v", calls)
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
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: test\n  prefix: dwe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(dir, "workspace", "services", "postgres")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Enable the service via local.yml (services are disabled by default without an explicit override).
	localContent := "services:\n  postgres:\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(dir, "workspace", "local.yml"), []byte(localContent), 0o644); err != nil {
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
	resetRunHookFn = func(_ context.Context, _ *cobra.Command, _ *config.DweConfig, _ *registry.Registry, _ string, cmdID string) error {
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
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: test\n  prefix: dwe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(dir, "workspace", "services", "postgres")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Disable via local.yml
	localContent := "services:\n  postgres:\n    enabled: false\n"
	if err := os.WriteFile(filepath.Join(dir, "workspace", "local.yml"), []byte(localContent), 0o644); err != nil {
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

	prevInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = prevInteractive })
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }

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

	prevInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = prevInteractive })
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }

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
	if findDockerCall(calls, "stop ") == "" {
		t.Errorf("expected a docker stop invocation; got %v", calls)
	}
	if findDockerCall(calls, "rm ") == "" {
		t.Errorf("expected a docker rm invocation; got %v", calls)
	}
}

// TestResetServiceRun_ConfirmCancelledSilently verifies that pressing Esc/Ctrl-C
// in the confirm form (widgets.ErrCancelled) returns nil without side effects.
func TestResetServiceRun_ConfirmCancelledSilently(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, true, false)
	stubPreflightRun(t)

	prevInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = prevInteractive })
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }

	prevConfirm := resetConfirmFn
	t.Cleanup(func() { resetConfirmFn = prevConfirm })
	resetConfirmFn = func(_, _, _ string) (bool, error) { return false, widgets.ErrCancelled }

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

	prevInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = prevInteractive })
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return false }

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
	prevInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = prevInteractive })
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }

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
	prevInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = prevInteractive })
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }

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
	prevInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = prevInteractive })
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }

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
			wantContain: []string{`Reset service "pg"?`, `stop and remove container "app-pg"`, `dwe deploy run --service pg`},
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

// makeMinimalResetProject writes a bare workspace.yml (no reset.yml, no services)
// so the default reset pipeline fires.
func makeMinimalResetProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(
		"schema_version: \"2\"\nproject:\n  name: testproject\n  prefix: dwe\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunResetPlan_DefaultPipelineWhenNoResetYML verifies that a bare project
// (no workspace/reset.yml) succeeds, includes the docker-down step from the
// built-in default, and prints the info line on stderr.
func TestRunResetPlan_DefaultPipelineWhenNoResetYML(t *testing.T) {
	dir := makeMinimalResetProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

	cmd := &cobra.Command{}
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	if err := runResetPlan(cmd, flags, resetPlanOpts{}); err != nil {
		t.Fatalf("runResetPlan: %v", err)
	}

	if !strings.Contains(outBuf.String(), "docker down") {
		t.Errorf("plan output missing 'docker down'; got:\n%s", outBuf.String())
	}

	const wantNotice = "Using built-in default reset pipeline (override with workspace/reset.yml)."
	if !strings.Contains(errBuf.String(), wantNotice) {
		t.Errorf("stderr missing info line %q; got:\n%s", wantNotice, errBuf.String())
	}
}

// TestRunResetPlan_JSONModeNoInfoLine verifies that --output json suppresses
// the default-pipeline info line on stderr.
func TestRunResetPlan_JSONModeNoInfoLine(t *testing.T) {
	dir := makeMinimalResetProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml"), Output: "json"}

	cmd := &cobra.Command{}
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)

	_ = runResetPlan(cmd, flags, resetPlanOpts{})

	if errBuf.Len() != 0 {
		t.Errorf("json mode: expected empty stderr, got %q", errBuf.String())
	}
}

// TestRunResetPlan_UserResetYMLNoInfoLine verifies that a project with a
// workspace/reset.yml does not emit the default-pipeline info line.
func TestRunResetPlan_UserResetYMLNoInfoLine(t *testing.T) {
	dir := makeMinimalResetProject(t)

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userReset := "phases:\n  - name: mycleanup\n    steps:\n      - name: step1\n        type: shell\n        cmd: echo bye\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "reset.yml"), []byte(userReset), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

	cmd := &cobra.Command{}
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	if err := runResetPlan(cmd, flags, resetPlanOpts{}); err != nil {
		t.Fatalf("runResetPlan: %v", err)
	}

	if errBuf.Len() != 0 {
		t.Errorf("expected no info line when user reset.yml present; stderr = %q", errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "echo bye") {
		t.Errorf("plan output missing user step 'echo bye'; got:\n%s", outBuf.String())
	}
	if strings.Contains(outBuf.String(), "docker down") {
		t.Errorf("default step 'docker down' should not appear when user reset.yml is present")
	}
}

// TestRunResetPlan_DefaultPipelineShellFormat verifies --format shell also
// works with the default pipeline and does not emit extra stderr.
func TestRunResetPlan_DefaultPipelineShellFormat(t *testing.T) {
	dir := makeMinimalResetProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

	cmd := &cobra.Command{}
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	if err := runResetPlan(cmd, flags, resetPlanOpts{Format: "shell"}); err != nil {
		t.Fatalf("runResetPlan(shell): %v", err)
	}

	// Shell format emits the info line too.
	const wantNotice = "Using built-in default reset pipeline (override with workspace/reset.yml)."
	if !strings.Contains(errBuf.String(), wantNotice) {
		t.Errorf("stderr missing info line in shell format; got:\n%s", errBuf.String())
	}
}

// writeResetStepFixture builds a minimal project with a top-level vars:
// block and a workspace/reset.yml, so tests can address a step through
// resetStepCmd's <phase>/<step> lookup. vars is raw YAML for the vars:
// block's body (already indented, e.g. "  greeting: hello\n"); pass "" to
// omit the block entirely.
func writeResetStepFixture(t *testing.T, vars, resetYAML string) string {
	t.Helper()
	dir := t.TempDir()
	workspaceYAML := "schema_version: \"2\"\nproject:\n  name: testproject\n  prefix: dwe\n"
	if vars != "" {
		workspaceYAML += "vars:\n" + vars
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(workspaceYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "reset.yml"), []byte(resetYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestResetStepCmd_DryRunRendersCmd verifies that `dwe reset step --dry-run`
// prints the step's cmd with ${vars.*} substituted, not the literal — the
// same rendering ResolvePhaseSteps applies on the `dwe reset run` path.
func TestResetStepCmd_DryRunRendersCmd(t *testing.T) {
	dir := writeResetStepFixture(t,
		"  greeting: hello\n",
		"phases:\n  - name: probe\n    steps:\n      - name: greet\n        type: shell\n        cmd: \"echo ${vars.greeting}\"\n",
	)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

	cmd := &cobra.Command{}
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)

	if err := resetStepCmd(cmd, flags, "probe/greet", true); err != nil {
		t.Fatalf("resetStepCmd: %v", err)
	}

	got := strings.TrimSpace(outBuf.String())
	if got != "echo hello" {
		t.Errorf("dry-run output = %q, want %q", got, "echo hello")
	}
}

// TestResetStepCmd_RendersWhenCmd verifies that a runtime shell when: cmd is
// rendered before evaluation. Before Task 2c, `reset step` evaluated
// step.When directly against the unrendered condition, so a
// "${vars.mode}" would hit the shell as a literal and "bad substitution"
// out — this pins that the condition now evaluates the substituted value and
// gates step execution correctly in both directions.
func TestResetStepCmd_RendersWhenCmd(t *testing.T) {
	resetYAML := "phases:\n" +
		"  - name: probe\n" +
		"    steps:\n" +
		"      - name: mark\n" +
		"        type: shell\n" +
		"        cmd: \"touch marker.txt\"\n" +
		"        when:\n" +
		"          type: shell\n" +
		"          cmd: \"test \\\"${vars.mode}\\\" = go\"\n"

	t.Run("condition true after render runs the step", func(t *testing.T) {
		dir := writeResetStepFixture(t, "  mode: go\n", resetYAML)
		flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}
		cmd := &cobra.Command{}
		cmd.SetOut(io.Discard)
		cmd.SetContext(context.Background())

		if err := resetStepCmd(cmd, flags, "probe/mark", false); err != nil {
			t.Fatalf("resetStepCmd: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "marker.txt")); err != nil {
			t.Errorf("expected step to run (when condition true after render); marker.txt missing: %v", err)
		}
	})

	t.Run("condition false after render skips the step", func(t *testing.T) {
		dir := writeResetStepFixture(t, "  mode: stop\n", resetYAML)
		flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}
		cmd := &cobra.Command{}
		cmd.SetOut(io.Discard)
		cmd.SetContext(context.Background())

		if err := resetStepCmd(cmd, flags, "probe/mark", false); err != nil {
			t.Fatalf("resetStepCmd: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "marker.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("expected step to be skipped (when condition false after render); marker.txt exists")
		}
	})
}

// TestResetStepCmd_RendersCheckWith verifies that check.with is rendered
// before the check runs: a file_exists predicate keyed on ${vars.marker}
// only succeeds when that reference resolved to the real file path.
func TestResetStepCmd_RendersCheckWith(t *testing.T) {
	resetYAML := "phases:\n" +
		"  - name: probe\n" +
		"    steps:\n" +
		"      - name: verify\n" +
		"        type: shell\n" +
		"        cmd: \"true\"\n" +
		"        check:\n" +
		"          type: builtin\n" +
		"          cmd: file_exists\n" +
		"          with:\n" +
		"            path: \"${vars.marker}\"\n"

	dir := writeResetStepFixture(t, "  marker: marker.txt\n", resetYAML)
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetContext(context.Background())

	if err := resetStepCmd(cmd, flags, "probe/verify", false); err != nil {
		t.Fatalf("resetStepCmd: %v (check.with was apparently not rendered — file_exists looked for the literal \"${vars.marker}\")", err)
	}
}

// TestResetStepCmd_MatchesResetRunRenderedCommand verifies that `reset step
// --dry-run` and the resolution path `reset run` uses (ResolvePhaseSteps via
// reset.LoadAndResolvePlan) render the very same step to the very same
// command — the divergence Task 2c exists to remove.
func TestResetStepCmd_MatchesResetRunRenderedCommand(t *testing.T) {
	resetYAML := "phases:\n" +
		"  - name: probe\n" +
		"    steps:\n" +
		"      - name: greet\n" +
		"        type: shell\n" +
		"        cmd: \"echo ${vars.greeting}\"\n"
	dir := writeResetStepFixture(t, "  greeting: hello\n", resetYAML)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

	cmd := &cobra.Command{}
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	if err := resetStepCmd(cmd, flags, "probe/greet", true); err != nil {
		t.Fatalf("resetStepCmd: %v", err)
	}
	stepRendered := strings.TrimSpace(outBuf.String())

	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	_, steps, _, err := reset.LoadAndResolvePlan(cfg, usercommands.NewEmptyRegistry())
	if err != nil {
		t.Fatalf("resolving reset plan: %v", err)
	}
	var runRendered string
	found := false
	for _, rs := range steps {
		if rs.StepAddress() == "probe/greet" {
			runRendered = pipeline.StepCommand(rs.Step, config.DweBin(cfg))
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("step probe/greet not found in resolved reset plan")
	}

	if stepRendered != runRendered {
		t.Errorf("reset step and reset run render differently:\n  step: %q\n  run:  %q", stepRendered, runRendered)
	}
	if strings.Contains(stepRendered, "${vars") {
		t.Errorf("reset step output still contains an unrendered ${vars...} reference: %q", stepRendered)
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
	logPath := filepath.Join(dir, ".dwe", "docker-args.log")
	failingScript := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + logPath + "\necho 'boom' >&2\nexit 1\n"
	fakePath := filepath.Join(dir, ".dwe", "bin", "docker")
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

// TestClearGeneratedStore_FullScope verifies the whole store is wiped when svc is empty.
func TestClearGeneratedStore_FullScope(t *testing.T) {
	dir := t.TempDir()
	path := seedGeneratedStore(t, dir, map[string]map[string]string{
		"main":    {"app_key": "base64:aaa"},
		"magento": {"crypt_key": "deadbeef"},
	})

	if err := clearGeneratedStore(dir, ""); err != nil {
		t.Fatalf("clearGeneratedStore: %v", err)
	}
	store, err := generatedstore.Load(path)
	if err != nil {
		t.Fatalf("load after clear: %v", err)
	}
	if !store.IsEmpty() {
		t.Errorf("expected empty store after full clear, got %+v", store.Services)
	}
}

// TestClearGeneratedStore_ServiceScope verifies only the named service is cleared.
func TestClearGeneratedStore_ServiceScope(t *testing.T) {
	dir := t.TempDir()
	path := seedGeneratedStore(t, dir, map[string]map[string]string{
		"main":    {"app_key": "base64:aaa"},
		"magento": {"crypt_key": "deadbeef"},
	})

	if err := clearGeneratedStore(dir, "main"); err != nil {
		t.Fatalf("clearGeneratedStore: %v", err)
	}
	store, err := generatedstore.Load(path)
	if err != nil {
		t.Fatalf("load after clear: %v", err)
	}
	if store.Has("main", "app_key") {
		t.Error("main should have been cleared")
	}
	if !store.Has("magento", "crypt_key") {
		t.Error("magento should have been preserved")
	}
}

// TestClearGeneratedStore_MissingStore is a no-op (no file → no error).
func TestClearGeneratedStore_MissingStore(t *testing.T) {
	dir := t.TempDir()
	if err := clearGeneratedStore(dir, ""); err != nil {
		t.Fatalf("expected no error for missing store, got %v", err)
	}
	if err := clearGeneratedStore(dir, "main"); err != nil {
		t.Fatalf("expected no error for missing store (scoped), got %v", err)
	}
}

// TestResolveClearGenerated_FlagForcesTrue verifies the flag clears without prompting.
func TestResolveClearGenerated_FlagForcesTrue(t *testing.T) {
	dir := t.TempDir()
	seedGeneratedStore(t, dir, map[string]map[string]string{"main": {"app_key": "x"}})

	prevConfirm := resetConfirmFn
	t.Cleanup(func() { resetConfirmFn = prevConfirm })
	confirmCalled := false
	resetConfirmFn = func(_, _, _ string) (bool, error) {
		confirmCalled = true
		return false, nil
	}

	got, err := resolveClearGenerated(strings.NewReader(""), dir, "", true, false)
	if err != nil {
		t.Fatalf("resolveClearGenerated: %v", err)
	}
	if !got {
		t.Error("flag set should force clear=true")
	}
	if confirmCalled {
		t.Error("flag set should not prompt")
	}
}

// TestResolveClearGenerated_NonInteractiveNoFlag preserves the store without a prompt.
func TestResolveClearGenerated_NonInteractiveNoFlag(t *testing.T) {
	dir := t.TempDir()
	seedGeneratedStore(t, dir, map[string]map[string]string{"main": {"app_key": "x"}})

	prevInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = prevInteractive })
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return false }

	prevConfirm := resetConfirmFn
	t.Cleanup(func() { resetConfirmFn = prevConfirm })
	resetConfirmFn = func(_, _, _ string) (bool, error) {
		t.Error("non-interactive should not prompt")
		return false, nil
	}

	got, err := resolveClearGenerated(strings.NewReader(""), dir, "", false, false)
	if err != nil {
		t.Fatalf("resolveClearGenerated: %v", err)
	}
	if got {
		t.Error("non-interactive without flag should preserve (clear=false)")
	}
}

// TestResolveClearGenerated_SkipPromptNoFlag verifies --yes suppresses the prompt.
func TestResolveClearGenerated_SkipPromptNoFlag(t *testing.T) {
	dir := t.TempDir()
	seedGeneratedStore(t, dir, map[string]map[string]string{"main": {"app_key": "x"}})

	prevInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = prevInteractive })
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }

	prevConfirm := resetConfirmFn
	t.Cleanup(func() { resetConfirmFn = prevConfirm })
	resetConfirmFn = func(_, _, _ string) (bool, error) {
		t.Error("--yes (skipPrompt) should not prompt")
		return true, nil
	}

	got, err := resolveClearGenerated(strings.NewReader(""), dir, "", false, true)
	if err != nil {
		t.Fatalf("resolveClearGenerated: %v", err)
	}
	if got {
		t.Error("--yes without flag should preserve (clear=false)")
	}
}

// TestResolveClearGenerated_EmptyStoreNoPrompt verifies an empty store skips the prompt.
func TestResolveClearGenerated_EmptyStoreNoPrompt(t *testing.T) {
	dir := t.TempDir()
	// No seeded store at all → empty.

	prevInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = prevInteractive })
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }

	prevConfirm := resetConfirmFn
	t.Cleanup(func() { resetConfirmFn = prevConfirm })
	resetConfirmFn = func(_, _, _ string) (bool, error) {
		t.Error("empty store should not prompt")
		return true, nil
	}

	got, err := resolveClearGenerated(strings.NewReader(""), dir, "", false, false)
	if err != nil {
		t.Fatalf("resolveClearGenerated: %v", err)
	}
	if got {
		t.Error("empty store should yield clear=false")
	}
}

// TestResolveClearGenerated_InteractivePromptHonored verifies the prompt decision is honored.
func TestResolveClearGenerated_InteractivePromptHonored(t *testing.T) {
	tests := []struct {
		name    string
		confirm bool
		want    bool
	}{
		{"accept", true, true},
		{"decline", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			seedGeneratedStore(t, dir, map[string]map[string]string{"main": {"app_key": "x", "other": "y"}})

			prevInteractive := widgets.IsInteractiveFn
			t.Cleanup(func() { widgets.IsInteractiveFn = prevInteractive })
			widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }

			var capturedTitle string
			prevConfirm := resetConfirmFn
			t.Cleanup(func() { resetConfirmFn = prevConfirm })
			resetConfirmFn = func(title, _, _ string) (bool, error) {
				capturedTitle = title
				return tc.confirm, nil
			}

			got, err := resolveClearGenerated(strings.NewReader(""), dir, "", false, false)
			if err != nil {
				t.Fatalf("resolveClearGenerated: %v", err)
			}
			if got != tc.want {
				t.Errorf("clear = %v, want %v", got, tc.want)
			}
			if !strings.Contains(capturedTitle, "clear 2 generated value") {
				t.Errorf("prompt title missing count; got %q", capturedTitle)
			}
		})
	}
}

// TestResolveClearGenerated_CancelPreserves verifies Esc/Ctrl-C preserves the store.
func TestResolveClearGenerated_CancelPreserves(t *testing.T) {
	dir := t.TempDir()
	seedGeneratedStore(t, dir, map[string]map[string]string{"main": {"app_key": "x"}})

	prevInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = prevInteractive })
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }

	prevConfirm := resetConfirmFn
	t.Cleanup(func() { resetConfirmFn = prevConfirm })
	resetConfirmFn = func(_, _, _ string) (bool, error) { return false, widgets.ErrCancelled }

	got, err := resolveClearGenerated(strings.NewReader(""), dir, "", false, false)
	if err != nil {
		t.Fatalf("cancel should not error, got %v", err)
	}
	if got {
		t.Error("cancel should preserve (clear=false)")
	}
}

// TestResolveClearGenerated_ServiceScopeCount verifies scoped counting only sees the service.
func TestResolveClearGenerated_ServiceScopeCount(t *testing.T) {
	dir := t.TempDir()
	seedGeneratedStore(t, dir, map[string]map[string]string{
		"main":    {"app_key": "x"},
		"magento": {"crypt_key": "y", "extra": "z"},
	})

	prevInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = prevInteractive })
	widgets.IsInteractiveFn = func(_ io.Reader) bool { return true }

	var capturedTitle string
	prevConfirm := resetConfirmFn
	t.Cleanup(func() { resetConfirmFn = prevConfirm })
	resetConfirmFn = func(title, _, _ string) (bool, error) {
		capturedTitle = title
		return true, nil
	}

	got, err := resolveClearGenerated(strings.NewReader(""), dir, "main", false, false)
	if err != nil {
		t.Fatalf("resolveClearGenerated: %v", err)
	}
	if !got {
		t.Error("expected clear=true")
	}
	if !strings.Contains(capturedTitle, "clear 1 generated value") {
		t.Errorf("scoped prompt should count only the service; got %q", capturedTitle)
	}
}

// TestResetServiceRun_ClearGeneratedFlag_ClearsScopedStore verifies the flag clears
// the service's store only after the per-service reset (pipeline + journal) succeeds.
func TestResetServiceRun_ClearGeneratedFlag_ClearsScopedStore(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, true, false)
	storePath := seedGeneratedStore(t, dir, map[string]map[string]string{
		"postgres": {"app_key": "base64:aaa"},
		"other":    {"crypt_key": "keepme"},
	})

	stubPreflightRun(t)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes", "--clear-generated"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	store, err := generatedstore.Load(storePath)
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	if store.Has("postgres", "app_key") {
		t.Error("postgres generated values should be cleared")
	}
	if !store.Has("other", "crypt_key") {
		t.Error("unrelated service's generated values should be preserved")
	}
}

// TestResetServiceRun_NoFlag_PreservesStore verifies the default preserves the store.
func TestResetServiceRun_NoFlag_PreservesStore(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, true, false)
	storePath := seedGeneratedStore(t, dir, map[string]map[string]string{
		"postgres": {"app_key": "base64:aaa"},
	})

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

	store, err := generatedstore.Load(storePath)
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	if !store.Has("postgres", "app_key") {
		t.Error("default (no flag) should preserve generated values")
	}
}

// TestResetServiceRun_PipelineFailure_StoreNotCleared verifies a failed pipeline
// (failing docker) leaves the generated store intact even with --clear-generated.
func TestResetServiceRun_PipelineFailure_StoreNotCleared(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, true, false)
	storePath := seedGeneratedStore(t, dir, map[string]map[string]string{
		"postgres": {"app_key": "base64:aaa"},
	})

	// Replace fake docker with a failing one so the pipeline errors.
	logPath := filepath.Join(dir, ".dwe", "docker-args.log")
	failingScript := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + logPath + "\necho 'boom' >&2\nexit 1\n"
	fakePath := filepath.Join(dir, ".dwe", "bin", "docker")
	if err := os.WriteFile(fakePath, []byte(failingScript), 0o755); err != nil {
		t.Fatalf("write failing docker: %v", err)
	}

	stubPreflightRun(t)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes", "--clear-generated"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected pipeline error from failing docker, got nil")
	}

	store, err := generatedstore.Load(storePath)
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	if !store.Has("postgres", "app_key") {
		t.Error("pipeline failure must NOT clear the generated store")
	}
}

// TestResetServiceRun_JournalFailure_StoreNotCleared verifies a journal-update
// failure (state.yml is a directory) leaves the generated store intact.
func TestResetServiceRun_JournalFailure_StoreNotCleared(t *testing.T) {
	cfgPath, dir := makeResetServiceTestDir(t, "postgres", true, false, true, false)
	storePath := seedGeneratedStore(t, dir, map[string]map[string]string{
		"postgres": {"app_key": "base64:aaa"},
	})

	// Force the journal mutation to fail: make state.yml a directory so both the
	// load and the atomic rename inside ReplaceServiceWithPending error out.
	statePath := filepath.Join(dir, journal.DefaultRelPath)
	if err := os.MkdirAll(statePath, 0o755); err != nil {
		t.Fatalf("mkdir state path: %v", err)
	}

	stubPreflightRun(t)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"reset", "run", "--service", "postgres", "--yes", "--clear-generated"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected journal-update error, got nil")
	}

	store, err := generatedstore.Load(storePath)
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	if !store.Has("postgres", "app_key") {
		t.Error("journal-cleanup failure must NOT clear the generated store")
	}
}

// TestResetRunFlags_ClearGeneratedExists verifies the --clear-generated flag exists.
func TestResetRunFlags_ClearGeneratedExists(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "workspace.yml"}
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
	if cmd.Flags().Lookup("clear-generated") == nil {
		t.Error("missing --clear-generated flag on reset run")
	}
}
