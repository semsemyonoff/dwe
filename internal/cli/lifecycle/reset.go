package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	pipeline "github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/core/workflow/reset"
	"github.com/semsemyonoff/dwe/internal/shared/promptcache"
	"github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"

	"github.com/spf13/cobra"
)

// resetServiceRunHook is the seam for running on_disable.before user commands
// in per-service reset. Tests override this to avoid needing a real runtime.
var resetServiceRunHook = runtime.RunCommand

// resetRunHookFn is the seam for runResetHook itself. Tests can override this
// to intercept hook calls before any registry lookup.
var resetRunHookFn = runResetHook

// resetConfirmFn is the swap seam for the per-service reset confirmation form.
// Tests override this to assert prompt content and inject Yes/No/cancelled.
// Required because runConfirmFormFn in internal/core/ui/confirm.go is
// package-private — tests in the lifecycle package cannot swap it directly.
var resetConfirmFn = widgets.RunConfirm

// NewResetCmd builds the `dwe reset` cobra command group.
func NewResetCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "reset",
		Short:        "Reset pipeline commands",
		Long:         `Commands for running the declarative reset pipeline (workspace/reset.yml).`,
		GroupID:      groupID,
		SilenceUsage: true,
	}
	cmd.AddCommand(newResetPlanCmd(flags))
	cmd.AddCommand(newResetRunCmd(flags))
	cmd.AddCommand(newResetStepCmd(flags))
	return cmd
}

type resetPlanOpts struct {
	Format string
}

// runResetPlan is the testable core of newResetPlanCmd. Callers set cmd's
// Out/Err writers before calling; stderr receives the default-pipeline notice
// when reset.yml is absent.
func runResetPlan(cmd *cobra.Command, flags *cmdctx.RootFlags, opts resetPlanOpts) error {
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	reg, err := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading command registry: %w", err)
	}
	_ = reg.ApplyVisibility(cfg, flags.ProjectRoot())
	_, steps, defaulted, err := reset.LoadAndResolvePlan(cfg, reg)
	if err != nil {
		return fmt.Errorf("resolving reset plan: %w", err)
	}
	if defaulted {
		cmdctx.EmitDefaultNotice(cmd, flags, "reset", "reset")
	}
	dweBin := config.DweBin(cfg)
	switch opts.Format {
	case "shell":
		reset.PrintPlanShell(steps, cmd.OutOrStdout(), dweBin)
	default:
		pipeline.PrintPlanTable(steps, render.NewWriter(cmd.OutOrStdout()), dweBin)
	}
	return nil
}

// newResetPlanCmd creates the `dwe reset plan` command.
// Shows the resolved reset plan from workspace/reset.yml.
func newResetPlanCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:          "plan",
		Short:        "Show resolved reset plan",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResetPlan(cmd, flags, resetPlanOpts{Format: format})
		},
	}

	cmd.Flags().StringVar(&format, "format", "table", "output format: table or shell")
	return cmd
}

// newResetRunCmd creates the `dwe reset run` command.
// Executes the reset pipeline from workspace/reset.yml, or a per-service reset
// pipeline from workspace/services/<name>/reset.yml when --service is given.
// Use --yes to skip confirmation prompts.
//
// File logging is controlled by the top-level `log:` field in workspace/reset.yml
// (default: disabled). Enable with `log: true` to write .dwe/logs/reset.log.
func newResetRunCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var yes bool
	var serviceName string
	var skipPreflight bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute the reset pipeline",
		Long: `Execute the reset pipeline from workspace/reset.yml.

When --service <name> is given, resets only that service:
runs on_disable.before hooks (if enabled), stops and removes the container,
deletes the service 'dir:' if declared and present on disk, executes
workspace/services/<name>/reset.yml (if present), then marks the service as
requiring a subsequent deploy. Volumes are NOT auto-removed; use
'docker_remove_project_volumes' in services/<name>/reset.yml to opt in.

File logging is disabled by default for reset. Enable it with 'log: true' at
the top of workspace/reset.yml; output will be written to .dwe/logs/reset.log.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if serviceName != "" {
				return resetServiceRunCmd(cmd, flags, serviceName, yes, skipPreflight)
			}
			return resetRunCmd(cmd, flags, yes, skipPreflight)
		},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompts")
	cmd.Flags().StringVar(&serviceName, "service", "", "reset only this service")
	cmdctx.AddSkipPreflight(cmd, &skipPreflight)
	return cmd
}

func resetRunCmd(cmd *cobra.Command, flags *cmdctx.RootFlags, yes bool, skipPreflight bool) error {
	ctx := cmd.Context()
	workDir := flags.ProjectRoot()
	statePath := filepath.Join(workDir, journal.DefaultRelPath)

	// Load cfg + registry BEFORE acquiring locks so preflight can reject
	// without leaving a stale lock file.
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	reg, regErr := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
	if regErr != nil {
		reg = nil
	}
	if reg != nil {
		_ = reg.ApplyVisibility(cfg, workDir)
	}

	// Preflight: run before any side effect on Docker, git, or the filesystem.
	if err := preflightRun(ctx, cfg, reg, workDir, "stop", skipPreflight, cmd.ErrOrStderr()); err != nil {
		return err
	}

	if regErr != nil {
		return fmt.Errorf("loading command registry: %w", regErr)
	}

	// Acquire deploy + snapshot project locks to prevent parallel resets
	// and to be mutually exclusive with snapshot mutating operations.
	releaseLocks, err := cmdctx.AcquireProjectLocksOrReport(workDir, render.Stdout())
	if err != nil {
		return err
	}
	defer releaseLocks()

	dockerCfg, err := config.LoadDockerConfigOrEmpty(workDir, cfg)
	if err != nil {
		return err
	}

	resetCfg, steps, defaulted, err := reset.LoadAndResolvePlan(cfg, reg)
	if err != nil {
		return fmt.Errorf("resolving reset plan: %w", err)
	}
	if defaulted {
		cmdctx.EmitDefaultNotice(cmd, flags, "reset", "reset")
	}

	logEnabled := resetCfg.LogEnabled()
	w, logWriter, termOut, logPath, cleanup, err := pipeline.OpenPipelineLog(workDir, "reset", logEnabled)
	if err != nil {
		return err
	}
	defer cleanup()

	rep := pipeline.NewPlainReporter(w, logWriter, termOut)
	defer rep.Close()

	opts := pipeline.RunOptions{
		Steps:        steps,
		Reporter:     rep,
		Name:         "reset",
		Config:       cfg,
		DockerConfig: dockerCfg,
		Registry:     reg,
		WorkDir:      workDir,
		LogWriter:    logWriter,
		SkipConfirm:  yes,
		Translator:   flags.I18n,
		Locale:       flags.Locale,
	}

	if err := pipeline.RunWithOptions(opts); err != nil {
		if errors.Is(err, pipeline.ErrSilent) && logEnabled {
			w.Warning("Full output saved to: " + logPath)
		}
		return err
	}

	// Pipeline succeeded (docker down already ran) — containers are stopped regardless
	// of what journal cleanup does below. Write the cache now so that a subsequent
	// journal.Remove failure doesn't leave the prompt showing a stale "running" state.
	_ = promptcache.Write(workDir, promptcache.StateStopped)

	// After reset succeeds, clean up the deploy state entirely.
	// Reset steps are always project-scoped (service == ""), so the whole state file is cleared.
	// Failure here is a hard error: leaving a stale deployed state would allow
	// dwe run to pass its gate even though services have been torn down.
	if err := journal.Remove(statePath); err != nil {
		return fmt.Errorf("cleaning deploy state after reset: %w", err)
	}

	if logEnabled {
		w.Info("Reset log saved to: " + logPath)
	}
	return nil
}

// resetServiceRunCmd implements `dwe reset run --service <name>`.
// It validates the service, runs preflight, runs on_disable.before hooks
// (outside the lock), then acquires the project lock and atomically stops
// the container, executes the per-service reset.yml (if present), and
// writes a PendingDeploy journal entry.
func resetServiceRunCmd(cmd *cobra.Command, flags *cmdctx.RootFlags, name string, yes bool, skipPreflight bool) error {
	ctx := cmd.Context()
	workDir := flags.ProjectRoot()
	statePath := filepath.Join(workDir, journal.DefaultRelPath)
	baseDir := workDir

	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Validation: service must exist.
	svc, ok := cfg.Services[name]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownService, name)
	}

	// Validation: service must have a deploy.yml so the pending deploy is actionable.
	svcDeploys, err := config.LoadServiceDeployConfigs(baseDir, cfg.Services)
	if err != nil {
		return fmt.Errorf("loading service deploy configs: %w", err)
	}
	if svcDeploys[name] == nil {
		return fmt.Errorf("%w: %s — per-service reset clears deployed state and requires a subsequent deploy; service %q has no deploy.yml, so its deployed state cannot be re-provisioned. Use full 'dwe reset run' instead", deploy.ErrServiceNoDeployFile, name, name)
	}

	reg, regErr := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
	if regErr != nil {
		reg = nil
	}
	if reg != nil {
		_ = reg.ApplyVisibility(cfg, baseDir)
	}

	// Preflight: stop-stage, before any hooks or locks.
	if err := preflightRun(ctx, cfg, reg, baseDir, "stop", skipPreflight, cmd.ErrOrStderr()); err != nil {
		return err
	}

	if regErr != nil {
		return fmt.Errorf("loading command registry: %w", regErr)
	}

	// Load the optional per-service reset.yml early so we can describe it in
	// the confirm body and assemble synthetic + user phases below.
	svcResetCfg, err := config.LoadServiceResetConfig(baseDir, name)
	if err != nil {
		return fmt.Errorf("loading reset config for service %q: %w", name, err)
	}

	// Resolve whether the service directory actually exists on disk; the
	// always-on baseline only adds the files phase when there is something to
	// remove.
	dirExists := false
	if svc.Dir != "" {
		if _, statErr := os.Stat(filepath.Join(baseDir, svc.Dir)); statErr == nil {
			dirExists = true
		}
	}
	hasResetYAML := svcResetCfg != nil && len(svcResetCfg.Phases) > 0

	// Confirmation prompt (after preflight so fast-fail checks run first).
	if !yes {
		if !widgets.IsInteractiveFn(cmd.InOrStdin()) {
			return fmt.Errorf("non-interactive terminal: use --yes to confirm per-service reset")
		}
		title := buildResetServiceConfirmTitle(name, svc.Container, svc.Dir, svc.Required, dirExists, hasResetYAML)
		ok, confirmErr := resetConfirmFn(title, "Reset", "Cancel")
		if confirmErr != nil {
			if errors.Is(confirmErr, widgets.ErrCancelled) {
				return nil
			}
			return fmt.Errorf("confirm reset: %w", confirmErr)
		}
		if !ok {
			return nil
		}
	}

	// Run on_disable.before hooks outside the lock (only when service is enabled).
	if svc.Enabled && svc.OnDisable != nil && len(svc.OnDisable.Before) > 0 {
		for _, cmdID := range svc.OnDisable.Before {
			if err := resetRunHookFn(ctx, cmd, cfg, reg, baseDir, cmdID); err != nil {
				return fmt.Errorf("on_disable.before hook %q: %w", cmdID, err)
			}
		}
	}

	// Acquire project locks around container stop, reset.yml, and journal update.
	releaseLocks, err := cmdctx.AcquireProjectLocksOrReport(baseDir, render.Stdout())
	if err != nil {
		return err
	}
	defer releaseLocks()

	// Docker config is needed by the pipeline for any docker-related builtins
	// (and is always present in opts so the executor doesn't need to nil-check).
	dockerCfg, err := config.LoadDockerConfigOrEmpty(baseDir, cfg)
	if err != nil {
		return err
	}

	// Assemble combined step list: synthetic baseline phases first, then any
	// phases declared in services/<name>/reset.yml. Synthetic phases use
	// Untracked: true so they're excluded from the [N/M] counter; phase names
	// must start with "_" so the executor permits KindInternal builtins.
	steps := []pipeline.ResolvedStep{
		{
			Phase: config.DeployPhase{
				Name:        "_container",
				Description: "Stop and remove container",
				Untracked:   true,
			},
			Step: config.DeployStep{
				Name: "stop-and-remove",
				Type: "builtin",
				Cmd:  "docker_stop_remove_container",
				With: map[string]any{
					"container_template": svc.Container,
					"stop_timeout":       "10s",
				},
			},
			Service: name,
		},
	}
	if dirExists {
		// Note: the files phase intentionally does NOT use a "_"-prefix so the
		// executor selects CtxUserYAML for the body — remove_paths is a
		// KindAction builtin and is not permitted in CtxInternal.
		steps = append(steps, pipeline.ResolvedStep{
			Phase: config.DeployPhase{
				Name:        "files",
				Description: "Remove service directory",
				Untracked:   true,
			},
			Step: config.DeployStep{
				Name: "remove-dir",
				Type: "builtin",
				Cmd:  "remove_paths",
				With: map[string]any{
					"paths": []any{svc.Dir},
				},
			},
			Service: name,
		})
	}
	if hasResetYAML {
		for _, phase := range svcResetCfg.Phases {
			resolved, phaseErr := pipeline.ResolvePhaseSteps(cfg, reg, phase, name)
			if phaseErr != nil {
				return fmt.Errorf("resolving reset phase for service %q: %w", name, phaseErr)
			}
			steps = append(steps, resolved...)
		}
	}

	logEnabled := false
	if svcResetCfg != nil {
		logEnabled = svcResetCfg.LogEnabled()
	}
	w, logWriter, termOut, logPath, cleanup, openErr := pipeline.OpenPipelineLog(workDir, "reset-"+name, logEnabled)
	if openErr != nil {
		return openErr
	}
	defer cleanup()

	rep := pipeline.NewPlainReporter(w, logWriter, termOut)
	defer rep.Close()

	runOpts := pipeline.RunOptions{
		Steps:        steps,
		Reporter:     rep,
		Name:         "reset-" + name,
		Config:       cfg,
		DockerConfig: dockerCfg,
		Registry:     reg,
		WorkDir:      workDir,
		LogWriter:    logWriter,
		SkipConfirm:  yes,
		Translator:   flags.I18n,
		Locale:       flags.Locale,
	}
	runErr := pipeline.RunWithOptions(runOpts)
	// Invalidate regardless of outcome: a failed reset may have partially
	// mutated container state (e.g. the stop step succeeded before a later
	// step failed). Let the next prompt refresh reflect ground truth.
	_ = promptcache.Remove(workDir)
	if runErr != nil {
		if errors.Is(runErr, pipeline.ErrSilent) && logEnabled {
			w.Warning("Full output saved to: " + logPath)
		}
		return runErr
	}
	if logEnabled {
		w.Info("Reset log saved to: " + logPath)
	}

	// Final step: atomic journal update — remove service state and add PendingDeploy.
	configHash := journal.ServiceConfigHash(svc, svcDeploys[name])
	pendingOp := journal.PendingOp{
		Kind:     journal.PendingDeploy,
		Services: []string{name},
	}
	if err := journal.ReplaceServiceWithPending(statePath, name, pendingOp, configHash); err != nil {
		return fmt.Errorf("updating journal for service %q: %w", name, err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Service %q reset. Deploy required: run 'dwe deploy run --service %s'\n", name, name)
	return nil
}

// buildResetServiceConfirmTitle constructs the multi-line title for the
// per-service reset huh confirm form. The body itemises exactly what the
// reset will do so the user has zero ambiguity before pressing Reset.
func buildResetServiceConfirmTitle(name, container, dir string, required, dirExists, hasResetYAML bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Reset service %q?", name)
	if required {
		b.WriteString("\n\nWarning: this is a required service.")
	}
	b.WriteString("\n\nThis will:")
	fmt.Fprintf(&b, "\n  • stop and remove container %q", container)
	if dirExists {
		fmt.Fprintf(&b, "\n  • delete directory %s", dir)
	}
	if hasResetYAML {
		fmt.Fprintf(&b, "\n  • run services/%s/reset.yml", name)
	}
	fmt.Fprintf(&b, "\n  • require a subsequent: dwe deploy run --service %s", name)
	return b.String()
}

// runResetHook looks up a command by ID in the registry and runs it
// non-interactively. Used for on_disable.before hooks in per-service reset.
func runResetHook(ctx context.Context, cmd *cobra.Command, cfg *config.DweConfig, reg *registry.Registry, baseDir, cmdID string) error {
	if reg == nil {
		return fmt.Errorf("command registry required for hook %q", cmdID)
	}
	cmdDef, err := reg.Get(cmdID)
	if err != nil {
		return fmt.Errorf("looking up command %q: %w", cmdID, err)
	}
	var stdout, stderr io.Writer
	var stdin io.Reader
	if cmd != nil {
		stdout = cmd.OutOrStdout()
		stderr = cmd.ErrOrStderr()
		stdin = cmd.InOrStdin()
	}
	rc := runtime.RunContext{
		Cmd:            cmdDef,
		Config:         cfg,
		Registry:       reg,
		ProjectRoot:    baseDir,
		Stdout:         stdout,
		Stderr:         stderr,
		Stdin:          stdin,
		SkipConfirm:    true,
		NonInteractive: true,
		SkipNotify:     true,
	}
	return resetServiceRunHook(ctx, rc)
}

// newResetStepCmd creates the `dwe reset step <phase>/<step>` command.
// Runs a single step from the reset pipeline by address.
func newResetStepCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:          "step <phase>/<step>",
		Short:        "Run a single reset step by <phase>/<step> address",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.ConfigPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			address := args[0]
			phase, step, err := reset.FindStep(cfg, address)
			if err != nil {
				return err
			}

			// Evaluate when condition.
			if step.When != nil {
				var (
					ok  bool
					err error
				)
				if step.When.IsRuntime() {
					ok, err = condition.EvalRuntimeTyped(step.When, flags.ProjectRoot())
				} else if step.When.Type == condition.TypeTemplate {
					ok, err = tpl.EvalCondition(step.When.Expr, cfg)
				}
				if err != nil {
					return fmt.Errorf("evaluating when condition for %s: %w", address, err)
				}
				if !ok {
					render.Stdout().Warning(fmt.Sprintf("skipping step %s/%s: when condition is false", phase.Name, step.Name))
					return nil
				}
			}

			if step.FilesGate != nil {
				render.Stdout().Warning(fmt.Sprintf("note: files_gate on step %s/%s is not evaluated by this command", phase.Name, step.Name))
			}

			resolved := pipeline.StepCommand(step, config.DweBin(cfg))
			if dryRun {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), resolved)
				return nil
			}

			workDir := flags.ProjectRoot()
			reg, err := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
			if err != nil {
				return fmt.Errorf("loading command registry: %w", err)
			}
			_ = reg.ApplyVisibility(cfg, workDir)
			// Single-step execution: no --yes flag, so confirm prompts are shown.
			actx := pipeline.ActionContext{
				WorkDir:     workDir,
				Cfg:         cfg,
				Reg:         reg,
				LogWriter:   nil,
				SkipConfirm: false,
			}

			if err := pipeline.ExecAction(cmd.Context(), step.Action(), actx); err != nil {
				return err
			}

			if step.Check != nil {
				if err := pipeline.ExecAction(cmd.Context(), *step.Check, actx); err != nil {
					return fmt.Errorf("step %s: check failed: %w", address, err)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved command without executing")
	return cmd
}
