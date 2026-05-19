package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"

	"devbox-cli/internal/builtin"
	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/filesgate"
	"devbox-cli/internal/filesgate/spec"
	"devbox-cli/internal/render"
	"devbox-cli/internal/usercommands"
)

// stdoutIsTTY reports whether os.Stdout is attached to a terminal.
// Overridable for tests.
var stdoutIsTTY = func() bool {
	return term.IsTerminal(os.Stdout.Fd())
}

// childIO returns the (stdout, stderr) writers a step should hand to a child
// process, plus a cleanup func that must be called after the child exits.
//
// Three paths, picked by (logWriter, stdout-is-tty):
//
//   - logWriter == nil → pass-through: os.Stdout / os.Stderr verbatim so the
//     child inherits the real terminal fd.
//
//   - logWriter != nil, stdout NOT a TTY → MultiWriter tee. Stdout was not a
//     TTY anyway, so docker compose would have used plain mode regardless.
//
//   - logWriter != nil, stdout IS a TTY → PTY. The child sees the tty slave as
//     its stdout/stderr (real terminal — docker compose's interactive UI
//     works), and a goroutine copies pty master output to (real os.Stdout +
//     log file with ANSI stripped). Cleanup closes the parent's tty fd and
//     waits for the copy goroutine to drain before closing the master. If
//     pty allocation fails, falls back transparently to the MultiWriter path
//     so log capture still happens (just without the TUI).
func childIO(logWriter io.Writer) (stdout, stderr io.Writer, cleanup func()) {
	if logWriter == nil {
		return os.Stdout, os.Stderr, func() {}
	}

	if !stdoutIsTTY() {
		logStripped := &ansiStripper{logWriter}
		return io.MultiWriter(os.Stdout, logStripped), io.MultiWriter(os.Stderr, logStripped), func() {}
	}

	ptmx, tty, err := pty.Open()
	if err != nil {
		// Fall back to MultiWriter — interactive UI is lost but log capture works.
		logStripped := &ansiStripper{logWriter}
		return io.MultiWriter(os.Stdout, logStripped), io.MultiWriter(os.Stderr, logStripped), func() {}
	}

	// Match the parent's terminal size so the child's TUI lays out correctly.
	// Use os.Stdout (not os.Stdin) because Stdout is the terminal when stdin is piped.
	_ = pty.InheritSize(os.Stdout, ptmx)

	done := make(chan struct{})
	sink := io.MultiWriter(os.Stdout, &ansiStripper{logWriter})
	go func() {
		defer close(done)
		_, _ = io.Copy(sink, ptmx)
	}()

	cleanup = func() {
		// Close parent's slave fd; when the child has also exited (its slave
		// fd closed by the kernel), the master gets EOF and the copy
		// goroutine returns. We wait for it before closing the master.
		_ = tty.Close()
		<-done
		_ = ptmx.Close()
	}

	return tty, tty, cleanup
}

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
// devbox subcommands also skip confirmation prompts. The supplied ctx
// propagates cancellation into the child via exec.CommandContext.
func buildDevboxCmd(ctx context.Context, devboxArg, workDir, shell, devboxBin string, skipConfirm bool) *exec.Cmd {
	bin, err := os.Executable()
	if err != nil {
		bin = devboxBin
	}
	cmd := exec.CommandContext(ctx, shell, "-c", shellQuote(bin)+" "+strings.TrimSpace(devboxArg)) //nolint:gosec
	bindCancelTerm(cmd)
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
// those stay in Run. The supplied ctx propagates cancellation into child processes
// via exec.CommandContext.
func ExecAction(ctx context.Context, a config.Action, actx ActionContext) error {
	switch a.Type {
	case "builtin":
		return execBuiltinAction(ctx, a, actx)
	case "command":
		return execCommandAction(ctx, a, actx)
	case "devbox":
		return execDevboxAction(ctx, a, actx)
	case "shell":
		return execShellAction(ctx, a, actx)
	default:
		return fmt.Errorf("unknown action type %q", a.Type)
	}
}

// execShellAction runs a shell command via sh -c.
func execShellAction(ctx context.Context, a config.Action, actx ActionContext) error {
	shell := config.ShellBin(actx.Cfg)
	cmd := exec.CommandContext(ctx, shell, "-c", strings.TrimSpace(a.Cmd)) //nolint:gosec
	bindCancelTerm(cmd)
	cmd.Dir = actx.WorkDir
	cmd.Stdin = os.Stdin
	stdout, stderr, cleanup := childIO(actx.LogWriter)
	defer cleanup()
	cmd.Stdout, cmd.Stderr = stdout, stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return fmt.Errorf("exit status %d", exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// execDevboxAction runs a devbox subcommand.
func execDevboxAction(ctx context.Context, a config.Action, actx ActionContext) error {
	shell := config.ShellBin(actx.Cfg)
	cmd := buildDevboxCmd(ctx, a.Cmd, actx.WorkDir, shell, config.DevboxBin(actx.Cfg), actx.SkipConfirm)
	cmd.Stdin = os.Stdin
	stdout, stderr, cleanup := childIO(actx.LogWriter)
	defer cleanup()
	cmd.Stdout, cmd.Stderr = stdout, stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return fmt.Errorf("exit status %d", exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// execBuiltinAction executes a builtin action.
func execBuiltinAction(ctx context.Context, a config.Action, actx ActionContext) error {
	if err := builtin.Validate(a.Cmd, a.With); err != nil {
		return fmt.Errorf("invalid builtin %q: %w", a.Cmd, err)
	}
	var out io.Writer = os.Stdout
	if actx.LogWriter != nil {
		out = io.MultiWriter(os.Stdout, &ansiStripper{actx.LogWriter})
	}
	ectx := builtin.ExecContext{
		Config:      actx.Cfg,
		ProjectRoot: actx.WorkDir,
		Output:      render.NewWriter(out),
		LogWriter:   actx.LogWriter,
		Stdin:       os.Stdin,
		SkipConfirm: actx.SkipConfirm,
	}
	return builtin.Run(ctx, a.Cmd, a.With, ectx)
}

// execCommandAction executes a registered user command.
func execCommandAction(ctx context.Context, a config.Action, actx ActionContext) error {
	if actx.Reg == nil {
		return fmt.Errorf("command registry not available for command %q", a.Cmd)
	}
	def, err := actx.Reg.Get(a.Cmd)
	if err != nil {
		return fmt.Errorf("command %q: %w", a.Cmd, err)
	}
	rctx, err := usercommands.BuildRunContext(actx.Cfg, actx.Reg, def, a.With, actx.WorkDir)
	if err != nil {
		return err
	}
	stdout, stderr, cleanup := childIO(actx.LogWriter)
	defer cleanup()
	rctx.Stdout = stdout
	rctx.Stderr = stderr
	rctx.Stdin = os.Stdin
	rctx.SkipConfirm = actx.SkipConfirm
	rctx.NonInteractive = actx.SkipConfirm
	return usercommands.RunCommand(ctx, rctx)
}

// ExecStep is a deprecated wrapper for backward compatibility.
// New code should use ExecAction directly.
func ExecStep(ctx context.Context, step config.DeployStep, workDir string, cfg *config.DevboxConfig, reg *usercommands.Registry, logWriter io.Writer, skipConfirm bool) error {
	actx := ActionContext{
		WorkDir:     workDir,
		Cfg:         cfg,
		Reg:         reg,
		LogWriter:   logWriter,
		SkipConfirm: skipConfirm,
	}
	return ExecAction(ctx, step.Action(), actx)
}

// bindCancelTerm configures cmd to send SIGTERM (instead of SIGKILL) on
// context cancellation, with a 5-second grace period before force-kill.
func bindCancelTerm(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second
}

// RunOptions carries all inputs to Run, replacing individual positional arguments.
// This struct bundles parameters to avoid signature churn and support optional
// state-tracking features (Recorder, SkipDecider) added after the initial pipeline design.
type RunOptions struct {
	Steps        []ResolvedStep
	Reporter     Reporter
	Name         string
	Config       *config.DevboxConfig
	Registry     *usercommands.Registry
	WorkDir      string
	LogWriter    io.Writer
	SkipConfirm  bool
	PostStepHook map[string]func() error

	// State tracking (optional): when non-nil, the executor records step
	// results and consults the skip decision before running each step.
	Recorder    Recorder
	SkipDecider SkipDecider

	// Context, when non-nil, is used as the parent for all child-process
	// cancellation. When nil, RunWithOptions creates one via signal.NotifyContext
	// that cancels on SIGINT / SIGTERM. Callers that already manage signals
	// should set this to avoid double-wrapping.
	Context context.Context
}

// Run executes a resolved step list, calling rep for all lifecycle events.
//
// Deprecated: Use RunWithOptions instead. This wrapper is kept for backward compatibility.
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
	return RunWithOptions(RunOptions{
		Steps:        steps,
		Reporter:     rep,
		Name:         name,
		Config:       cfg,
		Registry:     reg,
		WorkDir:      workDir,
		LogWriter:    logWriter,
		SkipConfirm:  skipConfirm,
		PostStepHook: postStepHooks,
		Recorder:     NopRecorder{},
		SkipDecider:  func(addr string, rs ResolvedStep, stepHash string) journal.Decision { return journal.Run },
	})
}

// RunWithOptions executes a resolved step list with full state-tracking support.
//
// name is a human-readable label passed to rep.StartPipeline (e.g. "deploy", "reset").
// postStepHooks maps step names to callbacks invoked after successful execution
// (before the check condition) — used e.g. to source .env after render-env.
//
// Recorder records step execution for state tracking; if nil, a NopRecorder is used.
// SkipDecider returns whether a step should be skipped based on previous state
// and action hashes. If nil, all steps are forced to Run.
//
// Per-step ordering for state tracking (does not affect steps without state):
//  1. compute stepHash := journal.StepHash(rs.Step)
//  2. evaluate phase-level when — if false, skip and continue
//  3. evaluate step-level when: — if false, skip and continue
//  4. if files_gate present: probe gate — if not satisfied, skip (bypasses SkipDecider)
//     else: consult SkipDecider(addr, rs, stepHash) — on Skip, record skip and continue
//  5. on Run, call recorder.OnStepStart, then ExecAction, then post-step hooks,
//     then check conditions
//  6. on success: recorder.OnStepFinish
//  7. on failure: recorder.OnStepFail
//
// Returns ErrSilent when any step fails (rep.FailStep has already been called).
// Returns other errors for config/condition evaluation failures.
func RunWithOptions(opts RunOptions) error {
	if opts.Recorder == nil {
		opts.Recorder = NopRecorder{}
	}
	if opts.SkipDecider == nil {
		opts.SkipDecider = func(addr string, rs ResolvedStep, stepHash string) journal.Decision {
			return journal.Run
		}
	}
	// Establish a cancellable parent context for all child-process work.
	// When the caller did not supply one, install signal.NotifyContext so
	// SIGINT / SIGTERM cancel ctx and propagate to children via cmd.Cancel.
	var ctx context.Context
	if opts.Context != nil {
		ctx = opts.Context
	} else {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
	}
	// trackedTotal excludes steps belonging to phases with Untracked=true.
	// These steps receive index=0, total=0 in reporter calls so PlainReporter
	// can suppress output for them.
	trackedTotal := 0
	for _, rs := range opts.Steps {
		if !rs.Phase.Untracked {
			trackedTotal++
		}
	}

	opts.Reporter.StartPipeline(opts.Name, trackedTotal)
	opts.Recorder.OnPipelineStart(opts.Name, len(opts.Steps))

	success := false
	defer func() {
		opts.Reporter.FinishPipeline(success)
		opts.Recorder.OnPipelineFinish(success)
	}()

	lastPhaseKey := ""
	phaseSkipped := false
	phaseWhenMsg := ""
	trackedIndex := 0

	for _, rs := range opts.Steps {
		phaseKey := rs.Phase.Name
		if rs.Service != "" {
			phaseKey = rs.Service + "/" + rs.Phase.Name
		}

		if phaseKey != lastPhaseKey {
			opts.Reporter.EnterPhase(phaseKey, rs.Phase)
			lastPhaseKey = phaseKey
			phaseSkipped = false
			phaseWhenMsg = ""

			if rs.PhaseWhen != nil {
				ok, err := condition.EvalRuntimeTyped(rs.PhaseWhen, opts.WorkDir)
				if err != nil {
					return fmt.Errorf("evaluating when condition for phase %s: %w", phaseKey, err)
				}
				if !ok {
					phaseSkipped = true
					phaseWhenMsg = FormatCondition(rs.PhaseWhen)
					opts.Reporter.SkipPhase(phaseKey, rs.Phase, "when: "+phaseWhenMsg)
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

		// Step 1: Compute step hash early (includes FilesGate if present; for gateless steps equals ActionHash).
		stepHash := journal.StepHash(rs.Step)

		// Phase-level when condition was false — skip all steps in this phase.
		if phaseSkipped {
			opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
			opts.Reporter.SkipStep(addr, rs.Step, stepIndex, stepTotal, "phase when: "+phaseWhenMsg)
			opts.Recorder.OnStepSkip(addr, rs, stepHash, "phase when: "+phaseWhenMsg)
			continue
		}

		// Step 2: Evaluate step-level runtime when condition (unchanged from before).
		if rs.RuntimeWhen != nil {
			ok, err := condition.EvalRuntimeTyped(rs.RuntimeWhen, opts.WorkDir)
			if err != nil {
				return fmt.Errorf("evaluating when condition for %s: %w", addr, err)
			}
			if !ok {
				opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
				opts.Reporter.SkipStep(addr, rs.Step, stepIndex, stepTotal, "when: "+FormatCondition(rs.RuntimeWhen))
				opts.Recorder.OnStepSkip(addr, rs, stepHash, "when: "+FormatCondition(rs.RuntimeWhen))
				continue
			}
		}

		// Step 3: Evaluate files_gate (if present).
		// When a gate is present, it replaces the journal-skip-decider logic (journal bypass).
		if rs.FilesGate != nil {
			// Guard: runtime must have a registry to evaluate gates.
			if opts.Registry == nil {
				opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
				err := fmt.Errorf("files_gate on step %q requires command registry but none was provided to the executor", addr)
				opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, err)
				opts.Recorder.OnStepFail(addr, rs, stepHash, 0, err)
				return ErrSilent
			}

			// Determine the target command (gate.Command or step.Cmd).
			targetCmd := rs.FilesGate.Command
			if targetCmd == "" {
				targetCmd = rs.Step.Cmd
			}

			// Resolve the target command from registry.
			def, err := opts.Registry.Get(targetCmd)
			if err != nil {
				// Config error: command not found.
				opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
				err = fmt.Errorf("files_gate on step %q references unknown command %q: %w", addr, targetCmd, err)
				opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, err)
				opts.Recorder.OnStepFail(addr, rs, stepHash, 0, err)
				return ErrSilent
			}

			// Build RunContext for the target command (using gate's with or step's with).
			// Treat an empty map the same as nil: inherit the step's with.
			gateWith := rs.FilesGate.With
			if len(gateWith) == 0 {
				gateWith = rs.Step.With
			}
			runCtx, err := usercommands.BuildRunContext(opts.Config, opts.Registry, def, gateWith, opts.WorkDir)
			if err != nil {
				// Config error: param resolution or docker config failed.
				opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
				err := fmt.Errorf("files_gate on step %q: building context for command %q: %w", addr, targetCmd, err)
				opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, err)
				opts.Recorder.OnStepFail(addr, rs, stepHash, 0, err)
				return ErrSilent
			}

			// Expand require spec to get file IDs.
			ids, err := spec.ResolveRequireIDs(rs.FilesGate.Require, def.Files)
			if err != nil {
				// Config error: invalid require spec.
				opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
				err := fmt.Errorf("files_gate on step %q: %w", addr, err)
				opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, err)
				opts.Recorder.OnStepFail(addr, rs, stepHash, 0, err)
				return ErrSilent
			}

			// Probe the selected files.
			probeResults, err := usercommands.ComputeFilePathsProbe(runCtx, ids)
			if err != nil {
				// Config error: glob/template/regex error.
				opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
				err := fmt.Errorf("files_gate on step %q: probing files: %w", addr, err)
				opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, err)
				opts.Recorder.OnStepFail(addr, rs, stepHash, 0, err)
				return ErrSilent
			}

			// Evaluate gate against probed state.
			// offendingIDs collects the file IDs that drove the gate decision.
			var offendingIDs []string
			switch rs.FilesGate.State {
			case filesgate.StateReadable:
				// All selected files must exist; collect the missing ones.
				for _, id := range ids {
					if !probeResults[id].Resolved {
						offendingIDs = append(offendingIDs, id)
					}
				}
			case filesgate.StateMissing:
				// None of the selected files may exist; collect the present ones.
				for _, id := range ids {
					if probeResults[id].Resolved {
						offendingIDs = append(offendingIDs, id)
					}
				}
			default:
				opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
				err := fmt.Errorf("files_gate on step %q: invalid state %q (must be \"readable\" or \"missing\")", addr, rs.FilesGate.State)
				opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, err)
				opts.Recorder.OnStepFail(addr, rs, stepHash, 0, err)
				return ErrSilent
			}

			if len(offendingIDs) > 0 {
				// Gate not satisfied — skip step, showing only the IDs that drove the decision.
				opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
				reason := FormatFilesGate(rs.FilesGate, offendingIDs...)
				opts.Reporter.SkipStep(addr, rs.Step, stepIndex, stepTotal, reason)
				opts.Recorder.OnStepSkip(addr, rs, stepHash, reason)
				continue
			}

			// Gate satisfied — proceed to execution (bypass journal-skip-decider).
		} else {
			// Step 3b: No gate present — consult skip decision from state/config hash invalidation.
			decision := opts.SkipDecider(addr, rs, stepHash)
			if decision == journal.Skip {
				opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
				opts.Reporter.SkipStep(addr, rs.Step, stepIndex, stepTotal, "state: already deployed")
				opts.Recorder.OnStepSkip(addr, rs, stepHash, "state")
				continue
			}
		}

		// Step 4: Execute the step.
		opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
		opts.Recorder.OnStepStart(addr, rs, stepHash)
		opts.Reporter.SuspendForExec()

		// Per-step skip_confirm: ORed with the pipeline-wide flag so a step can
		// opt in to bypass even when the pipeline was invoked without -y.
		skipConfirm := opts.SkipConfirm || rs.Step.SkipConfirm

		startTime := time.Now()
		stepErr := ExecStep(ctx, rs.Step, opts.WorkDir, opts.Config, opts.Registry, opts.LogWriter, skipConfirm)
		durationMs := time.Since(startTime).Milliseconds()

		opts.Reporter.ResumeAfterExec()

		if stepErr != nil {
			opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, stepErr)
			opts.Recorder.OnStepFail(addr, rs, stepHash, durationMs, stepErr)
			if rs.Step.ContinueOnError {
				// Step failed but is marked continue_on_error: report the failure
				// and proceed to the next step. Post-step hook and Check are skipped.
				continue
			}
			return ErrSilent
		}

		// Run post-step hook if registered (e.g. source .env after render-env).
		if hook, ok := opts.PostStepHook[rs.Step.Name]; ok {
			if err := hook(); err != nil {
				opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, err)
				opts.Recorder.OnStepFail(addr, rs, stepHash, durationMs, err)
				if rs.Step.ContinueOnError {
					continue
				}
				return ErrSilent
			}
		}

		// Execute check action after successful execution.
		if rs.Step.Check != nil {
			actx := ActionContext{
				WorkDir:     opts.WorkDir,
				Cfg:         opts.Config,
				Reg:         opts.Registry,
				LogWriter:   opts.LogWriter,
				SkipConfirm: skipConfirm,
			}
			checkErr := ExecAction(ctx, *rs.Step.Check, actx)
			if checkErr != nil {
				opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, checkErr)
				opts.Recorder.OnStepFail(addr, rs, stepHash, durationMs, checkErr)
				if rs.Step.ContinueOnError {
					// Check failed but step is marked continue_on_error: report the failure
					// and proceed to the next step.
					continue
				}
				return ErrSilent
			}
		}

		opts.Reporter.FinishStep(addr, rs.Step, stepIndex, stepTotal)
		opts.Recorder.OnStepFinish(addr, rs, stepHash, durationMs)
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

// FormatRequireSpec returns a human-readable form of a RequireSpec.
func FormatRequireSpec(req filesgate.RequireSpec) string {
	if req == nil {
		return "required"
	}
	switch spec := req.(type) {
	case filesgate.RequireRequired:
		return "required"
	case filesgate.RequireAll:
		return "all"
	case filesgate.RequireList:
		if len(spec.IDs) == 1 {
			return spec.IDs[0]
		}
		return strings.Join(spec.IDs, ",")
	default:
		return "unknown"
	}
}

// FormatFilesGate returns a short human-readable form of a files_gate for display.
// If ids are provided, they are the expanded file IDs that drove the gate decision.
func FormatFilesGate(fg *filesgate.FilesGate, ids ...string) string {
	if fg == nil {
		return ""
	}

	// Base format: "files_gate: <state>"
	result := "files_gate: " + fg.State.String()

	// If called with no ids (plan-time display), add the require spec.
	if len(ids) == 0 {
		requireStr := FormatRequireSpec(fg.Require)
		return result + " (" + requireStr + ")"
	}

	// If called with ids (runtime skip reporting), add the resolved file IDs.
	// For display, show up to 3 IDs joined by comma, then "..." if more.
	var shown []string
	for i, id := range ids {
		if i >= 3 {
			shown = append(shown, "...")
			break
		}
		shown = append(shown, id)
	}
	return result + " [" + strings.Join(shown, ",") + "]"
}

// shellQuote wraps a path in single quotes for safe inclusion in a sh -c string.
// Embedded single quotes are escaped via the \' idiom.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
