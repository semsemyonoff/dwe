package command

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/builtin"
	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"

	"github.com/spf13/cobra"
)

// implicitEnvStep is always the first step of any deploy plan.
// It regenerates .env from the current config before any phase runs.
var implicitEnvStep = config.DeployStep{
	Name:        "render-env",
	Devbox:      "render env -o .env",
	Description: "Generate .env from config (implicit first step)",
}

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
the plan to steps relevant to a specific service. Use --format yaml for machine-readable output.`,
		Example: `  devbox deploy plan
  devbox deploy plan --service main
  devbox deploy plan --format yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			var steps []resolvedStep
			if serviceName != "" {
				if _, ok := cfg.Services[serviceName]; !ok {
					return fmt.Errorf("service %q not found in config", serviceName)
				}
				steps, err = resolveServiceDeployPlan(cfg, serviceName)
			} else {
				steps, err = resolveDeployPlan(cfg)
			}
			if err != nil {
				return fmt.Errorf("resolving deploy plan: %w", err)
			}

			switch format {
			case "shell":
				printDeployPlanShell(steps, cmd.OutOrStdout())
			default:
				printDeployPlanTable(steps, render.Stdout())
			}
			return nil
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&format, "format", "table", "output format: table or shell")
	cmd.Flags().StringVar(&serviceName, "service", "", "show plan for a single service only")
	return cmd
}

// resolveDeployPlan builds the ordered list of active steps from cfg.Deploy.
// The implicit .env generation step is always prepended as step 0 (no phase).
// Steps whose when condition evaluates to false are excluded.
// Phases with deploy_services=true are expanded by inlining per-service
// deploy pipelines in topological dependency order.
func resolveDeployPlan(cfg *config.DevboxConfig) ([]resolvedStep, error) {
	// Implicit first step — no associated phase.
	implicit := resolvedStep{
		phase: config.DeployPhase{Name: "env", Description: "Environment"},
		step:  implicitEnvStep,
	}
	result := []resolvedStep{implicit}

	for _, phase := range cfg.Deploy.Phases {
		if phase.DeployServices {
			serviceSteps, err := resolveServicesDeploy(cfg)
			if err != nil {
				return nil, fmt.Errorf("resolving services deploy: %w", err)
			}
			result = append(result, serviceSteps...)
			continue
		}
		resolved, err := resolvePhaseSteps(cfg, phase, "")
		if err != nil {
			return nil, err
		}
		result = append(result, resolved...)
	}

	return result, nil
}

// resolveServiceDeployPlan builds the step list for a single service.
// Used by --service flag to deploy only one service.
func resolveServiceDeployPlan(cfg *config.DevboxConfig, serviceName string) ([]resolvedStep, error) {
	// Implicit first step.
	implicit := resolvedStep{
		phase: config.DeployPhase{Name: "env", Description: "Environment"},
		step:  implicitEnvStep,
	}
	result := []resolvedStep{implicit}

	cfgPath, ok := cfg.Raw["__configPath"].(string)
	if !ok {
		return nil, fmt.Errorf("internal: __configPath missing from config")
	}
	baseDir := filepath.Dir(cfgPath)
	svcDeploys, err := config.LoadServiceDeployConfigs(baseDir, map[string]config.ServiceConfig{
		serviceName: cfg.Services[serviceName],
	})
	if err != nil {
		return nil, err
	}
	svcDeploy, ok := svcDeploys[serviceName]
	if !ok {
		return nil, fmt.Errorf("no deploy pipeline found for service %q (expected devbox/deploy/%s.yml)", serviceName, serviceName)
	}

	for _, phase := range svcDeploy.Phases {
		resolved, err := resolvePhaseSteps(cfg, phase, serviceName)
		if err != nil {
			return nil, err
		}
		result = append(result, resolved...)
	}
	return result, nil
}

// resolveServicesDeploy loads all per-service deploy pipelines, sorts them
// by dependency order, and returns their steps inlined.
func resolveServicesDeploy(cfg *config.DevboxConfig) ([]resolvedStep, error) {
	cfgPath, ok := cfg.Raw["__configPath"].(string)
	if !ok {
		return nil, fmt.Errorf("internal: __configPath missing from config")
	}
	baseDir := filepath.Dir(cfgPath)

	// Only deploy enabled services.
	enabled := make(map[string]config.ServiceConfig)
	for name, svc := range cfg.Services {
		if svc.Enabled {
			enabled[name] = svc
		}
	}

	svcDeploys, err := config.LoadServiceDeployConfigs(baseDir, enabled)
	if err != nil {
		return nil, err
	}
	if len(svcDeploys) == 0 {
		return nil, nil
	}

	// Collect names that have deploy files and toposort.
	var names []string
	for name := range svcDeploys {
		names = append(names, name)
	}
	sorted, err := config.TopoSortServices(names, cfg.Services)
	if err != nil {
		return nil, err
	}

	var result []resolvedStep
	for _, name := range sorted {
		deploy := svcDeploys[name]
		for _, phase := range deploy.Phases {
			resolved, err := resolvePhaseSteps(cfg, phase, name)
			if err != nil {
				return nil, err
			}
			result = append(result, resolved...)
		}
	}
	return result, nil
}

// newDeployRunCmd creates the `devbox deploy run` command.
// It executes the resolved deploy plan step by step, printing phase/step
// progress and success messages directly — without generating a shell script.
// Devbox status messages are teed to deploy.log. Child process output
// (docker, make) goes directly to os.Stdout/os.Stderr so TTY detection works.
func newDeployRunCmd(flags *rootFlags) *cobra.Command {
	var serviceName string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute the deploy plan",
		Long: `Execute the full deploy pipeline from devbox/deploy.yml phase by phase.

Steps are run in declaration order. Progress and status messages are written to deploy.log.
The .env file is regenerated as the implicit first step. Use --service to run only the
steps relevant to a specific service.`,
		Example: `  devbox deploy run
  devbox deploy run --service main`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			workDir := filepath.Dir(flags.configPath)
			dockerCfg, err := config.LoadDockerConfig(workDir, cfg)
			if err != nil {
				return fmt.Errorf("loading docker config: %w", err)
			}
			if err := docker.EnsureVolumes(dockerCfg.Resources, "deploy", render.Stdout()); err != nil {
				return fmt.Errorf("ensuring volumes: %w", err)
			}

			var steps []resolvedStep
			if serviceName != "" {
				if _, ok := cfg.Services[serviceName]; !ok {
					return fmt.Errorf("service %q not found in config", serviceName)
				}
				steps, err = resolveServiceDeployPlan(cfg, serviceName)
			} else {
				steps, err = resolveDeployPlan(cfg)
			}
			if err != nil {
				return fmt.Errorf("resolving deploy plan: %w", err)
			}

			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading command registry: %w", err)
			}

			logsDir := filepath.Join(workDir, "logs")
			if err := os.MkdirAll(logsDir, 0o755); err != nil {
				return fmt.Errorf("creating logs directory %s: %w", logsDir, err)
			}
			logPath := filepath.Join(logsDir, "deploy.log")

			logFile, err := os.Create(logPath)
			if err != nil {
				return fmt.Errorf("creating deploy log %s: %w", logPath, err)
			}
			defer func() { _ = logFile.Close() }()

			// Devbox messages go to both terminal and log file.
			// Child process output goes directly to os.Stdout (see execStep).
			tee := io.MultiWriter(os.Stdout, &ansiStripper{logFile})
			w := render.NewWriter(tee)
			totalSteps := len(steps)
			lastPhaseKey := ""
			phaseSkipped := false // true when the current phase's when condition evaluated to false

			for i, rs := range steps {
				phaseKey := rs.phase.Name
				if rs.service != "" {
					phaseKey = rs.service + "/" + rs.phase.Name
				}
				if phaseKey != lastPhaseKey {
					phaseLabel := phaseKey
					if rs.phase.Description != "" {
						phaseLabel += ": " + rs.phase.Description
					}
					w.Info("Phase: " + phaseLabel)
					lastPhaseKey = phaseKey

					// Evaluate the phase-level when condition once at phase entry.
					phaseSkipped = false
					if rs.phaseWhen != "" {
						ok, err := condition.EvalRuntime(rs.phaseWhen, workDir)
						if err != nil {
							return fmt.Errorf("evaluating when condition for phase %s: %w", phaseKey, err)
						}
						if !ok {
							phaseSkipped = true
							w.Warning(fmt.Sprintf("  Skipping phase %s (when: %s)", phaseKey, rs.phaseWhen))
						}
					}
				}

				stepLabel := rs.stepAddress()
				if rs.step.Description != "" {
					stepLabel += ": " + rs.step.Description
				}
				w.Info(fmt.Sprintf("  [%d/%d] %s", i+1, totalSteps, stepLabel))

				// Skip all steps in a phase whose when condition was false.
				if phaseSkipped {
					w.Warning(fmt.Sprintf("  [%d/%d] Skipped: %s (phase when: %s)", i+1, totalSteps, rs.stepAddress(), rs.phaseWhen))
					continue
				}

				// Evaluate step-level when condition (independent of the phase condition).
				if rs.runtimeWhen != "" {
					ok, err := condition.EvalRuntime(rs.runtimeWhen, workDir)
					if err != nil {
						return fmt.Errorf("evaluating when condition for %s: %w", rs.stepAddress(), err)
					}
					if !ok {
						w.Warning(fmt.Sprintf("  [%d/%d] Skipped: %s (when: %s)", i+1, totalSteps, rs.stepAddress(), rs.runtimeWhen))
						continue
					}
				}

				if stepErr := execStep(rs.step, workDir, cfg, reg, logFile, false); stepErr != nil {
					w.Error(fmt.Sprintf("Deploy failed at step %q", rs.stepAddress()))
					w.Error("  " + stepErr.Error())
					w.Warning("Full output saved to: " + logPath)
					return ErrSilent
				}

				// After .env is regenerated, load it into the current process
				// environment so subsequent cmd: steps can reference its variables.
				if rs.step.Name == implicitEnvStep.Name {
					if err := sourceDotEnv(filepath.Join(workDir, ".env")); err != nil {
						return fmt.Errorf("sourcing .env: %w", err)
					}
				}

				if rs.step.Check != "" {
					ok, err := condition.EvalRuntime(rs.step.Check, workDir)
					if err != nil {
						w.Error(fmt.Sprintf("Deploy failed at step %q: check error", rs.stepAddress()))
						w.Error("  " + err.Error())
						w.Warning("Full output saved to: " + logPath)
						return ErrSilent
					}
					if !ok {
						w.Error(fmt.Sprintf("Deploy failed at step %q: check did not pass", rs.stepAddress()))
						w.Error(fmt.Sprintf("  check: %s", rs.step.Check))
						w.Warning("Full output saved to: " + logPath)
						return ErrSilent
					}
				}

				w.Success(fmt.Sprintf("  [%d/%d] Done: %s", i+1, totalSteps, rs.stepAddress()))
			}

			w.Info("Deploy log saved to: " + logPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&serviceName, "service", "", "deploy a single service only")
	return cmd
}

// sourceDotEnv reads a .env file and sets each KEY=VALUE pair as an OS
// environment variable so that subsequent exec.Cmd calls (with Env: nil)
// inherit them. Blank lines and comments are skipped.
func sourceDotEnv(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val := strings.TrimSpace(value)
		if n := len(val); n >= 2 && val[0] == val[n-1] && (val[0] == '"' || val[0] == '\'') {
			val = val[1 : n-1]
		}
		if err := os.Setenv(strings.TrimSpace(key), val); err != nil {
			return fmt.Errorf("setenv %s: %w", key, err)
		}
	}
	return nil
}

// findStep looks up a step by address in the deploy config.
// Supports two forms:
//   - "<phase>/<step>"          — orchestrator step
//   - "<service>/<phase>/<step>" — per-service step (loaded from devbox/deploy/<service>.yml)
func findStep(cfg *config.DevboxConfig, address string) (config.DeployPhase, config.DeployStep, error) {
	parts := strings.Split(address, "/")
	switch len(parts) {
	case 2:
		// Orchestrator step: <phase>/<step>
		phaseName, stepName := parts[0], parts[1]
		for _, phase := range cfg.Deploy.Phases {
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
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("phase %q not found", phaseName)

	case 3:
		// Service step: <service>/<phase>/<step>
		serviceName, phaseName, stepName := parts[0], parts[1], parts[2]
		if _, ok := cfg.Services[serviceName]; !ok {
			return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("service %q not found", serviceName)
		}
		cfgPath, ok := cfg.Raw["__configPath"].(string)
		if !ok {
			return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("internal: __configPath missing from config")
		}
		baseDir := filepath.Dir(cfgPath)
		svcDeploy, err := config.LoadDeployConfig(filepath.Join(baseDir, "devbox", "deploy", serviceName+".yml"))
		if err != nil {
			return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("loading deploy config for service %q: %w", serviceName, err)
		}
		for _, phase := range svcDeploy.Phases {
			if phase.Name != phaseName {
				continue
			}
			for _, step := range phase.Steps {
				if step.Name == stepName {
					return phase, step, nil
				}
			}
			return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("step %q not found in phase %q of service %q", stepName, phaseName, serviceName)
		}
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("phase %q not found in service %q", phaseName, serviceName)

	default:
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("invalid step address %q: expected <phase>/<step> or <service>/<phase>/<step>", address)
	}
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
			steps, err := resolveDeployPlan(cfg)
			if err != nil {
				return completions, cobra.ShellCompDirectiveNoFileComp
			}
			for _, s := range steps {
				addr := s.phase.Name + "/" + s.step.Name
				desc := s.step.Description
				if desc == "" {
					desc = s.step.Name
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
			phase, step, err := findStep(cfg, address)
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
				fmt.Println(resolved)
				return nil
			}

			workDir := filepath.Dir(flags.configPath)
			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading command registry: %w", err)
			}
			if err := execStep(step, workDir, cfg, reg, nil, false); err != nil {
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
			projectRoot := filepath.Dir(flags.configPath)
			ctx := builtin.ExecContext{
				Config:      cfg,
				ProjectRoot: projectRoot,
				Output:      render.Stdout(),
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

			projectRoot := filepath.Dir(flags.configPath)
			destDir := filepath.Join(projectRoot, svc.Dir, "configs")

			var missing []string
			for _, entry := range svc.Configs {
				dest := filepath.Join(destDir, entry.File)
				if !isRegularFile(dest) {
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

// isRegularFile reports whether path exists and is a regular file.
func isRegularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
