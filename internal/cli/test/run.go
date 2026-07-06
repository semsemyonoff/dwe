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

	cmd := &cobra.Command{
		Use:   "run [scenario...]",
		Short: "Run one or more integration test scenarios",
		Long: `Run declarative integration-test scenarios against a disposable, isolated copy
of the project.

With no arguments, every scenario under workspace/tests/*.yml runs, in sorted
name order. Passing scenario names runs exactly those (an unknown name fails
before anything runs). Ctrl+C cancels the scenario currently running, tears it
down, and skips the rest.

Exit codes: 0 = every scenario passed, 1 = at least one scenario failed,
2 = a scenario (or the run itself) could not be prepared.`,
		Example: `  dwe test run
  dwe test run redis-off db-migration
  dwe test run --keep smoke
  dwe test run --timeout 15m`,
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTestRun(cmd, flags, args, keep, timeout)
		},
	}

	cmd.Flags().BoolVar(&keep, "keep", false,
		"skip teardown; leave the copy, manifest, and Docker/bridge state running for debugging")
	cmd.Flags().DurationVar(&timeout, "timeout", 0,
		"override every scenario's own timeout (e.g. 15m); 0 = use the scenario's timeout: field or the 30m default")
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
	Name       string `json:"name"`
	Status     string `json:"status"`
	FailedStep string `json:"failed_step,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	ReportDir  string `json:"report_dir,omitempty"`
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

func runTestRun(cmd *cobra.Command, flags *cmdctx.RootFlags, args []string, keep bool, timeout time.Duration) error {
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
			BaseDir:         baseDir,
			Scenario:        name,
			Keep:            keep,
			Timeout:         timeout,
			Translator:      flags.I18n,
			Locale:          flags.Locale,
			ReporterFactory: reporterFactory,
			Warn:            warn,
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
			Name:       o.Name,
			Status:     string(o.Status),
			FailedStep: o.FailedStep,
			DurationMs: o.Duration.Milliseconds(),
			ReportDir:  o.ReportDir,
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
// scenario runs (a prep error, exit 2).
func resolveScenarioNames(baseDir string, args []string) ([]string, error) {
	all, err := envtest.ListScenarios(baseDir)
	if err != nil {
		return nil, fmt.Errorf("listing scenarios: %w", err)
	}
	if len(args) == 0 {
		return all, nil
	}
	known := make(map[string]bool, len(all))
	for _, n := range all {
		known[n] = true
	}
	for _, n := range args {
		if !known[n] {
			return nil, cmdctx.Err("unknown_scenario", fmt.Sprintf("unknown scenario %q", n)).
				WithHint("run `dwe test list` to see available scenarios").
				WithDetail("scenario", n)
		}
	}
	return args, nil
}

// jsonReporterFactory builds the steps-pipeline reporter with a silenced
// screen (io.Discard) so live output never reaches JSON stdout, while still
// writing the scenario's run log to disk.
func jsonReporterFactory(workDir, name string) (pipeline.Reporter, io.Writer, func(), error) {
	_, logFile, _, _, cleanup, err := pipeline.OpenPipelineLog(workDir, name, true)
	if err != nil {
		return nil, nil, nil, err
	}
	rep := pipeline.NewPlainReporter(sharedrender.NewWriter(io.Discard), logFile, io.Discard)
	return rep, logFile, func() {
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
		fmt.Fprintf(&b, "\n  kept: compose project %s, copy at %s — clean up manually when done", o.ComposeProject, o.CopyPath)
	}
	return b.String()
}
