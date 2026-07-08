package test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"
	sharedrender "github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// scenarioRunner is the seam `dwe test run` drives; envtest.NewRunner()
// satisfies it in production, tests inject a fake.
type scenarioRunner interface {
	RunScenario(ctx context.Context, req envtest.RunRequest) (*envtest.ScenarioResult, error)
}

// newRunner builds the production scenarioRunner. Tests reassign this var to
// inject a fake — real RunScenario spawns `dwe validate`/`dwe deploy run`
// subprocesses and would recurse into the test binary itself.
var newRunner = func() scenarioRunner { return envtest.NewRunner() }

func newTestRunCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var keep bool
	var timeout time.Duration
	var skipIsolationCheck bool

	cmd := &cobra.Command{
		Use:   "run [scenario...]",
		Short: "Run one or more integration test scenarios",
		Long: `Run declarative integration-test scenarios against a disposable, isolated copy
of the project.

With no arguments, every scenario under workspace/tests/*.yml runs, in sorted
name order. Passing scenario names runs exactly those (an unknown name fails
before anything runs). Ctrl+C cancels the scenario currently running, tears it
down, and skips the rest.

Before deploying, the copy's raw compose files are scanned for constructs
that bypass Docker-Compose project-name scoping (container_name:, literal
host ports, external/named volumes & networks). A blocking hazard
(container_name:, a literal host port) fails the scenario before anything is
deployed; pass --skip-isolation-check to downgrade every finding to a warning
and proceed anyway.

Exit codes: 0 = every scenario passed, 1 = at least one scenario failed,
2 = a scenario (or the run itself) could not be prepared.`,
		Example: `  dwe test run
  dwe test run redis-off db-migration
  dwe test run --keep smoke
  dwe test run --timeout 15m
  dwe test run --skip-isolation-check smoke`,
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTestRun(cmd, flags, args, keep, timeout, skipIsolationCheck)
		},
	}

	cmd.Flags().BoolVar(&keep, "keep", false,
		"skip teardown; leave the copy, manifest, and Docker/bridge state running for debugging")
	cmd.Flags().DurationVar(&timeout, "timeout", 0,
		"override every scenario's own timeout (e.g. 15m); 0 = use the scenario's timeout: field or the 30m default")
	cmd.Flags().BoolVar(&skipIsolationCheck, "skip-isolation-check", false,
		"downgrade compose isolation findings (container_name:, literal host ports, …) to warnings instead of blocking the scenario")
	return cmd
}

// scenarioOutcome is the CLI's per-scenario view: either a real
// *envtest.ScenarioResult, or a synthetic StatusError outcome for a scenario
// RunScenario refused to even attempt (flock held, a kept prior run, load
// failure) — those return a bare error with nothing to report through
// ScenarioResult.
type scenarioOutcome struct {
	Name           string
	Status         envtest.ScenarioStatus
	FailedStep     string
	Duration       time.Duration
	ReportDir      string
	ComposeProject string
	CopyPath       string
	Message        string
}

func scenarioOutcomeFromResult(res *envtest.ScenarioResult) scenarioOutcome {
	return scenarioOutcome{
		Name:           res.Name,
		Status:         res.Status,
		FailedStep:     res.FailedStep,
		Duration:       res.Duration,
		ReportDir:      res.ReportDir,
		ComposeProject: res.ComposeProject,
		CopyPath:       res.CopyPath,
	}
}

// testScenarioJSON is one scenario row of `dwe test run --output json`.
type testScenarioJSON struct {
	Name            string  `json:"name"`
	Status          string  `json:"status"`
	FailedStep      string  `json:"failed_step,omitempty"`
	DurationSeconds float64 `json:"duration_seconds"`
	ReportDir       string  `json:"report_dir,omitempty"`
}

// testRunJSON is the JSON payload for `dwe test run --output json`.
type testRunJSON struct {
	Scenarios []testScenarioJSON `json:"scenarios"`
	Summary   string             `json:"summary"`
}

// testRunOutcomeError carries the process exit code for a completed test run
// whose JSON/text payload has already been written. Its Error() renders no
// text (mirrors deployCancelledError) so main.go's ExitCode-bearing-error
// path suppresses any further output.
type testRunOutcomeError struct{ code int }

func (e *testRunOutcomeError) Error() string { return "" }
func (e *testRunOutcomeError) ExitCode() int { return e.code }

func runTestRun(cmd *cobra.Command, flags *cmdctx.RootFlags, args []string, keep bool, timeout time.Duration, skipIsolationCheck bool) error {
	// Must run before the flock, any goroutine, UI, or subprocess (spec §3).
	envtest.ScrubComposeEnv()

	baseDir := flags.ProjectRoot()
	names, err := resolveScenarioNames(baseDir, args)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner := newRunner()
	warn := func(msg string) {
		// Stderr in JSON mode is reserved for the error envelope.
		if flags.Output == "json" {
			return
		}
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+msg)
	}
	var reporterFactory envtest.ReporterFactory
	if flags.Output == "json" {
		// Live pipeline output must never leak into JSON stdout; the file log
		// still records everything.
		reporterFactory = jsonReporterFactory
	}

	outcomes := make([]scenarioOutcome, 0, len(names))
	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		req := envtest.RunRequest{
			BaseDir:            baseDir,
			Scenario:           name,
			Keep:               keep,
			Timeout:            timeout,
			Translator:         flags.I18n,
			Locale:             flags.Locale,
			ReporterFactory:    reporterFactory,
			Warn:               warn,
			SkipIsolationCheck: skipIsolationCheck,
		}
		res, err := runner.RunScenario(ctx, req)
		if err != nil {
			warn(fmt.Sprintf("scenario %q could not be prepared: %v", name, err))
			outcomes = append(outcomes, scenarioOutcome{Name: name, Status: envtest.StatusError, Message: err.Error()})
			continue
		}
		outcomes = append(outcomes, scenarioOutcomeFromResult(res))
	}

	payload := testRunJSON{Scenarios: make([]testScenarioJSON, 0, len(outcomes))}
	for _, o := range outcomes {
		payload.Scenarios = append(payload.Scenarios, testScenarioJSON{
			Name:            o.Name,
			Status:          string(o.Status),
			FailedStep:      o.FailedStep,
			DurationSeconds: o.Duration.Seconds(),
			ReportDir:       o.ReportDir,
		})
	}
	payload.Summary = buildSummary(outcomes)

	if err := cmdctx.WriteData(flags, cmd, payload, func(testRunJSON) string {
		return renderTestRunText(outcomes, keep)
	}); err != nil {
		return err
	}

	errorCount, failedCount := 0, 0
	for _, o := range outcomes {
		switch o.Status {
		case envtest.StatusError:
			errorCount++
		case envtest.StatusFailed:
			failedCount++
		}
	}
	switch {
	case errorCount > 0:
		return &testRunOutcomeError{code: 2}
	case failedCount > 0:
		return &testRunOutcomeError{code: 1}
	default:
		return nil
	}
}

// resolveScenarioNames returns the scenario names to run: every scenario
// (sorted) when args is empty, else exactly the requested names — validated
// against the discovered set up front so an unknown name fails before any
// scenario runs (a prep error, exit 2). Duplicate args are collapsed to a
// single run (order of first mention preserved): a repeated name would
// otherwise run redundantly and, under --keep, error on the second run
// because the first left a kept manifest behind.
func resolveScenarioNames(baseDir string, args []string) ([]string, error) {
	all, err := envtest.ListScenarios(baseDir)
	if err != nil {
		return nil, cmdctx.ErrWrap("scenario_list_failed", err)
	}
	if len(args) == 0 {
		return all, nil
	}
	known := make(map[string]bool, len(all))
	for _, n := range all {
		known[n] = true
	}
	seen := make(map[string]bool, len(args))
	names := make([]string, 0, len(args))
	for _, n := range args {
		if !known[n] {
			return nil, cmdctx.Err("unknown_scenario", fmt.Sprintf("unknown scenario %q", n)).
				WithHint("run `dwe test list` to see available scenarios").
				WithDetail("scenario", n)
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	return names, nil
}

// jsonReporterFactory builds the steps-pipeline reporter with a silenced
// screen (io.Discard) so live output never reaches JSON stdout, while still
// writing the scenario's run log to disk. subprocOut is the log file only (no
// terminal tee) so the validate/deploy subprocess output never leaks into the
// JSON payload either.
func jsonReporterFactory(workDir, name string) (pipeline.Reporter, io.Writer, io.Writer, func(), error) {
	_, logFile, _, _, cleanup, err := pipeline.OpenPipelineLog(workDir, name, true)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	rep := pipeline.NewPlainReporter(sharedrender.NewWriter(io.Discard), logFile, io.Discard)
	return rep, logFile, logFile, func() {
		rep.Close()
		cleanup()
	}, nil
}

// buildSummary renders the "N passed, M failed (…)" line. Both StatusFailed
// and StatusError count toward "failed" for this human-readable tally; the
// parenthetical lists every non-passed scenario with its most specific
// available detail (failing step, prep-error message, or bare status).
func buildSummary(outcomes []scenarioOutcome) string {
	passed, failed := 0, 0
	var details []string
	for _, o := range outcomes {
		if o.Status == envtest.StatusPassed {
			passed++
			continue
		}
		failed++
		switch {
		case o.FailedStep != "":
			details = append(details, fmt.Sprintf("%s: step %q", o.Name, o.FailedStep))
		case o.Message != "":
			details = append(details, fmt.Sprintf("%s: %s", o.Name, o.Message))
		default:
			details = append(details, fmt.Sprintf("%s: %s", o.Name, o.Status))
		}
	}
	summary := fmt.Sprintf("%d passed, %d failed", passed, failed)
	if len(details) > 0 {
		summary += " (" + strings.Join(details, "; ") + ")"
	}
	return summary
}

// renderTestRunText renders the per-scenario lines followed by a blank line
// and the summary. An empty outcome set (no scenarios to run) renders a
// single explanatory line instead of an empty body.
func renderTestRunText(outcomes []scenarioOutcome, keep bool) string {
	if len(outcomes) == 0 {
		return "no scenarios to run"
	}
	lines := make([]string, 0, len(outcomes)+2)
	for _, o := range outcomes {
		lines = append(lines, renderScenarioLine(o, keep))
	}
	lines = append(lines, "", buildSummary(outcomes))
	return strings.Join(lines, "\n")
}

func renderScenarioLine(o scenarioOutcome, keep bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", o.Name, o.Status)
	if o.FailedStep != "" {
		fmt.Fprintf(&b, " — step %q", o.FailedStep)
	} else if o.Message != "" {
		fmt.Fprintf(&b, " (%s)", o.Message)
	}
	fmt.Fprintf(&b, " [%s]", o.Duration.Round(time.Millisecond))
	if keep && o.ComposeProject != "" {
		fmt.Fprintf(&b, "\n  kept: compose project %s, copy at %s — run `dwe test clean %s` to remove", o.ComposeProject, o.CopyPath, o.Name)
	}
	if o.ReportDir != "" {
		fmt.Fprintf(&b, "\n  report: %s", o.ReportDir)
	}
	return b.String()
}
