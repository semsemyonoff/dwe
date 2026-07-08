package test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"

	"github.com/spf13/cobra"
)

// fakeRunner is the test double for scenarioRunner: it looks up a scripted
// result/error per scenario name (by call order, falling back to a default),
// and records every request it received.
type fakeRunner struct {
	results map[string]*envtest.ScenarioResult
	errs    map[string]error
	calls   []envtest.RunRequest
	// composeEnvAtCall records whether COMPOSE_* was already unset by the time
	// the first RunScenario call happened.
	composeEnvAtCall []string
}

func (f *fakeRunner) RunScenario(_ context.Context, req envtest.RunRequest) (*envtest.ScenarioResult, error) {
	f.calls = append(f.calls, req)
	f.composeEnvAtCall = append(f.composeEnvAtCall, os.Getenv("COMPOSE_PROJECT_NAME"))
	if err, ok := f.errs[req.Scenario]; ok {
		return nil, err
	}
	if res, ok := f.results[req.Scenario]; ok {
		return res, nil
	}
	return &envtest.ScenarioResult{Name: req.Scenario, Status: envtest.StatusPassed, Duration: time.Second}, nil
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

	err := runTestRun(cmd, flags, nil, false, 0)
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

	err := runTestRun(cmd, flags, nil, false, 0)
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

	err := runTestRun(cmd, flags, nil, false, 0)
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

	err := runTestRun(cmd, flags, []string{"does-not-exist"}, false, 0)
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

	if err := runTestRun(cmd, flags, []string{"b"}, false, 0); err != nil {
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
	if err := runTestRun(cmd, flags, []string{"b", "a", "b"}, false, 0); err != nil {
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

	if err := runTestRun(cmd, flags, nil, false, 0); err != nil {
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

	err := runTestRun(cmd, flags, nil, false, 0)
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

	if err := runTestRun(cmd, flags, nil, false, 0); err != nil {
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

	if err := runTestRun(cmd, flags, nil, true, 0); err != nil {
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

	if err := runTestRun(cmd, flags, nil, false, 15*time.Minute); err != nil {
		t.Fatalf("runTestRun: %v", err)
	}
	if f.calls[0].Timeout != 15*time.Minute {
		t.Errorf("Timeout = %v, want 15m", f.calls[0].Timeout)
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

	_ = runTestRun(cmd, flags, nil, false, 0)
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

	if err := runTestRun(cmd, flags, nil, false, 0); err != nil {
		t.Fatalf("runTestRun: %v", err)
	}
	if strings.Contains(out.String(), "report:") {
		t.Errorf("expected no report line for a passing scenario, got %q", out.String())
	}
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
