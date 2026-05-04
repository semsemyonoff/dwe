package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/builtin"
	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
	"devbox-cli/internal/docker"
	pipeline "devbox-cli/internal/pipeline"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"

	"github.com/spf13/cobra"
)

func newDeployCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy pipeline commands",
		Long: `Run and inspect the declarative deploy pipeline defined in devbox/deploy.yml.

The deploy pipeline consists of phases and steps that install, configure, and migrate
application services. Use 'devbox deploy plan' to preview before running.`,
		Example: `  devbox deploy plan
  devbox deploy run
  devbox deploy step init/render-env`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newDeployPlanCmd(flags))
	cmd.AddCommand(newDeployRunCmd(flags))
	cmd.AddCommand(newDeployStepCmd(flags))
	cmd.AddCommand(newDeployConfigCmd(flags))
	cmd.AddCommand(newDeployConfigCheckCmd(flags))
	return cmd
}

func newDeployPlanCmd(flags *rootFlags) *cobra.Command {
	var format string
	var serviceName string

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show resolved deploy plan",
		Long: `Print all phases and steps from devbox/deploy.yml as they would be executed.

The implicit .env generation step is always shown first. Use --service to filter
the plan to steps relevant to a specific service. Use --format shell for script-friendly output.`,
		Example: `  devbox deploy plan
  devbox deploy plan --service main
  devbox deploy plan --format shell`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			var steps []pipeline.ResolvedStep
			if serviceName != "" {
				if _, ok := cfg.Services[serviceName]; !ok {
					return fmt.Errorf("service %q not found in config", serviceName)
				}
				steps, err = deploy.ResolveServicePlan(cfg, serviceName)
			} else {
				steps, err = deploy.ResolvePlan(cfg)
			}
			if err != nil {
				return fmt.Errorf("resolving deploy plan: %w", err)
			}

			switch format {
			case "shell":
				deploy.PrintPlanShell(steps, cmd.OutOrStdout())
			default:
				pipeline.PrintPlanTable(steps, render.Stdout())
			}
			return nil
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&format, "format", "table", "output format: table or shell")
	cmd.Flags().StringVar(&serviceName, "service", "", "show plan for a single service only")
	return cmd
}

// newDeployRunCmd creates the `devbox deploy run` command.
// It executes the resolved deploy plan step by step, printing phase/step
// progress and success messages directly — without generating a shell script.
//
// File logging is controlled by the top-level `log:` field in devbox/deploy.yml
// (default: enabled). When enabled, devbox status messages are teed to
// logs/deploy.log; child process output (docker, make) goes directly to
// os.Stdout/os.Stderr so TTY detection works.
func newDeployRunCmd(flags *rootFlags) *cobra.Command {
	var serviceName string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute the deploy plan",
		Long: `Execute the full deploy pipeline from devbox/deploy.yml phase by phase.

Steps are run in declaration order. The .env file is regenerated as the implicit
first step. Use --service to run only the steps relevant to a specific service.

File logging is enabled by default for deploy and writes to logs/deploy.log.
Disable it with 'log: false' at the top of devbox/deploy.yml.`,
		Example: `  devbox deploy run
  devbox deploy run --service main`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			workDir := flags.ProjectRoot()
			dockerCfg, err := config.LoadDockerConfig(workDir, cfg)
			if err != nil {
				return fmt.Errorf("loading docker config: %w", err)
			}
			if err := docker.EnsureVolumes(dockerCfg.Resources, dockerCfg.ProjectName, "deploy", render.Stdout()); err != nil {
				return fmt.Errorf("ensuring volumes: %w", err)
			}

			var steps []pipeline.ResolvedStep
			if serviceName != "" {
				if _, ok := cfg.Services[serviceName]; !ok {
					return fmt.Errorf("service %q not found in config", serviceName)
				}
				steps, err = deploy.ResolveServicePlan(cfg, serviceName)
			} else {
				steps, err = deploy.ResolvePlan(cfg)
			}
			if err != nil {
				return fmt.Errorf("resolving deploy plan: %w", err)
			}

			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading command registry: %w", err)
			}

			logEnabled := cfg.Deploy.LogEnabled()
			w, logWriter, logPath, cleanup, err := pipeline.OpenPipelineLog(workDir, "deploy", logEnabled)
			if err != nil {
				return err
			}
			defer cleanup()

			rep := pipeline.NewPlainReporter(w)

			// After .env is regenerated, load it into the current process
			// environment so subsequent cmd: steps can reference its variables.
			postStepHooks := map[string]func() error{
				deploy.ImplicitEnvStep.Name: func() error {
					return deploy.SourceDotEnv(filepath.Join(workDir, ".env"))
				},
			}

			if err := pipeline.Run(steps, rep, "deploy", cfg, reg, workDir, logWriter, false, postStepHooks); err != nil {
				if errors.Is(err, ErrSilent) && logEnabled {
					w.Warning("Full output saved to: " + logPath)
				}
				return err
			}

			if logEnabled {
				w.Info("Deploy log saved to: " + logPath)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&serviceName, "service", "", "deploy a single service only")
	return cmd
}

func newDeployStepCmd(flags *rootFlags) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "step <phase>/<step>",
		Short: "Run a single deploy step by <phase>/<step> address",
		Long: `Execute a single step from the deploy pipeline by its address.

The address format is '<phase>/<step>' (e.g. 'init/render-env') or '<service>/<phase>/<step>'.
Use 'devbox deploy plan' to list available step addresses. Use --dry-run to preview without executing.`,
		Example: `  devbox deploy step init/render-env
  devbox deploy step main/setup/migrate
  devbox deploy step init/render-env --dry-run`,
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			var completions []string
			completions = cobra.AppendActiveHelp(completions, "Use 'devbox deploy plan' to see available phase/step addresses")
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return completions, cobra.ShellCompDirectiveNoFileComp
			}
			steps, err := deploy.ResolvePlan(cfg)
			if err != nil {
				return completions, cobra.ShellCompDirectiveNoFileComp
			}
			for _, s := range steps {
				addr := s.StepAddress()
				desc := s.Step.Description
				if desc == "" {
					desc = s.Step.Name
				}
				completions = append(completions, cobra.CompletionWithDesc(addr, desc))
			}
			return completions, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			address := args[0]
			phase, step, err := deploy.FindStep(cfg, address)
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
					ok, err = condition.EvalRuntime(step.When, flags.ProjectRoot())
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

			resolved := pipeline.StepCommand(step)
			if dryRun {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), resolved)
				return nil
			}

			workDir := flags.ProjectRoot()
			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading command registry: %w", err)
			}
			if err := pipeline.ExecStep(step, workDir, cfg, reg, nil, false); err != nil {
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
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved command without executing")
	return cmd
}

// newDeployConfigCmd creates the `devbox deploy config <service>` command.
// It copies all config files declared in services.<service>.configs from
// configs/services/<service>/ to services/<service>/configs/ using the given mode.
// Delegates to the service_configs_copy builtin for the actual copy logic.
func newDeployConfigCmd(flags *rootFlags) *cobra.Command {
	var mode string

	cmd := &cobra.Command{
		Use:   "config <service>",
		Short: "Copy template configs to service directory",
		Long: `Copy config file templates declared in services.<service>.configs from
configs/services/<service>/ to services/<service>/configs/.

Mode controls copy behavior: 'default' skips existing files, 'update' copies always, 'replace' overwrites.`,
		Example: `  devbox deploy config main
  devbox deploy config main --mode update`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			projectRoot := flags.ProjectRoot()
			ctx := builtin.ExecContext{
				Config:      cfg,
				ProjectRoot: projectRoot,
				Output:      render.Stdout(),
				Stdin:       os.Stdin,
			}
			with := map[string]any{"service": args[0], "mode": mode}
			return builtin.Run("service_configs_copy", with, ctx)
		},
	}

	cmd.Flags().StringVar(&mode, "mode", "replace", "copy mode: default, update, or replace")
	return cmd
}

// newDeployConfigCheckCmd creates the `devbox deploy config-check <service>` command.
// It verifies that all config files declared in services.<service>.configs exist in
// services/<service>/configs/. Exits non-zero and prints missing files if any are absent.
// Intended for use as a step check: "cmd: ./bin/devbox deploy config-check <service>".
func newDeployConfigCheckCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "config-check <service>",
		Short: "Verify that all declared service configs exist in the service directory",
		Long: `Check that every file listed in services.<service>.configs exists at
services/<service>/configs/<file>. Exits non-zero if any files are missing.

Intended as a deploy step check condition:
  check: "cmd: ./bin/devbox deploy config-check <service>"`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			serviceName := args[0]
			svc, ok := cfg.Services[serviceName]
			if !ok {
				return fmt.Errorf("service %q not found in config", serviceName)
			}

			projectRoot := flags.ProjectRoot()
			destDir := filepath.Join(projectRoot, svc.Dir, "configs")

			var missing []string
			for _, entry := range svc.Configs {
				dest := filepath.Join(destDir, entry.File)
				if !deploy.IsRegularFile(dest) {
					missing = append(missing, filepath.Join(svc.Dir, "configs", entry.File))
				}
			}

			if len(missing) > 0 {
				w := render.Stdout()
				for _, f := range missing {
					w.Error("missing config: " + f)
				}
				return ErrSilent
			}
			return nil
		},
	}
}
