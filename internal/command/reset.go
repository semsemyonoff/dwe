package command

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/command/cmdctx"
	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
	"devbox-cli/internal/deploy/journal"
	pipeline "devbox-cli/internal/pipeline"
	"devbox-cli/internal/reset"
	"devbox-cli/internal/shared/lock"
	"devbox-cli/internal/shared/render"
	"devbox-cli/internal/shared/tpl"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands"
	"devbox-cli/internal/usercommands/registry"
	"devbox-cli/internal/usercommands/runtime"

	"github.com/spf13/cobra"
)

// resetServiceRunHook is the seam for running on_disable.before user commands
// in per-service reset. Tests override this to avoid needing a real runtime.
var resetServiceRunHook = runtime.RunCommand

// resetRunHookFn is the seam for runResetHook itself. Tests can override this
// to intercept hook calls before any registry lookup.
var resetRunHookFn = runResetHook

func newResetCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "reset",
		Short:        "Reset pipeline commands",
		Long:         `Commands for running the declarative reset pipeline (devbox/reset.yml).`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newResetPlanCmd(flags))
	cmd.AddCommand(newResetRunCmd(flags))
	cmd.AddCommand(newResetStepCmd(flags))
	return cmd
}

// newResetPlanCmd creates the `devbox reset plan` command.
// Shows the resolved reset plan from devbox/reset.yml.
func newResetPlanCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:          "plan",
		Short:        "Show resolved reset plan",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.ConfigPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			reg, err := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
			if err != nil {
				return fmt.Errorf("loading command registry: %w", err)
			}
			steps, err := reset.ResolvePlan(cfg, reg)
			if err != nil {
				return fmt.Errorf("resolving reset plan: %w", err)
			}
			devboxBin := config.DevboxBin(cfg)
			switch format {
			case "shell":
				reset.PrintPlanShell(steps, cmd.OutOrStdout(), devboxBin)
			default:
				pipeline.PrintPlanTable(steps, render.Stdout(), devboxBin)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "table", "output format: table or shell")
	return cmd
}

// newResetRunCmd creates the `devbox reset run` command.
// Executes the reset pipeline from devbox/reset.yml, or a per-service reset
// pipeline from devbox/services/<name>/reset.yml when --service is given.
// Use --yes to skip confirmation prompts.
//
// File logging is controlled by the top-level `log:` field in devbox/reset.yml
// (default: disabled). Enable with `log: true` to write .devbox/logs/reset.log.
func newResetRunCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var yes bool
	var serviceName string
	var skipPreflight bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute the reset pipeline",
		Long: `Execute the reset pipeline from devbox/reset.yml.

When --service <name> is given, resets only that service:
runs on_disable.before hooks (if enabled), stops the container via 'docker stop',
executes devbox/services/<name>/reset.yml (if present), then marks the service
as requiring a subsequent deploy.

File logging is disabled by default for reset. Enable it with 'log: true' at
the top of devbox/reset.yml; output will be written to .devbox/logs/reset.log.`,
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

	// Preflight: run before any side effect on Docker, git, or the filesystem.
	if err := preflightRun(ctx, cfg, reg, workDir, "stop", skipPreflight, cmd.ErrOrStderr()); err != nil {
		return err
	}

	if regErr != nil {
		return fmt.Errorf("loading command registry: %w", regErr)
	}

	// Acquire deploy + snapshot project locks to prevent parallel resets
	// and to be mutually exclusive with snapshot mutating operations.
	releaseLocks, err := lock.AcquireProjectLocks(workDir)
	if err != nil {
		if phe, ok := errors.AsType[*lock.ProjectLockHeldError](err); ok {
			render.Stdout().Error(phe.Error())
			return phe
		}
		return fmt.Errorf("acquiring project locks: %w", err)
	}
	defer releaseLocks()

	dockerCfg, err := config.LoadDockerConfig(workDir, cfg)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("loading docker config: %w", err)
		}
		dockerCfg = &config.DockerConfig{}
	}

	resetCfg, steps, err := reset.LoadAndResolvePlan(cfg, reg)
	if err != nil {
		return fmt.Errorf("resolving reset plan: %w", err)
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

	// After reset succeeds, clean up the deploy state entirely.
	// Reset steps are always project-scoped (service == ""), so the whole state file is cleared.
	// Failure here is a hard error: leaving a stale deployed state would allow
	// devbox run to pass its gate even though services have been torn down.
	if err := journal.Remove(statePath); err != nil {
		return fmt.Errorf("cleaning deploy state after reset: %w", err)
	}

	if logEnabled {
		w.Info("Reset log saved to: " + logPath)
	}
	return nil
}

// resetServiceRunCmd implements `devbox reset run --service <name>`.
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
		return fmt.Errorf("%w: %s — per-service reset clears deployed state and requires a subsequent deploy; service %q has no deploy.yml, so its deployed state cannot be re-provisioned. Use full 'devbox reset run' instead", deploy.ErrServiceNoDeployFile, name, name)
	}

	reg, regErr := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
	if regErr != nil {
		reg = nil
	}

	// Preflight: stop-stage, before any hooks or locks.
	if err := preflightRun(ctx, cfg, reg, baseDir, "stop", skipPreflight, cmd.ErrOrStderr()); err != nil {
		return err
	}

	if regErr != nil {
		return fmt.Errorf("loading command registry: %w", regErr)
	}

	// Confirmation prompt (after preflight so fast-fail checks run first).
	if !yes {
		promptMsg := fmt.Sprintf("Reset service %q? This will stop the container and clear its deployed state, requiring a subsequent 'devbox deploy run --service %s'. Continue?", name, name)
		if svc.Required {
			promptMsg = fmt.Sprintf("Service %q is required. Reset will clear its deployed state and require a subsequent deploy. Continue?", name)
		}
		if ui.IsInteractiveFn(cmd.InOrStdin()) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", promptMsg)
			reader := bufio.NewReader(cmd.InOrStdin())
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(strings.ToLower(line))
			if line != "y" && line != "yes" {
				return nil
			}
		} else {
			return fmt.Errorf("non-interactive terminal: use --yes to confirm per-service reset")
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
	releaseLocks, err := lock.AcquireProjectLocks(baseDir)
	if err != nil {
		if phe, ok := errors.AsType[*lock.ProjectLockHeldError](err); ok {
			render.Stdout().Error(phe.Error())
			return phe
		}
		return fmt.Errorf("acquiring project locks: %w", err)
	}
	defer releaseLocks()

	deps := StopServiceDeps{
		Cfg:         cfg,
		CmdRegistry: reg,
		BaseDir:     baseDir,
	}

	// Step 2: Stop the container via compose-bypass (works whether enabled or disabled).
	if err := stopServiceLocked(ctx, deps, name); err != nil {
		return fmt.Errorf("stopping service %q: %w", name, err)
	}

	// Step 3: Execute per-service reset.yml if present.
	svcResetCfg, err := config.LoadServiceResetConfig(baseDir, name)
	if err != nil {
		return fmt.Errorf("loading reset config for service %q: %w", name, err)
	}
	if svcResetCfg != nil && len(svcResetCfg.Phases) > 0 {
		dockerCfg, loadErr := config.LoadDockerConfig(baseDir, cfg)
		if loadErr != nil {
			if !errors.Is(loadErr, os.ErrNotExist) {
				return fmt.Errorf("loading docker config: %w", loadErr)
			}
			dockerCfg = &config.DockerConfig{}
		}

		logEnabled := svcResetCfg.LogEnabled()
		w, logWriter, termOut, logPath, cleanup, openErr := pipeline.OpenPipelineLog(workDir, "reset-"+name, logEnabled)
		if openErr != nil {
			return openErr
		}
		defer cleanup()

		rep := pipeline.NewPlainReporter(w, logWriter, termOut)
		defer rep.Close()

		var steps []pipeline.ResolvedStep
		for _, phase := range svcResetCfg.Phases {
			resolved, phaseErr := pipeline.ResolvePhaseSteps(cfg, reg, phase, name)
			if phaseErr != nil {
				return fmt.Errorf("resolving reset phase for service %q: %w", name, phaseErr)
			}
			steps = append(steps, resolved...)
		}

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
		if runErr := pipeline.RunWithOptions(runOpts); runErr != nil {
			if errors.Is(runErr, pipeline.ErrSilent) && logEnabled {
				w.Warning("Full output saved to: " + logPath)
			}
			return runErr
		}
		if logEnabled {
			w.Info("Reset log saved to: " + logPath)
		}
	}

	// Step 4: Atomic journal update — remove service state and add PendingDeploy.
	configHash := journal.ServiceConfigHash(svc, svcDeploys[name])
	pendingOp := journal.PendingOp{
		Kind:     journal.PendingDeploy,
		Services: []string{name},
	}
	if err := journal.ReplaceServiceWithPending(statePath, name, pendingOp, configHash); err != nil {
		return fmt.Errorf("updating journal for service %q: %w", name, err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Service %q reset. Deploy required: run 'devbox deploy run --service %s'\n", name, name)
	return nil
}

// runResetHook looks up a command by ID in the registry and runs it
// non-interactively. Used for on_disable.before hooks in per-service reset.
func runResetHook(ctx context.Context, cmd *cobra.Command, cfg *config.DevboxConfig, reg *registry.Registry, baseDir, cmdID string) error {
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

// newResetStepCmd creates the `devbox reset step <phase>/<step>` command.
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

			resolved := pipeline.StepCommand(step, config.DevboxBin(cfg))
			if dryRun {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), resolved)
				return nil
			}

			workDir := flags.ProjectRoot()
			reg, err := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
			if err != nil {
				return fmt.Errorf("loading command registry: %w", err)
			}
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
