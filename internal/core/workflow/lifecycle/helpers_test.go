package lifecycle

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// init replaces PreflightFunc with a no-op for the test binary so lifecycle
// tests don't pick up the host's docker / compose / git binaries and fail
// preflight. Tests that exercise preflight behavior explicitly swap it back.
// The bridge seams are stubbed for the same reason: the real prepare hook
// resolves image architectures via docker and spawns a detached daemon via
// os.Executable() — re-executing the test binary (the documented recursion
// hazard). Bridge tests install recorders via recordBridgeSeams.
func init() {
	PreflightFunc = func(_ context.Context, _ *config.DweConfig, _ *usercommands.Registry, _, _ string, _ bool, _ io.Writer) error {
		return nil
	}
	BridgePrepareFunc = func(bridge.PrepareOptions) error { return nil }
	BridgeStopDaemonFunc = func(string) (bool, error) { return false, nil }
}

// bridgeSeamRecorder captures every bridge-seam invocation in order, so tests
// can assert both the calls and their position relative to other recorded
// events appended to the same slice.
type bridgeSeamRecorder struct {
	events   *[]string
	prepares []bridge.PrepareOptions
	stops    []string
}

// recordBridgeSeams installs recording fakes for both bridge seams for the
// duration of the test. events receives "bridge-prepare" / "bridge-stop"
// markers; pass a shared slice to interleave with other recorded steps.
func recordBridgeSeams(t *testing.T, events *[]string) *bridgeSeamRecorder {
	t.Helper()
	rec := &bridgeSeamRecorder{events: events}
	prevPrepare, prevStop := BridgePrepareFunc, BridgeStopDaemonFunc
	t.Cleanup(func() {
		BridgePrepareFunc, BridgeStopDaemonFunc = prevPrepare, prevStop
	})
	BridgePrepareFunc = func(opts bridge.PrepareOptions) error {
		rec.prepares = append(rec.prepares, opts)
		*rec.events = append(*rec.events, "bridge-prepare")
		return nil
	}
	BridgeStopDaemonFunc = func(bridgeDir string) (bool, error) {
		rec.stops = append(rec.stops, bridgeDir)
		*rec.events = append(*rec.events, "bridge-stop")
		return true, nil
	}
	return rec
}

// stubRunPhases replaces RunPhasesFunc with a no-op for the duration of a test.
// Used by tests that exercise the default-config path to avoid the recursive
// test-binary execution that occurs when type:dwe steps call os.Executable().
func stubRunPhases(t *testing.T) {
	t.Helper()
	prev := RunPhasesFunc
	t.Cleanup(func() { RunPhasesFunc = prev })
	RunPhasesFunc = func(_ *config.DweConfig, _ *usercommands.Registry, _ string, _ []config.DeployPhase, _, _ string, _ bool, _ bool, _ i18n.Translator, _ string) error {
		return nil
	}
}

// makeMinimalWorkspaceYML writes the minimum workspace.yml needed for config.LoadConfig to succeed.
func makeMinimalWorkspaceYML(t *testing.T, dir string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "workspace.yml")
	content := "project:\n  name: test\n  prefix: dwe\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	return cfgPath
}

// writeLifecycleYML writes lifecycle.yml with a single noop phase and the given FinalMessage.
func writeLifecycleYML(t *testing.T, workspaceDir string, finalMessage string) {
	t.Helper()
	yaml := "run:\n  final_message: " + finalMessage + "\n  phases:\n    - name: start\n      steps:\n        - name: noop\n          type: shell\n          cmd: \"true\"\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "lifecycle.yml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}
}
