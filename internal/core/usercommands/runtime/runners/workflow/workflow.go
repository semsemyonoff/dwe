// Package workflow implements the runtime Runner for type=workflow commands.
// It dispatches each step (command reference, confirmation prompt, or parallel
// group) through the recursive RunCommandFn seam wired by runtime root.
package workflow

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/internal/runio"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/liveui"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// RunCommandFn dispatches a leaf command through the runtime root's full
// pipeline (file paths, confirmation, runner dispatch, messages). The root
// runtime package wires it in init() to runtime.RunCommand. Allowing this
// indirection breaks the otherwise-circular dep between root and workflow.
//
// Tests that exercise workflow sub-step dispatch ensure this is wired by
// importing the root runtime package via an external test file.
var RunCommandFn func(ctx context.Context, rc spec.RunContext) error

// BuildRunContextFn constructs a RunContext for an inner command (used by
// the files_gate override probe path). Wired by runtime root init() to
// runtime.BuildRunContext.
var BuildRunContextFn func(
	cfg *config.DweConfig,
	reg *registry.Registry,
	def *model.CommandDef,
	with map[string]any,
	workDir string,
) (spec.RunContext, error)

// ComputeFilePathsProbeFn probes a subset of a command's declared files for
// presence. Wired by runtime root init() to runtime.ComputeFilePathsProbe.
var ComputeFilePathsProbeFn func(ctx spec.RunContext, only []string) (map[string]spec.FileProbeResult, error)

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

// ErrWorkflowNestedParallel is aliased from spec/ so workflow runner internals
// can reference it without prefix.
var ErrWorkflowNestedParallel = spec.ErrWorkflowNestedParallel

// ErrConfirmInsideParallel is aliased from spec/.
var ErrConfirmInsideParallel = spec.ErrConfirmInsideParallel

// Runner executes type=workflow commands by running each step in sequence.
//
// Each step is either a command reference or a confirm prompt:
//   - Command steps resolve the referenced command from the registry, merge `with`
//     param overrides, and dispatch through RunCommandFn.
//   - Confirm steps prompt the user for confirmation before continuing.  In
//     non-interactive mode (DWE_NONINTERACTIVE=1) confirm steps are skipped
//     (treated as auto-confirmed).
//
// Private commands may be referenced from workflow steps.
type Runner struct{}

// Run executes the workflow described by rc.
func (r *Runner) Run(ctx context.Context, rc spec.RunContext) error {
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
				fireOnStepEnd(rc, i, step, spec.StepResult{Status: spec.StepStatusFailed, Err: wrapped})
				return wrapped
			}
			if !ok {
				_, _ = fmt.Fprintf(runio.StderrOf(rc), "  ◎ workflow %q step[%d]: skipped (when: %s)\n",
					rc.Cmd.ID, i, step.When)
				fireOnStepEnd(rc, i, step, spec.StepResult{
					Status:     spec.StepStatusSkipped,
					SkipReason: "when: " + step.When,
				})
				continue
			}
		}

		switch {
		case step.Parallel != nil:
			stepStart := time.Now()
			err := func() error {
				fireOnStepStart(rc, i, total, step)
				suspender, hasSuspend := rc.StepObserver.(spec.StepIOSuspender)
				if hasSuspend {
					suspender.SuspendForExec()
					defer suspender.ResumeAfterExec()
				}
				return r.runParallelGroup(ctx, rc, step.Parallel, i)
			}()
			if err != nil {
				fireOnStepEnd(rc, i, step, spec.StepResult{
					Status:   spec.StepStatusFailed,
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
			fireOnStepEnd(rc, i, step, spec.StepResult{
				Status:   spec.StepStatusDone,
				Duration: time.Since(stepStart),
			})
		case step.Confirm != "":
			stepStart := time.Now()
			fireOnStepStart(rc, i, total, step)
			if err := r.runConfirmStep(rc, step.Confirm); err != nil {
				wrapped := fmt.Errorf("workflow %q step[%d] confirm: %w", rc.Cmd.ID, i, err)
				fireOnStepEnd(rc, i, step, spec.StepResult{
					Status:   spec.StepStatusFailed,
					Duration: time.Since(stepStart),
					Err:      wrapped,
				})
				return wrapped
			}
			fireOnStepEnd(rc, i, step, spec.StepResult{
				Status:   spec.StepStatusDone,
				Duration: time.Since(stepStart),
			})
		default:
			gateSkip, gateReason, gateErr := evalSubStepOverrideGate(rc, step)
			if gateErr != nil {
				wrapped := fmt.Errorf("workflow %q step[%d] %q: %w", rc.Cmd.ID, i, step.Command, gateErr)
				fireOnStepStart(rc, i, total, step)
				fireOnStepEnd(rc, i, step, spec.StepResult{Status: spec.StepStatusFailed, Err: wrapped})
				return wrapped
			}
			if gateSkip {
				_, _ = fmt.Fprintf(runio.StderrOf(rc), "  ◎ workflow %q step[%d] %q: skipped (%s)\n",
					rc.Cmd.ID, i, step.Command, gateReason)
				fireOnStepEnd(rc, i, step, spec.StepResult{
					Status:     spec.StepStatusSkipped,
					SkipReason: gateReason,
				})
				continue
			}

			stepStart := time.Now()
			err := func() error {
				fireOnStepStart(rc, i, total, step)
				suspender, hasSuspend := rc.StepObserver.(spec.StepIOSuspender)
				if hasSuspend {
					suspender.SuspendForExec()
					defer suspender.ResumeAfterExec()
				}
				return r.runCommandStep(ctx, rc, i, step)
			}()
			if err != nil {
				fireOnStepEnd(rc, i, step, spec.StepResult{
					Status:   spec.StepStatusFailed,
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
			fireOnStepEnd(rc, i, step, spec.StepResult{
				Status:   spec.StepStatusDone,
				Duration: time.Since(stepStart),
			})
		}
	}

	return nil
}
