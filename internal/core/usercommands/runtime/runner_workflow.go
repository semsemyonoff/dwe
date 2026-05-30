package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/x/term"

	"devbox-cli/internal/core/usercommands/runtime/internal/runio"
	"devbox-cli/internal/shared/liveui"
	"devbox-cli/internal/shared/tpl"
)

// workflowParallelStdoutIsTTY reports whether os.Stdout is attached to a
// terminal. Overridable for tests so the LiveLine integration can be exercised
// with both TTY-enabled and disabled paths.
var workflowParallelStdoutIsTTY = func() bool {
	return term.IsTerminal(os.Stdout.Fd())
}

// newWorkflowParallelLiveLine constructs the LiveLine used by
// runParallelGroup. The production implementation picks os.Stdout when stdout
// is a TTY, io.Discard otherwise; tests override it to inject a LiveLine
// writing to a buffer with test hooks installed so block-row state can be
// inspected without touching the real terminal.
var newWorkflowParallelLiveLine = func(workflowID string) *liveui.LiveLine {
	termOut := io.Writer(io.Discard)
	isTTY := workflowParallelStdoutIsTTY()
	if isTTY {
		termOut = os.Stdout
	}
	live := liveui.NewLiveLine(termOut, os.Stdout, isTTY)
	live.SetText(fmt.Sprintf("parallel: %s", workflowID))
	return live
}

// ErrWorkflowNestedParallel is returned when a workflow containing a
// `parallel:` block is invoked from another parallel context (pipeline or
// workflow). Only one LiveBlock owner is allowed per terminal.
var ErrWorkflowNestedParallel = errors.New("nested workflow parallel block is not supported in v1")

// ErrConfirmInsideParallel is returned when an interactive confirmation is
// reached inside a parallel group. Preflight catches direct cases; this
// sentinel catches transitive cases (workflow containing a confirm step or
// referencing a confirmation: true command from within a parallel sub-step).
var ErrConfirmInsideParallel = errors.New("interactive confirmation is not allowed inside a parallel group")

// WorkflowRunner executes type=workflow commands by running each step in sequence.
//
// Each step is either a command reference or a confirm prompt:
//   - Command steps resolve the referenced command from the registry, merge `with`
//     param overrides, and dispatch to the appropriate runner.
//   - Confirm steps prompt the user for confirmation before continuing.  In
//     non-interactive mode (DEVBOX_NONINTERACTIVE=1) confirm steps are skipped
//     (treated as auto-confirmed).
//
// Private commands may be referenced from workflow steps.
type WorkflowRunner struct{}

// Run executes the workflow described by rc.
func (r *WorkflowRunner) Run(ctx context.Context, rc RunContext) error {
	if rc.Registry == nil {
		return fmt.Errorf("workflow runner: registry is required but not set in RunContext")
	}

	if rc.UnderParallel {
		for i, step := range rc.Cmd.Steps {
			if step.Parallel != nil {
				return fmt.Errorf("%w: workflow %q step[%d]", ErrWorkflowNestedParallel, rc.Cmd.ID, i)
			}
		}
	}

	total := len(rc.Cmd.Steps)
	for i, step := range rc.Cmd.Steps {
		if step.When != "" {
			ok, err := tpl.EvalCommandCondition(step.When, rc.Render, rc.ProjectRoot)
			if err != nil {
				wrapped := fmt.Errorf("workflow %q step[%d]: %w", rc.Cmd.ID, i, err)
				fireOnStepStart(rc, i, total, step)
				fireOnStepEnd(rc, i, step, StepResult{Status: StepStatusFailed, Err: wrapped})
				return wrapped
			}
			if !ok {
				_, _ = fmt.Fprintf(runio.StderrOf(rc), "  ◎ workflow %q step[%d]: skipped (when: %s)\n",
					rc.Cmd.ID, i, step.When)
				fireOnStepEnd(rc, i, step, StepResult{
					Status:     StepStatusSkipped,
					SkipReason: "when: " + step.When,
				})
				continue
			}
		}

		switch {
		case step.Parallel != nil:
			stepStart := time.Now()
			// Iteration-scoped closure mirrors the default case: suspend the
			// outer observer's LiveLine for the duration of the inner parallel
			// group's LiveLine. Without this, two LiveLines write ANSI cursor
			// sequences to os.Stdout concurrently (outer = snapshot observer
			// ticker, inner = runParallelGroup ticker), causing visible flicker
			// and interleaved frames on TTY. The deferred ResumeAfterExec pairs
			// with this iteration's SuspendForExec regardless of error path.
			err := func() error {
				fireOnStepStart(rc, i, total, step)
				suspender, hasSuspend := rc.StepObserver.(StepIOSuspender)
				if hasSuspend {
					suspender.SuspendForExec()
					defer suspender.ResumeAfterExec()
				}
				return r.runParallelGroup(ctx, rc, step.Parallel, i)
			}()
			if err != nil {
				fireOnStepEnd(rc, i, step, StepResult{
					Status:   StepStatusFailed,
					Duration: time.Since(stepStart),
					Err:      err,
				})
				if step.ContinueOnError {
					_, _ = fmt.Fprintf(runio.StderrOf(rc), "  ⚠ workflow %q step[%d] parallel: continue_on_error: %v\n",
						rc.Cmd.ID, i, err)
					continue
				}
				return err
			}
			fireOnStepEnd(rc, i, step, StepResult{
				Status:   StepStatusDone,
				Duration: time.Since(stepStart),
			})
		case step.Confirm != "":
			stepStart := time.Now()
			fireOnStepStart(rc, i, total, step)
			if err := r.runConfirmStep(rc, step.Confirm); err != nil {
				wrapped := fmt.Errorf("workflow %q step[%d] confirm: %w", rc.Cmd.ID, i, err)
				fireOnStepEnd(rc, i, step, StepResult{
					Status:   StepStatusFailed,
					Duration: time.Since(stepStart),
					Err:      wrapped,
				})
				return wrapped
			}
			fireOnStepEnd(rc, i, step, StepResult{
				Status:   StepStatusDone,
				Duration: time.Since(stepStart),
			})
		default:
			gateSkip, gateReason, gateErr := evalSubStepOverrideGate(rc, step)
			if gateErr != nil {
				wrapped := fmt.Errorf("workflow %q step[%d] %q: %w", rc.Cmd.ID, i, step.Command, gateErr)
				fireOnStepStart(rc, i, total, step)
				fireOnStepEnd(rc, i, step, StepResult{Status: StepStatusFailed, Err: wrapped})
				return wrapped
			}
			if gateSkip {
				_, _ = fmt.Fprintf(runio.StderrOf(rc), "  ◎ workflow %q step[%d] %q: skipped (%s)\n",
					rc.Cmd.ID, i, step.Command, gateReason)
				fireOnStepEnd(rc, i, step, StepResult{
					Status:     StepStatusSkipped,
					SkipReason: gateReason,
				})
				continue
			}

			stepStart := time.Now()
			// Iteration-scoped closure so the deferred ResumeAfterExec pairs
			// with this iteration's SuspendForExec regardless of error or
			// panic; using `defer` directly inside the loop would only fire
			// at function return and leave the live UI suspended across the
			// remaining steps.
			err := func() error {
				fireOnStepStart(rc, i, total, step)
				suspender, hasSuspend := rc.StepObserver.(StepIOSuspender)
				if hasSuspend {
					suspender.SuspendForExec()
					defer suspender.ResumeAfterExec()
				}
				return r.runCommandStep(ctx, rc, i, step)
			}()
			if err != nil {
				fireOnStepEnd(rc, i, step, StepResult{
					Status:   StepStatusFailed,
					Duration: time.Since(stepStart),
					Err:      err,
				})
				if step.ContinueOnError {
					_, _ = fmt.Fprintf(runio.StderrOf(rc), "  ⚠ workflow %q step[%d] %q: continue_on_error: %v\n",
						rc.Cmd.ID, i, step.Command, err)
					continue
				}
				return err
			}
			fireOnStepEnd(rc, i, step, StepResult{
				Status:   StepStatusDone,
				Duration: time.Since(stepStart),
			})
		}
	}

	return nil
}
