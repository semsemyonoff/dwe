package command

import (
	"errors"
	"fmt"
	"path/filepath"

	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/lock"
	pipeline "devbox-cli/internal/pipeline"
	"devbox-cli/internal/render"
	"devbox-cli/internal/reset"
	"devbox-cli/internal/tpl"

	"github.com/spf13/cobra"
)

func newResetCmd(flags *rootFlags) *cobra.Command {
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
func newResetPlanCmd(flags *rootFlags) *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:          "plan",
		Short:        "Show resolved reset plan",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			steps, err := reset.ResolvePlan(cfg)
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
// Executes the reset pipeline from devbox/reset.yml.
// Use --yes to skip confirmation prompts.
//
// File logging is controlled by the top-level `log:` field in devbox/reset.yml
// (default: disabled). Enable with `log: true` to write .devbox/logs/reset.log.
func newResetRunCmd(flags *rootFlags) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute the reset pipeline",
		Long: `Execute the reset pipeline from devbox/reset.yml.

File logging is disabled by default for reset. Enable it with 'log: true' at
the top of devbox/reset.yml; output will be written to .devbox/logs/reset.log.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return resetRunCmd(flags, yes)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompts")
	return cmd
}

func resetRunCmd(flags *rootFlags, yes bool) error {
	workDir := flags.ProjectRoot()
	stateDir := filepath.Join(workDir, ".devbox", "deploy")
	statePath := filepath.Join(stateDir, "state.yml")
	lockPath := filepath.Join(stateDir, "deploy.lock")

	// Acquire file lock to prevent parallel resets
	lck, err := lock.Acquire(lockPath)
	if err != nil {
		if heldErr, ok := errors.AsType[*lock.HeldError](err); ok {
			return &lockHeldError{operation: "reset", pid: heldErr.PID}
		}
		return fmt.Errorf("acquiring lock: %w", err)
	}
	defer func() {
		_ = lck.Release()
	}()

	cfg, err := config.LoadConfig(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	resetCfg, steps, err := reset.LoadAndResolvePlan(cfg)
	if err != nil {
		return fmt.Errorf("resolving reset plan: %w", err)
	}

	reg, err := loadCommandRegistry(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading command registry: %w", err)
	}

	logEnabled := resetCfg.LogEnabled()
	w, logWriter, logPath, cleanup, err := pipeline.OpenPipelineLog(workDir, "reset", logEnabled)
	if err != nil {
		return err
	}
	defer cleanup()

	rep := pipeline.NewPlainReporter(w)

	opts := pipeline.RunOptions{
		Steps:       steps,
		Reporter:    rep,
		Name:        "reset",
		Config:      cfg,
		Registry:    reg,
		WorkDir:     workDir,
		LogWriter:   logWriter,
		SkipConfirm: yes,
	}

	if err := pipeline.RunWithOptions(opts); err != nil {
		if errors.Is(err, ErrSilent) && logEnabled {
			w.Warning("Full output saved to: " + logPath)
		}
		return err
	}

	// After reset succeeds, clean up the deploy state entirely.
	// Reset steps are always project-scoped (service == ""), so the whole state file is cleared.
	if err := journal.Remove(statePath); err != nil {
		w.Warning("Failed to clean deploy state: " + err.Error())
	}

	if logEnabled {
		w.Info("Reset log saved to: " + logPath)
	}
	return nil
}

// newResetStepCmd creates the `devbox reset step <phase>/<step>` command.
// Runs a single step from the reset pipeline by address.
func newResetStepCmd(flags *rootFlags) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:          "step <phase>/<step>",
		Short:        "Run a single reset step by <phase>/<step> address",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
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

			resolved := pipeline.StepCommand(step, config.DevboxBin(cfg))
			if dryRun {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), resolved)
				return nil
			}

			workDir := flags.ProjectRoot()
			reg, err := loadCommandRegistry(flags.configPath)
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

			if err := pipeline.ExecAction(step.Action(), actx); err != nil {
				return err
			}

			if step.Check != nil {
				if err := pipeline.ExecAction(*step.Check, actx); err != nil {
					return fmt.Errorf("step %s: check failed: %w", address, err)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved command without executing")
	return cmd
}
