package envtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
)

// defaultScenarioTimeout is used when neither --timeout nor the scenario's
// own timeout: field is set (spec §3/§6).
const defaultScenarioTimeout = 30 * time.Minute

// teardownTimeout bounds teardown's own context, which is always fresh
// (never the scenario's expired deadline) — a hung `compose down` or docker
// probe during teardown must still give up eventually.
const teardownTimeout = 5 * time.Minute

// reportTimeout bounds report collection's own context, which is always
// fresh (never the scenario's expired deadline — a timeout failure has a
// cancelled scenarioCtx) — a hung compose/docker capture must still give up
// eventually, and collection always runs before teardown so it must not
// starve it.
const reportTimeout = 2 * time.Minute

// manifestRunIDSuffix matches "<6-hex-char-run-id>.yml" — the exact suffix a
// manifest filename has after its scenario-name prefix is stripped. Used by
// the kept-run guard to find existing manifests for a scenario without
// mismatching on a scenario name that is itself a prefix of another (e.g.
// "foo" must not match "foo-bar-<runid>.yml").
var manifestRunIDSuffix = regexp.MustCompile(`^[0-9a-f]{6}\.yml$`)

// ScenarioStatus is the outcome of a single scenario run.
type ScenarioStatus string

const (
	// StatusPassed means the copy deployed and every step succeeded.
	StatusPassed ScenarioStatus = "passed"
	// StatusFailed means the copy deployed but a step (or the deploy
	// subprocess itself, or a scenario timeout) failed.
	StatusFailed ScenarioStatus = "failed"
	// StatusError means the scenario could not even be prepared: copy,
	// config/manifest generation, or the `dwe validate` subprocess failed.
	StatusError ScenarioStatus = "error"
)

// ScenarioResult is the outcome of a single RunScenario call.
type ScenarioResult struct {
	// Name is the scenario name.
	Name string
	// Status is the run outcome.
	Status ScenarioStatus
	// FailedStep is the resolved step address of the first failing step
	// (only set when a step failure caused Status == StatusFailed).
	FailedStep string
	// Duration is the wall-clock time RunScenario spent on this scenario.
	Duration time.Duration
	// ReportDir is the failure-artifact report directory
	// (.dwe/tests/reports/<scenario>/), set when the scenario did not pass
	// and --keep was not requested; empty otherwise (including a passed
	// scenario, a --keep run, or a report-collection failure).
	ReportDir string
	// ComposeProject is the exact compose project name used for this run —
	// meaningful chiefly with Keep, so the caller can report it.
	ComposeProject string
	// CopyPath is the absolute path to the disposable project copy —
	// meaningful chiefly with Keep, so the caller can report it.
	CopyPath string
}

// ReporterFactory builds the pipeline reporter and its writers for a
// scenario run. workDir is the disposable copy root; name is a short label
// used for the on-disk log file.
//
//   - logWriter is the run log: the in-process steps pipeline's LogWriter and
//     the teardown log — a single file for the whole scenario.
//   - subprocOut is where the `dwe validate` / `dwe deploy run` subprocess
//     output goes. In interactive mode it tees the subprocess to the terminal
//     (live progress) AND the run log; in JSON mode it is the log only, so the
//     deploy's chatter never leaks into JSON stdout.
//
// cleanup releases every resource the factory opened (including the reporter
// itself) and must be called exactly once.
//
// The CLI (stage-1b Task 8) injects a silent variant in JSON output mode
// (screen writer = io.Discard, subprocOut = log only) so live pipeline output
// never leaks into JSON stdout, while the file log keeps recording. A nil
// RunRequest.ReporterFactory uses defaultReporterFactory.
type ReporterFactory func(workDir, name string) (rep pipeline.Reporter, logWriter, subprocOut io.Writer, cleanup func(), err error)

// defaultReporterFactory is the production ReporterFactory: the same
// OpenPipelineLog + NewPlainReporter pairing deploy/reset use, always with
// logging enabled (a scenario run always gets a run log under the copy's
// own .dwe/logs/).
func defaultReporterFactory(workDir, name string) (pipeline.Reporter, io.Writer, io.Writer, func(), error) {
	screen, logFile, termOut, _, cleanup, err := pipeline.OpenPipelineLog(workDir, name, true)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	rep := pipeline.NewPlainReporter(screen, logFile, termOut)
	// subprocOut streams the validate/deploy subprocess output live to the
	// terminal (termOut = os.Stdout on a TTY, io.Discard when piped) while
	// mirroring it into the run log — so a long deploy shows progress instead of
	// a silent console until it finishes. termOut is idle during validate/deploy
	// (the reporter's own live frame only starts with the in-process steps
	// phase, after the deploy subprocess has returned), so nothing interleaves.
	subprocOut := io.MultiWriter(termOut, logFile)
	return rep, logFile, subprocOut, func() {
		rep.Close()
		cleanup()
	}, nil
}

// RunRequest parametrizes a single RunScenario call.
type RunRequest struct {
	// BaseDir is the absolute root of the ORIGINAL project (never mutated).
	BaseDir string
	// Scenario is the scenario name (workspace/tests/<Scenario>.yml).
	Scenario string
	// Keep, when true, skips teardown entirely: the manifest, copy, and any
	// Docker/bridge state are left running for debugging.
	Keep bool
	// Timeout overrides the scenario's own timeout: field when non-zero
	// (flag > scenario > defaultScenarioTimeout, spec §3).
	Timeout time.Duration
	// Translator and Locale thread i18n through to `type: command` steps
	// invoked by the scenario's pipeline (display-string contract).
	Translator i18n.Translator
	Locale     string
	// ReporterFactory builds the steps pipeline's reporter/log; nil uses
	// defaultReporterFactory.
	ReporterFactory ReporterFactory
	// Warn receives non-fatal diagnostics (compose.extra strips, non-git
	// copy fallback, teardown step failures, retry attempts); nil discards
	// them.
	Warn func(string)
}

// execDweFunc is the injectable subprocess-spawn seam for `dwe validate` /
// `dwe deploy run`. Production resolves the running executable (the same
// os.Executable() pattern used by the bridge daemon and workflow runners);
// tests MUST stub this — spawning the real binary from a test would recurse
// into the test binary itself (the documented test-recursion hazard).
type execDweFunc func(ctx context.Context, dir string, extraEnv []string, stdout, stderr io.Writer, args ...string) error

// execDweProcess is the real execDweFunc implementation.
func execDweProcess(ctx context.Context, dir string, extraEnv []string, stdout, stderr io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, resolveTestDweBin(), args...) //nolint:gosec
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// resolveTestDweBin resolves the dwe binary to spawn for validate/deploy
// subprocesses: the running executable when available, else the bare name
// (relies on PATH — matches the fallback used elsewhere in this repo).
func resolveTestDweBin() string {
	if bin, err := os.Executable(); err == nil {
		return bin
	}
	return "dwe"
}

// Runner executes scenarios. Every external side effect (subprocess spawn,
// port allocation, teardown) is a seam so tests never touch a real Docker
// daemon or spawn a real dwe subprocess.
type Runner struct {
	execDwe         execDweFunc
	allocatePorts   func(n int) ([]int, error)
	newTeardownDeps func(manifestPath string, log io.Writer) TeardownDeps
	// collectReport is the stage-2 failure-report seam. It is nil-checked at
	// the call site — runner_test.go has no shared constructor and builds
	// &Runner{...} literals directly, several of which reach finish() on a
	// non-passed status; production NewRunner always sets it, so a nil seam
	// only occurs in tests that don't care about report collection.
	collectReport func(ctx context.Context, m *Manifest, warn func(string)) (string, error)
	clock         func() time.Time
}

// NewRunner builds a Runner wired to the real implementations.
func NewRunner() *Runner {
	return &Runner{
		execDwe:         execDweProcess,
		allocatePorts:   AllocatePorts,
		newTeardownDeps: NewTeardownDeps,
		collectReport: func(ctx context.Context, m *Manifest, warn func(string)) (string, error) {
			return CollectReport(ctx, m, NewReportDeps(), warn)
		},
		clock: time.Now,
	}
}

// KeptRunError is returned when a scenario has an existing run manifest —
// left behind by a --keep run or a half-dead prior run — that must be
// cleaned up (or waited out) before running the scenario again. The copy
// itself is never touched when this error is returned.
type KeptRunError struct {
	Scenario      string
	ManifestPaths []string
}

func (e *KeptRunError) Error() string {
	return fmt.Sprintf(
		"scenario %q already has a run manifest (%s) — a --keep environment or an unfinished run owns its copy; clean it up (or wait for it to finish) before running again",
		e.Scenario, strings.Join(e.ManifestPaths, ", "))
}

// RunScenario executes a single scenario end to end (spec §6): acquire the
// per-scenario flock, load the scenario, resolve the timeout, guard against
// a kept/half-dead prior run, copy the project tree, generate the copy's
// local.yml + docker identity, write the manifest (before any Docker
// interaction), run `dwe validate` then `dwe deploy run --silent` as
// subprocesses (one retry on deploy failure when the scenario has `auto`
// ports), run the scenario's steps in-process, then tear down (unless Keep).
//
// A non-nil error means the scenario could not be attempted at all (flock
// held, a kept run already owns the copy, the scenario failed to load, or
// the timeout failed to parse) — nothing was created, so there is nothing to
// tear down. Every failure from CopyTree onward is instead reported via the
// returned ScenarioResult (Status == StatusError for a prep/validate
// failure, StatusFailed for a deploy/step/timeout failure) with a nil error,
// since a copy (and, from WriteManifest onward, a manifest) exists and the
// caller needs the result to report per-scenario outcomes across a run of
// several scenarios.
func (r *Runner) RunScenario(ctx context.Context, req RunRequest) (*ScenarioResult, error) {
	if req.BaseDir == "" {
		return nil, fmt.Errorf("envtest: RunRequest.BaseDir is required")
	}
	if req.Scenario == "" {
		return nil, fmt.Errorf("envtest: RunRequest.Scenario is required")
	}
	warn := req.Warn
	if warn == nil {
		warn = func(string) {}
	}

	start := r.clock()
	fail := func(stage string, err error) (*ScenarioResult, error) {
		return nil, fmt.Errorf("envtest: scenario %q: %s: %w", req.Scenario, stage, err)
	}

	lk, err := lock.Acquire(LockPath(req.BaseDir, req.Scenario))
	if err != nil {
		return fail("acquiring lock", err)
	}
	defer func() { _ = lk.Release() }()

	scenarioPath := filepath.Join(TestsDir(req.BaseDir), req.Scenario+".yml")
	scn, err := LoadScenario(scenarioPath)
	if err != nil {
		return fail("loading scenario", err)
	}

	timeout, err := resolveScenarioTimeout(req.Timeout, scn.Timeout)
	if err != nil {
		return fail("resolving timeout", err)
	}

	keptPaths, err := existingManifestPaths(req.BaseDir, req.Scenario)
	if err != nil {
		return fail("checking for a kept run", err)
	}
	if len(keptPaths) > 0 {
		return nil, &KeptRunError{Scenario: req.Scenario, ManifestPaths: keptPaths}
	}

	origCfg, err := config.LoadConfigOrWrap(filepath.Join(req.BaseDir, "workspace.yml"))
	if err != nil {
		return fail("loading project config", err)
	}

	runID, err := NewRunID()
	if err != nil {
		return fail("generating run ID", err)
	}
	composeProject := ComposeProjectName(origCfg, req.Scenario, runID)
	copyRoot := RunDir(req.BaseDir, req.Scenario)
	manifestPath := ManifestPath(req.BaseDir, req.Scenario, runID)

	if err := CopyTree(req.BaseDir, copyRoot, config.GitBin(origCfg), warn); err != nil {
		return fail("copying project tree", err)
	}

	// From here on, a prep failure must best-effort remove the copy itself:
	// no manifest exists yet, so the manifest-driven Teardown cannot find it
	// (and stage-2 `dwe test clean` would otherwise never see the leftover).
	prepFail := func(stage string, err error) (*ScenarioResult, error) {
		if rmErr := os.RemoveAll(copyRoot); rmErr != nil {
			warn(fmt.Sprintf("removing copy after %s failure: %v", stage, rmErr))
		}
		return fail(stage, err)
	}

	seedLocal, err := LoadSeedLocalYAML(filepath.Join(req.BaseDir, "workspace.yml"))
	if err != nil {
		return prepFail("loading seed local.yml", err)
	}

	if err := r.writeCopyLocalYAML(origCfg, seedLocal, scn, composeProject, copyRoot, warn); err != nil {
		return prepFail("generating local.yml", err)
	}
	if err := WriteDockerIdentity(copyRoot, composeProject); err != nil {
		return prepFail("writing docker identity", err)
	}

	manifest := &Manifest{
		Scenario:       req.Scenario,
		RunID:          runID,
		ComposeProject: composeProject,
		CopyPath:       copyRoot,
		BridgeDir:      bridge.DefaultBridgeDir(copyRoot),
		ReportDir:      ReportsDir(req.BaseDir, req.Scenario),
		CreatedAt:      r.clock().UTC(),
	}
	if err := WriteManifest(manifestPath, manifest); err != nil {
		return prepFail("writing manifest", err)
	}

	// From here on, the manifest exists: every failure is reported through
	// ScenarioResult (never a bare error) and cleaned up via the
	// manifest-driven Teardown, never a raw os.RemoveAll.
	result := &ScenarioResult{
		Name:           req.Scenario,
		ComposeProject: composeProject,
		CopyPath:       copyRoot,
	}

	scenarioCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var logWriter io.Writer
	teardown := func() {
		if req.Keep {
			return
		}
		tctx, tcancel := context.WithTimeout(context.Background(), teardownTimeout)
		defer tcancel()
		deps := r.newTeardownDeps(manifestPath, logWriter)
		if err := Teardown(tctx, manifest, deps, warn); err != nil {
			warn(fmt.Sprintf("teardown reported errors: %v", err))
		}
	}

	finish := func(status ScenarioStatus, failedStep string) (*ScenarioResult, error) {
		if errors.Is(scenarioCtx.Err(), context.DeadlineExceeded) {
			status = StatusFailed
		}
		result.Status = status
		result.FailedStep = failedStep
		// Collect the failure report BEFORE teardown: containers must still be
		// alive for compose ps/logs, and the pipeline log must still be present
		// (RemoveCopy hasn't run yet). Skipped for a passed scenario and under
		// --keep (the live environment is its own "report"). Always a fresh
		// context — scenarioCtx may already be cancelled (timeout).
		if r.collectReport != nil && !req.Keep && status != StatusPassed {
			rctx, rcancel := context.WithTimeout(context.Background(), reportTimeout)
			dir, err := r.collectReport(rctx, manifest, warn)
			rcancel()
			if err != nil {
				warn(fmt.Sprintf("collecting failure report: %v", err))
			} else {
				result.ReportDir = dir
			}
		}
		teardown()
		result.Duration = r.clock().Sub(start)
		return result, nil
	}

	reporterFactory := req.ReporterFactory
	if reporterFactory == nil {
		reporterFactory = defaultReporterFactory
	}
	rep, lw, subprocOut, cleanup, err := reporterFactory(copyRoot, "test")
	if err != nil {
		warn(fmt.Sprintf("opening pipeline log: %v", err))
		return finish(StatusError, "")
	}
	logWriter = lw
	defer cleanup()

	extraEnv := []string{"DWE_NONINTERACTIVE=1"}

	// validate + deploy run as subprocesses; their output goes to subprocOut
	// (terminal + run log interactively, run log only in JSON mode) so a long
	// deploy streams progress live instead of a silent console until it ends.
	if err := r.execDwe(scenarioCtx, copyRoot, extraEnv, subprocOut, subprocOut, "validate"); err != nil {
		warn(fmt.Sprintf("dwe validate failed: %v", err))
		return finish(StatusError, "")
	}

	// Capture the deploy output tail so the one retry fires ONLY on a real
	// port-bind conflict — a TOCTOU loss on a port we allocated. A blind retry
	// on ANY deploy failure (an app crash, a bad env var, an unrelated compose
	// error) would just double the wall-clock cost of every genuine failure
	// before reporting it anyway; hasAllocatedPorts is true for nearly every
	// project, so it cannot be the sole gate.
	deployTail := &tailRecorder{limit: deployTailLimit}
	deployOut := io.MultiWriter(subprocOut, deployTail)
	deployErr := r.execDwe(scenarioCtx, copyRoot, extraEnv, deployOut, deployOut, "deploy", "run", "--silent")
	// The conflict signal usually surfaces in the streamed deploy output (the
	// subprocess exit error itself is just "exit status N"); scan both so the
	// gate holds whether the message lands in the output or the returned error.
	if deployErr != nil && hasAllocatedPorts(origCfg, scn) &&
		isPortBindConflict(deployTail.String()+"\n"+deployErr.Error()) {
		warn(fmt.Sprintf("deploy failed on a port conflict, retrying once with freshly allocated ports: %v", deployErr))
		deployErr = r.retryDeployWithFreshPorts(scenarioCtx, copyRoot, extraEnv, subprocOut, origCfg, seedLocal, scn, composeProject, warn)
	}
	if deployErr != nil {
		warn(fmt.Sprintf("dwe deploy run failed: %v", deployErr))
		return finish(StatusFailed, "")
	}

	failedStep, err := r.runSteps(scenarioCtx, copyRoot, scn, req, rep, logWriter)
	if err != nil {
		warn(fmt.Sprintf("scenario steps failed: %v", err))
		return finish(StatusFailed, failedStep)
	}

	return finish(StatusPassed, "")
}

// hasAllocatedPorts reports whether the copy's local.yml carried any
// runner-allocated port — a remapped host port for an enabled service, or an
// env.vars: auto port. It is the FIRST of two gates on the one deploy retry
// (the second being isPortBindConflict): only a run that allocated ports can
// lose a TOCTOU race worth re-allocating for, but on its own it is true for
// nearly every project and so must never gate the retry alone.
func hasAllocatedPorts(cfg *config.DweConfig, scn *Scenario) bool {
	return len(enabledHostPortKeys(cfg, scn)) > 0 || len(autoPortVarPaths(scn)) > 0
}

// deployTailLimit bounds how much of the deploy subprocess output the retry
// gate retains. A port-bind conflict surfaces at bind time (after pull/build),
// i.e. near the end, so a tail is enough — and it caps memory for a long deploy.
const deployTailLimit = 64 << 10 // 64 KiB

// tailRecorder is an io.Writer that retains only the last `limit` bytes written
// to it. It never errors (an observation sink must not break the deploy stream).
type tailRecorder struct {
	limit int
	buf   []byte
}

func (t *tailRecorder) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
	}
	return len(p), nil
}

func (t *tailRecorder) String() string { return string(t.buf) }

// portBindConflictSignals are substrings (matched case-insensitively) that mark
// a deploy failure as an actual host-port conflict: dwe's own ports_free
// preflight ("port N (svc.port) is in use"), a raw bind failure, the Docker
// daemon's "port is already allocated", and Docker Desktop's "ports are not
// available".
var portBindConflictSignals = []string{
	"is in use",
	"address already in use",
	"port is already allocated",
	"ports are not available",
}

// isPortBindConflict reports whether the deploy output looks like a host-port
// bind conflict — the only failure the one retry (with freshly allocated ports)
// can plausibly clear.
func isPortBindConflict(output string) bool {
	lower := strings.ToLower(output)
	for _, sig := range portBindConflictSignals {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

// writeCopyLocalYAML allocates a fresh free host port for every enabled
// service's declared host port (spec §5 isolation — a test copy never needs the
// original host ports) plus any env.vars: auto port, then builds and writes the
// copy's generated local.yml. All ports come from a SINGLE AllocatePorts batch
// so a host port and a var port can never collide with each other. Re-invoked
// by the one deploy retry so a TOCTOU loss on any allocated port gets an
// entirely fresh set (spec §9).
func (r *Runner) writeCopyLocalYAML(
	origCfg *config.DweConfig, seedLocal map[string]any, scn *Scenario,
	composeProject, copyRoot string, warn func(string),
) error {
	keys := enabledHostPortKeys(origCfg, scn)
	autoPaths := autoPortVarPaths(scn)

	var hostPorts []HostPortOverride
	varPorts := make(map[string]int, len(autoPaths))
	if total := len(keys) + len(autoPaths); total > 0 {
		allocated, err := r.allocatePorts(total)
		if err != nil {
			return fmt.Errorf("allocating ports: %w", err)
		}
		hostPorts = buildHostPortOverrides(origCfg, keys, allocated[:len(keys)])
		for i, path := range autoPaths {
			varPorts[path] = allocated[len(keys)+i]
		}
	}

	overlay, err := BuildLocalOverlay(seedLocal, scn, composeProject, varPorts, warn)
	if err != nil {
		return err
	}
	ApplyHostPortOverrides(overlay, hostPorts)
	return WriteGeneratedLocalYAML(copyRoot, overlay)
}

// retryDeployWithFreshPorts re-generates the copy's local.yml with a fresh set
// of allocated ports (host + vars) and retries `dwe deploy run --silent`
// exactly once. Any failure while preparing the retry is reported to the caller
// and counts as the one permitted retry being exhausted — the original
// deployErr stands and no second attempt is made.
func (r *Runner) retryDeployWithFreshPorts(
	ctx context.Context, copyRoot string, extraEnv []string, subprocOut io.Writer,
	origCfg *config.DweConfig, seedLocal map[string]any, scn *Scenario, composeProject string,
	warn func(string),
) error {
	if err := r.writeCopyLocalYAML(origCfg, seedLocal, scn, composeProject, copyRoot, warn); err != nil {
		return fmt.Errorf("retry: %w", err)
	}
	return r.execDwe(ctx, copyRoot, extraEnv, subprocOut, subprocOut, "deploy", "run", "--silent")
}

// runSteps loads the copy's own (post-deploy) config and command registry,
// renders and resolves the scenario's steps, and runs them in-process via
// pipeline.RunWithOptions. Returns the failed step's resolved address (if
// any) alongside the error.
func (r *Runner) runSteps(
	ctx context.Context, copyRoot string, scn *Scenario, req RunRequest,
	rep pipeline.Reporter, logWriter io.Writer,
) (failedStep string, err error) {
	copyWorkspacePath := filepath.Join(copyRoot, "workspace.yml")

	copyCfg, err := config.LoadConfigOrWrap(copyWorkspacePath)
	if err != nil {
		return "", err
	}
	copyDockerCfg, err := config.LoadDockerConfigOrEmpty(copyRoot, copyCfg)
	if err != nil {
		return "", fmt.Errorf("loading copy docker config: %w", err)
	}
	reg, err := usercommands.LoadRegistryFromConfigPath(copyWorkspacePath)
	if err != nil {
		return "", fmt.Errorf("loading command registry: %w", err)
	}
	// Mirrors deploy's own resolution: a `type: command` step targeting a
	// command hidden via `hide:` must be skipped the same way here as in a
	// real deploy (executor.go's Hidden-target skip), which requires Hidden
	// to have been resolved first.
	_ = reg.ApplyVisibility(copyCfg, copyRoot)

	if err := RenderSteps(scn.Steps, copyCfg); err != nil {
		return "", fmt.Errorf("rendering scenario steps: %w", err)
	}

	phase := config.DeployPhase{Name: "tests", Steps: scn.Steps}
	resolved, err := pipeline.ResolvePhaseSteps(copyCfg, reg, phase, "")
	if err != nil {
		return "", fmt.Errorf("resolving scenario steps: %w", err)
	}

	capture := &failStepCapture{Reporter: rep}
	runErr := pipeline.RunWithOptions(pipeline.RunOptions{
		Steps:        resolved,
		Reporter:     capture,
		Name:         "test",
		Config:       copyCfg,
		DockerConfig: copyDockerCfg,
		Registry:     reg,
		WorkDir:      copyRoot,
		LogWriter:    logWriter,
		SkipConfirm:  true,
		Translator:   req.Translator,
		Locale:       req.Locale,
		Context:      ctx,
	})
	if runErr != nil {
		return capture.failedStep, runErr
	}
	return "", nil
}

// failStepCapture wraps a Reporter to record the resolved address of the
// first step to fail, so RunScenario can surface it as ScenarioResult.FailedStep.
type failStepCapture struct {
	pipeline.Reporter
	failedStep string
}

func (c *failStepCapture) FailStep(stepAddr string, step config.DeployStep, index, total int, err error) {
	if c.failedStep == "" {
		c.failedStep = stepAddr
	}
	c.Reporter.FailStep(stepAddr, step, index, total, err)
}

// resolveScenarioTimeout applies the flag > scenario > default precedence
// (spec §3).
func resolveScenarioTimeout(flagTimeout time.Duration, scenarioTimeout string) (time.Duration, error) {
	if flagTimeout > 0 {
		return flagTimeout, nil
	}
	if scenarioTimeout != "" {
		d, err := time.ParseDuration(scenarioTimeout)
		if err != nil {
			return 0, fmt.Errorf("parsing scenario timeout %q: %w", scenarioTimeout, err)
		}
		if d <= 0 {
			return 0, fmt.Errorf("scenario timeout %q must be positive", scenarioTimeout)
		}
		return d, nil
	}
	return defaultScenarioTimeout, nil
}

// autoPortVarPaths returns the sorted dot-paths (relative to vars:) of every
// scenario env.vars entry whose value is AutoPortSentinel.
func autoPortVarPaths(scn *Scenario) []string {
	var paths []string
	for path, v := range scn.Env.Vars {
		if s, ok := v.(string); ok && s == AutoPortSentinel {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

// existingManifestPaths finds every manifest belonging to scenario under
// baseDir's manifests directory (any run ID), sorted. An absent manifests
// directory yields (nil, nil) — no kept run.
func existingManifestPaths(baseDir, scenario string) ([]string, error) {
	dir := ManifestsDir(baseDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading manifests directory: %w", err)
	}
	prefix := scenario + "-"
	var matches []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if !manifestRunIDSuffix.MatchString(strings.TrimPrefix(name, prefix)) {
			continue
		}
		matches = append(matches, filepath.Join(dir, name))
	}
	sort.Strings(matches)
	return matches, nil
}
