package command

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/envfile"
	"devbox-cli/internal/lifecycle"
	"devbox-cli/internal/localconfig"
	"devbox-cli/internal/lock"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands/registry"
	"devbox-cli/internal/usercommands/runtime"

	"github.com/spf13/cobra"
)

// runMultiSelect is a package-level wrapper for ui.RunMultiSelect.
// Tests in this package swap it to inject fake multi-select behaviour.
var runMultiSelect = ui.RunMultiSelect

// singleToggleAddPendingOps is the seam for journal.AddPendingOps.
// Tests swap it to inject write failures for rollback coverage.
var singleToggleAddPendingOps = journal.AddPendingOps

// singleToggleRunDeploy and singleToggleRunRestart are seams for the apply
// phase so tests can stub the callees without a real Docker environment.
var singleToggleRunDeploy func(ctx context.Context, cmd *cobra.Command, flags *rootFlags, opts DeployOpts) error

func init() {
	// Assigned in init to avoid an init-order cycle (runDeployHelper is defined
	// in deploy.go, same package, but var init order is file-declaration order).
	singleToggleRunDeploy = runDeployHelper
	multiToggleRunDeploy = runDeployHelper
}

var singleToggleRunRestart = lifecycle.RunRestart
var singleToggleRunUserCmd = runtime.RunCommand

// multiToggleAddPendingOps is the seam for journal.AddPendingOps in the
// multi-select toggle flow, so tests can inject write failures independently.
var multiToggleAddPendingOps = journal.AddPendingOps

// multiToggleRunDeploy, multiToggleRunRestart, multiToggleRunUserCmd are seams
// for the multi-select toggle apply phase.
var multiToggleRunDeploy func(ctx context.Context, cmd *cobra.Command, flags *rootFlags, opts DeployOpts) error
var multiToggleRunRestart = lifecycle.RunRestart
var multiToggleRunUserCmd = runtime.RunCommand

// singleToggleFlags holds the parsed flags for a single-service enable/disable command.
type singleToggleFlags struct {
	apply     bool
	printPlan bool
	skipHooks bool
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
	tmp, err := os.CreateTemp(dir, ".devbox-restore-*")
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
func buildContributors(cfg *config.DevboxConfig, toggles []ToggleAction) []Contributor {
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
		requires := rawRequires.OrDefault()
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
// captures pre-state, writes local.yml, regenerates .env, builds the toggle plan,
// renders it to out, then atomically writes pending ops to the journal.
// On any failure in steps 2-5, local.yml and .env are restored to their pre-toggle state.
func mutateAndPlan(
	out io.Writer,
	baseDir, configPath, localPath, envPath, statePath string,
	cfg *config.DevboxConfig,
	reg *registry.Registry,
	svcDeploys map[string]*config.DeployConfig,
	name string,
	direction ToggleDirection,
) (TogglePlan, []Contributor, *config.DevboxConfig, error) {
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
	local, err := localconfig.LoadLocalYAML(localPath)
	if err != nil {
		return TogglePlan{}, nil, nil, err
	}
	if err := localconfig.ApplyServiceTogglesToYAML(cfg, local, toEnable, toDisable); err != nil {
		return TogglePlan{}, nil, nil, err
	}
	if err := localconfig.WriteLocalYAML(localPath, local); err != nil {
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
	plan, err := buildTogglePlan(cfgNew, reg, svcDeploys, toggles)
	if err != nil {
		rollback()
		return TogglePlan{}, nil, nil, err
	}

	// Step 4: Render plan to stdout.
	renderTogglePlan(out, plan)

	// Step 5: Write pending entries atomically via a single batch call.
	contributors := buildContributors(cfgNew, toggles)
	ops := buildPendingOpsFromContributors(contributors)
	svc := cfgNew.Services[name]
	configHash := journal.ServiceConfigHash(svc, svcDeploys[name])
	if err := singleToggleAddPendingOps(statePath, ops, configHash); err != nil {
		rollback()
		return TogglePlan{}, nil, nil, fmt.Errorf("writing pending state: %w", err)
	}

	return plan, contributors, cfgNew, nil
}

// batchServiceConfigHash computes a combined config hash covering all toggled services.
// It concatenates the per-service hashes in sorted-name order, which is deterministic
// and unique per configuration state.
func batchServiceConfigHash(cfg *config.DevboxConfig, svcDeploys map[string]*config.DeployConfig, names ...string) string {
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
func mutateAndPlanBatch(
	out io.Writer,
	baseDir, configPath, localPath, envPath, statePath string,
	cfg *config.DevboxConfig,
	reg *registry.Registry,
	svcDeploys map[string]*config.DeployConfig,
	toEnable, toDisable []string,
) (TogglePlan, []Contributor, *config.DevboxConfig, error) {
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
	local, err := localconfig.LoadLocalYAML(localPath)
	if err != nil {
		return TogglePlan{}, nil, nil, err
	}
	if err := localconfig.ApplyServiceTogglesToYAML(cfg, local, toEnable, toDisable); err != nil {
		return TogglePlan{}, nil, nil, err
	}
	if err := localconfig.WriteLocalYAML(localPath, local); err != nil {
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
	plan, err := buildTogglePlan(cfgNew, reg, svcDeploys, toggles)
	if err != nil {
		rollback()
		return TogglePlan{}, nil, nil, err
	}

	// Step 4: Render plan to out.
	renderTogglePlan(out, plan)

	// Step 5: Write pending entries atomically via a single batch call.
	contributors := buildContributors(cfgNew, toggles)
	ops := buildPendingOpsFromContributors(contributors)
	allNames := make([]string, 0, len(toEnable)+len(toDisable))
	allNames = append(allNames, toEnable...)
	allNames = append(allNames, toDisable...)
	configHash := batchServiceConfigHash(cfgNew, svcDeploys, allNames...)
	if err := multiToggleAddPendingOps(statePath, ops, configHash); err != nil {
		rollback()
		return TogglePlan{}, nil, nil, fmt.Errorf("writing pending state: %w", err)
	}

	return plan, contributors, cfgNew, nil
}

// runSingleServiceToggle implements the full enable/disable mutation flow for a
// single named service. Handles --print-plan (dry-run), --apply (execute without
// prompt), TTY (prompt), and non-TTY (defer with hint) modes.
func runSingleServiceToggle(
	ctx context.Context,
	cmd *cobra.Command,
	flags *rootFlags,
	name string,
	direction ToggleDirection,
	opts singleToggleFlags,
) error {
	baseDir := flags.ProjectRoot()
	configPath := flags.configPath

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	reg, regErr := loadCommandRegistry(configPath)
	if regErr != nil {
		reg = nil
	}

	svcDeploys, err := config.LoadServiceDeployConfigs(baseDir, cfg.Services)
	if err != nil {
		return fmt.Errorf("loading service deploy configs: %w", err)
	}

	// --print-plan: pure dry-run — build and render without mutating anything.
	if opts.printPlan {
		plan, err := buildTogglePlan(cfg, reg, svcDeploys, []ToggleAction{{Service: name, Direction: direction}})
		if err != nil {
			return err
		}
		renderTogglePlan(cmd.OutOrStdout(), plan)
		return nil
	}

	if regErr != nil {
		return fmt.Errorf("loading command registry: %w", regErr)
	}

	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	envPath := filepath.Join(baseDir, ".env")
	statePath := filepath.Join(baseDir, journal.DefaultRelPath)

	plan, contributors, cfgNew, err := mutateAndPlan(
		cmd.OutOrStdout(),
		baseDir, configPath, localPath, envPath, statePath,
		cfg, reg, svcDeploys,
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

	if opts.apply {
		return executeTogglePlan(ctx, deps, plan, execOpts)
	}

	if len(plan.ApplySteps) == 0 && len(plan.BeforeSteps) == 0 && len(plan.AfterSteps) == 0 {
		// No apply work and no hooks; pending was not written (all RequiresNone).
		return nil
	}
	if len(plan.ApplySteps) == 0 {
		// Hooks exist but no apply step — execute immediately without prompting.
		return executeTogglePlan(ctx, deps, plan, execOpts)
	}

	if ui.IsInteractiveFn(cmd.InOrStdin()) {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), "Run them now? [y/N] ")
		reader := bufio.NewReader(cmd.InOrStdin())
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "y" || line == "yes" {
			return executeTogglePlan(ctx, deps, plan, execOpts)
		}
		return nil
	}

	// Non-TTY, no --apply: leave pending in place, print hint.
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "to apply: rerun with --apply")
	return nil
}
