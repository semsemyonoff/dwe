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
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"golang.org/x/sync/errgroup"

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
// stepWriter encodes the full destination for child output as configured by
// executeStepBody. The semantics differ by execution mode:
//
//   - SEQUENTIAL (parallel=false): the LiveLine footer has been paused
//     (SuspendForExec) so the child can write to the host terminal directly.
//     stepWriter is expected to include os.Stdout (typically as
//     io.MultiWriter(os.Stdout, logSanitizer{logFile})) so the user sees
//     the child output with colors / cursor positioning intact while an
//     ANSI-stripped copy lands in the on-disk log. When stdoutIsTTY a PTY
//     is allocated and ptmx → stepWriter so the child sees a real TTY.
//     If stepWriter is nil (ad-hoc callers outside RunWithOptions, e.g.
//     `devbox deploy run`), childIO falls back to os.Stdout/os.Stderr.
//
//   - PARALLEL (parallel=true): the LiveLine block owns the host terminal,
//     so we must NOT write to os.Stdout from child goroutines.
//     stepWriter is the lineTee + per-sub-step log joinWriters target.
//     NO PTY is allocated — granting the child a PTY while stdin is the
//     empty reader causes `docker compose exec/run` to fail with
//     "cannot attach stdin to a TTY-enabled container because stdin is
//     not a terminal". Without PTY the child sees a pipe and falls back
//     to non-TTY output, which is what the live-block expects.
func childIO(stepWriter io.Writer, parallel bool) (stdout, stderr io.Writer, cleanup func()) {
	if parallel {
		if stepWriter == nil {
			return io.Discard, io.Discard, func() {}
		}
		return stepWriter, stepWriter, func() {}
	}
	if stepWriter == nil {
		return os.Stdout, os.Stderr, func() {}
	}
	if !stdoutIsTTY() {
		return stepWriter, stepWriter, func() {}
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		return stepWriter, stepWriter, func() {}
	}
	_ = pty.InheritSize(os.Stdout, ptmx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(stepWriter, ptmx)
	}()
	cleanup = func() {
		_ = tty.Close()
		<-done
		_ = ptmx.Close()
	}
	return tty, tty, cleanup
}

// ActionContext carries the inputs needed by ExecAction.
// It is constructed once per step by Run and reused for both body and check.
type ActionContext struct {
	WorkDir string
	Cfg     *config.DevboxConfig
	Reg     *usercommands.Registry
	// StepWriter is the per-step destination for child output, populated by
	// executeStepBody. Its shape depends on Parallel:
	//
	//   - sequential: typically io.MultiWriter(os.Stdout, logSanitizer{logFile})
	//     so the user sees colored / TTY output on the host terminal while a
	//     sanitized copy lands in the pipeline log. The LiveLine footer is
	//     paused for the duration of the step.
	//   - parallel: a lineTee + per-sub-step-log joinWriters target wrapped
	//     in ansiOnlyStripper. Child output flows ONLY through this writer —
	//     never to os.Stdout — so the LiveLine block remains intact.
	//
	// When nil (ad-hoc external callers outside RunWithOptions), childIO and
	// execBuiltinAction fall back to os.Stdout/os.Stderr passthrough.
	StepWriter io.Writer
	// LogWriter carries the raw per-sub-step log file in parallel mode. The
	// lineTee callback in executeStepBody writes each assembled ANSI-clean frame
	// to it directly (via fmt.Fprintln). In sequential mode it is nil — the
	// global pipeline log receives lines via PlainReporter's writeLog path
	// and via the tee inside StepWriter.
	LogWriter   io.Writer
	SkipConfirm bool
	// Parallel indicates the action is running as a sub-step of a parallel
	// group. In that mode all child output is routed through StepWriter
	// (never directly to os.Stdout / os.Stderr), no PTY is allocated, and
	// stdin is detached so concurrent sub-steps do not contend for the
	// terminal. Sequential steps run with cmd.Stdin = os.Stdin and may
	// receive a PTY when stdout is a TTY (set up by childIO).
	Parallel bool
}

// buildDevboxCmd constructs an exec.Cmd for a devbox: pipeline step.
//
// It sets CLICOLOR_FORCE=1 in the child environment so that lipgloss enables
// colors even when stdout is wrapped in an io.MultiWriter (which the child sees
// as a pipe rather than a TTY). The log tee via logSanitizer is unaffected.
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
	if !actx.Parallel {
		cmd.Stdin = os.Stdin
	}
	stdout, stderr, cleanup := childIO(actx.StepWriter, actx.Parallel)
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
	if !actx.Parallel {
		cmd.Stdin = os.Stdin
	}
	stdout, stderr, cleanup := childIO(actx.StepWriter, actx.Parallel)
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

// execBuiltinAction executes a builtin action. Builtin output is routed
// through actx.StepWriter, which in sequential mode is
// io.MultiWriter(os.Stdout, logSanitizer{logFile}) — so the user sees the
// colored builtin messages on the host terminal while a sanitized copy
// lands in the on-disk log. In parallel mode StepWriter is the
// lineTee → live-block routing. When nil (ad-hoc external callers),
// output falls back to os.Stdout directly.
func execBuiltinAction(ctx context.Context, a config.Action, actx ActionContext) error {
	if err := builtin.Validate(a.Cmd, a.With); err != nil {
		return fmt.Errorf("invalid builtin %q: %w", a.Cmd, err)
	}
	out := actx.StepWriter
	if out == nil {
		out = os.Stdout
	}
	var stdinForBuiltin *os.File
	if !actx.Parallel {
		stdinForBuiltin = os.Stdin
	}
	ectx := builtin.ExecContext{
		Config:      actx.Cfg,
		ProjectRoot: actx.WorkDir,
		Output:      render.NewWriter(out),
		Stdin:       stdinForBuiltin,
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
	stdout, stderr, cleanup := childIO(actx.StepWriter, actx.Parallel)
	defer cleanup()
	rctx.Stdout = stdout
	rctx.Stderr = stderr
	if !actx.Parallel {
		rctx.Stdin = os.Stdin
	} else {
		// Never let a parallel sub-step read from shared stdin.
		// Interactive commands are rejected at plan time, but shell
		// scripts could still block on an unexpected read without this.
		rctx.Stdin = strings.NewReader("")
	}
	rctx.SkipConfirm = actx.SkipConfirm
	rctx.NonInteractive = actx.SkipConfirm
	return usercommands.RunCommand(ctx, rctx)
}

// ExecStep is a deprecated wrapper for backward compatibility.
// New code should use ExecAction directly.
//
// The legacy logWriter argument is mapped to ActionContext.StepWriter so
// any external caller that supplies a writer still receives child output via
// the same single durable path. Passing nil yields os.Stdout/os.Stderr
// passthrough (legacy `devbox deploy run` semantics).
func ExecStep(ctx context.Context, step config.DeployStep, workDir string, cfg *config.DevboxConfig, reg *usercommands.Registry, logWriter io.Writer, skipConfirm bool) error {
	actx := ActionContext{
		WorkDir:     workDir,
		Cfg:         cfg,
		Reg:         reg,
		StepWriter:  logWriter,
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

	// Parallel marks this RunOptions as describing a parallel sub-step. It is
	// set by executeParallelGroup on a per-sub-step copy before delegating to
	// executeStepBody, and is threaded into ActionContext.Parallel so that
	// child I/O (childIO, execBuiltinAction) routes only to StepWriter — never
	// to os.Stdout / os.Stderr — and skips PTY allocation. Callers MUST NOT
	// set this field directly; sequential pipelines leave it false.
	Parallel bool
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
	// A parallel group contributes len(group.Steps) to the total — each
	// sub-step gets its own reserved index in the [N/M] display.
	trackedTotal := 0
	for _, rs := range opts.Steps {
		if rs.Phase.Untracked {
			continue
		}
		if rs.Parallel != nil {
			trackedTotal += len(rs.Parallel.Steps)
			continue
		}
		trackedTotal++
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

		// Determine the index/total to pass to reporter calls for this step
		// or parallel group. Untracked phase steps always receive 0/0 so
		// reporters can identify them. Parallel groups reserve a contiguous
		// block of len(group.Steps) indices in declaration order; the group
		// itself (when emitted as a single StartStep/SkipStep on skip) uses
		// the leading index of that block.
		stepIndex, stepTotal := 0, 0
		var subIndices []int
		if !rs.Phase.Untracked {
			if rs.Parallel != nil {
				subIndices = make([]int, len(rs.Parallel.Steps))
				for i := range subIndices {
					trackedIndex++
					subIndices[i] = trackedIndex
				}
				stepIndex, stepTotal = subIndices[0], trackedTotal
			} else {
				trackedIndex++
				stepIndex, stepTotal = trackedIndex, trackedTotal
			}
		} else if rs.Parallel != nil {
			// Untracked parallel groups still need a valid slice so goroutines
			// can index into it; zero indices tell reporters to suppress [n/N].
			subIndices = make([]int, len(rs.Parallel.Steps))
		}

		// Step 1: Compute step hash early (includes FilesGate if present; for gateless steps equals ActionHash).
		stepHash := journal.StepHash(rs.Step)

		// Phase-level when condition was false — skip all steps in this phase.
		if phaseSkipped {
			opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
			opts.Reporter.SkipStep(addr, rs.Step, stepIndex, stepTotal, "phase when: "+phaseWhenMsg)
			if rs.Parallel != nil {
				// Record each sub-step as skipped so the journal does not
				// treat them as never-attempted on the next run.
				for _, sub := range rs.Parallel.Steps {
					opts.Recorder.OnStepSkip(sub.StepAddress(), sub, journal.StepHash(sub.Step), "parent phase when=false: "+phaseWhenMsg)
				}
			} else {
				opts.Recorder.OnStepSkip(addr, rs, stepHash, "phase when: "+phaseWhenMsg)
			}
			continue
		}

		// Parallel group: branch into the concurrent executor.
		if rs.Parallel != nil {
			if err := executeParallelGroup(ctx, opts, rs, addr, subIndices, trackedTotal); err != nil {
				return err
			}
			continue
		}

		if err := executeStepBody(ctx, opts, rs, addr, stepIndex, stepTotal); err != nil {
			return err
		}
	}

	success = true
	return nil
}

// executeStepBody runs the per-step pipeline for a single resolved step (or a
// single sub-step inside a parallel group). It handles step-level when,
// files_gate vs SkipDecider, ExecAction, post-step hook, and check action.
//
// Return semantics:
//   - nil: step succeeded, was skipped, or failed with continue_on_error=true
//     (the failure was already reported via FailStep/OnStepFail).
//   - ErrSilent: step failed and continue_on_error=false; caller should abort.
//   - other error: config / condition evaluation error; caller propagates.
func executeStepBody(ctx context.Context, opts RunOptions, rs ResolvedStep, addr string, stepIndex, stepTotal int) error {
	stepHash := journal.StepHash(rs.Step)

	// Step 2: Evaluate step-level runtime when condition.
	if rs.RuntimeWhen != nil {
		ok, err := condition.EvalRuntimeTyped(rs.RuntimeWhen, opts.WorkDir)
		if err != nil {
			return fmt.Errorf("evaluating when condition for %s: %w", addr, err)
		}
		if !ok {
			opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
			opts.Reporter.SkipStep(addr, rs.Step, stepIndex, stepTotal, "when: "+FormatCondition(rs.RuntimeWhen))
			opts.Recorder.OnStepSkip(addr, rs, stepHash, "when: "+FormatCondition(rs.RuntimeWhen))
			return nil
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

		targetCmd := rs.FilesGate.Command
		if targetCmd == "" {
			targetCmd = rs.Step.Cmd
		}

		def, err := opts.Registry.Get(targetCmd)
		if err != nil {
			opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
			err = fmt.Errorf("files_gate on step %q references unknown command %q: %w", addr, targetCmd, err)
			opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, err)
			opts.Recorder.OnStepFail(addr, rs, stepHash, 0, err)
			return ErrSilent
		}

		gateWith := rs.FilesGate.With
		if len(gateWith) == 0 {
			gateWith = rs.Step.With
		}
		runCtx, err := usercommands.BuildRunContext(opts.Config, opts.Registry, def, gateWith, opts.WorkDir)
		if err != nil {
			opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
			err := fmt.Errorf("files_gate on step %q: building context for command %q: %w", addr, targetCmd, err)
			opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, err)
			opts.Recorder.OnStepFail(addr, rs, stepHash, 0, err)
			return ErrSilent
		}

		ids, err := spec.ResolveRequireIDs(rs.FilesGate.Require, def.Files)
		if err != nil {
			opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
			err := fmt.Errorf("files_gate on step %q: %w", addr, err)
			opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, err)
			opts.Recorder.OnStepFail(addr, rs, stepHash, 0, err)
			return ErrSilent
		}

		probeResults, err := usercommands.ComputeFilePathsProbe(runCtx, ids)
		if err != nil {
			opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
			err := fmt.Errorf("files_gate on step %q: probing files: %w", addr, err)
			opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, err)
			opts.Recorder.OnStepFail(addr, rs, stepHash, 0, err)
			return ErrSilent
		}

		var offendingIDs []string
		switch rs.FilesGate.State {
		case filesgate.StateReadable:
			for _, id := range ids {
				if !probeResults[id].Resolved {
					offendingIDs = append(offendingIDs, id)
				}
			}
		case filesgate.StateMissing:
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
			opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
			reason := FormatFilesGate(rs.FilesGate, offendingIDs...)
			opts.Reporter.SkipStep(addr, rs.Step, stepIndex, stepTotal, reason)
			opts.Recorder.OnStepSkip(addr, rs, stepHash, reason)
			return nil
		}
		// Gate satisfied — proceed to execution (bypass journal-skip-decider).
	} else {
		// Step 3b: No gate present — consult skip decision.
		decision := opts.SkipDecider(addr, rs, stepHash)
		if decision == journal.Skip {
			opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
			opts.Reporter.SkipStep(addr, rs.Step, stepIndex, stepTotal, "state: already deployed")
			opts.Recorder.OnStepSkip(addr, rs, stepHash, "state")
			return nil
		}
	}

	// Step 4: Execute the step.
	opts.Reporter.StartStep(addr, rs.Step, stepIndex, stepTotal)
	opts.Recorder.OnStepStart(addr, rs, stepHash)

	skipConfirm := opts.SkipConfirm || rs.Step.SkipConfirm

	// Construct the per-step output destination (stepWriter) and any
	// cleanup hooks. The two modes differ significantly:
	//
	//   - SEQUENTIAL: pause the LiveLine footer so the child can write to
	//     the host terminal directly. stepWriter becomes
	//     io.MultiWriter(os.Stdout, logSanitizer{logFile}) — colored output
	//     reaches the user verbatim while an ANSI-stripped copy lands in
	//     the on-disk log. No lineTee is needed because nothing is fed
	//     back to the reporter (the footer just shows the step name).
	//
	//   - PARALLEL: route everything through a lineTee → Reporter.StepOutput
	//     so the LiveLine block row can display the latest \n-terminated
	//     frame, plus a side-write to the per-sub-step log file inside the
	//     lineTee callback. The host terminal is owned by the LiveLine
	//     block; child output MUST NOT go to os.Stdout.
	var (
		stepWriter io.Writer
		flushTee   func()
		closeStep  func()
	)
	if opts.Parallel {
		stepAddr := addr
		subLog := opts.LogWriter // per-sub-step log writer (set by runParallelSubStep)
		tee := newLineTee(func(frame string, final bool) {
			opts.Reporter.StepOutput(stepAddr, frame, final)
			// Write the assembled, ANSI-clean frame to the per-sub-step log
			// file. Routing through the lineTee ensures OSC/CSI sequences
			// split across PTY read boundaries are reassembled and double-
			// stripped before reaching disk (a stateless per-Write
			// logSanitizer cannot handle split sequences).
			if subLog != nil {
				_, _ = fmt.Fprintln(subLog, frame)
			}
		})
		stepWriter = &ansiOnlyStripper{w: tee}
		flushTee = tee.Flush
		// tee.Flush must run BEFORE any reporter end-of-step event so the
		// trailing non-newline-terminated tail (delivered as final=false via
		// lineTee.Flush) is recorded in PlainReporter.inProgress in time
		// for commitTrailingTail to flush it inside FinishStep/FailStep/
		// SkipStep. lineTee.Flush is idempotent, so multiple eager calls
		// (before each finish event) plus the defer for short-circuit
		// returns are all safe.
	} else {
		// Sequential: pause the footer for the duration of the step body so
		// the child owns the terminal. ResumeAfterExec is matched in the
		// closeStep teardown so every return path (success / failure /
		// panic via deferred run) restores the footer.
		opts.Reporter.SuspendForExec()
		closeStep = func() { opts.Reporter.ResumeAfterExec() }
		if opts.LogWriter != nil {
			stepWriter = io.MultiWriter(os.Stdout, &logSanitizer{w: opts.LogWriter})
		} else {
			stepWriter = os.Stdout
		}
	}
	if flushTee != nil {
		defer flushTee()
	}
	if closeStep != nil {
		defer closeStep()
	}

	bodyActx := ActionContext{
		WorkDir:     opts.WorkDir,
		Cfg:         opts.Config,
		Reg:         opts.Registry,
		StepWriter:  stepWriter,
		LogWriter:   opts.LogWriter,
		SkipConfirm: skipConfirm,
		Parallel:    opts.Parallel,
	}
	startTime := time.Now()
	stepErr := ExecAction(ctx, rs.Step.Action(), bodyActx)
	durationMs := time.Since(startTime).Milliseconds()
	if flushTee != nil {
		flushTee()
	}

	if stepErr != nil {
		opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, stepErr)
		opts.Recorder.OnStepFail(addr, rs, stepHash, durationMs, stepErr)
		if rs.Step.ContinueOnError {
			return nil
		}
		return ErrSilent
	}

	if hook, ok := opts.PostStepHook[rs.Step.Name]; ok {
		if err := hook(); err != nil {
			opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, err)
			opts.Recorder.OnStepFail(addr, rs, stepHash, durationMs, err)
			if rs.Step.ContinueOnError {
				return nil
			}
			return ErrSilent
		}
	}

	if rs.Step.Check != nil {
		// Commit any body trailing tail before check: runs. Without this, a
		// final=true frame from the check displaces inProgress (set by
		// tee.Flush above) before commitTrailingTail sees it, silently
		// dropping the body's last non-newline-terminated line.
		opts.Reporter.FlushOutput(addr)
		actx := ActionContext{
			WorkDir:     opts.WorkDir,
			Cfg:         opts.Config,
			Reg:         opts.Registry,
			StepWriter:  stepWriter,
			LogWriter:   opts.LogWriter,
			SkipConfirm: skipConfirm,
			Parallel:    opts.Parallel,
		}
		checkErr := ExecAction(ctx, *rs.Step.Check, actx)
		if flushTee != nil {
			flushTee()
		}
		if checkErr != nil {
			opts.Reporter.FailStep(addr, rs.Step, stepIndex, stepTotal, checkErr)
			opts.Recorder.OnStepFail(addr, rs, stepHash, durationMs, checkErr)
			if rs.Step.ContinueOnError {
				return nil
			}
			return ErrSilent
		}
	}

	opts.Reporter.FinishStep(addr, rs.Step, stepIndex, stepTotal)
	opts.Recorder.OnStepFinish(addr, rs, stepHash, durationMs)
	return nil
}

// executeParallelGroup runs the sub-steps of a resolved parallel group
// concurrently. The group itself is consumed as one element from the top-level
// step list; sub-step indices have been pre-reserved by the caller.
//
// Group-level when (rs.RuntimeWhen) is evaluated once before any sub-step is
// launched. On false the entire group is skipped: a single StartStep/SkipStep
// pair is emitted for the group address (consuming the leading reserved index),
// and one OnStepSkip is recorded per sub-step so the journal does not treat
// them as never-attempted.
//
// On true the executor calls StartGroup, dispatches each sub-step's body
// through executeStepBody concurrently under an errgroup with the configured
// concurrency limit, then calls FinishGroup. With FailFast=true an erroring
// sub-step cancels the group context; with FailFast=false errors are collected
// and joined into a single returned error.
func executeParallelGroup(parentCtx context.Context, opts RunOptions, rs ResolvedStep, addr string, subIndices []int, total int) error {
	// Group-level when: evaluate once.
	if rs.RuntimeWhen != nil {
		ok, err := condition.EvalRuntimeTyped(rs.RuntimeWhen, opts.WorkDir)
		if err != nil {
			return fmt.Errorf("evaluating when condition for %s: %w", addr, err)
		}
		if !ok {
			leadIdx := 0
			if len(subIndices) > 0 {
				leadIdx = subIndices[0]
			}
			reason := "when: " + FormatCondition(rs.RuntimeWhen)
			opts.Reporter.StartStep(addr, rs.Step, leadIdx, total)
			opts.Reporter.SkipStep(addr, rs.Step, leadIdx, total, reason)
			for _, sub := range rs.Parallel.Steps {
				opts.Recorder.OnStepSkip(sub.StepAddress(), sub, journal.StepHash(sub.Step), "parent group when=false")
			}
			return nil
		}
	}

	// Build a filtered step containing only resolved sub-steps so the reporter
	// does not register phantom entries for sub-steps removed by template when:.
	filteredStep := rs.Step
	if rs.Step.Parallel != nil {
		filteredParallel := *rs.Step.Parallel
		filteredParallel.Steps = make([]config.DeployStep, len(rs.Parallel.Steps))
		for i, sub := range rs.Parallel.Steps {
			filteredParallel.Steps[i] = sub.Step
		}
		filteredStep.Parallel = &filteredParallel
	}
	opts.Reporter.StartGroup(addr, filteredStep, subIndices, total)

	eg, gctx := errgroup.WithContext(parentCtx)
	if rs.Parallel.MaxConcurrent > 0 {
		eg.SetLimit(rs.Parallel.MaxConcurrent)
	}

	var (
		groupErrs []error
		errsMu    sync.Mutex
	)

	for i, sub := range rs.Parallel.Steps {
		eg.Go(func() error {
			if gctx.Err() != nil {
				// Return ErrSilent (not gctx.Err()) so callers that inspect
				// errors.Is(err, ErrSilent) behave consistently regardless of
				// which goroutine's error errgroup.Wait returns.
				return fmt.Errorf("cancelled: %w", ErrSilent)
			}
			subAddr := sub.StepAddress()
			err := runParallelSubStep(gctx, opts, rs, sub, subAddr, subIndices[i], total)
			if err == nil {
				return nil
			}
			wrapped := fmt.Errorf("parallel sub-step %q: %w", subAddr, err)
			if rs.Parallel.FailFast {
				return wrapped
			}
			errsMu.Lock()
			groupErrs = append(groupErrs, wrapped)
			errsMu.Unlock()
			return nil
		})
	}

	var groupErr error
	if rs.Parallel.FailFast {
		groupErr = eg.Wait()
	} else {
		_ = eg.Wait()
		if len(groupErrs) > 0 {
			groupErr = errors.Join(groupErrs...)
		}
	}

	opts.Reporter.FinishGroup(addr, rs.Step, groupErr == nil)
	return groupErr
}

// runParallelSubStep opens a per-sub-step log file, builds a tee writer
// (sub-step log + global pipeline log + line tee to Reporter.SubStepOutput),
// and delegates to executeStepBody with Parallel=true so child I/O is routed
// only through the tee — never to os.Stdout / os.Stderr, never via PTY.
//
// The sub-step log file is closed on every return path. The line tee is
// flushed so any trailing un-terminated bytes still surface as a final
// SubStepOutput line.
func runParallelSubStep(ctx context.Context, opts RunOptions, group ResolvedStep, sub ResolvedStep, subAddr string, idx, total int) error {
	subFile, logPath, openErr := OpenSubStepLog(opts.WorkDir, opts.Name, group.Step.Name, sub.Step.Name, opts.LogWriter != nil)
	if openErr != nil {
		return fmt.Errorf("opening sub-step log for %q: %w", subAddr, openErr)
	}
	if subFile != nil {
		defer func() { _ = subFile.Close() }()
	}
	// Push the per-sub-step log path to the reporter so the TTY buffer-dump
	// policy can suppress the on-screen dump in favour of a "Full log:" hint
	// on success/skip. An empty path (log disabled) is treated as a no-op by
	// SetSubStepLogPath. Strictly later than StartGroup, before any output.
	opts.Reporter.SetSubStepLogPath(subAddr, logPath)

	// Per-sub-step log file is passed as a raw io.Writer to executeStepBody.
	// The lineTee callback in executeStepBody writes each assembled, ANSI-clean
	// frame (both final and non-final) to this writer via fmt.Fprintln, so the
	// file receives clean line-separated output without needing a logSanitizer
	// wrapper. This approach also handles OSC/CSI sequences split across PTY
	// read boundaries — the double-strip inside lineTee reassembles them before
	// the callback fires. The global pipeline log is fed via PlainReporter's
	// writeLog side-channel, not via this writer.
	subOpts := opts
	if subFile != nil {
		subOpts.LogWriter = subFile
	} else {
		subOpts.LogWriter = nil
	}
	subOpts.Parallel = true

	return executeStepBody(ctx, subOpts, sub, subAddr, idx, total)
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
