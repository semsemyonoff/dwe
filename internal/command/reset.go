package command

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	pipeline "devbox-cli/internal/pipeline"
	"devbox-cli/internal/render"
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
	cmd.AddCommand(newResetConfigCheckCmd(flags))
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
			steps, err := resolveResetPlan(cfg)
			if err != nil {
				return fmt.Errorf("resolving reset plan: %w", err)
			}
			switch format {
			case "shell":
				printResetPlanShell(steps, cmd.OutOrStdout())
			default:
				printDeployPlanTable(steps, render.Stdout())
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
func newResetRunCmd(flags *rootFlags) *cobra.Command {
	var yes bool
	var uiFlag string

	cmd := &cobra.Command{
		Use:          "run",
		Short:        "Execute the reset pipeline",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			workDir := filepath.Dir(flags.configPath)

			steps, err := resolveResetPlan(cfg)
			if err != nil {
				return fmt.Errorf("resolving reset plan: %w", err)
			}

			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading command registry: %w", err)
			}

			logsDir := filepath.Join(workDir, "logs")
			if err := os.MkdirAll(logsDir, 0o755); err != nil {
				return fmt.Errorf("creating logs directory %s: %w", logsDir, err)
			}
			logPath := filepath.Join(logsDir, "reset.log")
			logFile, err := os.Create(logPath)
			if err != nil {
				return fmt.Errorf("creating reset log %s: %w", logPath, err)
			}
			defer func() { _ = logFile.Close() }()

			tee := io.MultiWriter(os.Stdout, &ansiStripper{logFile})
			w := render.NewWriter(tee)

			mode, err := pipeline.ParseUIMode(uiFlag)
			if err != nil {
				return err
			}
			rep := pipeline.NewReporter(mode, w, logFile)

			if err := runPipeline(steps, rep, "reset", cfg, reg, workDir, logFile, yes, nil); err != nil {
				if errors.Is(err, ErrSilent) {
					w.Warning("Full output saved to: " + logPath)
				}
				return err
			}

			w.Info("Reset log saved to: " + logPath)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompts")
	cmd.Flags().StringVar(&uiFlag, "ui", "auto", "output mode: auto, plain, or tui")
	return cmd
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
			phase, step, err := findResetStep(cfg, address)
			if err != nil {
				return err
			}

			// Evaluate when condition.
			if step.When != "" {
				var (
					ok  bool
					err error
				)
				if condition.IsRuntime(step.When) {
					ok, err = condition.EvalRuntime(step.When, filepath.Dir(flags.configPath))
				} else {
					ok, err = tpl.EvalCondition(step.When, cfg)
				}
				if err != nil {
					return fmt.Errorf("evaluating when condition for %s: %w", address, err)
				}
				if !ok {
					render.Stdout().Warning(fmt.Sprintf("skipping step %s/%s: when condition is false (%s)", phase.Name, step.Name, step.When))
					return nil
				}
			}

			resolved := stepCommand(step)
			if dryRun {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), resolved)
				return nil
			}

			workDir := filepath.Dir(flags.configPath)
			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading command registry: %w", err)
			}
			// Single-step execution: no --yes flag, so confirm prompts are shown.
			if err := execStep(step, workDir, cfg, reg, nil, false, nil); err != nil {
				return err
			}

			if step.Check != "" {
				ok, err := condition.EvalRuntime(step.Check, workDir)
				if err != nil {
					return fmt.Errorf("step %s: check error: %w", address, err)
				}
				if !ok {
					return fmt.Errorf("step %s: check did not pass (%s)", address, step.Check)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved command without executing")
	return cmd
}

// newResetConfigCheckCmd creates the `devbox reset config` command group
// with `devbox reset config check` as a subcommand.
func newResetConfigCheckCmd(flags *rootFlags) *cobra.Command {
	parent := &cobra.Command{
		Use:          "config",
		Short:        "Reset config subcommands",
		SilenceUsage: true,
	}

	checkCmd := &cobra.Command{
		Use:   "check",
		Short: "Validate the reset pipeline config (devbox/reset.yml)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			steps, err := resolveResetPlan(cfg)
			if err != nil {
				return err
			}
			w := render.Stdout()
			w.Success(fmt.Sprintf("reset config OK: %d step(s) resolved", len(steps)))
			return nil
		},
		SilenceUsage: true,
	}

	parent.AddCommand(checkCmd)
	return parent
}

// resolveResetPlan builds the ordered step list from the reset pipeline config.
// Loads devbox/reset.yml and resolves all phases/steps.
func resolveResetPlan(cfg *config.DevboxConfig) ([]resolvedStep, error) {
	cfgPath, ok := cfg.Raw["__configPath"].(string)
	if !ok {
		return nil, fmt.Errorf("internal: __configPath missing from config")
	}
	baseDir := filepath.Dir(cfgPath)
	resetPath := filepath.Join(baseDir, "devbox", "reset.yml")

	resetCfg, err := config.LoadResetConfig(resetPath)
	if err != nil {
		return nil, fmt.Errorf("loading reset config %s: %w", resetPath, err)
	}

	var result []resolvedStep
	for _, phase := range resetCfg.Phases {
		resolved, err := resolvePhaseSteps(cfg, phase, "")
		if err != nil {
			return nil, err
		}
		result = append(result, resolved...)
	}
	return result, nil
}

// findResetStep looks up a step by <phase>/<step> address in the reset config.
func findResetStep(cfg *config.DevboxConfig, address string) (config.DeployPhase, config.DeployStep, error) {
	parts := strings.Split(address, "/")
	if len(parts) != 2 {
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("invalid step address %q: expected <phase>/<step>", address)
	}
	phaseName, stepName := parts[0], parts[1]

	cfgPath, ok := cfg.Raw["__configPath"].(string)
	if !ok {
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("internal: __configPath missing from config")
	}
	baseDir := filepath.Dir(cfgPath)
	resetCfg, err := config.LoadResetConfig(filepath.Join(baseDir, "devbox", "reset.yml"))
	if err != nil {
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("loading reset config: %w", err)
	}

	for _, phase := range resetCfg.Phases {
		if phase.Name != phaseName {
			continue
		}
		for _, step := range phase.Steps {
			if step.Name == stepName {
				return phase, step, nil
			}
		}
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("step %q not found in phase %q", stepName, phaseName)
	}
	return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("phase %q not found in reset config", phaseName)
}

// printResetPlanShell emits shell commands for the reset plan.
// Unlike the deploy plan shell output, there is no implicit .env step.
func printResetPlanShell(steps []resolvedStep, w io.Writer) {
	_, _ = fmt.Fprintln(w, "set -e")
	lastPhaseKey := ""
	for _, rs := range steps {
		if rs.phase.Name != lastPhaseKey {
			if rs.phase.When != "" {
				_, _ = fmt.Fprintf(w, "# phase %s [when: %s]\n", rs.phase.Name, rs.phase.When)
			}
			lastPhaseKey = rs.phase.Name
		}
		if rs.runtimeWhen != "" {
			_, _ = fmt.Fprintf(w, "# when: %s\n", rs.runtimeWhen)
		}
		if rs.step.Builtin != "" {
			// Builtins are in-process Go; delegate to the CLI step runner so the
			// generated script remains executable and behaviorally equivalent.
			_, _ = fmt.Fprintf(w, "./bin/devbox reset step %s\n", rs.stepAddress())
		} else {
			_, _ = fmt.Fprintln(w, stepCommand(rs.step))
		}
		if rs.step.Check != "" {
			_, _ = fmt.Fprintf(w, "# check: %s\n", rs.step.Check)
		}
	}
}
