package test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"

	"github.com/spf13/cobra"
)

// fakeRunner is the test double for scenarioRunner: it looks up a scripted
// result/error per scenario name (by call order, falling back to a default),
// and records every request it received. The recording is mutex-guarded so the
// parallel path can drive it from multiple goroutines under -race.
type fakeRunner struct {
	results map[string]*envtest.ScenarioResult
	errs    map[string]error

	mu    sync.Mutex
	calls []envtest.RunRequest
	// composeEnvAtCall records whether COMPOSE_* was already unset by the time
	// the first RunScenario call happened.
	composeEnvAtCall []string

	// Optional hooks driving concurrency tests. release, when non-nil, gates a
	// scenario's return on a per-scenario channel (reverse-order completion).
	// inFlight / peak track live concurrency; started is closed after the first
	// call is recorded.
	release  map[string]chan struct{}
	inFlight atomic.Int32
	peak     atomic.Int32

	// firePhases, when non-empty, is fired through req.Progress (in order) on
	// every scenario before it returns — exercises the aggregated display wiring.
	firePhases []envtest.ProgressPhase
}

func (f *fakeRunner) RunScenario(ctx context.Context, req envtest.RunRequest) (*envtest.ScenarioResult, error) {
	n := f.inFlight.Add(1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	defer f.inFlight.Add(-1)

	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.composeEnvAtCall = append(f.composeEnvAtCall, os.Getenv("COMPOSE_PROJECT_NAME"))
	f.mu.Unlock()

	if req.Progress != nil {
		for _, p := range f.firePhases {
			req.Progress(p)
		}
	}

	if f.release != nil {
		if ch, ok := f.release[req.Scenario]; ok {
			select {
			case <-ch:
			case <-ctx.Done():
			}
		}
	}

	if err, ok := f.errs[req.Scenario]; ok {
		return nil, err
	}
	if res, ok := f.results[req.Scenario]; ok {
		return res, nil
	}
	return &envtest.ScenarioResult{Name: req.Scenario, Status: envtest.StatusPassed, Duration: time.Second}, nil
}

// recordedCalls returns a snapshot copy of the recorded requests.
func (f *fakeRunner) recordedCalls() []envtest.RunRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]envtest.RunRequest(nil), f.calls...)
}

func withFakeRunner(t *testing.T, f *fakeRunner) {
	t.Helper()
	orig := newRunner
	newRunner = func() scenarioRunner { return f }
	t.Cleanup(func() { newRunner = orig })
}

func newRunTestCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	var out, errW bytes.Buffer
	cmd := &cobra.Command{Use: "run"}
	cmd.SetOut(&out)
	cmd.SetErr(&errW)
	cmd.SetContext(context.Background())
	return cmd, &out, &errW
}

func TestRunTestRun_AllPassed_ExitZero(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "redis-off", "description: x\n")
	writeScenarioFile(t, baseDir, "smoke", "description: y\n")

	f := &fakeRunner{}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, _ := newRunTestCmd()

	err := runTestRun(cmd, flags, nil, false, 0, false)
	if err != nil {
		t.Fatalf("expected nil (exit 0), got %v", err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("expected 2 scenario runs, got %d", len(f.calls))
	}
	if !strings.Contains(out.String(), "2 passed, 0 failed") {
		t.Errorf("summary missing from text output: %q", out.String())
	}
}

func TestRunTestRun_FailedScenario_ExitOne(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "redis-off", "description: x\n")

	f := &fakeRunner{
		results: map[string]*envtest.ScenarioResult{
			"redis-off": {Name: "redis-off", Status: envtest.StatusFailed, FailedStep: "app answers", Duration: 2 * time.Second},
		},
	}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, _ := newRunTestCmd()

	err := runTestRun(cmd, flags, nil, false, 0, false)
	var oe *testRunOutcomeError
	if !errors.As(err, &oe) || oe.ExitCode() != 1 {
		t.Fatalf("expected exit-code-1 error, got %v", err)
	}
	if oe.Error() != "" {
		t.Errorf("outcome error must render no text, got %q", oe.Error())
	}
	if !strings.Contains(out.String(), `0 passed, 1 failed (redis-off: step "app answers")`) {
		t.Errorf("summary missing expected detail: %q", out.String())
	}
}

func TestRunTestRun_PrepError_ExitTwo(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: x\n")

	f := &fakeRunner{
		errs: map[string]error{
			"smoke": errors.New("flock held by process 123"),
		},
	}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, _, _ := newRunTestCmd()

	err := runTestRun(cmd, flags, nil, false, 0, false)
	var oe *testRunOutcomeError
	if !errors.As(err, &oe) || oe.ExitCode() != 2 {
		t.Fatalf("expected exit-code-2 error, got %v", err)
	}
}

func TestRunTestRun_UnknownScenario_PrepErrorBeforeAnyRun(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: x\n")

	f := &fakeRunner{}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, _, _ := newRunTestCmd()

	err := runTestRun(cmd, flags, []string{"does-not-exist"}, false, 0, false)
	if err == nil {
		t.Fatal("expected an error for an unknown scenario name")
	}
	ce, ok := errors.AsType[*cmdctx.CodedError](err)
	if !ok || ce.Code != "unknown_scenario" {
		t.Fatalf("expected unknown_scenario CodedError, got %T: %v", err, err)
	}
	if cmdctx.ExitCodeFor(err) != 2 {
		t.Errorf("ExitCodeFor(unknown_scenario) = %d, want 2", cmdctx.ExitCodeFor(err))
	}
	if len(f.calls) != 0 {
		t.Errorf("no scenario should have been attempted, got %d calls", len(f.calls))
	}
}

func TestRunTestRun_ExplicitScenarioNames(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "a", "description: x\n")
	writeScenarioFile(t, baseDir, "b", "description: x\n")

	f := &fakeRunner{}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, _, _ := newRunTestCmd()

	if err := runTestRun(cmd, flags, []string{"b"}, false, 0, false); err != nil {
		t.Fatalf("runTestRun: %v", err)
	}
	if len(f.calls) != 1 || f.calls[0].Scenario != "b" {
		t.Fatalf("expected exactly scenario 'b' to run, got %+v", f.calls)
	}
}

func TestRunTestRun_DuplicateArgs_RunOnce(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "a", "description: x\n")
	writeScenarioFile(t, baseDir, "b", "description: x\n")

	f := &fakeRunner{}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, _, _ := newRunTestCmd()

	// A repeated name must collapse to a single run (first-mention order),
	// not run twice — the second run would spuriously trip the --keep guard.
	if err := runTestRun(cmd, flags, []string{"b", "a", "b"}, false, 0, false); err != nil {
		t.Fatalf("runTestRun: %v", err)
	}
	if len(f.calls) != 2 || f.calls[0].Scenario != "b" || f.calls[1].Scenario != "a" {
		t.Fatalf("expected scenarios [b a] to run once each, got %+v", f.calls)
	}
}

func TestRunTestRun_NoScenarios_ExitZero(t *testing.T) {
	baseDir := t.TempDir()
	f := &fakeRunner{}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, _ := newRunTestCmd()

	if err := runTestRun(cmd, flags, nil, false, 0, false); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !strings.Contains(out.String(), "no scenarios to run") {
		t.Errorf("expected the no-scenarios message, got %q", out.String())
	}
}

func TestRunTestRun_JSONShape(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: x\n")

	f := &fakeRunner{
		results: map[string]*envtest.ScenarioResult{
			"smoke": {Name: "smoke", Status: envtest.StatusPassed, Duration: 1500 * time.Millisecond, ReportDir: ""},
		},
	}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir, Output: "json"}
	cmd, out, _ := newRunTestCmd()

	err := runTestRun(cmd, flags, nil, false, 0, false)
	if err != nil {
		t.Fatalf("expected nil (exit 0), got %v", err)
	}

	var got testRunJSON
	if uerr := json.Unmarshal(out.Bytes(), &got); uerr != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", uerr, out.String())
	}
	if len(got.Scenarios) != 1 || got.Scenarios[0].Name != "smoke" || got.Scenarios[0].Status != "passed" {
		t.Fatalf("unexpected scenarios: %+v", got.Scenarios)
	}
	if got.Scenarios[0].DurationSeconds != 1.5 {
		t.Errorf("DurationSeconds = %v, want 1.5", got.Scenarios[0].DurationSeconds)
	}
	if got.Summary == "" {
		t.Error("expected a non-empty summary")
	}
	// The fake runner injects a live pipeline reporter factory in JSON mode;
	// verify nothing beyond the JSON payload landed on stdout.
	var probe map[string]any
	if uerr := json.Unmarshal(out.Bytes(), &probe); uerr != nil {
		t.Fatalf("stdout must contain only the JSON payload: %v", uerr)
	}
}

func TestRunTestRun_ScrubsComposeEnvBeforeRunnerCalls(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "leftover")
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: x\n")

	f := &fakeRunner{}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, _, _ := newRunTestCmd()

	if err := runTestRun(cmd, flags, nil, false, 0, false); err != nil {
		t.Fatalf("runTestRun: %v", err)
	}
	if len(f.composeEnvAtCall) != 1 || f.composeEnvAtCall[0] != "" {
		t.Errorf("expected COMPOSE_PROJECT_NAME to be scrubbed before RunScenario, got %q", f.composeEnvAtCall)
	}
}

func TestRunTestRun_KeepPrintsCleanupHint(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: x\n")

	f := &fakeRunner{
		results: map[string]*envtest.ScenarioResult{
			"smoke": {Name: "smoke", Status: envtest.StatusPassed, ComposeProject: "proj-t-smoke-abc123", CopyPath: "/tmp/copy"},
		},
	}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, _ := newRunTestCmd()

	if err := runTestRun(cmd, flags, nil, true, 0, false); err != nil {
		t.Fatalf("runTestRun: %v", err)
	}
	if !f.calls[0].Keep {
		t.Error("expected RunRequest.Keep to be true")
	}
	if !strings.Contains(out.String(), "proj-t-smoke-abc123") || !strings.Contains(out.String(), "/tmp/copy") {
		t.Errorf("expected keep hint with project/copy path, got %q", out.String())
	}
	if !strings.Contains(out.String(), "dwe test clean smoke") {
		t.Errorf("expected keep hint to point at `dwe test clean smoke`, got %q", out.String())
	}
}

func TestRunTestRun_TimeoutFlagThreaded(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: x\n")

	f := &fakeRunner{}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, _, _ := newRunTestCmd()

	if err := runTestRun(cmd, flags, nil, false, 15*time.Minute, false); err != nil {
		t.Fatalf("runTestRun: %v", err)
	}
	if f.calls[0].Timeout != 15*time.Minute {
		t.Errorf("Timeout = %v, want 15m", f.calls[0].Timeout)
	}
}

func TestRunTestRun_SkipIsolationCheckFlagThreaded(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: x\n")

	f := &fakeRunner{}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, _, _ := newRunTestCmd()

	if err := runTestRun(cmd, flags, nil, false, 0, true); err != nil {
		t.Fatalf("runTestRun: %v", err)
	}
	if !f.calls[0].SkipIsolationCheck {
		t.Error("expected RunRequest.SkipIsolationCheck to be true")
	}
}

func TestRunTestRun_FailedScenarioWithReportDir_RendersReportLine(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: x\n")

	f := &fakeRunner{
		results: map[string]*envtest.ScenarioResult{
			"smoke": {
				Name: "smoke", Status: envtest.StatusFailed, FailedStep: "app answers",
				ReportDir: "/tmp/.dwe/tests/reports/smoke",
			},
		},
	}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, _ := newRunTestCmd()

	_ = runTestRun(cmd, flags, nil, false, 0, false)
	if !strings.Contains(out.String(), "report: /tmp/.dwe/tests/reports/smoke") {
		t.Errorf("expected report line for failed scenario, got %q", out.String())
	}
}

func TestRunTestRun_PassingScenario_NoReportLine(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: x\n")

	f := &fakeRunner{
		results: map[string]*envtest.ScenarioResult{
			"smoke": {Name: "smoke", Status: envtest.StatusPassed, ReportDir: ""},
		},
	}
	withFakeRunner(t, f)

	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, _ := newRunTestCmd()

	if err := runTestRun(cmd, flags, nil, false, 0, false); err != nil {
		t.Fatalf("runTestRun: %v", err)
	}
	if strings.Contains(out.String(), "report:") {
		t.Errorf("expected no report line for a passing scenario, got %q", out.String())
	}
}

// --- Exact full-match output tests (pin N=1 byte-identity) ---
//
// These require.Equal on the COMPLETE stdout/stderr for the effective<=1
// paths, so the parallel restructure cannot silently alter a line, newline, or
// reporter. They drive runTest (the RunE entry) at --parallel 1.

func TestRunTest_Seq_AllPassed_ExactOutput(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "redis-off", "description: x\n")
	writeScenarioFile(t, baseDir, "smoke", "description: y\n")

	withFakeRunner(t, &fakeRunner{})
	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, errW := newRunTestCmd()

	if err := runTest(cmd, flags, nil, false, 0, false, 1); err != nil {
		t.Fatalf("expected nil (exit 0), got %v", err)
	}
	const want = "redis-off: passed [1s]\nsmoke: passed [1s]\n\n2 passed, 0 failed\n"
	if out.String() != want {
		t.Errorf("stdout mismatch:\n got: %q\nwant: %q", out.String(), want)
	}
	if errW.String() != "" {
		t.Errorf("stderr must be empty, got %q", errW.String())
	}
}

func TestRunTest_Seq_FailedScenario_ExactOutput(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "redis-off", "description: x\n")

	f := &fakeRunner{results: map[string]*envtest.ScenarioResult{
		"redis-off": {Name: "redis-off", Status: envtest.StatusFailed, FailedStep: "app answers", Duration: 2 * time.Second},
	}}
	withFakeRunner(t, f)
	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, errW := newRunTestCmd()

	err := runTest(cmd, flags, nil, false, 0, false, 1)
	var oe *testRunOutcomeError
	if !errors.As(err, &oe) || oe.ExitCode() != 1 {
		t.Fatalf("expected exit-code-1 error, got %v", err)
	}
	const want = "redis-off: failed — step \"app answers\" [2s]\n\n0 passed, 1 failed (redis-off: step \"app answers\")\n"
	if out.String() != want {
		t.Errorf("stdout mismatch:\n got: %q\nwant: %q", out.String(), want)
	}
	if errW.String() != "" {
		t.Errorf("stderr must be empty, got %q", errW.String())
	}
}

func TestRunTest_Seq_PrepError_ExactOutput(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: x\n")

	f := &fakeRunner{errs: map[string]error{"smoke": errors.New("flock held by process 123")}}
	withFakeRunner(t, f)
	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, errW := newRunTestCmd()

	err := runTest(cmd, flags, nil, false, 0, false, 1)
	var oe *testRunOutcomeError
	if !errors.As(err, &oe) || oe.ExitCode() != 2 {
		t.Fatalf("expected exit-code-2 error, got %v", err)
	}
	const wantOut = "smoke: error (flock held by process 123) [0s]\n\n0 passed, 1 failed (smoke: flock held by process 123)\n"
	if out.String() != wantOut {
		t.Errorf("stdout mismatch:\n got: %q\nwant: %q", out.String(), wantOut)
	}
	const wantErr = "warning: scenario \"smoke\" could not be prepared: flock held by process 123\n"
	if errW.String() != wantErr {
		t.Errorf("stderr mismatch:\n got: %q\nwant: %q", errW.String(), wantErr)
	}
}

func TestRunTest_Seq_Keep_ExactOutput(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: x\n")

	f := &fakeRunner{results: map[string]*envtest.ScenarioResult{
		"smoke": {Name: "smoke", Status: envtest.StatusPassed, ComposeProject: "proj-t-smoke-abc123", CopyPath: "/tmp/copy"},
	}}
	withFakeRunner(t, f)
	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, errW := newRunTestCmd()

	if err := runTest(cmd, flags, nil, true, 0, false, 1); err != nil {
		t.Fatalf("runTest: %v", err)
	}
	const want = "smoke: passed [0s]\n  kept: compose project proj-t-smoke-abc123, copy at /tmp/copy — run `dwe test clean smoke` to remove\n\n1 passed, 0 failed\n"
	if out.String() != want {
		t.Errorf("stdout mismatch:\n got: %q\nwant: %q", out.String(), want)
	}
	if errW.String() != "" {
		t.Errorf("stderr must be empty, got %q", errW.String())
	}
}

func TestRunTest_Seq_NoScenarios_ExactOutput(t *testing.T) {
	baseDir := t.TempDir()
	withFakeRunner(t, &fakeRunner{})
	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, errW := newRunTestCmd()

	if err := runTest(cmd, flags, nil, false, 0, false, 1); err != nil {
		t.Fatalf("runTest: %v", err)
	}
	if out.String() != "no scenarios to run\n" {
		t.Errorf("stdout mismatch: %q", out.String())
	}
	if errW.String() != "" {
		t.Errorf("stderr must be empty, got %q", errW.String())
	}
}

func TestRunTest_Seq_JSON_ExactOutput(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: x\n")

	f := &fakeRunner{results: map[string]*envtest.ScenarioResult{
		"smoke": {Name: "smoke", Status: envtest.StatusPassed, Duration: 1500 * time.Millisecond},
	}}
	withFakeRunner(t, f)
	flags := &cmdctx.RootFlags{Root: baseDir, Output: "json"}
	cmd, out, errW := newRunTestCmd()

	if err := runTest(cmd, flags, nil, false, 0, false, 1); err != nil {
		t.Fatalf("runTest: %v", err)
	}
	const want = `{"scenarios":[{"name":"smoke","status":"passed","duration_seconds":1.5}],"summary":"1 passed, 0 failed"}` + "\n"
	if out.String() != want {
		t.Errorf("stdout mismatch:\n got: %q\nwant: %q", out.String(), want)
	}
	if errW.String() != "" {
		t.Errorf("stderr must be empty, got %q", errW.String())
	}
	// In sequential text/JSON mode the request carries NO silent reporter
	// factory beyond JSON's own; the parallel path is the one that always sets
	// it. Here JSON mode does set it — assert it is present exactly as before.
	if f.recordedCalls()[0].ReporterFactory == nil {
		t.Error("JSON mode must set a silent ReporterFactory")
	}
}

// --- Parallel orchestration tests ---

func TestRunTest_InvalidParallel_ExitTwo(t *testing.T) {
	for _, n := range []int{0, -1, -8} {
		baseDir := t.TempDir()
		writeScenarioFile(t, baseDir, "smoke", "description: x\n")
		f := &fakeRunner{}
		withFakeRunner(t, f)
		flags := &cmdctx.RootFlags{Root: baseDir}
		cmd, _, _ := newRunTestCmd()

		err := runTest(cmd, flags, nil, false, 0, false, n)
		ce, ok := errors.AsType[*cmdctx.CodedError](err)
		if !ok || ce.Code != "invalid_parallel" {
			t.Fatalf("--parallel %d: expected invalid_parallel CodedError, got %T: %v", n, err, err)
		}
		if cmdctx.ExitCodeFor(err) != 2 {
			t.Errorf("--parallel %d: ExitCodeFor = %d, want 2", n, cmdctx.ExitCodeFor(err))
		}
		if len(f.recordedCalls()) != 0 {
			t.Errorf("--parallel %d: no scenario should run, got %d calls", n, len(f.recordedCalls()))
		}
	}
}

func TestRunTest_ParallelExceedsScenarioCount_SequentialPath(t *testing.T) {
	baseDir := t.TempDir()
	writeScenarioFile(t, baseDir, "smoke", "description: x\n")

	// --parallel 8 with one scenario → effective 1 → sequential path.
	fPar := &fakeRunner{}
	withFakeRunner(t, fPar)
	flags := &cmdctx.RootFlags{Root: baseDir}
	cmdPar, outPar, errPar := newRunTestCmd()
	if err := runTest(cmdPar, flags, nil, false, 0, false, 8); err != nil {
		t.Fatalf("runTest --parallel 8: %v", err)
	}

	// A no-flag (parallel 1) run must produce byte-identical output.
	fSeq := &fakeRunner{}
	withFakeRunner(t, fSeq)
	cmdSeq, outSeq, errSeq := newRunTestCmd()
	if err := runTest(cmdSeq, flags, nil, false, 0, false, 1); err != nil {
		t.Fatalf("runTest --parallel 1: %v", err)
	}

	if outPar.String() != outSeq.String() {
		t.Errorf("--parallel 8 (one scenario) stdout diverged from sequential:\n par: %q\n seq: %q", outPar.String(), outSeq.String())
	}
	if errPar.String() != errSeq.String() {
		t.Errorf("--parallel 8 (one scenario) stderr diverged from sequential:\n par: %q\n seq: %q", errPar.String(), errSeq.String())
	}
	// Sequential text path installs NO silent reporter factory.
	if fPar.recordedCalls()[0].ReporterFactory != nil {
		t.Error("sequential text path must not set a ReporterFactory")
	}
}

func TestRunTest_Parallel_OrderPreservedReverseCompletion(t *testing.T) {
	baseDir := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		writeScenarioFile(t, baseDir, n, "description: x\n")
	}

	f := &fakeRunner{release: map[string]chan struct{}{
		"a": make(chan struct{}),
		"b": make(chan struct{}),
		"c": make(chan struct{}),
	}}
	withFakeRunner(t, f)
	flags := &cmdctx.RootFlags{Root: baseDir, Output: "json"}
	cmd, out, _ := newRunTestCmd()

	done := make(chan error, 1)
	go func() { done <- runTest(cmd, flags, nil, false, 0, false, 3) }()

	waitFor(t, func() bool { return len(f.recordedCalls()) == 3 })
	// Release in reverse order — completion order is c, b, a.
	close(f.release["c"])
	close(f.release["b"])
	close(f.release["a"])

	if err := <-done; err != nil {
		t.Fatalf("runTest: %v", err)
	}

	var got testRunJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out.String())
	}
	names := []string{got.Scenarios[0].Name, got.Scenarios[1].Name, got.Scenarios[2].Name}
	if names[0] != "a" || names[1] != "b" || names[2] != "c" {
		t.Errorf("output order must match original name order [a b c], got %v", names)
	}
}

func TestRunTest_Parallel_PeakConcurrencyBounded(t *testing.T) {
	baseDir := t.TempDir()
	names := []string{"a", "b", "c", "d"}
	release := map[string]chan struct{}{}
	for _, n := range names {
		writeScenarioFile(t, baseDir, n, "description: x\n")
		release[n] = make(chan struct{})
	}

	f := &fakeRunner{release: release}
	withFakeRunner(t, f)
	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, _, _ := newRunTestCmd()

	done := make(chan error, 1)
	go func() { done <- runTest(cmd, flags, nil, false, 0, false, 2) }()

	// With SetLimit(2) exactly two scenarios can be in-flight at once.
	waitFor(t, func() bool { return f.inFlight.Load() == 2 })
	for _, ch := range release {
		close(ch)
	}
	<-done

	if peak := f.peak.Load(); peak != 2 {
		t.Errorf("peak concurrency = %d, want exactly 2 (>1 proves parallelism, ≤2 proves the limit held)", peak)
	}
}

func TestRunTest_Parallel_CancelBeforeDispatch_NoOutcomes(t *testing.T) {
	baseDir := t.TempDir()
	for _, n := range []string{"a", "b"} {
		writeScenarioFile(t, baseDir, n, "description: x\n")
	}

	f := &fakeRunner{}
	withFakeRunner(t, f)
	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, _ := newRunTestCmd()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before dispatch
	cmd.SetContext(ctx)

	if err := runTest(cmd, flags, nil, false, 0, false, 2); err != nil {
		t.Fatalf("runTest: %v", err)
	}
	// Every scenario sees ctx.Err() != nil first → nil slot → absent.
	if len(f.recordedCalls()) != 0 {
		t.Errorf("no scenario should have started, got %d calls", len(f.recordedCalls()))
	}
	if out.String() != "no scenarios to run\n" {
		t.Errorf("expected empty-outcome body, got %q", out.String())
	}
}

func TestRunTest_Parallel_InFlightHonorsCtx_OutcomePresent(t *testing.T) {
	baseDir := t.TempDir()
	for _, n := range []string{"a", "b"} {
		writeScenarioFile(t, baseDir, n, "description: x\n")
	}

	f := &fakeRunner{release: map[string]chan struct{}{
		"a": make(chan struct{}),
		"b": make(chan struct{}),
	}}
	withFakeRunner(t, f)
	flags := &cmdctx.RootFlags{Root: baseDir, Output: "json"}
	cmd, out, _ := newRunTestCmd()
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)

	done := make(chan error, 1)
	go func() { done <- runTest(cmd, flags, nil, false, 0, false, 2) }()

	waitFor(t, func() bool { return len(f.recordedCalls()) == 2 })
	cancel() // both scenarios are in-flight; the fake honors ctx and returns

	if err := <-done; err != nil {
		t.Fatalf("runTest: %v", err)
	}
	var got testRunJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out.String())
	}
	if len(got.Scenarios) != 2 {
		t.Errorf("in-flight scenarios must be present in outcomes, got %+v", got.Scenarios)
	}
}

func TestRunTest_Parallel_JSONShape(t *testing.T) {
	baseDir := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		writeScenarioFile(t, baseDir, n, "description: x\n")
	}

	f := &fakeRunner{}
	withFakeRunner(t, f)
	flags := &cmdctx.RootFlags{Root: baseDir, Output: "json"}
	cmd, out, errW := newRunTestCmd()

	if err := runTest(cmd, flags, nil, false, 0, false, 3); err != nil {
		t.Fatalf("runTest: %v", err)
	}
	var got testRunJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout not clean JSON: %v\n%s", err, out.String())
	}
	if len(got.Scenarios) != 3 || got.Scenarios[0].Name != "a" || got.Scenarios[2].Name != "c" {
		t.Errorf("expected ordered scenarios [a b c], got %+v", got.Scenarios)
	}
	if errW.String() != "" {
		t.Errorf("JSON mode stderr must stay clean, got %q", errW.String())
	}
	// Parallel path installs the silent reporter factory in every mode.
	for _, c := range f.recordedCalls() {
		if c.ReporterFactory == nil {
			t.Errorf("parallel path must set a silent ReporterFactory for %q", c.Scenario)
		}
	}
}

// withForcedTTYDisplay overrides liveDisplayEnv so the parallel path builds a
// TTY-mode aggregated display (fixed height) even though the test's stdout is a
// buffer — lets the end-to-end test observe Progress-driven row labels.
func withForcedTTYDisplay(t *testing.T, height int) {
	t.Helper()
	orig := liveDisplayEnv
	liveDisplayEnv = func() (bool, func() (int, int)) {
		return true, func() (int, int) { return 80, height }
	}
	t.Cleanup(func() { liveDisplayEnv = orig })
}

// TestRunTest_Parallel_DisplayReceivesProgress drives a parallel text run with
// a fake runner that fires Progress phases; the forced-TTY aggregated display
// must relabel rows with the coarse phase text.
func TestRunTest_Parallel_DisplayReceivesProgress(t *testing.T) {
	baseDir := t.TempDir()
	for _, n := range []string{"a", "b"} {
		writeScenarioFile(t, baseDir, n, "description: x\n")
	}
	withForcedTTYDisplay(t, 24)

	f := &fakeRunner{firePhases: []envtest.ProgressPhase{envtest.PhaseValidating, envtest.PhaseDeploying}}
	withFakeRunner(t, f)
	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, _ := newRunTestCmd()

	if err := runTest(cmd, flags, nil, false, 0, false, 2); err != nil {
		t.Fatalf("runTest: %v", err)
	}
	text := stripANSI(out.String())
	// Phase labels reached the display and relabeled the rows.
	if !strings.Contains(text, "validating…") || !strings.Contains(text, "deploying…") {
		t.Errorf("expected phase labels in the aggregated display, got:\n%s", text)
	}
	// Rows finalize with the passed label + summary still renders below.
	if !strings.Contains(text, "a  passed") || !strings.Contains(text, "b  passed") {
		t.Errorf("expected finalized row labels, got:\n%s", text)
	}
	if !strings.Contains(text, "2 passed, 0 failed") {
		t.Errorf("expected the final text report below the block, got:\n%s", text)
	}
}

// TestRunTest_Parallel_NonTTYFlatLines drives a parallel text run in disabled
// (buffer / non-TTY) mode: every scenario must emit its flat start/status lines
// so piped/CI runs are not silent until the final report.
func TestRunTest_Parallel_NonTTYFlatLines(t *testing.T) {
	baseDir := t.TempDir()
	for _, n := range []string{"a", "b"} {
		writeScenarioFile(t, baseDir, n, "description: x\n")
	}
	// Default liveDisplayEnv: the test's stdout buffer is non-TTY → disabled.

	f := &fakeRunner{}
	withFakeRunner(t, f)
	flags := &cmdctx.RootFlags{Root: baseDir}
	cmd, out, _ := newRunTestCmd()

	if err := runTest(cmd, flags, nil, false, 0, false, 2); err != nil {
		t.Fatalf("runTest: %v", err)
	}
	got := out.String()
	for _, want := range []string{"scenario a: started", "scenario b: started", "scenario a: passed", "scenario b: passed"} {
		if !strings.Contains(got, want) {
			t.Errorf("non-TTY parallel run missing flat line %q, got:\n%s", want, got)
		}
	}
}

// waitFor polls cond up to ~2s, failing the test on timeout.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

// TestNewTestRunCmd_TimeoutParseError exercises the cobra flag parsing path
// directly (DurationVar) rather than the RunE body.
func TestNewTestRunCmd_TimeoutParseError(t *testing.T) {
	flags := &cmdctx.RootFlags{}
	cmd := newTestRunCmd(flags)
	cmd.SetArgs([]string{"--timeout", "not-a-duration"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected a flag-parse error for an invalid --timeout value")
	}
}
