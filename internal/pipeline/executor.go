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

// ActionContext carries the inputs needed by ExecAction.
// It is constructed once per step by Run and reused for both body and check.
type ActionContext struct {
	WorkDir     string
	Cfg         *config.DevboxConfig
	Reg         *usercommands.Registry
	LogWriter   io.Writer
	SkipConfirm bool
}

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
	cmd := exec.Command(shell, "-c", shellQuote(bin)+" "+strings.TrimSpace(devboxArg)) //nolint:gosec
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "CLICOLOR_FORCE=1")
	if skipConfirm {
		cmd.Env = append(cmd.Env, "DEVBOX_NONINTERACTIVE=1")
	}
	return cmd
}

// ExecAction executes a typed action (used in step bodies and checks).
// It dispatches based on action type: shell, devbox, command, or builtin.
// Does NOT handle reporter calls, when evaluation, hooks, or check orchestration —
// those stay in Run.
func ExecAction(a config.Action, actx ActionContext) error {
	switch a.Type {
	case "builtin":
		return execBuiltinAction(a, actx)
	case "command":
		return execCommandAction(a, actx)
	case "devbox":
		return execDevboxAction(a, actx)
	case "shell":
		return execShellAction(a, actx)
	default:
		return fmt.Errorf("unknown action type %q", a.Type)
	}
}

// execShellAction runs a shell command via sh -c.
func execShellAction(a config.Action, actx ActionContext) error {
	shell := config.ShellBin(actx.Cfg)
	cmd := exec.Command(shell, "-c", strings.TrimSpace(a.Cmd)) //nolint:gosec
	cmd.Dir = actx.WorkDir
	cmd.Stdin = os.Stdin
	if actx.LogWriter != nil {
		logStripped := &ansiStripper{actx.LogWriter}
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
		if sig, ok := <-sigCh; ok {
			signal.Stop(sigCh)
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
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

// execDevboxAction runs a devbox subcommand.
func execDevboxAction(a config.Action, actx ActionContext) error {
	shell := config.ShellBin(actx.Cfg)
	cmd := buildDevboxCmd(a.Cmd, actx.WorkDir, shell, config.DevboxBin(actx.Cfg), actx.SkipConfirm)
	cmd.Stdin = os.Stdin
	if actx.LogWriter != nil {
		logStripped := &ansiStripper{actx.LogWriter}
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
		if sig, ok := <-sigCh; ok {
			signal.Stop(sigCh)
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
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

// execBuiltinAction executes a builtin action.
func execBuiltinAction(a config.Action, actx ActionContext) error {
	if err := builtin.Validate(a.Cmd, a.With); err != nil {
		return fmt.Errorf("invalid builtin %q: %w", a.Cmd, err)
	}
	var out io.Writer = os.Stdout
	if actx.LogWriter != nil {
		out = io.MultiWriter(os.Stdout, &ansiStripper{actx.LogWriter})
	}
	ctx := builtin.ExecContext{
		Config:      actx.Cfg,
		ProjectRoot: actx.WorkDir,
		Output:      render.NewWriter(out),
		LogWriter:   actx.LogWriter,
		Stdin:       os.Stdin,
		SkipConfirm: actx.SkipConfirm,
	}
	return builtin.Run(a.Cmd, a.With, ctx)
}

// execCommandAction executes a registered user command.
func execCommandAction(a config.Action, actx ActionContext) error {
	if actx.Reg == nil {
		return fmt.Errorf("command registry not available for command %q", a.Cmd)
	}
	def, err := actx.Reg.Get(a.Cmd)
	if err != nil {
		return fmt.Errorf("command %q: %w", a.Cmd, err)
	}
	// Convert With map[string]any → map[string]string for command param resolution.
	strWith := make(map[string]string, len(a.With))
	for k, v := range a.With {
		strWith[k] = fmt.Sprintf("%v", v)
	}
	params, err := usercommands.ResolveParams(def.Params, strWith, actx.Cfg)
	if err != nil {
		return fmt.Errorf("resolving params: %w", err)
	}
	ctx, err := usercommands.ResolveContext(def.Context, actx.Cfg)
	if err != nil {
		return fmt.Errorf("resolving context: %w", err)
	}
	rctx := &tpl.RenderContext{
		Raw:     actx.Cfg.Raw,
		Params:  params,
		Context: ctx,
		Host:    tpl.CurrentHostInfo(),
	}
	dockerCfg, err := config.LoadDockerConfig(actx.WorkDir, actx.Cfg)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("loading docker config: %w", err)
		}
		dockerCfg = &config.DockerConfig{}
	}
	stdout := io.Writer(os.Stdout)
	stderr := io.Writer(os.Stderr)
	if actx.LogWriter != nil {
		logStripped := &ansiStripper{actx.LogWriter}
		stdout = io.MultiWriter(os.Stdout, logStripped)
		stderr = io.MultiWriter(os.Stderr, logStripped)
	}
	return usercommands.RunCommand(usercommands.RunContext{
		Cmd:            def,
		Params:         params,
		Context:        ctx,
		Render:         rctx,
		Config:         actx.Cfg,
		DockerConfig:   dockerCfg,
		Registry:       actx.Reg,
		ProjectRoot:    actx.WorkDir,
		Stdout:         stdout,
		Stderr:         stderr,
		Stdin:          os.Stdin,
		SkipConfirm:    actx.SkipConfirm,
		NonInteractive: actx.SkipConfirm,
	})
}

// ExecStep is a deprecated wrapper for backward compatibility.
// New code should use ExecAction directly.
func ExecStep(step config.DeployStep, workDir string, cfg *config.DevboxConfig, reg *usercommands.Registry, logWriter io.Writer, skipConfirm bool) error {
	actx := ActionContext{
		WorkDir:     workDir,
		Cfg:         cfg,
		Reg:         reg,
		LogWriter:   logWriter,
		SkipConfirm: skipConfirm,
	}
	return ExecAction(step.Action(), actx)
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
	phaseWhenMsg := ""
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
			phaseWhenMsg = ""

			if rs.PhaseWhen != nil {
				ok, err := condition.EvalRuntimeTyped(rs.PhaseWhen, workDir)
				if err != nil {
					return fmt.Errorf("evaluating when condition for phase %s: %w", phaseKey, err)
				}
				if !ok {
					phaseSkipped = true
					phaseWhenMsg = FormatCondition(rs.PhaseWhen)
					rep.SkipPhase(phaseKey, rs.Phase, "when: "+phaseWhenMsg)
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
			rep.SkipStep(addr, rs.Step, stepIndex, stepTotal, "phase when: "+phaseWhenMsg)
			continue
		}

		// Step-level runtime when condition.
		if rs.RuntimeWhen != nil {
			ok, err := condition.EvalRuntimeTyped(rs.RuntimeWhen, workDir)
			if err != nil {
				return fmt.Errorf("evaluating when condition for %s: %w", addr, err)
			}
			if !ok {
				rep.StartStep(addr, rs.Step, stepIndex, stepTotal)
				rep.SkipStep(addr, rs.Step, stepIndex, stepTotal, "when: "+FormatCondition(rs.RuntimeWhen))
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

		// Execute check action after successful execution.
		if rs.Step.Check != nil {
			actx := ActionContext{
				WorkDir:     workDir,
				Cfg:         cfg,
				Reg:         reg,
				LogWriter:   logWriter,
				SkipConfirm: skipConfirm,
			}
			checkErr := ExecAction(*rs.Step.Check, actx)
			if checkErr != nil {
				rep.FailStep(addr, rs.Step, stepIndex, stepTotal, checkErr)
				if rs.Step.ContinueOnError {
					// Check failed but step is marked continue_on_error: report the failure
					// and proceed to the next step.
					continue
				}
				return ErrSilent
			}
		}

		rep.FinishStep(addr, rs.Step, stepIndex, stepTotal)
	}

	success = true
	return nil
}

// FormatCondition returns a short human-readable form of a typed condition for display.
func FormatCondition(c *condition.Condition) string {
	if c == nil {
		return ""
	}
	switch c.Type {
	case condition.TypeBuiltin:
		return "builtin " + c.Cmd
	case condition.TypeShell:
		return "shell " + c.Cmd
	case condition.TypeTemplate:
		return "template " + c.Expr
	default:
		return string(c.Type)
	}
}

// FormatAction returns a short human-readable form of a typed action for display.
func FormatAction(a *config.Action) string {
	if a == nil {
		return ""
	}
	return a.Type + " " + a.Cmd
}

// shellQuote wraps a path in single quotes for safe inclusion in a sh -c string.
// Embedded single quotes are escaped via the \' idiom.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
