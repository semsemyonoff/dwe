package command

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"devbox-cli/internal/commands"
	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"

	"github.com/spf13/cobra"
)

// implicitEnvStep is always the first step of any deploy plan.
// It regenerates .env from the current config before any phase runs.
var implicitEnvStep = config.DeployStep{
	Name:        "render-env",
	Cmd:         "./bin/devbox render env -o .env",
	Description: "Generate .env from config (implicit first step)",
}

func newDeployCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "deploy",
		Short:        "Deploy pipeline commands",
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
		Args:  cobra.NoArgs,
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

// resolvedStep holds a deploy step together with the phase it belongs to,
// after when-condition filtering.
//
// service is non-empty for steps that belong to a per-service deploy pipeline.
// runtimeWhen is non-empty when the step's When condition is a runtime expression
// (builtin predicate or cmd:). Such conditions are NOT evaluated at plan-resolution
// time — they are checked immediately before the step executes.
type resolvedStep struct {
	phase       config.DeployPhase
	step        config.DeployStep
	service     string // non-empty for per-service steps (e.g. "main")
	runtimeWhen string // copy of step.When when IsRuntime; empty otherwise
}

// stepAddress returns the full address of a step for display and lookup:
//   - orchestrator steps: "<phase>/<step>"
//   - service steps:      "<service>/<phase>/<step>"
func (rs resolvedStep) stepAddress() string {
	if rs.service != "" {
		return rs.service + "/" + rs.phase.Name + "/" + rs.step.Name
	}
	return rs.phase.Name + "/" + rs.step.Name
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

	baseDir := filepath.Dir(cfg.Raw["__configPath"].(string))
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
	baseDir := filepath.Dir(cfg.Raw["__configPath"].(string))

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

// resolvePhaseSteps resolves steps for a single phase, evaluating when conditions.
// service is empty for orchestrator phases.
//
// Phase-level when is evaluated first:
//   - Go template: evaluated at plan time; entire phase is excluded when false.
//   - Runtime condition: propagated to each step that does not already carry its
//     own runtime when condition. The phase condition is stored in runtimeWhen and
//     evaluated before each such step at execution time.
func resolvePhaseSteps(cfg *config.DevboxConfig, phase config.DeployPhase, service string) ([]resolvedStep, error) {
	// Evaluate phase-level when condition.
	phaseRuntimeWhen := ""
	if phase.When != "" {
		if condition.IsRuntime(phase.When) {
			// Propagate to steps at execution time.
			phaseRuntimeWhen = phase.When
		} else {
			// Template condition — evaluate at plan time.
			ok, err := tpl.EvalCondition(phase.When, cfg)
			if err != nil {
				prefix := phase.Name
				if service != "" {
					prefix = service + "/" + prefix
				}
				return nil, fmt.Errorf("evaluating when condition for phase %s: %w", prefix, err)
			}
			if !ok {
				return nil, nil
			}
		}
	}

	var result []resolvedStep
	for _, step := range phase.Steps {
		if step.When != "" {
			if condition.IsRuntime(step.When) {
				// Step has its own runtime condition — it takes priority over the phase condition.
				result = append(result, resolvedStep{phase: phase, step: step, service: service, runtimeWhen: step.When})
				continue
			}
			ok, err := tpl.EvalCondition(step.When, cfg)
			if err != nil {
				prefix := phase.Name + "/" + step.Name
				if service != "" {
					prefix = service + "/" + prefix
				}
				return nil, fmt.Errorf("evaluating when condition for step %s: %w", prefix, err)
			}
			if !ok {
				continue
			}
		}
		// Apply phase runtime condition to steps that have no condition of their own.
		result = append(result, resolvedStep{phase: phase, step: step, service: service, runtimeWhen: phaseRuntimeWhen})
	}
	return result, nil
}

// printDeployPlanTable prints the plan in human-readable table format.
func printDeployPlanTable(steps []resolvedStep, w *render.Writer) {
	lastPhaseKey := "" // "phase" or "service/phase" to detect transitions
	lastService := ""
	for _, rs := range steps {
		phaseKey := rs.phase.Name
		if rs.service != "" {
			phaseKey = rs.service + "/" + rs.phase.Name
		}

		if phaseKey != lastPhaseKey {
			// Show service header when entering a new service.
			if rs.service != "" && rs.service != lastService {
				w.TableSubheader("service: " + rs.service)
				lastService = rs.service
			}
			phaseLine := rs.phase.Name
			if rs.service != "" {
				phaseLine = rs.service + "/" + rs.phase.Name
			}
			if rs.phase.Description != "" {
				phaseLine += ": " + rs.phase.Description
			}
			if rs.phase.When != "" {
				phaseLine += " [when: " + rs.phase.When + "]"
			}
			indent := ""
			if rs.service != "" {
				indent = "  "
			}
			w.TableSubheader(indent + phaseLine)
			lastPhaseKey = phaseKey
		}

		indent := "  "
		detailIndent := "        "
		if rs.service != "" {
			indent = "    "
			detailIndent = "          "
		}

		badge := stepBadge(rs.step)
		name := rs.step.Name
		desc := rs.step.Description
		cmd := stepCommand(rs.step)

		if desc != "" {
			w.Definition(badge+" "+name, desc, len(indent), "", "—")
		} else {
			w.Println(indent + badge + " " + name)
		}
		if cmd != "" {
			w.Println(detailIndent + cmd)
		}
		// Show step-level when only when it differs from the phase condition
		// (phase condition is already shown in the phase header).
		if rs.runtimeWhen != "" && rs.runtimeWhen != rs.phase.When {
			w.Println(detailIndent + "[when: " + rs.runtimeWhen + "]")
		}
		if rs.step.Check != "" {
			w.Println(detailIndent + "[check: " + rs.step.Check + "]")
		}
	}
}

// printDeployPlanShell emits executable shell commands for each step to w.
// Prepends "set -e" so the pipeline aborts on any step failure.
// cmd: steps are emitted as-is; make: steps become "make <target>".
// After the implicit .env generation step, ". .env" is emitted so that
// variables exported by .env (PROJECT_PREFIX, PROJECT_NAME, etc.) are
// available to all subsequent steps in the generated script.
func printDeployPlanShell(steps []resolvedStep, w io.Writer) {
	_, _ = fmt.Fprintln(w, "set -e")
	lastService := ""
	lastPhaseKey := ""
	for _, rs := range steps {
		if rs.service != "" && rs.service != lastService {
			_, _ = fmt.Fprintf(w, "\n# === service: %s ===\n", rs.service)
			lastService = rs.service
		}
		phaseKey := rs.service + "/" + rs.phase.Name
		if phaseKey != lastPhaseKey {
			if rs.phase.When != "" {
				_, _ = fmt.Fprintf(w, "# phase %s [when: %s]\n", rs.phase.Name, rs.phase.When)
			}
			lastPhaseKey = phaseKey
		}
		if rs.runtimeWhen != "" && rs.runtimeWhen != rs.phase.When {
			_, _ = fmt.Fprintf(w, "# when: %s\n", rs.runtimeWhen)
		}
		_, _ = fmt.Fprintln(w, stepCommand(rs.step))
		if rs.step.Name == implicitEnvStep.Name {
			_, _ = fmt.Fprintln(w, ". .env")
		}
		if rs.step.Check != "" {
			_, _ = fmt.Fprintf(w, "# check: %s\n", rs.step.Check)
		}
	}
}

// newDeployRunCmd creates the `devbox deploy run` command.
// It executes the resolved deploy plan step by step, printing phase/step
// progress and success messages directly — without generating a shell script.
// Devbox status messages are teed to deploy.log. Child process output
// (docker, make) goes directly to os.Stdout/os.Stderr so TTY detection works.
func newDeployRunCmd(flags *rootFlags) *cobra.Command {
	var serviceName string

	cmd := &cobra.Command{
		Use:          "run",
		Short:        "Execute the deploy plan",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
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

			workDir := filepath.Dir(flags.configPath)
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
				}

				stepLabel := rs.stepAddress()
				if rs.step.Description != "" {
					stepLabel += ": " + rs.step.Description
				}
				w.Info(fmt.Sprintf("  [%d/%d] %s", i+1, totalSteps, stepLabel))

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

				if stepErr := execStep(rs.step, workDir, cfg, reg); stepErr != nil {
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
		if err := os.Setenv(strings.TrimSpace(key), strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("setenv %s: %w", key, err)
		}
	}
	return nil
}

// stepBadge returns the display badge for a step: [cmd], [command], or [config].
func stepBadge(step config.DeployStep) string {
	if step.Command != "" {
		return "[command]"
	}
	if step.ServiceConfigsCopy != "" {
		return "[config]"
	}
	return "[cmd]"
}

// stepCommand returns the resolved shell command for a step.
// For command: steps, it returns "devbox command run <id> [--set key=value...]".
// For service_configs_copy: steps, it returns the equivalent devbox deploy config command.
// For cmd: steps it returns the raw command.
func stepCommand(step config.DeployStep) string {
	if step.Command != "" {
		parts := []string{"./bin/devbox", "command", "run", strings.TrimSpace(step.Command)}
		// Sort With keys for deterministic output.
		if len(step.With) > 0 {
			keys := make([]string, 0, len(step.With))
			for k := range step.With {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				parts = append(parts, "--set", k+"="+step.With[k])
			}
		}
		return strings.Join(parts, " ")
	}
	if step.ServiceConfigsCopy != "" {
		mode := step.Mode
		if mode == "" {
			mode = "replace"
		}
		return "./bin/devbox deploy config " + strings.TrimSpace(step.ServiceConfigsCopy) + " --mode " + mode
	}
	return strings.TrimSpace(step.Cmd)
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
		baseDir := filepath.Dir(cfg.Raw["__configPath"].(string))
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

// ansiRe matches ANSI/VT100 escape sequences and bare carriage returns.
// Used to strip control codes before writing to a plain-text log file.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b[a-zA-Z]|\r`)

// ansiStripper wraps an io.Writer, stripping ANSI escape sequences and carriage
// returns before writing. The original byte-count is always returned so callers
// never see a short-write error.
type ansiStripper struct{ w io.Writer }

func (s *ansiStripper) Write(p []byte) (int, error) {
	_, err := s.w.Write(ansiRe.ReplaceAll(p, nil))
	return len(p), err
}

// execStep executes a deploy step in workDir.
// For cmd: steps it runs the command via sh -c.
// For command: steps it dispatches to the command runner via cfg and reg.
// For service_configs_copy: steps it runs the equivalent devbox CLI command.
// Output is attached to the current process stdin/stdout/stderr.
// workDir must be the project root so that relative paths in commands work correctly.
//
// Signal handling: the child inherits devbox's terminal foreground process group,
// so Ctrl+C (SIGINT) is delivered by the terminal directly to the entire group
// (devbox + shell + docker). devbox suppresses its own default SIGINT handler while
// waiting so it does not exit before the child finishes cleanup. A second Ctrl+C
// restores the default handler, allowing the user to force-exit if cleanup hangs.
func execStep(step config.DeployStep, workDir string, cfg *config.DevboxConfig, reg *commands.Registry) error {
	if step.Command != "" {
		return execCommandStep(step, workDir, cfg, reg)
	}

	var cmd *exec.Cmd
	switch {
	case step.ServiceConfigsCopy != "":
		cmd = exec.Command("sh", "-c", stepCommand(step))
	default:
		cmd = exec.Command("sh", "-c", strings.TrimSpace(step.Cmd))
	}
	cmd.Dir = workDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// No Setpgid: child stays in the terminal foreground process group so Ctrl+C
	// reaches child processes (make, shell, docker) directly from the terminal.

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	// Suppress devbox's own default SIGINT/SIGTERM handlers while the child runs.
	// The terminal already delivers the signal to the whole foreground process group;
	// shell traps inside Make recipes handle Docker resource cleanup.
	// After the first signal, restore defaults: a second Ctrl+C will force-exit devbox.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		if _, ok := <-sigCh; ok {
			signal.Reset(syscall.SIGINT, syscall.SIGTERM)
		}
	}()

	waitErr := cmd.Wait()
	signal.Stop(sigCh)
	close(sigCh)

	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return fmt.Errorf("exit status %d", exitErr.ExitCode())
		}
		return waitErr
	}
	return nil
}

// execCommandStep executes a deploy step with a command: reference via the command runner.
// It looks up the command ID in the registry, resolves params (merging step.With overrides),
// resolves context, and dispatches to the appropriate runner. Output goes directly to
// os.Stdout/os.Stderr so TTY detection works correctly.
func execCommandStep(step config.DeployStep, workDir string, cfg *config.DevboxConfig, reg *commands.Registry) error {
	if reg == nil {
		return fmt.Errorf("command registry not available for step %q (command: %s)", step.Name, step.Command)
	}
	def, err := reg.Get(step.Command)
	if err != nil {
		return fmt.Errorf("step %q: %w", step.Name, err)
	}
	params, err := commands.ResolveParams(def.Params, step.With, cfg)
	if err != nil {
		return fmt.Errorf("step %q: resolving params: %w", step.Name, err)
	}
	ctx, err := commands.ResolveContext(def.Context, cfg)
	if err != nil {
		return fmt.Errorf("step %q: resolving context: %w", step.Name, err)
	}
	rctx := &tpl.RenderContext{
		Raw:     cfg.Raw,
		Params:  params,
		Context: ctx,
		Host:    tpl.CurrentHostInfo(),
	}
	runner, err := commands.NewRunner(def)
	if err != nil {
		return fmt.Errorf("step %q: %w", step.Name, err)
	}
	return runner.Run(commands.RunContext{
		Cmd:         def,
		Params:      params,
		Context:     ctx,
		Render:      rctx,
		Config:      cfg,
		Registry:    reg,
		ProjectRoot: workDir,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
	})
}

func newDeployStepCmd(flags *rootFlags) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "step <phase>/<step>",
		Short: "Run a single deploy step by <phase>/<step> address",
		Args:  cobra.ExactArgs(1),
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
			if err := execStep(step, workDir, cfg, reg); err != nil {
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
// It reads services.<service>.configs from devbox.yml (list of filenames) and copies
// each file from configs/services/<service>/<file> to services/<service>/configs/<file>
// using the given mode (default: replace). Dest paths are validated against the service
// configs directory to prevent path traversal.
func newDeployConfigCmd(flags *rootFlags) *cobra.Command {
	var mode string

	cmd := &cobra.Command{
		Use:   "config <service>",
		Short: "Copy template configs to service directory",
		Args:  cobra.ExactArgs(1),
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
			if svc.Dir == "" {
				return fmt.Errorf("service %q: dir is not set", serviceName)
			}

			projectRoot := filepath.Dir(flags.configPath)
			// Source: configs/services/<service>/
			srcDir := filepath.Join(projectRoot, "configs", "services", serviceName)
			// Dest: services/<service>/configs/
			destDir := filepath.Join(projectRoot, svc.Dir, "configs")

			w := render.Stdout()
			svcDir := filepath.Join(projectRoot, svc.Dir)
			for _, entry := range svc.Configs {
				src := filepath.Join(srcDir, entry.File)
				dest := filepath.Join(destDir, entry.File)
				// Guard against path traversal: dest must remain inside destDir.
				cleanDestDir := filepath.Clean(destDir)
				cleanDest := filepath.Clean(dest)
				if cleanDest == cleanDestDir || !strings.HasPrefix(cleanDest, cleanDestDir+string(filepath.Separator)) {
					return fmt.Errorf("service %q: config %q escapes the configs directory", serviceName, entry.File)
				}
				if err := copyConfigFile(src, dest, mode); err != nil {
					return fmt.Errorf("copying %s → %s: %w", src, dest, err)
				}
				w.Success(fmt.Sprintf("config %s → %s [%s]", src, dest, mode))

				// If a mountpoint is declared, ensure the file exists at that path
				// (relative to the service dir) so Docker Desktop virtiofs can create
				// a nested file bind mount over it. Touch only — content comes from
				// the bind mount at runtime.
				if entry.Mountpoint != "" {
					mp := filepath.Join(svcDir, entry.Mountpoint)
					if err := touchFile(mp); err != nil {
						return fmt.Errorf("creating mountpoint %s: %w", mp, err)
					}
					w.Success(fmt.Sprintf("mountpoint %s [touched]", mp))
				}
			}
			return nil
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&mode, "mode", "replace", "copy mode: default, update, or replace")
	return cmd
}

// copyConfigFile copies src to dest using the given mode:
//   - "default" — skip if dest already exists
//   - "replace" — overwrite unconditionally
//   - "update"  — merge new keys from src into dest without overwriting existing values
//
// The dest directory is created if it does not exist.
func copyConfigFile(src, dest, mode string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}

	srcData, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read source %s: %w", src, err)
	}

	switch mode {
	case "default":
		if _, err := os.Stat(dest); err == nil {
			// Destination exists — skip.
			return nil
		}
		return os.WriteFile(dest, srcData, 0o644)

	case "replace":
		return os.WriteFile(dest, srcData, 0o644)

	case "update":
		return updateEnvFile(srcData, dest)

	default:
		// Treat unknown mode as "default".
		if _, err := os.Stat(dest); err == nil {
			return nil
		}
		return os.WriteFile(dest, srcData, 0o644)
	}
}

// updateEnvFile merges new KEY=VALUE entries from srcData into the dest file.
// Keys already present in dest are preserved unchanged. New keys from the
// source template are appended to dest. If dest does not exist it is created
// with the full content of srcData.
func updateEnvFile(srcData []byte, dest string) error {
	destData, err := os.ReadFile(dest)
	if errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(dest, srcData, 0o644)
	}
	if err != nil {
		return fmt.Errorf("read dest %s: %w", dest, err)
	}

	// Parse existing dest keys.
	existingKeys := parseEnvKeys(destData)

	// Build lines to append: src keys not already in dest.
	var additions []string
	scanner := bufio.NewScanner(strings.NewReader(string(srcData)))
	for scanner.Scan() {
		line := scanner.Text()
		key := envLineKey(line)
		if key == "" {
			// Comment or blank — skip (do not copy comments from template to existing file).
			continue
		}
		if !existingKeys[key] {
			additions = append(additions, line)
		}
	}

	if len(additions) == 0 {
		return nil
	}

	// Append new keys to dest, preceded by a blank line separator if the
	// dest does not already end with a newline.
	f, err := os.OpenFile(dest, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open dest for append: %w", err)
	}

	var writeErr error
	// Ensure a trailing newline before appending.
	if len(destData) > 0 && destData[len(destData)-1] != '\n' {
		_, writeErr = f.WriteString("\n")
	}
	for _, line := range additions {
		if writeErr != nil {
			break
		}
		_, writeErr = f.WriteString(line + "\n")
	}

	if closeErr := f.Close(); closeErr != nil && writeErr == nil {
		return closeErr
	}
	return writeErr
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

// touchFile creates an empty file at path (and its parent directories) if it does
// not already exist. If it exists it is left unchanged.
func touchFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return nil // already exists — nothing to do
	}
	if err != nil {
		return err
	}
	return f.Close()
}

// parseEnvKeys returns a set of KEY names found in an .env file content.
func parseEnvKeys(data []byte) map[string]bool {
	keys := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		if key := envLineKey(scanner.Text()); key != "" {
			keys[key] = true
		}
	}
	return keys
}

// envLineKey returns the KEY part of a "KEY=VALUE" env line.
// Returns "" for blank lines and comment lines (starting with #).
func envLineKey(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	key, _, _ := strings.Cut(line, "=")
	return strings.TrimSpace(key)
}
