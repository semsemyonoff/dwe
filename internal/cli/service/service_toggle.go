package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	cmdDeploy "github.com/semsemyonoff/dwe/internal/cli/deploy"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	localpkg "github.com/semsemyonoff/dwe/internal/core/project/local"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/core/workflow/lifecycle"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/envfile"
	"github.com/semsemyonoff/dwe/internal/shared/lock"

	"github.com/spf13/cobra"
)

// detectStackRunning reports whether the project currently has any compose
// container in a running state.
//
// Three-valued result:
//
//   - (true, nil)   — at least one project container is running
//   - (false, nil)  — probe succeeded, no containers running
//   - (false, err)  — probe could not determine state (docker missing, daemon
//     down, dockerCfg load error, etc.); err describes why
//
// Callers must NOT collapse the error case into "stopped": when state is
// unknown, the safer default is to behave as if the stack is running so the
// pending journal entry is still recorded and `--apply` is still honored.
//
// Seamed so tests can force a value without spawning docker.
var detectStackRunning = func(cfg *config.DweConfig, baseDir string) (bool, error) {
	dockerCfg, err := config.LoadDockerConfig(baseDir, cfg)
	if err != nil {
		return false, fmt.Errorf("loading docker config: %w", err)
	}
	ids, err := docker.NewCompose(cfg, dockerCfg, baseDir).ContainerIDs()
	if err != nil {
		return false, fmt.Errorf("probing stack containers: %w", err)
	}
	return len(ids) > 0, nil
}

// runMultiSelect is a package-level wrapper for widgets.RunMultiSelect.
// Tests in this package swap it to inject fake multi-select behaviour.
var runMultiSelect = widgets.RunMultiSelect

// confirmApplyPrompt is a package-level wrapper for widgets.RunConfirm used by the
// "Run them now?" confirmation in the toggle flow. Tests swap it to inject
// canned answers without driving a real huh form.
var confirmApplyPrompt = func() (bool, error) {
	return widgets.RunConfirm("Run them now?", "Yes", "No")
}

// singleToggleAddPendingOps is the seam for journal.AddPendingOps.
// Tests swap it to inject write failures for rollback coverage.
var singleToggleAddPendingOps = journal.AddPendingOps

// singleToggleRunDeploy and singleToggleRunRestart are seams for the apply
// phase so tests can stub the callees without a real Docker environment.
var singleToggleRunDeploy func(ctx context.Context, cmd *cobra.Command, flags *cmdctx.RootFlags, opts cmdDeploy.Opts) error

func init() {
	// Assigned in init because var init order is file-declaration order across
	// packages; using a package-init avoids a nil seam if this file initializes
	// before cmdDeploy.RunHelper is set up.
	singleToggleRunDeploy = cmdDeploy.RunHelper
	multiToggleRunDeploy = cmdDeploy.RunHelper
}

var singleToggleRunRestart = lifecycle.RunRestart
var singleToggleRunUserCmd = runtime.RunCommand

// multiToggleAddPendingOps is the seam for journal.AddPendingOps in the
// multi-select toggle flow, so tests can inject write failures independently.
var multiToggleAddPendingOps = journal.AddPendingOps

// multiToggleRunDeploy, multiToggleRunRestart, multiToggleRunUserCmd are seams
// for the multi-select toggle apply phase.
var multiToggleRunDeploy func(ctx context.Context, cmd *cobra.Command, flags *cmdctx.RootFlags, opts cmdDeploy.Opts) error
var multiToggleRunRestart = lifecycle.RunRestart
var multiToggleRunUserCmd = runtime.RunCommand

// singleToggleFlags holds the parsed flags for a single-service enable/disable command.
type singleToggleFlags struct {
	apply     bool
	printPlan bool
	skipHooks bool
}

// journalSnapshot captures the two journal-derived signals the toggle flow needs.
//
//   - deployed is the per-service "currently deployed" set used to resolve
//     RequiresDeployOrRestart contributors. Only StatusDeployed counts.
//   - everDeployed is the "has a deploy ever been attempted on this stack"
//     signal that gates the pending-write code path. Any prior deploy attempt
//     (including failed / in-progress / partial / project-only) counts, and a
//     journal load error is treated as "ever deployed" so we don't silently
//     skip pending on a corrupt file — AddPendingOps will then hard-fail on
//     the same corrupt file, surfacing the corruption rather than letting it
//     drop a toggle's pending intent.
type journalSnapshot struct {
	deployed     map[string]bool
	everDeployed bool
}

// loadJournalSnapshot loads the deploy journal and derives the two signals the
// toggle flow needs. A load error is surfaced to the caller (so it can warn)
// but everDeployed defaults to true on error — see journalSnapshot comment.
func loadJournalSnapshot(statePath string) (journalSnapshot, error) {
	state, err := journal.Load(statePath)
	if err != nil {
		// Corrupt journal: warn upstream, but assume the stack has been
		// deployed so pending writes still happen. Silently dropping pending
		// here would be worse than letting AddPendingOps fail later.
		return journalSnapshot{deployed: map[string]bool{}, everDeployed: true}, err
	}
	// journal.Load never returns (nil, nil): a missing file produces a
	// zero-value ProjectState with Project=&{}, Services=map{}.
	snap := journalSnapshot{deployed: make(map[string]bool, len(state.Services))}
	for name, st := range state.Services {
		if st == nil {
			continue
		}
		if st.Status == journal.StatusDeployed {
			snap.deployed[name] = true
		}
		// Any service in a non-empty, non-NotDeployed status means a deploy
		// has touched this service — failed / in_progress / partial / skipped
		// all count.
		if st.Status != "" && st.Status != journal.StatusNotDeployed {
			snap.everDeployed = true
		}
	}
	// Project-level signals: an explicit LastRun or a non-default Project.Status
	// means a deploy has been attempted (covers project-only deploys, where
	// state.Services may be empty but Project.Status is StatusDeployed).
	if state.Project != nil {
		if state.Project.LastRun != nil {
			snap.everDeployed = true
		}
		if state.Project.Status != "" && state.Project.Status != journal.StatusNotDeployed {
			snap.everDeployed = true
		}
	}
	return snap, nil
}

// probeStackOrWarn calls detectStackRunning and prints a warning to errOut if
// the probe failed. A probe failure is treated as "unknown" → return true so
// callers default to running-semantics (write pending, attempt apply).
func probeStackOrWarn(errOut io.Writer, cfg *config.DweConfig, baseDir string) bool {
	running, err := detectStackRunning(cfg, baseDir)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, styles.StyleWarning(fmt.Sprintf(
			"⚠ could not probe stack state (%v); proceeding as if stack is running", err)))
		return true
	}
	return running
}

// warnStackStopped prints the "stack not running, work skipped" banner when
// the toggle had any apply/hook work that would otherwise have run.
func warnStackStopped(out io.Writer, plan TogglePlan) {
	if len(plan.ApplySteps) == 0 && len(plan.BeforeSteps) == 0 && len(plan.AfterSteps) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, styles.StyleWarning(
		"⚠ stack is not running; hooks and pending state were skipped (local.yml updated)."))
}

// warnDeployedServicesLoad emits a warning to errOut when the journal could
// not be loaded cleanly. The caller proceeds with an empty per-service
// deployed set (so RequiresDeployOrRestart contributors fall back to the
// deploy variant) but everDeployed is still treated as true upstream — the
// pending write is attempted and either repairs the file or hard-fails.
func warnDeployedServicesLoad(errOut io.Writer, err error) {
	if err == nil {
		return
	}
	_, _ = fmt.Fprintln(errOut, styles.StyleWarning(fmt.Sprintf(
		"⚠ could not read deploy journal (%v); per-service deploy state unknown, pending will still be written", err)))
}

// captureFileState returns the current bytes of path, or nil if the file is absent.
func captureFileState(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// restoreFileState restores path to its captured state.
// If captured is nil (file was absent before), removes path (ignores not-exist).
// Otherwise, atomically restores the original bytes via write-to-temp + rename.
func restoreFileState(path string, captured []byte) error {
	if captured == nil {
		err := os.Remove(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir for restore: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".dwe-restore-*")
	if err != nil {
		return fmt.Errorf("create temp for restore: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(captured); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp for restore: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp for restore: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp for restore: %w", err)
	}
	return nil
}

// buildContributors derives the contributor list from toggle actions.
// Must only be called after buildTogglePlan succeeds (all validations passed).
//
// deployedServices is the journal-derived "currently deployed" set; passed
// here so the deploy-or-restart resolution (via ToggleRequires.Resolve) lands
// on the same value buildTogglePlan used to shape ApplySteps.
func buildContributors(cfg *config.DweConfig, toggles []ToggleAction, deployedServices map[string]bool) []Contributor {
	var contributors []Contributor
	for _, t := range toggles {
		svc, ok := cfg.Services[t.Service]
		if !ok {
			// Precondition: buildTogglePlan already validated all service names.
			continue
		}
		var hooks *config.ServiceToggleHooks
		if t.Direction == DirectionEnable {
			hooks = svc.OnEnable
		} else {
			hooks = svc.OnDisable
		}
		var rawRequires config.ToggleRequires
		if hooks != nil {
			rawRequires = hooks.Requires
		}
		requires := rawRequires.OrDefault().Resolve(deployedServices[t.Service])
		contributors = append(contributors, Contributor{Service: t.Service, Requires: requires})
	}
	return contributors
}

// buildPendingOpsFromContributors builds the PendingOp slice for journal.AddPendingOps.
// Deploy contributors collapse into one {PendingDeploy, sorted services} op.
// Restart contributors collapse into a single {PendingRestart} op (no service list).
// RequiresNone contributors produce no ops. An empty (non-nil) slice is returned
// when all contributors are RequiresNone — journal.AddPendingOps treats that as a no-op.
func buildPendingOpsFromContributors(contributors []Contributor) []journal.PendingOp {
	var deployServices []string
	hasRestart := false
	for _, c := range contributors {
		switch c.Requires {
		case config.RequiresDeploy:
			deployServices = append(deployServices, c.Service)
		case config.RequiresRestart:
			hasRestart = true
		}
	}
	ops := make([]journal.PendingOp, 0)
	if len(deployServices) > 0 {
		sort.Strings(deployServices)
		ops = append(ops, journal.PendingOp{Kind: journal.PendingDeploy, Services: deployServices})
	}
	if hasRestart {
		ops = append(ops, journal.PendingOp{Kind: journal.PendingRestart})
	}
	return ops
}

// mutateAndPlan performs the locked mutation flow for a single-service toggle:
// captures pre-state, writes local.yml, regenerates .env, builds the toggle
// plan, renders it to out, then atomically writes pending ops to the journal
// (only when stackEverDeployed is true). On any failure in steps 2-5,
// local.yml and .env are restored to their pre-toggle state.
//
// Whether to RUN the planned work immediately (vs leaving it deferred) is the
// caller's decision — it depends on `--apply` and stack-running state, not on
// this helper.
func mutateAndPlan(
	out io.Writer,
	baseDir, configPath, localPath, envPath, statePath string,
	cfg *config.DweConfig,
	reg *registry.Registry,
	svcDeploys map[string]*config.ServiceDeployConfig,
	deployedServices map[string]bool,
	stackEverDeployed bool,
	name string,
	direction ToggleDirection,
) (TogglePlan, []Contributor, *config.DweConfig, error) {
	releaseLock, err := lock.AcquireProjectLocks(baseDir)
	if err != nil {
		return TogglePlan{}, nil, nil, fmt.Errorf("acquiring project locks: %w", err)
	}
	defer releaseLock()

	// Step 0: Capture pre-state before any mutation.
	capturedLocal, err := captureFileState(localPath)
	if err != nil {
		return TogglePlan{}, nil, nil, fmt.Errorf("capturing local.yml state: %w", err)
	}
	capturedEnv, err := captureFileState(envPath)
	if err != nil {
		return TogglePlan{}, nil, nil, fmt.Errorf("capturing .env state: %w", err)
	}

	rollback := func() {
		_ = restoreFileState(localPath, capturedLocal)
		_ = restoreFileState(envPath, capturedEnv)
	}

	// Step 1: Write local.yml.
	var toEnable, toDisable []string
	if direction == DirectionEnable {
		toEnable = []string{name}
	} else {
		toDisable = []string{name}
	}
	local, err := localpkg.LoadLocalYAML(localPath)
	if err != nil {
		return TogglePlan{}, nil, nil, err
	}
	if err := localpkg.ApplyServiceTogglesToYAML(cfg, local, toEnable, toDisable); err != nil {
		return TogglePlan{}, nil, nil, err
	}
	if err := localpkg.WriteLocalYAML(localPath, local); err != nil {
		rollback()
		return TogglePlan{}, nil, nil, err
	}

	// Step 2: Reload config (picks up the local.yml change) and regenerate .env.
	cfgNew, err := config.LoadConfig(configPath)
	if err != nil {
		rollback()
		return TogglePlan{}, nil, nil, fmt.Errorf("reloading config after toggle: %w", err)
	}
	if err := envfile.Write(cfgNew, envPath); err != nil {
		rollback()
		return TogglePlan{}, nil, nil, fmt.Errorf("regenerating .env: %w", err)
	}

	// Step 3: Build toggle plan (in-memory, guard failures trigger rollback).
	toggles := []ToggleAction{{Service: name, Direction: direction}}
	plan, err := buildTogglePlan(cfgNew, reg, svcDeploys, toggles, deployedServices)
	if err != nil {
		rollback()
		return TogglePlan{}, nil, nil, err
	}

	// Step 4: Render plan to stdout.
	renderTogglePlan(out, plan)

	// Step 5: Write pending entries atomically via a single batch call.
	// Skipped only when the stack has never been deployed (no prior deploy
	// attempt of any kind) — pending represents "config drifted since deploy"
	// and is meaningless before the first deploy. Failed / in-progress /
	// project-only deploys all count as "ever deployed", and corrupt-journal
	// load errors default to ever-deployed too (see loadJournalSnapshot).
	// When the gate opens, AddPendingOps still runs even if contributors yield
	// zero ops (e.g. all RequiresNone) — it's a no-op then.
	contributors := buildContributors(cfgNew, toggles, deployedServices)
	if stackEverDeployed {
		ops := buildPendingOpsFromContributors(contributors)
		svc := cfgNew.Services[name]
		configHash := journal.ServiceConfigHash(svc, svcDeploys[name])
		if err := singleToggleAddPendingOps(statePath, ops, configHash); err != nil {
			rollback()
			return TogglePlan{}, nil, nil, fmt.Errorf("writing pending state: %w", err)
		}
	}

	return plan, contributors, cfgNew, nil
}

// batchServiceConfigHash computes a combined config hash covering all toggled services.
// It concatenates the per-service hashes in sorted-name order, which is deterministic
// and unique per configuration state.
func batchServiceConfigHash(cfg *config.DweConfig, svcDeploys map[string]*config.ServiceDeployConfig, names ...string) string {
	sorted := make([]string, len(names))
	copy(sorted, names)
	sort.Strings(sorted)
	parts := make([]string, len(sorted))
	for i, name := range sorted {
		svc := cfg.Services[name]
		parts[i] = journal.ServiceConfigHash(svc, svcDeploys[name])
	}
	return strings.Join(parts, ":")
}

// mutateAndPlanBatch performs the locked mutation flow for a multi-service toggle:
// captures pre-state, writes local.yml, regenerates .env, builds the toggle plan
// from all toEnable/toDisable actions, renders it to out, then atomically writes
// pending ops. On any failure in steps 2-5, local.yml and .env are restored.
// Pending is always written; see mutateAndPlan for the policy.
func mutateAndPlanBatch(
	out io.Writer,
	baseDir, configPath, localPath, envPath, statePath string,
	cfg *config.DweConfig,
	reg *registry.Registry,
	svcDeploys map[string]*config.ServiceDeployConfig,
	deployedServices map[string]bool,
	stackEverDeployed bool,
	toEnable, toDisable []string,
) (TogglePlan, []Contributor, *config.DweConfig, error) {
	releaseLock, err := lock.AcquireProjectLocks(baseDir)
	if err != nil {
		return TogglePlan{}, nil, nil, fmt.Errorf("acquiring project locks: %w", err)
	}
	defer releaseLock()

	// Step 0: Capture pre-state before any mutation.
	capturedLocal, err := captureFileState(localPath)
	if err != nil {
		return TogglePlan{}, nil, nil, fmt.Errorf("capturing local.yml state: %w", err)
	}
	capturedEnv, err := captureFileState(envPath)
	if err != nil {
		return TogglePlan{}, nil, nil, fmt.Errorf("capturing .env state: %w", err)
	}

	rollback := func() {
		_ = restoreFileState(localPath, capturedLocal)
		_ = restoreFileState(envPath, capturedEnv)
	}

	// Step 1: Write local.yml with all toggles applied in one pass.
	local, err := localpkg.LoadLocalYAML(localPath)
	if err != nil {
		return TogglePlan{}, nil, nil, err
	}
	if err := localpkg.ApplyServiceTogglesToYAML(cfg, local, toEnable, toDisable); err != nil {
		return TogglePlan{}, nil, nil, err
	}
	if err := localpkg.WriteLocalYAML(localPath, local); err != nil {
		rollback()
		return TogglePlan{}, nil, nil, err
	}

	// Step 2: Reload config (picks up the local.yml change) and regenerate .env.
	cfgNew, err := config.LoadConfig(configPath)
	if err != nil {
		rollback()
		return TogglePlan{}, nil, nil, fmt.Errorf("reloading config after toggle: %w", err)
	}
	if err := envfile.Write(cfgNew, envPath); err != nil {
		rollback()
		return TogglePlan{}, nil, nil, fmt.Errorf("regenerating .env: %w", err)
	}

	// Step 3: Build toggle plan (in-memory; guard failures trigger rollback).
	var toggles []ToggleAction
	for _, name := range toEnable {
		toggles = append(toggles, ToggleAction{Service: name, Direction: DirectionEnable})
	}
	for _, name := range toDisable {
		toggles = append(toggles, ToggleAction{Service: name, Direction: DirectionDisable})
	}
	plan, err := buildTogglePlan(cfgNew, reg, svcDeploys, toggles, deployedServices)
	if err != nil {
		rollback()
		return TogglePlan{}, nil, nil, err
	}

	// Step 4: Render plan to out.
	renderTogglePlan(out, plan)

	// Step 5: Write pending entries atomically via a single batch call.
	// Skipped only when the stack has never been deployed — see mutateAndPlan
	// for the rationale.
	contributors := buildContributors(cfgNew, toggles, deployedServices)
	if stackEverDeployed {
		ops := buildPendingOpsFromContributors(contributors)
		allNames := make([]string, 0, len(toEnable)+len(toDisable))
		allNames = append(allNames, toEnable...)
		allNames = append(allNames, toDisable...)
		configHash := batchServiceConfigHash(cfgNew, svcDeploys, allNames...)
		if err := multiToggleAddPendingOps(statePath, ops, configHash); err != nil {
			rollback()
			return TogglePlan{}, nil, nil, fmt.Errorf("writing pending state: %w", err)
		}
	}

	return plan, contributors, cfgNew, nil
}

// runSingleServiceToggle implements the full enable/disable mutation flow for a
// single named service. Handles --print-plan (dry-run), --apply (execute without
// prompt), TTY (prompt), and non-TTY (defer with hint) modes.
func runSingleServiceToggle(
	ctx context.Context,
	cmd *cobra.Command,
	flags *cmdctx.RootFlags,
	name string,
	direction ToggleDirection,
	opts singleToggleFlags,
) error {
	baseDir := flags.ProjectRoot()
	configPath := flags.ConfigPath

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	reg, regErr := usercommands.LoadRegistryFromConfigPath(configPath)
	if regErr != nil {
		return fmt.Errorf("loading command registry: %w", regErr)
	}

	svcDeploys, err := config.LoadServiceDeployConfigs(baseDir, cfg.Services)
	if err != nil {
		return fmt.Errorf("loading service deploy configs: %w", err)
	}

	statePath := filepath.Join(baseDir, journal.DefaultRelPath)
	snap, journalErr := loadJournalSnapshot(statePath)
	warnDeployedServicesLoad(cmd.ErrOrStderr(), journalErr)
	deployedServices := snap.deployed

	// --print-plan: pure dry-run — build and render without mutating anything.
	if opts.printPlan {
		plan, err := buildTogglePlan(cfg, reg, svcDeploys, []ToggleAction{{Service: name, Direction: direction}}, deployedServices)
		if err != nil {
			return err
		}
		renderTogglePlan(cmd.OutOrStdout(), plan)
		return nil
	}

	localPath := filepath.Join(baseDir, "workspace", "local.yml")
	envPath := filepath.Join(baseDir, ".env")

	// Stack has never been deployed: pending makes no sense (the first
	// `dwe deploy` will pick up the new local.yml fresh). Skip the probe and
	// any banner/prompt for the no-apply path. With --apply we still probe so
	// a broken docker setup surfaces an early warning before the deeper
	// executor error.
	neverDeployed := !snap.everDeployed
	stackRunning := true
	if !neverDeployed || opts.apply {
		stackRunning = probeStackOrWarn(cmd.ErrOrStderr(), cfg, baseDir)
	}

	plan, contributors, cfgNew, err := mutateAndPlan(
		cmd.OutOrStdout(),
		baseDir, configPath, localPath, envPath, statePath,
		cfg, reg, svcDeploys, deployedServices,
		snap.everDeployed,
		name, direction,
	)
	if err != nil {
		return err
	}

	// Step 6: Apply decision (outside the lock — apply steps acquire their own locks).
	deps := ExecuteDeps{
		Cmd:        cmd,
		Flags:      flags,
		BaseDir:    baseDir,
		StatePath:  statePath,
		Cfg:        cfgNew,
		CmdReg:     reg,
		RunDeploy:  singleToggleRunDeploy,
		RunRestart: singleToggleRunRestart,
		RunUserCmd: singleToggleRunUserCmd,
	}
	execOpts := ExecuteOptions{
		SkipHooks:    opts.skipHooks,
		Contributors: contributors,
	}

	// Explicit --apply always executes — even when the stack probe says
	// stopped — because the apply step (deploy/restart) is itself what brings
	// the stack up. Honored even on a never-deployed stack (the user opted
	// into the initial deploy).
	if opts.apply {
		return executeTogglePlan(ctx, deps, plan, execOpts)
	}

	// Never deployed: print the dwe-deploy hint regardless of plan shape,
	// since even a RequiresNone toggle won't take effect until the first
	// deploy. local.yml is updated, no pending was recorded, no hooks/apply
	// auto-run.
	if neverDeployed {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "stack has not been deployed yet; run `dwe deploy` to apply.")
		return nil
	}

	if len(plan.ApplySteps) == 0 && len(plan.BeforeSteps) == 0 && len(plan.AfterSteps) == 0 {
		// No work to defer or apply.
		return nil
	}

	// Stack not running and no --apply: hooks/apply are not auto-run. Pending
	// is already recorded so `dwe status` will remind the user.
	if !stackRunning {
		warnStackStopped(cmd.OutOrStdout(), plan)
		return nil
	}

	if len(plan.ApplySteps) == 0 {
		// Hooks exist but no apply step — execute immediately without prompting.
		return executeTogglePlan(ctx, deps, plan, execOpts)
	}

	if widgets.IsInteractiveFn(cmd.InOrStdin()) {
		ok, err := confirmApplyPrompt()
		if err != nil {
			if errors.Is(err, widgets.ErrCancelled) {
				return nil
			}
			return err
		}
		if ok {
			return executeTogglePlan(ctx, deps, plan, execOpts)
		}
		return nil
	}

	// Non-TTY, no --apply: leave pending in place, print hint.
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "to apply: rerun with --apply")
	return nil
}
