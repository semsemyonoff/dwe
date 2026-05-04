package pipeline

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"devbox-cli/internal/builtin"
	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/usercommands"
)

// buildDevboxCmd constructs an exec.Cmd for a devbox: pipeline step.
//
// It sets CLICOLOR_FORCE=1 in the child environment so that lipgloss enables
// colors even when stdout is wrapped in an io.MultiWriter (which the child sees
// as a pipe rather than a TTY). The log tee via ansiStripper is unaffected.
// When skipConfirm is true, DEVBOX_NONINTERACTIVE=1 is added so that nested
// devbox subcommands also skip confirmation prompts.
func buildDevboxCmd(devboxArg, workDir, shell, devboxBin string, skipConfirm bool) *exec.Cmd {
	bin, err := os.Executable()
	if err != nil {
		bin = devboxBin
	}
	cmd := exec.Command(shell, "-c", bin+" "+strings.TrimSpace(devboxArg)) //nolint:gosec
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "CLICOLOR_FORCE=1")
	if skipConfirm {
		cmd.Env = append(cmd.Env, "DEVBOX_NONINTERACTIVE=1")
	}
	return cmd
}

// ExecStep executes a pipeline step in workDir.
// Dispatches to the appropriate handler based on step type:
//   - builtin: — execBuiltinStep
//   - command: — execCommandStep
//   - devbox:  — runs ./devbox <args> via sh
//   - run:     — runs shell command via sh
//
// Signal handling: the child inherits devbox's terminal foreground process group,
// so Ctrl+C is delivered by the terminal to the entire group. devbox suppresses
// its own SIGINT handler while waiting so it does not exit before the child finishes.
func ExecStep(step config.DeployStep, workDir string, cfg *config.DevboxConfig, reg *usercommands.Registry, logWriter io.Writer, skipConfirm bool) error {
	if step.Builtin != "" {
		return execBuiltinStep(step, workDir, cfg, logWriter, skipConfirm)
	}
	if step.Command != "" {
		return execCommandStep(step, workDir, cfg, reg, logWriter, skipConfirm)
	}

	shell := config.ShellBin(cfg)
	var cmd *exec.Cmd
	if step.Devbox != "" {
		cmd = buildDevboxCmd(step.Devbox, workDir, shell, config.DevboxBin(cfg), skipConfirm)
	} else {
		cmd = exec.Command(shell, "-c", strings.TrimSpace(step.Run)) //nolint:gosec
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
		if exitErr, ok := errors.AsType[*exec.ExitError](waitErr); ok {
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
		Stdin:       os.Stdin,
		SkipConfirm: skipConfirm,
	}
	return builtin.Run(step.Builtin, step.With, ctx)
}

// execCommandStep executes a command: pipeline step via the command runner.
func execCommandStep(step config.DeployStep, workDir string, cfg *config.DevboxConfig, reg *usercommands.Registry, logWriter io.Writer, skipConfirm bool) error {
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
	params, err := usercommands.ResolveParams(def.Params, strWith, cfg)
	if err != nil {
		return fmt.Errorf("step %q: resolving params: %w", step.Name, err)
	}
	ctx, err := usercommands.ResolveContext(def.Context, cfg)
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
	stdout := io.Writer(os.Stdout)
	stderr := io.Writer(os.Stderr)
	if logWriter != nil {
		logStripped := &ansiStripper{logWriter}
		stdout = io.MultiWriter(os.Stdout, logStripped)
		stderr = io.MultiWriter(os.Stderr, logStripped)
	}
	if err := usercommands.RunCommand(usercommands.RunContext{
		Cmd:            def,
		Params:         params,
		Context:        ctx,
		Render:         rctx,
		Config:         cfg,
		DockerConfig:   dockerCfg,
		Registry:       reg,
		ProjectRoot:    workDir,
		Stdout:         stdout,
		Stderr:         stderr,
		Stdin:          os.Stdin,
		SkipConfirm:    skipConfirm,
		NonInteractive: skipConfirm,
	}); err != nil {
		return fmt.Errorf("step %q: %w", step.Name, err)
	}
	return nil
}

// Run executes a resolved step list, calling rep for all lifecycle events.
//
// name is a human-readable label passed to rep.StartPipeline (e.g. "deploy", "reset").
// postStepHooks maps step names to callbacks invoked after successful execution
// (before the check condition) — used e.g. to source .env after render-env.
//
// Returns ErrSilent when any step fails (rep.FailStep has already been called).
// Returns other errors for config/condition evaluation failures.
func Run(
	steps []ResolvedStep,
	rep Reporter,
	name string,
	cfg *config.DevboxConfig,
	reg *usercommands.Registry,
	workDir string,
	logWriter io.Writer,
	skipConfirm bool,
	postStepHooks map[string]func() error,
) error {
	// trackedTotal excludes steps belonging to phases with Untracked=true.
	// These steps receive index=0, total=0 in reporter calls so PlainReporter
	// can suppress output for them.
	trackedTotal := 0
	for _, rs := range steps {
		if !rs.Phase.Untracked {
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
		phaseKey := rs.Phase.Name
		if rs.Service != "" {
			phaseKey = rs.Service + "/" + rs.Phase.Name
		}

		if phaseKey != lastPhaseKey {
			rep.EnterPhase(phaseKey, rs.Phase)
			lastPhaseKey = phaseKey
			phaseSkipped = false
			phaseWhen = ""

			if rs.PhaseWhen != "" {
				ok, err := condition.EvalRuntime(rs.PhaseWhen, workDir)
				if err != nil {
					return fmt.Errorf("evaluating when condition for phase %s: %w", phaseKey, err)
				}
				if !ok {
					phaseSkipped = true
					phaseWhen = rs.PhaseWhen
					rep.SkipPhase(phaseKey, rs.Phase, "when: "+phaseWhen)
				}
			}
		}

		addr := rs.StepAddress()

		// Determine the index/total to pass to reporter calls for this step.
		// Untracked phase steps always receive 0/0 so reporters can identify them.
		stepIndex, stepTotal := 0, 0
		if !rs.Phase.Untracked {
			trackedIndex++
			stepIndex, stepTotal = trackedIndex, trackedTotal
		}

		// Phase-level when condition was false — skip all steps in this phase.
		if phaseSkipped {
			rep.StartStep(addr, rs.Step, stepIndex, stepTotal)
			rep.SkipStep(addr, rs.Step, stepIndex, stepTotal, "phase when: "+phaseWhen)
			continue
		}

		// Step-level runtime when condition.
		if rs.RuntimeWhen != "" {
			ok, err := condition.EvalRuntime(rs.RuntimeWhen, workDir)
			if err != nil {
				return fmt.Errorf("evaluating when condition for %s: %w", addr, err)
			}
			if !ok {
				rep.StartStep(addr, rs.Step, stepIndex, stepTotal)
				rep.SkipStep(addr, rs.Step, stepIndex, stepTotal, "when: "+rs.RuntimeWhen)
				continue
			}
		}

		rep.StartStep(addr, rs.Step, stepIndex, stepTotal)
		rep.SuspendForExec()
		stepErr := ExecStep(rs.Step, workDir, cfg, reg, logWriter, skipConfirm)
		rep.ResumeAfterExec()

		if stepErr != nil {
			rep.FailStep(addr, rs.Step, stepIndex, stepTotal, stepErr)
			if rs.Step.ContinueOnError {
				// Step failed but is marked continue_on_error: report the failure
				// and proceed to the next step. Post-step hook and Check are skipped.
				continue
			}
			return ErrSilent
		}

		// Run post-step hook if registered (e.g. source .env after render-env).
		if hook, ok := postStepHooks[rs.Step.Name]; ok {
			if err := hook(); err != nil {
				return err
			}
		}

		// Evaluate check condition after successful execution.
		if rs.Step.Check != "" {
			ok, err := condition.EvalRuntime(rs.Step.Check, workDir)
			if err != nil {
				rep.FailStep(addr, rs.Step, stepIndex, stepTotal, fmt.Errorf("check error: %w", err))
				return ErrSilent
			}
			if !ok {
				rep.FailStep(addr, rs.Step, stepIndex, stepTotal, fmt.Errorf("check did not pass (%s)", rs.Step.Check))
				return ErrSilent
			}
		}

		rep.FinishStep(addr, rs.Step, stepIndex, stepTotal)
	}

	success = true
	return nil
}
