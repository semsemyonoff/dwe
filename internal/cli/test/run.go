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

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
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
	var parallel int

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
			return runTest(cmd, flags, args, keep, timeout, skipIsolationCheck, parallel)
		},
	}

	cmd.Flags().BoolVar(&keep, "keep", false,
		"skip teardown; leave the copy, manifest, and Docker/bridge state running for debugging")
	cmd.Flags().DurationVar(&timeout, "timeout", 0,
		"override every scenario's own timeout (e.g. 15m); 0 = use the scenario's timeout: field or the 30m default")
	cmd.Flags().BoolVar(&skipIsolationCheck, "skip-isolation-check", false,
		"downgrade compose isolation findings (container_name:, literal host ports, …) to warnings instead of blocking the scenario")
	cmd.Flags().IntVar(&parallel, "parallel", 1,
		"run up to N scenarios concurrently (default 1); effective parallelism is min(N, scenario count). At >1 a compact aggregated status view replaces the per-scenario streaming output")
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
		reporterFactory = silentReporterFactory
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

	return finishTestRun(cmd, flags, keep, outcomes)
}

// runTest is the `dwe test run` entry point (RunE target). It validates
// --parallel, then dispatches: effective parallelism min(N, scenario count) of
// 1 or less runs the existing sequential/streaming path (runTestRun,
// byte-identical); anything higher fans the scenarios out concurrently.
func runTest(cmd *cobra.Command, flags *cmdctx.RootFlags, args []string, keep bool, timeout time.Duration, skipIsolationCheck bool, parallel int) error {
	if parallel < 1 {
		return cmdctx.Err("invalid_parallel",
			fmt.Sprintf("--parallel must be at least 1, got %d", parallel)).
			WithHint("pass --parallel with a value of 1 or more").
			WithDetail("parallel", parallel)
	}

	// Must run before the flock, any goroutine, UI, or subprocess (spec §3).
	// runTestRun scrubs again on the sequential path; the unset is idempotent.
	envtest.ScrubComposeEnv()

	baseDir := flags.ProjectRoot()
	names, err := resolveScenarioNames(baseDir, args)
	if err != nil {
		return err
	}

	effective := min(parallel, len(names))
	if effective <= 1 {
		// Flag absent, --parallel 1, or fewer scenarios than requested workers:
		// the existing sequential/streaming path runs untouched.
		return runTestRun(cmd, flags, args, keep, timeout, skipIsolationCheck)
	}

	return runTestParallel(cmd, flags, baseDir, names, effective, keep, timeout, skipIsolationCheck)
}

// liveDisplayEnv resolves the TTY status + terminal size for the aggregated
// live display. A package var so tests inject fixed values (force-TTY frames,
// deterministic height clamp) without a real terminal.
var liveDisplayEnv = func() (isTTY bool, termSize func() (int, int)) {
	isTTY = term.IsTerminal(os.Stdout.Fd())
	termSize = func() (int, int) {
		w, h, err := term.GetSize(os.Stdout.Fd())
		if err != nil || w <= 0 || h <= 0 {
			return liveLineDefaultWidth, liveLineDefaultHeight
		}
		return w, h
	}
	return isTTY, termSize
}

// liveLineDefaultWidth / liveLineDefaultHeight back the termSize fallback when
// the terminal cannot be queried (mirrors liveui's own 80-col default).
const (
	liveLineDefaultWidth  = 80
	liveLineDefaultHeight = 24
)

// runTestParallel fans the resolved scenarios out over an errgroup capped at
// `effective` workers. Each scenario writes its outcome into a fixed slot by
// original index, so text/JSON output is deterministic regardless of
// completion order. Goroutines ALWAYS return nil: a RunScenario error becomes a
// per-scenario StatusError outcome, never a group cancellation of siblings.
// Ctrl+C cancels in-flight scenarios via the shared ctx (their teardown still
// runs on a fresh context inside the runner); scenarios that never started
// leave a nil slot and are compacted out.
//
// deploy/pipeline output is silenced via silentReporterFactory. In text mode an
// aggregated live display (one block row per scenario, coarse phase + elapsed)
// replaces the streaming output; each scenario's Progress events drive its row
// and per-scenario warnings frame above the block. JSON mode installs no
// display and keeps warnings suppressed.
func runTestParallel(cmd *cobra.Command, flags *cmdctx.RootFlags, baseDir string, names []string, effective int, keep bool, timeout time.Duration, skipIsolationCheck bool) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner := newRunner()

	// Aggregated live display in text mode only; JSON stdout must stay clean.
	var display *runLiveStatus
	if flags.Output != "json" {
		isTTY, termSize := liveDisplayEnv()
		display = newRunLiveStatus(names, isTTY, termSize, cmd.OutOrStdout(), cmd.ErrOrStderr())
		display.start()
	}

	makeWarn := func(i int) func(string) {
		return func(msg string) {
			if display == nil {
				// JSON mode: warnings stay suppressed (stderr reserved for the
				// error envelope).
				return
			}
			display.Warn(i, msg)
		}
	}

	slots := make([]*scenarioOutcome, len(names))
	var g errgroup.Group
	g.SetLimit(effective)
	for i, name := range names {
		g.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}
			if display != nil {
				display.Started(i)
			}
			warn := makeWarn(i)
			req := envtest.RunRequest{
				BaseDir:            baseDir,
				Scenario:           name,
				Keep:               keep,
				Timeout:            timeout,
				Translator:         flags.I18n,
				Locale:             flags.Locale,
				ReporterFactory:    silentReporterFactory,
				Warn:               warn,
				SkipIsolationCheck: skipIsolationCheck,
			}
			if display != nil {
				req.Progress = func(p envtest.ProgressPhase) { display.Phase(i, p) }
			}
			res, err := runner.RunScenario(ctx, req)
			var o scenarioOutcome
			if err != nil {
				warn(fmt.Sprintf("scenario %q could not be prepared: %v", name, err))
				o = scenarioOutcome{Name: name, Status: envtest.StatusError, Message: err.Error()}
			} else {
				o = scenarioOutcomeFromResult(res)
			}
			slots[i] = &o
			if display != nil {
				display.Finished(i, o)
			}
			return nil
		})
	}
	_ = g.Wait()

	if display != nil {
		// A nil slot is a scenario that was queued but never admitted to a
		// worker before Ctrl+C (it returned early on ctx.Err() without ever
		// calling Started/Finished). Finalize its pre-seeded pending row as
		// skipped so cancellation never leaves a queued row looking pending.
		for i, s := range slots {
			if s == nil {
				display.FinalizeCancelled(i)
			}
		}
		display.Close()
	}

	outcomes := make([]scenarioOutcome, 0, len(slots))
	for _, s := range slots {
		if s != nil {
			outcomes = append(outcomes, *s)
		}
	}
	return finishTestRun(cmd, flags, keep, outcomes)
}

// finishTestRun renders the collected outcomes (text or JSON) and returns the
// exit-code-bearing error: exit 2 if any scenario errored (could not be
// prepared), exit 1 if any failed, else nil. Shared by the sequential and
// parallel paths.
func finishTestRun(cmd *cobra.Command, flags *cmdctx.RootFlags, keep bool, outcomes []scenarioOutcome) error {
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

// silentReporterFactory builds the steps-pipeline reporter with a silenced
// screen (io.Discard) so live output never reaches stdout, while still
// writing the scenario's run log to disk. subprocOut is the log file only (no
// terminal tee) so the validate/deploy subprocess output never leaks into the
// payload either. Used by BOTH JSON mode (where any stdout leak would corrupt
// the JSON payload) AND the parallel text path (where per-scenario streaming
// output would tangle with the aggregated status view).
func silentReporterFactory(workDir, name string) (pipeline.Reporter, io.Writer, io.Writer, func(), error) {
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
