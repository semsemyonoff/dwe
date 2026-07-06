package envtest

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
)

// noopReporter implements pipeline.Reporter with no-op methods, so runner
// tests never spin up the real terminal/log machinery.
type noopReporter struct{}

func (noopReporter) StartPipeline(string, int)                            {}
func (noopReporter) EnterPhase(string, config.DeployPhase)                {}
func (noopReporter) SkipPhase(string, config.DeployPhase, string)         {}
func (noopReporter) StartStep(string, config.DeployStep, int, int)        {}
func (noopReporter) SkipStep(string, config.DeployStep, int, int, string) {}
func (noopReporter) FinishStep(string, config.DeployStep, int, int)       {}
func (noopReporter) FailStep(string, config.DeployStep, int, int, error)  {}
func (noopReporter) FinishPipeline(bool)                                  {}
func (noopReporter) StartGroup(string, config.DeployStep, []int, int)     {}
func (noopReporter) FinishGroup(string, config.DeployStep, bool)          {}
func (noopReporter) StepOutput(string, string, bool)                      {}
func (noopReporter) SetSubStepLogPath(string, string)                     {}
func (noopReporter) FlushOutput(string)                                   {}
func (noopReporter) SuspendForExec()                                      {}
func (noopReporter) ResumeAfterExec()                                     {}

// noopReporterFactory is a RunRequest.ReporterFactory that skips the real
// OpenPipelineLog/PlainReporter machinery entirely.
func noopReporterFactory(string, string) (pipeline.Reporter, io.Writer, func(), error) {
	return noopReporter{}, io.Discard, func() {}, nil
}

// writeRunnerFixtureProject creates a minimal project at t.TempDir() with a
// workspace.yml and, when scenarioYAML is non-empty, a single scenario file
// workspace/tests/<name>.yml. Returns the project root.
func writeRunnerFixtureProject(t *testing.T, name, scenarioYAML string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(`schema_version: "1"
project:
  name: runnertest
  prefix: dwe
`), 0o644); err != nil {
		t.Fatalf("writing workspace.yml: %v", err)
	}
	if scenarioYAML != "" {
		testsDir := filepath.Join(dir, "workspace", "tests")
		if err := os.MkdirAll(testsDir, 0o755); err != nil {
			t.Fatalf("creating tests dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(testsDir, name+".yml"), []byte(scenarioYAML), 0o644); err != nil {
			t.Fatalf("writing scenario file: %v", err)
		}
	}
	return dir
}

// fatalTeardownDeps returns a newTeardownDeps stub that fails the test if
// ever invoked — used to assert teardown of Docker resources never runs.
func fatalTeardownDeps(t *testing.T) func(string, io.Writer) TeardownDeps {
	t.Helper()
	return func(string, io.Writer) TeardownDeps {
		t.Fatal("teardown deps must not be constructed")
		return TeardownDeps{}
	}
}

// stubExecDwe returns an execDweFunc keyed by the joined subcommand args
// (e.g. "validate", "deploy run --silent"); calls records every invocation's
// args in order.
func stubExecDwe(results map[string]error, calls *[]string) execDweFunc {
	return func(_ context.Context, _ string, _ []string, _, _ io.Writer, args ...string) error {
		key := strings.Join(args, " ")
		*calls = append(*calls, key)
		return results[key]
	}
}

const noStepsScenario = "description: no-op scenario\n"

func TestRunScenario_HappyPath(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "smoke", noStepsScenario)

	var teardownOrder []string
	var execCalls []string

	r := &Runner{
		execDwe:       stubExecDwe(nil, &execCalls),
		allocatePorts: AllocatePorts,
		newTeardownDeps: func(manifestPath string, _ io.Writer) TeardownDeps {
			return recordingTeardownDeps(&teardownOrder, nil)
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "smoke",
		ReporterFactory: noopReporterFactory,
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusPassed {
		t.Fatalf("status = %q, want passed", result.Status)
	}
	if result.ComposeProject == "" || result.CopyPath == "" {
		t.Fatalf("expected ComposeProject/CopyPath to be set: %+v", result)
	}
	wantCalls := []string{"validate", "deploy run --silent"}
	if strings.Join(execCalls, "|") != strings.Join(wantCalls, "|") {
		t.Fatalf("execCalls = %v, want %v", execCalls, wantCalls)
	}
	wantTeardown := []string{"compose_down", "reap_containers", "remove_volumes", "stop_bridge", "remove_copy", "delete_manifest"}
	if strings.Join(teardownOrder, "|") != strings.Join(wantTeardown, "|") {
		t.Fatalf("teardownOrder = %v, want %v", teardownOrder, wantTeardown)
	}
}

func TestRunScenario_PrepFailureBeforeManifest_NoDockerTeardown(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "autoport", "env:\n  vars:\n    app.port: auto\n")

	r := &Runner{
		execDwe: func(context.Context, string, []string, io.Writer, io.Writer, ...string) error {
			t.Fatal("execDwe must not be called on a prep failure")
			return nil
		},
		allocatePorts:   func(int) ([]int, error) { return nil, errors.New("boom: no free ports") },
		newTeardownDeps: fatalTeardownDeps(t),
		clock:           time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "autoport",
		ReporterFactory: noopReporterFactory,
	})
	if err == nil {
		t.Fatalf("expected an error, got result %+v", result)
	}
	if result != nil {
		t.Fatalf("expected nil result on prep failure, got %+v", result)
	}
	if _, statErr := os.Stat(RunDir(dir, "autoport")); !os.IsNotExist(statErr) {
		t.Fatalf("expected the copy to be removed, stat err = %v", statErr)
	}
	manifests, globErr := existingManifestPaths(dir, "autoport")
	if globErr != nil || len(manifests) != 0 {
		t.Fatalf("expected no manifests, got %v (err %v)", manifests, globErr)
	}
}

func TestRunScenario_ValidateFailure(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "smoke", noStepsScenario)

	var teardownOrder []string
	var execCalls []string
	results := map[string]error{"validate": errors.New("validate: boom")}

	r := &Runner{
		execDwe:       stubExecDwe(results, &execCalls),
		allocatePorts: AllocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(&teardownOrder, nil)
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "smoke",
		ReporterFactory: noopReporterFactory,
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if len(execCalls) != 1 || execCalls[0] != "validate" {
		t.Fatalf("execCalls = %v, want only validate (deploy must not run)", execCalls)
	}
	if len(teardownOrder) == 0 {
		t.Fatalf("expected teardown to still run after validate failure")
	}
	// The copy must be gone (removed by teardown's RemoveCopy step, recorded here).
	if !containsStep(teardownOrder, "remove_copy") {
		t.Fatalf("teardownOrder missing remove_copy: %v", teardownOrder)
	}
}

func TestRunScenario_DeployFailure(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "smoke", noStepsScenario)

	var teardownOrder []string
	var execCalls []string
	results := map[string]error{"deploy run --silent": errors.New("deploy: boom")}

	r := &Runner{
		execDwe:       stubExecDwe(results, &execCalls),
		allocatePorts: AllocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(&teardownOrder, nil)
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "smoke",
		ReporterFactory: noopReporterFactory,
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if len(execCalls) != 2 {
		t.Fatalf("execCalls = %v, want exactly validate+deploy (no auto ports, no retry)", execCalls)
	}
	if !containsStep(teardownOrder, "remove_copy") {
		t.Fatalf("expected teardown to run: %v", teardownOrder)
	}
}

func TestRunScenario_PortConflictRetry_Succeeds(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "ports", "env:\n  vars:\n    app.port: auto\n")

	var execCalls []string
	deployAttempt := 0
	execDwe := func(_ context.Context, _ string, _ []string, _, _ io.Writer, args ...string) error {
		key := strings.Join(args, " ")
		execCalls = append(execCalls, key)
		if key == "deploy run --silent" {
			deployAttempt++
			if deployAttempt == 1 {
				return errors.New("port is already allocated")
			}
			return nil
		}
		return nil
	}

	var allocCalls int
	allocatePorts := func(n int) ([]int, error) {
		allocCalls++
		ports := make([]int, n)
		for i := range ports {
			ports[i] = 10000 + allocCalls*10 + i
		}
		return ports, nil
	}

	var teardownOrder []string
	r := &Runner{
		execDwe:       execDwe,
		allocatePorts: allocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(&teardownOrder, nil)
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "ports",
		ReporterFactory: noopReporterFactory,
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusPassed {
		t.Fatalf("status = %q, want passed after successful retry", result.Status)
	}
	if allocCalls != 2 {
		t.Fatalf("allocatePorts called %d times, want exactly 2 (initial + one retry)", allocCalls)
	}
	wantExec := []string{"validate", "deploy run --silent", "deploy run --silent"}
	if strings.Join(execCalls, "|") != strings.Join(wantExec, "|") {
		t.Fatalf("execCalls = %v, want %v", execCalls, wantExec)
	}
}

func TestRunScenario_PortConflictRetry_StillFails(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "ports", "env:\n  vars:\n    app.port: auto\n")

	var execCalls []string
	execDwe := func(_ context.Context, _ string, _ []string, _, _ io.Writer, args ...string) error {
		key := strings.Join(args, " ")
		execCalls = append(execCalls, key)
		if key == "deploy run --silent" {
			return errors.New("port is already allocated")
		}
		return nil
	}

	var allocCalls int
	allocatePorts := func(n int) ([]int, error) {
		allocCalls++
		return make([]int, n), nil
	}

	var teardownOrder []string
	r := &Runner{
		execDwe:       execDwe,
		allocatePorts: allocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(&teardownOrder, nil)
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "ports",
		ReporterFactory: noopReporterFactory,
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if allocCalls != 2 {
		t.Fatalf("allocatePorts called %d times, want exactly 2 (one retry, not two)", allocCalls)
	}
	// validate + 2 deploy attempts, no third attempt.
	if len(execCalls) != 3 {
		t.Fatalf("execCalls = %v, want exactly 3 (validate + 2 deploy attempts)", execCalls)
	}
}

func TestRunScenario_StepFailure(t *testing.T) {
	scenarioYAML := `steps:
  - name: "boom"
    type: shell
    cmd: "exit 1"
`
	dir := writeRunnerFixtureProject(t, "stepfail", scenarioYAML)

	var teardownOrder []string
	var execCalls []string
	r := &Runner{
		execDwe:       stubExecDwe(nil, &execCalls),
		allocatePorts: AllocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(&teardownOrder, nil)
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "stepfail",
		ReporterFactory: noopReporterFactory,
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.FailedStep != "tests/boom" {
		t.Fatalf("FailedStep = %q, want %q", result.FailedStep, "tests/boom")
	}
	if !containsStep(teardownOrder, "remove_copy") {
		t.Fatalf("expected teardown to run: %v", teardownOrder)
	}
}

func TestRunScenario_TimeoutExpiry(t *testing.T) {
	scenarioYAML := `steps:
  - name: "slow"
    type: shell
    cmd: "sleep 5"
`
	dir := writeRunnerFixtureProject(t, "slow", scenarioYAML)

	var teardownOrder []string
	var freshCtxSeen bool
	deps := func(string, io.Writer) TeardownDeps {
		d := recordingTeardownDeps(&teardownOrder, nil)
		realComposeDown := d.ComposeDown
		d.ComposeDown = func(ctx context.Context, m *Manifest) (bool, error) {
			freshCtxSeen = ctx.Err() == nil
			return realComposeDown(ctx, m)
		}
		return d
	}

	var execCalls []string
	r := &Runner{
		execDwe:         stubExecDwe(nil, &execCalls),
		allocatePorts:   AllocatePorts,
		newTeardownDeps: deps,
		clock:           time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "slow",
		Timeout:         50 * time.Millisecond,
		ReporterFactory: noopReporterFactory,
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %q, want failed on timeout", result.Status)
	}
	if !freshCtxSeen {
		t.Fatalf("expected teardown's ComposeDown to observe a fresh (non-expired) context")
	}
	if !containsStep(teardownOrder, "remove_copy") {
		t.Fatalf("expected teardown to still run after timeout: %v", teardownOrder)
	}
}

func TestRunScenario_Keep(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "smoke", noStepsScenario)

	var execCalls []string
	r := &Runner{
		execDwe:         stubExecDwe(nil, &execCalls),
		allocatePorts:   AllocatePorts,
		newTeardownDeps: fatalTeardownDeps(t),
		clock:           time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "smoke",
		Keep:            true,
		ReporterFactory: noopReporterFactory,
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusPassed {
		t.Fatalf("status = %q, want passed", result.Status)
	}
	if _, statErr := os.Stat(result.CopyPath); statErr != nil {
		t.Fatalf("expected the copy to survive --keep: %v", statErr)
	}
	manifests, err := existingManifestPaths(dir, "smoke")
	if err != nil || len(manifests) != 1 {
		t.Fatalf("expected exactly one kept manifest, got %v (err %v)", manifests, err)
	}
}

func TestRunScenario_FlockHeld(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "smoke", noStepsScenario)

	held, err := lock.Acquire(LockPath(dir, "smoke"))
	if err != nil {
		t.Fatalf("pre-acquiring lock: %v", err)
	}
	defer func() { _ = held.Release() }()

	r := NewRunner()
	r.execDwe = func(context.Context, string, []string, io.Writer, io.Writer, ...string) error {
		t.Fatal("execDwe must not be called when the flock is held")
		return nil
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:  dir,
		Scenario: "smoke",
	})
	if err == nil {
		t.Fatalf("expected an error, got result %+v", result)
	}
	var heldErr *lock.HeldError
	if !errors.As(err, &heldErr) {
		t.Fatalf("expected error to wrap *lock.HeldError, got %v", err)
	}
}

func TestRunScenario_KeptRunGuard(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "smoke", noStepsScenario)

	// Simulate a --keep'd (or half-dead) prior run: a manifest exists, and
	// the copy directory it names has a sentinel file that must survive.
	copyRoot := RunDir(dir, "smoke")
	if err := os.MkdirAll(copyRoot, 0o755); err != nil {
		t.Fatalf("creating fake kept copy: %v", err)
	}
	sentinel := filepath.Join(copyRoot, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("kept"), 0o644); err != nil {
		t.Fatalf("writing sentinel: %v", err)
	}
	manifest := &Manifest{
		Scenario:       "smoke",
		RunID:          "abcdef",
		ComposeProject: "runnertest-t-smoke-abcdef",
		CopyPath:       copyRoot,
	}
	if err := WriteManifest(ManifestPath(dir, "smoke", "abcdef"), manifest); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}

	r := NewRunner()
	r.execDwe = func(context.Context, string, []string, io.Writer, io.Writer, ...string) error {
		t.Fatal("execDwe must not be called when a kept run owns the copy")
		return nil
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:  dir,
		Scenario: "smoke",
	})
	if err == nil {
		t.Fatalf("expected an error, got result %+v", result)
	}
	var keptErr *KeptRunError
	if !errors.As(err, &keptErr) {
		t.Fatalf("expected error to wrap *KeptRunError, got %v", err)
	}
	if keptErr.Scenario != "smoke" || len(keptErr.ManifestPaths) != 1 {
		t.Fatalf("unexpected KeptRunError: %+v", keptErr)
	}
	// The kept copy must be completely untouched.
	if data, readErr := os.ReadFile(sentinel); readErr != nil || string(data) != "kept" {
		t.Fatalf("kept copy's sentinel file was touched: data=%q err=%v", data, readErr)
	}
}

func containsStep(order []string, step string) bool {
	return slices.Contains(order, step)
}
