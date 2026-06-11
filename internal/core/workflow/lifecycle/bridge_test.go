package lifecycle

import (
	"errors"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// stubRunPhasesRecording stubs phase execution and appends "phases" to events
// per invocation, so bridge-seam ordering can be asserted against it.
func stubRunPhasesRecording(t *testing.T, events *[]string) {
	t.Helper()
	prev := RunPhasesFunc
	t.Cleanup(func() { RunPhasesFunc = prev })
	RunPhasesFunc = func(_ *config.DweConfig, _ *usercommands.Registry, _ string, _ []config.DeployPhase, _, _ string, _ bool, _ bool, _ i18n.Translator, _ string) error {
		*events = append(*events, "phases")
		return nil
	}
}

func TestRunRun_BridgePrepareBeforePhases(t *testing.T) {
	var events []string
	rec := recordBridgeSeams(t, &events)
	stubRunPhasesRecording(t, &events)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	if err := RunRun(RunContext{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("RunRun: %v", err)
	}
	if len(events) != 2 || events[0] != "bridge-prepare" || events[1] != "phases" {
		t.Errorf("events = %v, want [bridge-prepare phases]", events)
	}
	if len(rec.prepares) != 1 {
		t.Fatalf("prepare called %d times, want 1", len(rec.prepares))
	}
	opts := rec.prepares[0]
	if opts.BaseDir != dir {
		t.Errorf("prepare BaseDir = %q, want %q", opts.BaseDir, dir)
	}
	if opts.Cfg == nil {
		t.Error("prepare Cfg = nil, want loaded config")
	}
	if opts.CycleDaemon {
		t.Error("plain run must ENSURE the daemon (CycleDaemon = true, want false — design D6)")
	}
	if len(rec.stops) != 0 {
		t.Errorf("run must not signal the daemon; stop calls = %v", rec.stops)
	}
}

func TestRunRun_BridgePrepareErrorAbortsBeforePhases(t *testing.T) {
	var events []string
	stubRunPhasesRecording(t, &events)
	prev := BridgePrepareFunc
	t.Cleanup(func() { BridgePrepareFunc = prev })
	prepErr := errors.New("overlay write failed")
	BridgePrepareFunc = func(bridge.PrepareOptions) error { return prepErr }
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	err := RunRun(RunContext{ConfigPath: cfgPath})
	if !errors.Is(err, prepErr) {
		t.Fatalf("err = %v, want wrapped %v", err, prepErr)
	}
	if !strings.Contains(err.Error(), "preparing host bridge") {
		t.Errorf("err = %q, want 'preparing host bridge' context", err)
	}
	for _, e := range events {
		if e == "phases" {
			t.Error("phases ran despite bridge prepare failure")
		}
	}
}

// TestRunRun_GateFails_BridgeNotPrepared pins the hook position: the prepare
// hook sits AFTER the deployment gate, so a gate-rejected run must not touch
// the overlay or the daemon.
func TestRunRun_GateFails_BridgeNotPrepared(t *testing.T) {
	var events []string
	rec := recordBridgeSeams(t, &events)
	stubRunPhases(t)
	dir := t.TempDir()
	deployYML := "phases:\n  - name: setup\n    steps:\n      - name: noop\n        type: shell\n        cmd: \"true\"\n"
	cfgPath := writeConfigRenderProject(t, dir, "APP_KEY=x\n", "APP_KEY=x\n", deployYML)

	// No journal state → tracked service not deployed → gate fails.
	if err := RunRun(RunContext{ConfigPath: cfgPath}); err == nil {
		t.Fatal("expected deployment gate error, got nil")
	}
	if len(rec.prepares) != 0 {
		t.Errorf("prepare called %d times on gate failure, want 0", len(rec.prepares))
	}
}

func TestRunStop_SignalsBridgeDaemonAfterPhases(t *testing.T) {
	var events []string
	rec := recordBridgeSeams(t, &events)
	stubRunPhasesRecording(t, &events)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	if err := RunStop(StopContext{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("RunStop: %v", err)
	}
	if len(events) != 2 || events[0] != "phases" || events[1] != "bridge-stop" {
		t.Errorf("events = %v, want [phases bridge-stop]", events)
	}
	if want := bridge.DefaultBridgeDir(dir); len(rec.stops) != 1 || rec.stops[0] != want {
		t.Errorf("stop calls = %v, want [%s]", rec.stops, want)
	}
	if len(rec.prepares) != 0 {
		t.Errorf("stop must not prepare the bridge; prepare calls = %d", len(rec.prepares))
	}
}

func TestRunStop_BridgeStopErrorIsNonFatal(t *testing.T) {
	stubRunPhases(t)
	prev := BridgeStopDaemonFunc
	t.Cleanup(func() { BridgeStopDaemonFunc = prev })
	BridgeStopDaemonFunc = func(string) (bool, error) {
		return false, errors.New("signal refused")
	}
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	if err := RunStop(StopContext{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("RunStop must not fail on a bridge-daemon signal error, got: %v", err)
	}
}

// TestRunRestart_CyclesDaemon verifies the restart composition (design D6):
// the stop leg SIGTERMs the daemon, the run leg's prepare hook CYCLES it
// (BridgeDaemonCycle propagated by RunRestart), and the phases interleave as
// stop-phases → SIGTERM → prepare → run-phases.
func TestRunRestart_CyclesDaemon(t *testing.T) {
	var events []string
	rec := recordBridgeSeams(t, &events)
	stubRunPhasesRecording(t, &events)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	if err := RunRestart(RunContext{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("RunRestart: %v", err)
	}
	want := []string{"phases", "bridge-stop", "bridge-prepare", "phases"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
	if len(rec.prepares) != 1 || !rec.prepares[0].CycleDaemon {
		t.Errorf("restart run leg must cycle the daemon; prepares = %+v", rec.prepares)
	}
}

// TestRunRun_ExplicitCycleFlagThreaded covers the RunContext field directly:
// callers (deploy / restart) opt into the cycle daemon step.
func TestRunRun_ExplicitCycleFlagThreaded(t *testing.T) {
	var events []string
	rec := recordBridgeSeams(t, &events)
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	if err := RunRun(RunContext{ConfigPath: cfgPath, BridgeDaemonCycle: true}); err != nil {
		t.Fatalf("RunRun: %v", err)
	}
	if len(rec.prepares) != 1 || !rec.prepares[0].CycleDaemon {
		t.Errorf("CycleDaemon not threaded; prepares = %+v", rec.prepares)
	}
}
