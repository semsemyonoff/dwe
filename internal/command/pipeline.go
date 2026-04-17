package command

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"devbox-cli/internal/builtin"
	"devbox-cli/internal/commands"
	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	pipeline "devbox-cli/internal/pipeline"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/ui"
)

// resolvedStep holds a pipeline step together with the phase it belongs to,
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
	runtimeWhen string // step-level runtime when condition (step.When); empty otherwise
	phaseWhen   string // phase-level runtime when condition; evaluated once per phase, not per step
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

// resolvePhaseSteps resolves steps for a single phase, evaluating when conditions.
// service is empty for orchestrator phases.
//
// Phase-level when is evaluated first:
//   - Go template: evaluated at plan time; entire phase is excluded when false.
//   - Runtime condition: propagated to each step that does not already carry its
//     own runtime when condition. The phase condition is stored in runtimeWhen and
//     evaluated before each such step at execution time.
func resolvePhaseSteps(cfg *config.DevboxConfig, phase config.DeployPhase, service string) ([]resolvedStep, error) {
	phaseRuntimeWhen := ""
	if phase.When != "" {
		if condition.IsRuntime(phase.When) {
			phaseRuntimeWhen = phase.When
		} else {
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
				if step.Builtin != "" {
					if err := builtin.Validate(step.Builtin, step.With); err != nil {
						prefix := phase.Name + "/" + step.Name
						if service != "" {
							prefix = service + "/" + prefix
						}
						return nil, fmt.Errorf("step %s: invalid builtin: %w", prefix, err)
					}
				}
				result = append(result, resolvedStep{phase: phase, step: step, service: service, runtimeWhen: step.When, phaseWhen: phaseRuntimeWhen})
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
		if step.Builtin != "" {
			if err := builtin.Validate(step.Builtin, step.With); err != nil {
				prefix := phase.Name + "/" + step.Name
				if service != "" {
					prefix = service + "/" + prefix
				}
				return nil, fmt.Errorf("step %s: invalid builtin: %w", prefix, err)
			}
		}
		result = append(result, resolvedStep{phase: phase, step: step, service: service, phaseWhen: phaseRuntimeWhen})
	}
	return result, nil
}

// stepBadge returns the display badge for a step based on its type.
func stepBadge(step config.DeployStep) string {
	switch {
	case step.Command != "":
		return "[command]"
	case step.Builtin != "":
		return "[builtin]"
	case step.Devbox != "":
		return "[devbox]"
	default:
		return "[run]"
	}
}

// stepCommand returns the resolved command or action string for plan display.
//   - command: steps — "devbox commands run <id> [--set key=value...]"
//   - builtin: steps — builtin description from registry (e.g. "builtin: confirm(...)")
//   - devbox: steps  — "./bin/devbox <args>"
//   - run: steps     — raw shell command
func stepCommand(step config.DeployStep) string {
	switch {
	case step.Command != "":
		parts := []string{"./bin/devbox", "commands", "run", strings.TrimSpace(step.Command)}
		if len(step.With) > 0 {
			keys := make([]string, 0, len(step.With))
			for k := range step.With {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				parts = append(parts, "--set", k+"="+fmt.Sprintf("%v", step.With[k]))
			}
		}
		return strings.Join(parts, " ")
	case step.Builtin != "":
		return builtin.Describe(step.Builtin, step.With)
	case step.Devbox != "":
		return "./bin/devbox " + strings.TrimSpace(step.Devbox)
	default:
		return strings.TrimSpace(step.Run)
	}
}

// printDeployPlanTable prints the plan in human-readable table format.
func printDeployPlanTable(steps []resolvedStep, w *render.Writer) {
	out := w.Writer()
	lastPhaseKey := ""
	lastService := ""
	for _, rs := range steps {
		phaseKey := rs.phase.Name
		if rs.service != "" {
			phaseKey = rs.service + "/" + rs.phase.Name
		}

		if phaseKey != lastPhaseKey {
			if rs.service != "" && rs.service != lastService {
				_, _ = fmt.Fprintln(out, ui.RenderSubheader("service: "+rs.service))
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
			_, _ = fmt.Fprintln(out, ui.RenderSubheader(indent+phaseLine))
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
			_, _ = fmt.Fprintln(out, ui.RenderDefinition(badge+" "+name, desc, len(indent), ""))
		} else {
			_, _ = fmt.Fprintln(out, indent+badge+" "+name)
		}
		if cmd != "" {
			_, _ = fmt.Fprintln(out, detailIndent+cmd)
		}
		if rs.runtimeWhen != "" {
			_, _ = fmt.Fprintln(out, detailIndent+"[when: "+rs.runtimeWhen+"]")
		}
		if rs.step.Check != "" {
			_, _ = fmt.Fprintln(out, detailIndent+"[check: "+rs.step.Check+"]")
		}
	}
}

// printDeployPlanShell emits executable shell commands for each step.
// Prepends "set -e" so the pipeline aborts on any step failure.
// After the implicit .env generation step, ". .env" is emitted so variables
// are available to all subsequent steps in the generated script.
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
		if rs.runtimeWhen != "" {
			_, _ = fmt.Fprintf(w, "# when: %s\n", rs.runtimeWhen)
		}
		if rs.step.Builtin != "" {
			// Builtins are in-process Go; delegate to the CLI step runner so the
			// generated script remains executable and behaviorally equivalent.
			_, _ = fmt.Fprintf(w, "./bin/devbox deploy step %s\n", rs.stepAddress())
		} else {
			_, _ = fmt.Fprintln(w, stepCommand(rs.step))
		}
		if rs.step.Name == implicitEnvStep.Name {
			_, _ = fmt.Fprintln(w, ". .env")
		}
		if rs.step.Check != "" {
			_, _ = fmt.Fprintf(w, "# check: %s\n", rs.step.Check)
		}
	}
}

// ansiRe matches ANSI/VT100 escape sequences and bare carriage returns.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b[a-zA-Z]|\r`)

// ansiStripper wraps an io.Writer, stripping ANSI escape sequences before writing.
type ansiStripper struct{ w io.Writer }

func (s *ansiStripper) Write(p []byte) (int, error) {
	stripped := ansiRe.ReplaceAll(p, nil)
	if _, err := s.w.Write(stripped); err != nil {
		return 0, err
	}
	return len(p), nil
}

// buildDevboxCmd constructs an exec.Cmd for a devbox: pipeline step.
//
// It sets CLICOLOR_FORCE=1 in the child environment so that lipgloss enables
// colors even when stdout is wrapped in an io.MultiWriter (which the child sees
// as a pipe rather than a TTY). The log tee via ansiStripper is unaffected.
func buildDevboxCmd(devboxArg, workDir string) *exec.Cmd {
	bin, err := os.Executable()
	if err != nil {
		bin = "./bin/devbox"
	}
	cmd := exec.Command("sh", "-c", bin+" "+strings.TrimSpace(devboxArg)) //nolint:gosec
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "CLICOLOR_FORCE=1")
	return cmd
}

// execStep executes a pipeline step in workDir.
// Dispatches to the appropriate handler based on step type:
//   - builtin: — execBuiltinStep
//   - command: — execCommandStep
//   - devbox:  — runs ./devbox <args> via sh
//   - run:     — runs shell command via sh
//
// Signal handling: the child inherits devbox's terminal foreground process group,
// so Ctrl+C is delivered by the terminal to the entire group. devbox suppresses
// its own SIGINT handler while waiting so it does not exit before the child finishes.
func execStep(step config.DeployStep, workDir string, cfg *config.DevboxConfig, reg *commands.Registry, logWriter io.Writer, skipConfirm bool) error {
	if step.Builtin != "" {
		return execBuiltinStep(step, workDir, cfg, logWriter, skipConfirm)
	}
	if step.Command != "" {
		return execCommandStep(step, workDir, cfg, reg, logWriter)
	}

	var cmd *exec.Cmd
	if step.Devbox != "" {
		cmd = buildDevboxCmd(step.Devbox, workDir)
	} else {
		cmd = exec.Command("sh", "-c", strings.TrimSpace(step.Run)) //nolint:gosec
		cmd.Dir = workDir
	}
	cmd.Stdin = os.Stdin
	if logWriter != nil {
		logStripped := &ansiStripper{logWriter}
		cmd.Stdout = io.MultiWriter(os.Stdout, logStripped)
		cmd.Stderr = io.MultiWriter(os.Stderr, logStripped)
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

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

// execBuiltinStep executes a builtin: pipeline step directly in Go.
// Validates builtin params before running so that single-step execution
// (devbox deploy step / devbox reset step) enforces the same contract as
// full-pipeline plan resolution.
func execBuiltinStep(step config.DeployStep, workDir string, cfg *config.DevboxConfig, logWriter io.Writer, skipConfirm bool) error {
	if err := builtin.Validate(step.Builtin, step.With); err != nil {
		return fmt.Errorf("invalid builtin %q: %w", step.Builtin, err)
	}
	var out io.Writer = os.Stdout
	if logWriter != nil {
		out = io.MultiWriter(os.Stdout, &ansiStripper{logWriter})
	}
	ctx := builtin.ExecContext{
		Config:      cfg,
		ProjectRoot: workDir,
		Output:      render.NewWriter(out),
		LogWriter:   logWriter,
		SkipConfirm: skipConfirm,
	}
	return builtin.Run(step.Builtin, step.With, ctx)
}

// runPipeline executes a resolved step list, calling rep for all lifecycle events.
//
// name is a human-readable label passed to rep.StartPipeline (e.g. "deploy", "reset").
// postStepHooks maps step names to callbacks invoked after successful execution
// (before the check condition) — used e.g. to source .env after render-env.
//
// Returns ErrSilent when any step fails (rep.FailStep has already been called).
// Returns other errors for config/condition evaluation failures.
func runPipeline(
	steps []resolvedStep,
	rep pipeline.Reporter,
	name string,
	cfg *config.DevboxConfig,
	reg *commands.Registry,
	workDir string,
	logWriter io.Writer,
	skipConfirm bool,
	postStepHooks map[string]func() error,
) error {
	// trackedTotal excludes steps belonging to phases with Untracked=true.
	// These steps receive index=0, total=0 in reporter calls so the TUI can
	// distinguish them from counted steps and exclude them from the progress bar.
	trackedTotal := 0
	for _, rs := range steps {
		if !rs.phase.Untracked {
			trackedTotal++
		}
	}

	rep.StartPipeline(name, trackedTotal)

	success := false
	defer func() { rep.FinishPipeline(success) }()

	lastPhaseKey := ""
	phaseSkipped := false
	phaseWhen := ""
	trackedIndex := 0

	for _, rs := range steps {
		phaseKey := rs.phase.Name
		if rs.service != "" {
			phaseKey = rs.service + "/" + rs.phase.Name
		}

		if phaseKey != lastPhaseKey {
			rep.EnterPhase(phaseKey, rs.phase)
			lastPhaseKey = phaseKey
			phaseSkipped = false
			phaseWhen = ""

			if rs.phaseWhen != "" {
				ok, err := condition.EvalRuntime(rs.phaseWhen, workDir)
				if err != nil {
					return fmt.Errorf("evaluating when condition for phase %s: %w", phaseKey, err)
				}
				if !ok {
					phaseSkipped = true
					phaseWhen = rs.phaseWhen
					rep.SkipPhase(phaseKey, rs.phase, "when: "+phaseWhen)
				}
			}
		}

		addr := rs.stepAddress()

		// Determine the index/total to pass to reporter calls for this step.
		// Untracked phase steps always receive 0/0 so reporters can identify them.
		stepIndex, stepTotal := 0, 0
		if !rs.phase.Untracked {
			trackedIndex++
			stepIndex, stepTotal = trackedIndex, trackedTotal
		}

		// Phase-level when condition was false — skip all steps in this phase.
		if phaseSkipped {
			rep.StartStep(addr, rs.step, stepIndex, stepTotal)
			rep.SkipStep(addr, rs.step, stepIndex, stepTotal, "phase when: "+phaseWhen)
			continue
		}

		// Step-level runtime when condition.
		if rs.runtimeWhen != "" {
			ok, err := condition.EvalRuntime(rs.runtimeWhen, workDir)
			if err != nil {
				return fmt.Errorf("evaluating when condition for %s: %w", addr, err)
			}
			if !ok {
				rep.StartStep(addr, rs.step, stepIndex, stepTotal)
				rep.SkipStep(addr, rs.step, stepIndex, stepTotal, "when: "+rs.runtimeWhen)
				continue
			}
		}

		rep.StartStep(addr, rs.step, stepIndex, stepTotal)
		rep.SuspendForExec()
		stepErr := execStep(rs.step, workDir, cfg, reg, logWriter, skipConfirm)
		rep.ResumeAfterExec()

		if stepErr != nil {
			rep.FailStep(addr, rs.step, stepIndex, stepTotal, stepErr)
			return ErrSilent
		}

		// Run post-step hook if registered (e.g. source .env after render-env).
		if hook, ok := postStepHooks[rs.step.Name]; ok {
			if err := hook(); err != nil {
				return err
			}
		}

		// Evaluate check condition after successful execution.
		if rs.step.Check != "" {
			ok, err := condition.EvalRuntime(rs.step.Check, workDir)
			if err != nil {
				rep.FailStep(addr, rs.step, stepIndex, stepTotal, fmt.Errorf("check error: %w", err))
				return ErrSilent
			}
			if !ok {
				rep.FailStep(addr, rs.step, stepIndex, stepTotal, fmt.Errorf("check did not pass (%s)", rs.step.Check))
				return ErrSilent
			}
		}

		rep.FinishStep(addr, rs.step, stepIndex, stepTotal)
	}

	success = true
	return nil
}

// execCommandStep executes a command: pipeline step via the command runner.
func execCommandStep(step config.DeployStep, workDir string, cfg *config.DevboxConfig, reg *commands.Registry, logWriter io.Writer) error {
	if reg == nil {
		return fmt.Errorf("command registry not available for step %q (command: %s)", step.Name, step.Command)
	}
	def, err := reg.Get(step.Command)
	if err != nil {
		return fmt.Errorf("step %q: %w", step.Name, err)
	}
	// Convert With map[string]any → map[string]string for command param resolution.
	strWith := make(map[string]string, len(step.With))
	for k, v := range step.With {
		strWith[k] = fmt.Sprintf("%v", v)
	}
	params, err := commands.ResolveParams(def.Params, strWith, cfg)
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
	dockerCfg, err := config.LoadDockerConfig(workDir, cfg)
	if err != nil {
		return fmt.Errorf("step %q: loading docker config: %w", step.Name, err)
	}
	runner, err := commands.NewRunner(def)
	if err != nil {
		return fmt.Errorf("step %q: %w", step.Name, err)
	}
	stdout := io.Writer(os.Stdout)
	stderr := io.Writer(os.Stderr)
	if logWriter != nil {
		logStripped := &ansiStripper{logWriter}
		stdout = io.MultiWriter(os.Stdout, logStripped)
		stderr = io.MultiWriter(os.Stderr, logStripped)
	}
	return runner.Run(commands.RunContext{
		Cmd:          def,
		Params:       params,
		Context:      ctx,
		Render:       rctx,
		Config:       cfg,
		DockerConfig: dockerCfg,
		Registry:     reg,
		ProjectRoot:  workDir,
		Stdout:       stdout,
		Stderr:       stderr,
	})
}
