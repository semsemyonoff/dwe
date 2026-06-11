package lifecycle

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	lifecyclepkg "github.com/semsemyonoff/dwe/internal/core/workflow/lifecycle"
)

// recordResetBridgeStop swaps the same-package reset seam for a recorder.
func recordResetBridgeStop(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	prev := bridgeStopDaemonFn
	t.Cleanup(func() { bridgeStopDaemonFn = prev })
	bridgeStopDaemonFn = func(bridgeDir string) (bool, error) {
		calls = append(calls, bridgeDir)
		return true, nil
	}
	return &calls
}

// recordWorkflowBridgeSeams swaps the workflow-package bridge seams (already
// no-op'd by the init in testhelpers_test.go) for recorders, so per-service
// CLI paths can assert the daemon is never touched.
func recordWorkflowBridgeSeams(t *testing.T) (prepares *[]bridge.PrepareOptions, stops *[]string) {
	t.Helper()
	var p []bridge.PrepareOptions
	var s []string
	prevPrepare, prevStop := lifecyclepkg.BridgePrepareFunc, lifecyclepkg.BridgeStopDaemonFunc
	t.Cleanup(func() {
		lifecyclepkg.BridgePrepareFunc, lifecyclepkg.BridgeStopDaemonFunc = prevPrepare, prevStop
	})
	lifecyclepkg.BridgePrepareFunc = func(opts bridge.PrepareOptions) error {
		p = append(p, opts)
		return nil
	}
	lifecyclepkg.BridgeStopDaemonFunc = func(bridgeDir string) (bool, error) {
		s = append(s, bridgeDir)
		return true, nil
	}
	return &p, &s
}

// TestResetRun_WholeStack_SignalsBridgeDaemon drives the full `reset run`
// command and asserts the bridge daemon is SIGTERMed once the pipeline
// succeeds (design D6).
func TestResetRun_WholeStack_SignalsBridgeDaemon(t *testing.T) {
	stubPreflightRun(t)
	calls := recordResetBridgeStop(t)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resetYML := "phases:\n  - name: cleanup\n    steps:\n      - name: noop\n        type: shell\n        cmd: \"true\"\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "reset.yml"), []byte(resetYML), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"reset", "run", "--yes"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	if err := root.Execute(); err != nil {
		t.Fatalf("reset run: %v", err)
	}
	if want := bridge.DefaultBridgeDir(dir); len(*calls) != 1 || (*calls)[0] != want {
		t.Errorf("bridge stop calls = %v, want [%s]", *calls, want)
	}
}

// TestResetRun_PipelineFailure_DoesNotSignalDaemon: a failed reset leaves the
// stack in an unknown state — the daemon must stay up for diagnostics; only a
// successful whole-stack reset stops it.
func TestResetRun_PipelineFailure_DoesNotSignalDaemon(t *testing.T) {
	stubPreflightRun(t)
	calls := recordResetBridgeStop(t)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resetYML := "phases:\n  - name: cleanup\n    steps:\n      - name: boom\n        type: shell\n        cmd: \"false\"\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "reset.yml"), []byte(resetYML), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"reset", "run", "--yes"})
	root.SetErr(io.Discard)
	root.SetOut(io.Discard)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	if err := root.Execute(); err == nil {
		t.Fatal("expected reset pipeline failure")
	}
	if len(*calls) != 0 {
		t.Errorf("bridge stop called %d times on pipeline failure, want 0", len(*calls))
	}
}

// TestResetRun_PerService_DoesNotTouchBridgeDaemon pins the D6 table: the
// per-service reset variant leaves the daemon alone.
func TestResetRun_PerService_DoesNotTouchBridgeDaemon(t *testing.T) {
	stubPreflightRun(t)
	calls := recordResetBridgeStop(t)
	prepares, stops := recordWorkflowBridgeSeams(t)
	cfgPath, _ := makeResetServiceTestDir(t, "main", false, false, true, false)

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"reset", "run", "--service", "main", "--yes"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	if err := root.Execute(); err != nil {
		t.Fatalf("reset run --service: %v", err)
	}
	if n := len(*calls) + len(*prepares) + len(*stops); n != 0 {
		t.Errorf("per-service reset touched the bridge %d times, want 0", n)
	}
}

// TestStopService_DoesNotTouchBridgeDaemon pins the D6 table: per-service
// stop bypasses compose AND the bridge daemon.
func TestStopService_DoesNotTouchBridgeDaemon(t *testing.T) {
	calls := recordResetBridgeStop(t)
	prepares, stops := recordWorkflowBridgeSeams(t)
	prev := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prev })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error { return nil }

	cfgPath := writeStopTestConfig(t, map[string]struct {
		enabled   bool
		container string
	}{
		"postgres": {enabled: true, container: "pg"},
	})
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	deps := StopServiceDeps{
		Cfg:           cfg,
		BaseDir:       filepath.Dir(cfgPath),
		SkipPreflight: true,
	}
	if err := StopService(context.Background(), deps, "postgres"); err != nil {
		t.Fatalf("StopService: %v", err)
	}
	if n := len(*calls) + len(*prepares) + len(*stops); n != 0 {
		t.Errorf("per-service stop touched the bridge %d times, want 0", n)
	}
}

// TestRestartService_DoesNotTouchBridgeDaemon pins the D6 table: per-service
// restart bypasses compose, lifecycle hooks, AND the bridge daemon.
func TestRestartService_DoesNotTouchBridgeDaemon(t *testing.T) {
	calls := recordResetBridgeStop(t)
	prepares, stops := recordWorkflowBridgeSeams(t)
	prev := restartContainerFn
	t.Cleanup(func() { restartContainerFn = prev })
	restartContainerFn = func(_ context.Context, _, _ string, _ int) error { return nil }

	cfgPath := writeStopTestConfig(t, map[string]struct {
		enabled   bool
		container string
	}{
		"postgres": {enabled: true, container: "pg"},
	})
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := RestartService(context.Background(), filepath.Dir(cfgPath), cfg, "postgres", io.Discard); err != nil {
		t.Fatalf("RestartService: %v", err)
	}
	if n := len(*calls) + len(*prepares) + len(*stops); n != 0 {
		t.Errorf("per-service restart touched the bridge %d times, want 0", n)
	}
}
