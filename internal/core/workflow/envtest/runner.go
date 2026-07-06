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
	// ReportDir is reserved for stage-2 failure-artifact reports; always
	// empty in stage 1.
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
// used for the on-disk log file. The returned io.Writer is where the
// `dwe validate` / `dwe deploy run` subprocess output (and the in-process
// steps pipeline's LogWriter) is written — a single run log for the whole
// scenario. cleanup releases every resource the factory opened (including
// the reporter itself) and must be called exactly once.
//
// The CLI (stage-1b Task 8) injects a silent variant in JSON output mode
// (screen writer = io.Discard) so live pipeline output never leaks into
// JSON stdout, while the file log keeps recording. A nil RunRequest.ReporterFactory
// uses defaultReporterFactory.
type ReporterFactory func(workDir, name string) (rep pipeline.Reporter, logWriter io.Writer, cleanup func(), err error)

// defaultReporterFactory is the production ReporterFactory: the same
// OpenPipelineLog + NewPlainReporter pairing deploy/reset use, always with
// logging enabled (a scenario run always gets a run log under the copy's
// own .dwe/logs/).
func defaultReporterFactory(workDir, name string) (pipeline.Reporter, io.Writer, func(), error) {
	screen, logFile, termOut, _, cleanup, err := pipeline.OpenPipelineLog(workDir, name, true)
	if err != nil {
		return nil, nil, nil, err
	}
	rep := pipeline.NewPlainReporter(screen, logFile, termOut)
	return rep, logFile, func() {
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
	clock           func() time.Time
}

// NewRunner builds a Runner wired to the real implementations.
func NewRunner() *Runner {
	return &Runner{
		execDwe:         execDweProcess,
		allocatePorts:   AllocatePorts,
		newTeardownDeps: NewTeardownDeps,
		clock:           time.Now,
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

	autoPaths := autoPortVarPaths(scn)
	ports := make(map[string]int, len(autoPaths))
	if len(autoPaths) > 0 {
		allocated, err := r.allocatePorts(len(autoPaths))
		if err != nil {
			return prepFail("allocating ports", err)
		}
		for i, path := range autoPaths {
			ports[path] = allocated[i]
		}
	}

	overlay, err := BuildLocalOverlay(seedLocal, scn, composeProject, ports, warn)
	if err != nil {
		return prepFail("building local.yml overlay", err)
	}
	if err := WriteGeneratedLocalYAML(copyRoot, overlay); err != nil {
		return prepFail("writing generated local.yml", err)
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
		teardown()
		result.Duration = r.clock().Sub(start)
		return result, nil
	}

	reporterFactory := req.ReporterFactory
	if reporterFactory == nil {
		reporterFactory = defaultReporterFactory
	}
	rep, lw, cleanup, err := reporterFactory(copyRoot, "test")
	if err != nil {
		warn(fmt.Sprintf("opening pipeline log: %v", err))
		return finish(StatusError, "")
	}
	logWriter = lw
	defer cleanup()

	extraEnv := []string{"DWE_NONINTERACTIVE=1"}

	if err := r.execDwe(scenarioCtx, copyRoot, extraEnv, logWriter, logWriter, "validate"); err != nil {
		warn(fmt.Sprintf("dwe validate failed: %v", err))
		return finish(StatusError, "")
	}

	deployErr := r.execDwe(scenarioCtx, copyRoot, extraEnv, logWriter, logWriter, "deploy", "run", "--silent")
	if deployErr != nil && len(autoPaths) > 0 {
		warn(fmt.Sprintf("deploy failed, retrying once with freshly allocated ports: %v", deployErr))
		deployErr = r.retryDeployWithFreshPorts(scenarioCtx, copyRoot, extraEnv, logWriter, seedLocal, scn, composeProject, autoPaths, ports, warn)
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

// retryDeployWithFreshPorts re-allocates every `auto` port, rewrites the
// copy's local.yml, and retries `dwe deploy run --silent` exactly once. Any
// failure while preparing the retry (port allocation, overlay rebuild,
// write) is reported via warn and counts as the one permitted retry being
// exhausted — the original deployErr stands and no second attempt is made.
func (r *Runner) retryDeployWithFreshPorts(
	ctx context.Context, copyRoot string, extraEnv []string, logWriter io.Writer,
	seedLocal map[string]any, scn *Scenario, composeProject string, autoPaths []string,
	ports map[string]int, warn func(string),
) error {
	reallocated, err := r.allocatePorts(len(autoPaths))
	if err != nil {
		return fmt.Errorf("retry: reallocating ports: %w", err)
	}
	for i, path := range autoPaths {
		ports[path] = reallocated[i]
	}
	overlay, err := BuildLocalOverlay(seedLocal, scn, composeProject, ports, warn)
	if err != nil {
		return fmt.Errorf("retry: rebuilding local.yml overlay: %w", err)
	}
	if err := WriteGeneratedLocalYAML(copyRoot, overlay); err != nil {
		return fmt.Errorf("retry: rewriting local.yml: %w", err)
	}
	return r.execDwe(ctx, copyRoot, extraEnv, logWriter, logWriter, "deploy", "run", "--silent")
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

	copyCfg, err := config.LoadConfig(copyWorkspacePath)
	if err != nil {
		return "", fmt.Errorf("loading copy config: %w", err)
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
