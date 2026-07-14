package envtest

import (
	"bytes"
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
	"github.com/semsemyonoff/dwe/internal/core/project/local"
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
func noopReporterFactory(string, string) (pipeline.Reporter, io.Writer, io.Writer, func(), error) {
	return noopReporter{}, io.Discard, io.Discard, func() {}, nil
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

// TestRunScenario_ForceColorEnv verifies that RunRequest.ForceColor controls
// whether the validate/deploy subprocesses are spawned with CLICOLOR_FORCE=1
// (so their piped stdout still renders in color when streamed to the terminal).
func TestRunScenario_ForceColorEnv(t *testing.T) {
	for _, tc := range []struct {
		name       string
		forceColor bool
		wantForce  bool
	}{
		{"force", true, true},
		{"no-force", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeRunnerFixtureProject(t, "smoke", noStepsScenario)
			var gotEnv [][]string
			exec := func(_ context.Context, _ string, extraEnv []string, _, _ io.Writer, _ ...string) error {
				gotEnv = append(gotEnv, extraEnv)
				return nil
			}
			r := &Runner{
				execDwe:       exec,
				allocatePorts: AllocatePorts,
				newTeardownDeps: func(string, io.Writer) TeardownDeps {
					return recordingTeardownDeps(new([]string), nil)
				},
				clock: time.Now,
			}
			res, err := r.RunScenario(context.Background(), RunRequest{
				BaseDir:         dir,
				Scenario:        "smoke",
				ReporterFactory: noopReporterFactory,
				ForceColor:      tc.forceColor,
			})
			if err != nil {
				t.Fatalf("RunScenario: %v", err)
			}
			if res.Status != StatusPassed {
				t.Fatalf("status = %q, want passed", res.Status)
			}
			if len(gotEnv) == 0 {
				t.Fatal("execDwe was never called")
			}
			for i, env := range gotEnv {
				has := slices.Contains(env, "CLICOLOR_FORCE=1")
				if has != tc.wantForce {
					t.Fatalf("call %d env = %v, CLICOLOR_FORCE present=%v want=%v", i, env, has, tc.wantForce)
				}
				// DWE_NONINTERACTIVE is always set regardless.
				if !slices.Contains(env, "DWE_NONINTERACTIVE=1") {
					t.Fatalf("call %d env = %v, missing DWE_NONINTERACTIVE=1", i, env)
				}
			}
		})
	}
}

// TestRunScenario_DiagnosticFlagPropagation verifies that the parent's
// --verbose/--debug flags are propagated as leading args to the validate and
// deploy subprocesses (so `dwe test run --debug` surfaces what happens inside
// the copy), with --debug winning over --verbose and neither leaving the arg
// list byte-identical to a normal run.
func TestRunScenario_DiagnosticFlagPropagation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verbose bool
		debug   bool
		want    []string
	}{
		{"none", false, false, []string{"validate", "deploy run --silent"}},
		{"verbose", true, false, []string{"--verbose validate", "--verbose deploy run --silent"}},
		{"debug", false, true, []string{"--debug validate", "--debug deploy run --silent"}},
		{"debug-wins", true, true, []string{"--debug validate", "--debug deploy run --silent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeRunnerFixtureProject(t, "smoke", noStepsScenario)
			var execCalls []string
			r := &Runner{
				execDwe:       stubExecDwe(nil, &execCalls),
				allocatePorts: AllocatePorts,
				newTeardownDeps: func(string, io.Writer) TeardownDeps {
					return recordingTeardownDeps(new([]string), nil)
				},
				clock: time.Now,
			}
			res, err := r.RunScenario(context.Background(), RunRequest{
				BaseDir:         dir,
				Scenario:        "smoke",
				ReporterFactory: noopReporterFactory,
				Verbose:         tc.verbose,
				Debug:           tc.debug,
			})
			if err != nil {
				t.Fatalf("RunScenario: %v", err)
			}
			if res.Status != StatusPassed {
				t.Fatalf("status = %q, want passed", res.Status)
			}
			if strings.Join(execCalls, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("execCalls = %v, want %v", execCalls, tc.want)
			}
		})
	}
}

// TestDefaultReporterFactory_LogSideStripsANSI verifies the production factory
// keeps the run log plain even when the subprocess streams colored output —
// the terminal leg keeps color, the log leg is ANSI-stripped.
func TestDefaultReporterFactory_LogSideStripsANSI(t *testing.T) {
	dir := t.TempDir()
	_, _, subprocOut, cleanup, err := defaultReporterFactory(dir, "test")
	if err != nil {
		t.Fatalf("defaultReporterFactory: %v", err)
	}
	if _, err := io.WriteString(subprocOut, "\x1b[38;2;239;68;68mred\x1b[0m line\n"); err != nil {
		t.Fatalf("writing to subprocOut: %v", err)
	}
	cleanup()

	data, err := os.ReadFile(filepath.Join(dir, ".dwe", "logs", "test.log"))
	if err != nil {
		t.Fatalf("reading run log: %v", err)
	}
	if got, want := string(data), "red line\n"; got != want {
		t.Fatalf("run log = %q, want %q (ANSI must be stripped)", got, want)
	}
}

// TestRunScenario_SubprocessOutputStreamsToSubprocOut pins that the runner
// routes the validate/deploy subprocess stdout/stderr to the factory's
// subprocOut writer (the console+log tee), not the run-log-only writer — the
// fix for the "console is silent during the deploy, everything appears at the
// end" problem.
func TestRunScenario_SubprocessOutputStreamsToSubprocOut(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "smoke", noStepsScenario)

	var subprocBuf bytes.Buffer
	capturingFactory := func(string, string) (pipeline.Reporter, io.Writer, io.Writer, func(), error) {
		// logWriter = io.Discard, subprocOut = the recording buffer: proves the
		// subprocess output goes to subprocOut specifically, not logWriter.
		return noopReporter{}, io.Discard, &subprocBuf, func() {}, nil
	}

	// The stub writes a per-invocation marker to its stdout writer, mimicking a
	// deploy that streams progress.
	exec := func(_ context.Context, _ string, _ []string, stdout, _ io.Writer, args ...string) error {
		_, _ = io.WriteString(stdout, "["+strings.Join(args, " ")+"] streaming\n")
		return nil
	}

	r := &Runner{
		execDwe:       exec,
		allocatePorts: AllocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(new([]string), nil)
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "smoke",
		ReporterFactory: capturingFactory,
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusPassed {
		t.Fatalf("status = %q, want passed", result.Status)
	}
	got := subprocBuf.String()
	if !strings.Contains(got, "[validate] streaming") {
		t.Fatalf("subprocOut missing validate output; got:\n%s", got)
	}
	if !strings.Contains(got, "[deploy run --silent] streaming") {
		t.Fatalf("subprocOut missing deploy output; got:\n%s", got)
	}
}

// TestDefaultReporterFactory_SubprocOutMirrorsToLog verifies the production
// factory's subprocOut also mirrors into the run log (it is a MultiWriter over
// the terminal writer + the log file), so streaming to the console never costs
// the on-disk record. In a test process stdout is not a TTY, so the terminal
// leg is io.Discard and only the log leg is observable.
func TestDefaultReporterFactory_SubprocOutMirrorsToLog(t *testing.T) {
	dir := t.TempDir()
	_, logWriter, subprocOut, cleanup, err := defaultReporterFactory(dir, "test")
	if err != nil {
		t.Fatalf("defaultReporterFactory: %v", err)
	}
	if logWriter == nil || subprocOut == nil {
		t.Fatalf("factory returned nil writers: log=%v subproc=%v", logWriter, subprocOut)
	}
	const marker = "deploy-progress-line\n"
	if _, err := io.WriteString(subprocOut, marker); err != nil {
		t.Fatalf("writing to subprocOut: %v", err)
	}
	cleanup() // closes the log file

	logPath := filepath.Join(dir, ".dwe", "logs", "test.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading run log %s: %v", logPath, err)
	}
	if !strings.Contains(string(data), "deploy-progress-line") {
		t.Fatalf("run log did not capture subprocOut write; got:\n%s", data)
	}
}

// TestRunScenario_RemapsEnabledServiceHostPorts pins the host-port auto-remap:
// every enabled service's declared host port is rewritten to a freshly
// allocated free port in the copy's generated local.yml (services.<name>.ports),
// so ports_free preflight and the compose bind land off the working
// environment's ports with no scenario port config.
func TestRunScenario_RemapsEnabledServiceHostPorts(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "workspace.yml", "project:\n  name: runnertest\n  prefix: dwe\n")
	writeFixtureFile(t, dir, "workspace/services/db/service.yml",
		"type: infra\ncontainer: db\nrequired: true\nports:\n  mysql: 13306\n")
	writeFixtureFile(t, dir, "workspace/tests/smoke.yml", noStepsScenario)

	var order []string
	var execCalls []string
	r := &Runner{
		execDwe: stubExecDwe(nil, &execCalls),
		allocatePorts: func(n int) ([]int, error) {
			out := make([]int, n)
			for i := range out {
				out[i] = 20000 + i
			}
			return out, nil
		},
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(&order, nil)
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

	// recordingTeardownDeps.RemoveCopy is a no-op, so the copy's generated
	// local.yml survives for inspection.
	localPath := config.LocalLayerPath(filepath.Join(RunDir(dir, "smoke"), "workspace.yml"))
	m, err := local.LoadLocalYAML(localPath)
	if err != nil {
		t.Fatalf("loading generated local.yml: %v", err)
	}
	got := digToInt(t, m, "services", "db", "ports", "mysql")
	if got != 20000 {
		t.Fatalf("services.db.ports.mysql = %d, want 20000 (remapped off the original 13306)", got)
	}
}

// writeFixtureFile writes content to dir/rel, creating parent directories.
func writeFixtureFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", rel, err)
	}
}

// digToInt walks nested map[string]any by keys and returns the leaf as an int.
func digToInt(t *testing.T, m map[string]any, keys ...string) int {
	t.Helper()
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("digToInt: %q is not a map (path %v)", k, keys)
		}
		cur = mm[k]
	}
	switch v := cur.(type) {
	case int:
		return v
	case int64:
		return int(v)
	default:
		t.Fatalf("digToInt: leaf %v is %T, not an int", cur, cur)
		return 0
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

// A deploy failure that is NOT a port-bind conflict must fail the scenario
// immediately — no retry — even though the copy allocated host/auto ports.
// Retrying a genuine failure (an app crash, a bad env var) just doubles the
// wall-clock cost before failing anyway.
func TestRunScenario_NonPortConflictFailure_NoRetry(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "ports", "env:\n  vars:\n    app.port: auto\n")

	var execCalls []string
	execDwe := func(_ context.Context, _ string, _ []string, _, _ io.Writer, args ...string) error {
		key := strings.Join(args, " ")
		execCalls = append(execCalls, key)
		if key == "deploy run --silent" {
			// A crash with no port-conflict signal in output or error.
			return errors.New("exit status 1: application boot failed")
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
	if allocCalls != 1 {
		t.Fatalf("allocatePorts called %d times, want exactly 1 (no retry on a non-port failure)", allocCalls)
	}
	// validate + exactly one deploy attempt — no second attempt.
	wantExec := []string{"validate", "deploy run --silent"}
	if strings.Join(execCalls, "|") != strings.Join(wantExec, "|") {
		t.Fatalf("execCalls = %v, want %v (no retry)", execCalls, wantExec)
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

// TestExistingManifestPaths_PrefixDisambiguation locks in the load-bearing
// behaviour of manifestRunIDSuffix: a scenario's kept-run guard must match ONLY
// that scenario's own manifests, never another scenario whose name shares its
// prefix (e.g. "foo" must not claim "foo-bar"'s manifest, and vice versa), and
// must ignore files that carry the prefix but not a valid <6-hex>.yml run-id
// suffix.
// TestRunScenario_IsolationGate_BlocksOnContainerName pins that a blocking
// isolation finding (container_name:) fails the scenario BEFORE the `dwe
// validate` subprocess is even spawned, warns with the finding's message, and
// still runs teardown (removing the copy).
func TestRunScenario_IsolationGate_BlocksOnContainerName(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "workspace.yml", "project:\n  name: runnertest\n  prefix: dwe\ncompose:\n  base: docker-compose.yml\n")
	writeFixtureFile(t, dir, "docker-compose.yml", "services:\n  app:\n    container_name: fixed-name\n")
	writeFixtureFile(t, dir, "workspace/tests/smoke.yml", noStepsScenario)

	var teardownOrder []string
	var execCalls []string
	var warnings []string
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
		Scenario:        "smoke",
		ReporterFactory: noopReporterFactory,
		Warn:            func(msg string) { warnings = append(warnings, msg) },
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if len(execCalls) != 0 {
		t.Fatalf("execCalls = %v, want none (isolation gate must block before validate)", execCalls)
	}
	if !containsStep(teardownOrder, "remove_copy") {
		t.Fatalf("expected teardown to still run: %v", teardownOrder)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "fixed-name") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning mentioning the isolation hazard, got %v", warnings)
	}
}

// TestRunScenario_IsolationGate_SkipDowngradesToWarning pins that
// --skip-isolation-check (RunRequest.SkipIsolationCheck) downgrades a
// blocking finding to a warning and lets the scenario proceed to the deploy
// subprocess normally.
func TestRunScenario_IsolationGate_SkipDowngradesToWarning(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "workspace.yml", "project:\n  name: runnertest\n  prefix: dwe\ncompose:\n  base: docker-compose.yml\n")
	writeFixtureFile(t, dir, "docker-compose.yml", "services:\n  app:\n    container_name: fixed-name\n")
	writeFixtureFile(t, dir, "workspace/tests/smoke.yml", noStepsScenario)

	var teardownOrder []string
	var execCalls []string
	var warnings []string
	r := &Runner{
		execDwe:       stubExecDwe(nil, &execCalls),
		allocatePorts: AllocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(&teardownOrder, nil)
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:            dir,
		Scenario:           "smoke",
		ReporterFactory:    noopReporterFactory,
		SkipIsolationCheck: true,
		Warn:               func(msg string) { warnings = append(warnings, msg) },
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusPassed {
		t.Fatalf("status = %q, want passed", result.Status)
	}
	wantCalls := []string{"validate", "deploy run --silent"}
	if strings.Join(execCalls, "|") != strings.Join(wantCalls, "|") {
		t.Fatalf("execCalls = %v, want %v (--skip-isolation-check must still deploy)", execCalls, wantCalls)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "fixed-name") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning mentioning the isolation hazard even when skipped, got %v", warnings)
	}
}

// TestRunScenario_IsolationGate_WarnOnlyFindingProceeds pins that a
// non-blocking finding (an external volume) never fails the scenario — it is
// surfaced as a warning only.
func TestRunScenario_IsolationGate_WarnOnlyFindingProceeds(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "workspace.yml", "project:\n  name: runnertest\n  prefix: dwe\ncompose:\n  base: docker-compose.yml\n")
	writeFixtureFile(t, dir, "docker-compose.yml", "volumes:\n  data:\n    external: true\n")
	writeFixtureFile(t, dir, "workspace/tests/smoke.yml", noStepsScenario)

	var execCalls []string
	var warnings []string
	r := &Runner{
		execDwe:       stubExecDwe(nil, &execCalls),
		allocatePorts: AllocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(new([]string), nil)
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "smoke",
		ReporterFactory: noopReporterFactory,
		Warn:            func(msg string) { warnings = append(warnings, msg) },
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusPassed {
		t.Fatalf("status = %q, want passed", result.Status)
	}
	wantCalls := []string{"validate", "deploy run --silent"}
	if strings.Join(execCalls, "|") != strings.Join(wantCalls, "|") {
		t.Fatalf("execCalls = %v, want %v", execCalls, wantCalls)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "external") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning mentioning the external volume, got %v", warnings)
	}
}

// TestScanComposeIsolationGate_CopyConfigLoadFailure_ScanSkipped pins that a
// copy whose own workspace.yml fails to load simply skips the isolation scan
// (never blocks) — exercised directly against scanComposeIsolationGate since
// RunScenario loads the ORIGINAL project's config up front and would never
// reach the copy in this state via the full flow.
func TestScanComposeIsolationGate_CopyConfigLoadFailure_ScanSkipped(t *testing.T) {
	dir := t.TempDir()
	// binaries: is a rejected legacy top-level key — LoadConfigOrWrap fails.
	writeFixtureFile(t, dir, "workspace.yml", "project:\n  name: runnertest\n  prefix: dwe\nbinaries:\n  docker: /bin/docker\n")

	var warnings []string
	blocked := scanComposeIsolationGate(dir, false, func(msg string) { warnings = append(warnings, msg) })
	if blocked {
		t.Fatal("expected the gate not to block when the copy config fails to load")
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings when the scan is skipped, got %v", warnings)
	}
}

func TestExistingManifestPaths_PrefixDisambiguation(t *testing.T) {
	dir := t.TempDir()
	manifests := ManifestsDir(dir)
	if err := os.MkdirAll(manifests, 0o755); err != nil {
		t.Fatalf("creating manifests dir: %v", err)
	}
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(manifests, name), []byte("scenario: x\n"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	// foo's own runs.
	write("foo-abcdef.yml")
	write("foo-123abc.yml")
	// A different scenario that merely shares the "foo-" prefix.
	write("foo-bar-abcdef.yml")
	// Prefix-carrying files with an invalid run-id suffix — must be ignored.
	write("foo-notanid.yml")
	write("foo-abcde.yml")   // 5 hex, too short
	write("foo-ABCDEF.yml")  // uppercase, not [0-9a-f]
	write("foo-abcdef.yaml") // wrong extension

	got, err := existingManifestPaths(dir, "foo")
	if err != nil {
		t.Fatalf("existingManifestPaths(foo): %v", err)
	}
	want := []string{
		filepath.Join(manifests, "foo-123abc.yml"),
		filepath.Join(manifests, "foo-abcdef.yml"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("existingManifestPaths(foo)\n got: %v\nwant: %v", got, want)
	}

	// The prefix-sharing scenario resolves to only its own manifest.
	gotBar, err := existingManifestPaths(dir, "foo-bar")
	if err != nil {
		t.Fatalf("existingManifestPaths(foo-bar): %v", err)
	}
	wantBar := []string{filepath.Join(manifests, "foo-bar-abcdef.yml")}
	if !slices.Equal(gotBar, wantBar) {
		t.Fatalf("existingManifestPaths(foo-bar)\n got: %v\nwant: %v", gotBar, wantBar)
	}
}

func containsStep(order []string, step string) bool {
	return slices.Contains(order, step)
}

// recordProgress returns a RunRequest.Progress callback appending each phase
// (as a plain string) to phases, so tests can assert firing order.
func recordProgress(phases *[]string) func(ProgressPhase) {
	return func(p ProgressPhase) { *phases = append(*phases, string(p)) }
}

// TestRunScenario_ProgressPhases_PassedRun pins the exact phase-firing order
// for a passing scenario with no report and no retry.
func TestRunScenario_ProgressPhases_PassedRun(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "smoke", noStepsScenario)

	var execCalls []string
	var phases []string
	r := &Runner{
		execDwe:       stubExecDwe(nil, &execCalls),
		allocatePorts: AllocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(new([]string), nil)
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "smoke",
		ReporterFactory: noopReporterFactory,
		Progress:        recordProgress(&phases),
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusPassed {
		t.Fatalf("status = %q, want passed", result.Status)
	}
	want := []string{
		string(PhasePreparing), string(PhaseValidating), string(PhaseDeploying),
		string(PhaseRunningSteps), string(PhaseTearingDown),
	}
	if strings.Join(phases, "|") != strings.Join(want, "|") {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
}

// TestRunScenario_ProgressPhases_FailedDeploy pins that a failed deploy (no
// auto ports, so no retry) fires collecting_report before tearing_down and
// never running_steps.
func TestRunScenario_ProgressPhases_FailedDeploy(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "smoke", noStepsScenario)

	var execCalls []string
	var phases []string
	r := &Runner{
		execDwe:       stubExecDwe(map[string]error{"deploy run --silent": errors.New("deploy: boom")}, &execCalls),
		allocatePorts: AllocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(new([]string), nil)
		},
		collectReport: func(context.Context, *Manifest, func(string)) (string, error) {
			return "/some/report", nil
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "smoke",
		ReporterFactory: noopReporterFactory,
		Progress:        recordProgress(&phases),
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	want := []string{
		string(PhasePreparing), string(PhaseValidating), string(PhaseDeploying),
		string(PhaseCollectingReport), string(PhaseTearingDown),
	}
	if strings.Join(phases, "|") != strings.Join(want, "|") {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
}

// TestRunScenario_ProgressPhases_DeployRetry pins that the one port-conflict
// retry fires deploy_retry between deploying and (on a still-failing retry)
// collecting_report.
func TestRunScenario_ProgressPhases_DeployRetry(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "ports", "env:\n  vars:\n    app.port: auto\n")

	execDwe := func(_ context.Context, _ string, _ []string, _, _ io.Writer, args ...string) error {
		if strings.Join(args, " ") == "deploy run --silent" {
			return errors.New("port is already allocated")
		}
		return nil
	}

	var phases []string
	r := &Runner{
		execDwe:       execDwe,
		allocatePorts: func(n int) ([]int, error) { return make([]int, n), nil },
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(new([]string), nil)
		},
		collectReport: func(context.Context, *Manifest, func(string)) (string, error) {
			return "/some/report", nil
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "ports",
		ReporterFactory: noopReporterFactory,
		Progress:        recordProgress(&phases),
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	want := []string{
		string(PhasePreparing), string(PhaseValidating), string(PhaseDeploying),
		string(PhaseDeployRetry), string(PhaseCollectingReport), string(PhaseTearingDown),
	}
	if strings.Join(phases, "|") != strings.Join(want, "|") {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
}

// TestRunScenario_ProgressPhases_Keep pins that a --keep run fires neither
// tearing_down nor collecting_report, and that a nil Progress callback never
// panics.
func TestRunScenario_ProgressPhases_Keep(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "stepfail", stepFailScenario)

	var execCalls []string
	var phases []string
	r := &Runner{
		execDwe:         stubExecDwe(nil, &execCalls),
		allocatePorts:   AllocatePorts,
		newTeardownDeps: fatalTeardownDeps(t),
		collectReport: func(context.Context, *Manifest, func(string)) (string, error) {
			t.Fatal("collectReport must not be called under --keep")
			return "", nil
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "stepfail",
		Keep:            true,
		ReporterFactory: noopReporterFactory,
		Progress:        recordProgress(&phases),
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if slices.Contains(phases, string(PhaseTearingDown)) {
		t.Fatalf("--keep run must not fire tearing_down: %v", phases)
	}
	if slices.Contains(phases, string(PhaseCollectingReport)) {
		t.Fatalf("--keep run must not fire collecting_report: %v", phases)
	}
	want := []string{
		string(PhasePreparing), string(PhaseValidating), string(PhaseDeploying),
		string(PhaseRunningSteps),
	}
	if strings.Join(phases, "|") != strings.Join(want, "|") {
		t.Fatalf("phases = %v, want %v", phases, want)
	}

	// A nil Progress callback must not panic anywhere along the full flow.
	dir2 := writeRunnerFixtureProject(t, "smoke", noStepsScenario)
	r2 := &Runner{
		execDwe:       stubExecDwe(nil, new([]string)),
		allocatePorts: AllocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(new([]string), nil)
		},
		clock: time.Now,
	}
	if _, err := r2.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir2,
		Scenario:        "smoke",
		ReporterFactory: noopReporterFactory,
	}); err != nil {
		t.Fatalf("RunScenario with nil Progress: %v", err)
	}
}

// recordingCollectReport returns a Runner.collectReport stub that appends
// "collect_report" to order (pass the SAME slice pointer given to
// recordingTeardownDeps to assert collection happens before teardown) and
// returns the given dir/err.
func recordingCollectReport(order *[]string, dir string, err error) func(context.Context, *Manifest, func(string)) (string, error) {
	return func(context.Context, *Manifest, func(string)) (string, error) {
		*order = append(*order, "collect_report")
		return dir, err
	}
}

const stepFailScenario = `steps:
  - name: "boom"
    type: shell
    cmd: "exit 1"
`

// TestRunScenario_CollectsReportOnFailure pins that every non-passed outcome
// (validate error, deploy failure, step failure, a timeout override) triggers
// exactly one collectReport call and sets ScenarioResult.ReportDir from it.
func TestRunScenario_CollectsReportOnFailure(t *testing.T) {
	tests := []struct {
		name        string
		scenario    string
		scenarioDef string
		execResults map[string]error
		timeout     time.Duration
		wantStatus  ScenarioStatus
	}{
		{
			name:        "validate error",
			scenario:    "smoke",
			scenarioDef: noStepsScenario,
			execResults: map[string]error{"validate": errors.New("validate: boom")},
			wantStatus:  StatusError,
		},
		{
			name:        "deploy failure",
			scenario:    "smoke",
			scenarioDef: noStepsScenario,
			execResults: map[string]error{"deploy run --silent": errors.New("deploy: boom")},
			wantStatus:  StatusFailed,
		},
		{
			name:        "step failure",
			scenario:    "stepfail",
			scenarioDef: stepFailScenario,
			wantStatus:  StatusFailed,
		},
		{
			name:        "timeout",
			scenario:    "slow",
			scenarioDef: "steps:\n  - name: \"slow\"\n    type: shell\n    cmd: \"sleep 5\"\n",
			timeout:     50 * time.Millisecond,
			wantStatus:  StatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeRunnerFixtureProject(t, tt.scenario, tt.scenarioDef)

			var teardownOrder []string
			var execCalls []string
			var reportCalls int
			wantDir := filepath.Join(dir, "report-for-"+tt.scenario)
			r := &Runner{
				execDwe:       stubExecDwe(tt.execResults, &execCalls),
				allocatePorts: AllocatePorts,
				newTeardownDeps: func(string, io.Writer) TeardownDeps {
					return recordingTeardownDeps(&teardownOrder, nil)
				},
				collectReport: func(ctx context.Context, m *Manifest, warn func(string)) (string, error) {
					reportCalls++
					// The report context must be fresh (never the scenario
					// context, which is already cancelled on a timeout failure) —
					// otherwise every timeout report would capture nothing.
					if err := ctx.Err(); err != nil {
						t.Errorf("collectReport received a cancelled context: %v", err)
					}
					return wantDir, nil
				},
				clock: time.Now,
			}

			result, err := r.RunScenario(context.Background(), RunRequest{
				BaseDir:         dir,
				Scenario:        tt.scenario,
				Timeout:         tt.timeout,
				ReporterFactory: noopReporterFactory,
			})
			if err != nil {
				t.Fatalf("RunScenario: %v", err)
			}
			if result.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", result.Status, tt.wantStatus)
			}
			if reportCalls != 1 {
				t.Fatalf("collectReport called %d times, want 1", reportCalls)
			}
			if result.ReportDir != wantDir {
				t.Fatalf("ReportDir = %q, want %q", result.ReportDir, wantDir)
			}
		})
	}
}

// TestRunScenario_PassingScenario_NoReport pins that a passed scenario never
// collects a report — collectReport must not even be called.
func TestRunScenario_PassingScenario_NoReport(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "smoke", noStepsScenario)

	var execCalls []string
	r := &Runner{
		execDwe:       stubExecDwe(nil, &execCalls),
		allocatePorts: AllocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(new([]string), nil)
		},
		collectReport: func(context.Context, *Manifest, func(string)) (string, error) {
			t.Fatal("collectReport must not be called for a passing scenario")
			return "", nil
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
	if result.ReportDir != "" {
		t.Fatalf("ReportDir = %q, want empty", result.ReportDir)
	}
}

// TestRunScenario_KeepFailingScenario_NoReport pins that --keep skips report
// collection even when the scenario fails — the live environment is its own
// debugging artifact, and collectReport must not run before the (skipped)
// teardown either.
func TestRunScenario_KeepFailingScenario_NoReport(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "stepfail", stepFailScenario)

	var execCalls []string
	r := &Runner{
		execDwe:         stubExecDwe(nil, &execCalls),
		allocatePorts:   AllocatePorts,
		newTeardownDeps: fatalTeardownDeps(t),
		collectReport: func(context.Context, *Manifest, func(string)) (string, error) {
			t.Fatal("collectReport must not be called under --keep")
			return "", nil
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "stepfail",
		Keep:            true,
		ReporterFactory: noopReporterFactory,
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.ReportDir != "" {
		t.Fatalf("ReportDir = %q, want empty", result.ReportDir)
	}
}

// TestRunScenario_CollectReportError_ScenarioOutcomeUnchanged pins that a
// failing report collection is best-effort: it warns but never changes the
// scenario's status/failed-step, leaves ReportDir empty, and teardown still
// runs.
func TestRunScenario_CollectReportError_ScenarioOutcomeUnchanged(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "stepfail", stepFailScenario)

	var teardownOrder []string
	var execCalls []string
	var warnings []string
	collectErr := errors.New("report: boom")
	r := &Runner{
		execDwe:       stubExecDwe(nil, &execCalls),
		allocatePorts: AllocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(&teardownOrder, nil)
		},
		collectReport: func(context.Context, *Manifest, func(string)) (string, error) {
			return "", collectErr
		},
		clock: time.Now,
	}

	result, err := r.RunScenario(context.Background(), RunRequest{
		BaseDir:         dir,
		Scenario:        "stepfail",
		ReporterFactory: noopReporterFactory,
		Warn:            func(msg string) { warnings = append(warnings, msg) },
	})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.FailedStep != "tests/boom" {
		t.Fatalf("FailedStep = %q, want tests/boom", result.FailedStep)
	}
	if result.ReportDir != "" {
		t.Fatalf("ReportDir = %q, want empty on collection failure", result.ReportDir)
	}
	if !containsStep(teardownOrder, "remove_copy") {
		t.Fatalf("expected teardown to still run: %v", teardownOrder)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, collectErr.Error()) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning mentioning %q, got %v", collectErr, warnings)
	}
}

// TestRunScenario_ReportCollectedBeforeTeardown pins the ordering contract:
// collectReport must run BEFORE teardown (containers still alive, copy's log
// still present).
func TestRunScenario_ReportCollectedBeforeTeardown(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "stepfail", stepFailScenario)

	var order []string
	var execCalls []string
	r := &Runner{
		execDwe:       stubExecDwe(nil, &execCalls),
		allocatePorts: AllocatePorts,
		newTeardownDeps: func(string, io.Writer) TeardownDeps {
			return recordingTeardownDeps(&order, nil)
		},
		collectReport: recordingCollectReport(&order, "/some/report/dir", nil),
		clock:         time.Now,
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
	if len(order) == 0 || order[0] != "collect_report" {
		t.Fatalf("order = %v, want collect_report first", order)
	}
	if !containsStep(order, "compose_down") {
		t.Fatalf("expected teardown steps to follow: %v", order)
	}
}
