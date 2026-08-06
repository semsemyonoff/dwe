package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/resolve"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/internal/runio"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// runConfirm is the package-level wrapper for widgets.RunConfirm; swappable in tests.
var runConfirm = widgets.RunConfirm

// runConfirmStep handles a confirm step.
func (r *Runner) runConfirmStep(ctx spec.RunContext, message string) error {
	if ctx.SkipConfirm || ctx.NonInteractive || runio.IsNonInteractive() {
		return nil
	}

	if ctx.UnderParallel {
		return fmt.Errorf("%w: confirm step %q in workflow %q", ErrConfirmInsideParallel, message, ctx.Cmd.ID)
	}

	stdin := ctx.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	if widgets.IsInteractiveFn(stdin) {
		confirmed, err := runConfirm(message, "Yes", "No")
		if err != nil {
			if errors.Is(err, widgets.ErrCancelled) {
				return fmt.Errorf("aborted by user")
			}
			return err
		}
		if !confirmed {
			return fmt.Errorf("aborted by user")
		}
		return nil
	}

	if render.NewWriter(runio.StdoutOf(ctx)).Confirm(message, stdin) {
		return nil
	}
	return fmt.Errorf("aborted by user")
}

// runCommandStep resolves and executes a single command-reference step.
func (r *Runner) runCommandStep(ctx context.Context, rc spec.RunContext, stepIdx int, step model.WorkflowStep) error {
	cmd, err := rc.Registry.Get(step.Command)
	if err != nil {
		return fmt.Errorf("workflow %q step[%d]: %w", rc.Cmd.ID, stepIdx, err)
	}

	provided := make(map[string]string, len(step.With))
	for k, v := range step.With {
		rendered, err := tpl.RenderCommand(v, rc.Render)
		if err != nil {
			return fmt.Errorf("workflow %q step[%d]: render with[%q]: %w", rc.Cmd.ID, stepIdx, k, err)
		}
		provided[k] = rendered
	}

	resolvedParams, err := resolve.Params(cmd.Params, provided, rc.Config)
	if err != nil {
		return fmt.Errorf("workflow %q step[%d] %q: resolve params: %w",
			rc.Cmd.ID, stepIdx, step.Command, err)
	}

	resolvedCtx, err := resolve.Context(cmd.Context, rc.Config)
	if err != nil {
		return fmt.Errorf("workflow %q step[%d] %q: resolve context: %w",
			rc.Cmd.ID, stepIdx, step.Command, err)
	}

	// Args is deliberately not set here: this context is handed to RunCommand
	// with subCtx.Cmd = cmd, and RunCommand normalizes ${args} defaults for
	// every dispatcher in one place. A workflow step never supplies
	// pass-through arguments of its own.
	renderCtx := &tpl.RenderContext{
		Params:  resolvedParams,
		Context: resolvedCtx,
		Host:    tpl.CurrentHostInfo(),
	}
	if rc.Config != nil {
		renderCtx.Raw = rc.Config.Raw
	}
	if rc.Render != nil {
		renderCtx.Snapshot = rc.Render.Snapshot
		renderCtx.SnapshotScope = rc.Render.SnapshotScope
	}

	subCtx := spec.RunContext{
		Cmd:            cmd,
		Params:         resolvedParams,
		Context:        resolvedCtx,
		Render:         renderCtx,
		Config:         rc.Config,
		DockerConfig:   rc.DockerConfig,
		Registry:       rc.Registry,
		ProjectRoot:    rc.ProjectRoot,
		Stdout:         rc.Stdout,
		Stderr:         rc.Stderr,
		Stdin:          rc.Stdin,
		SkipConfirm:    rc.SkipConfirm,
		NonInteractive: rc.NonInteractive,
		UnderParallel:  rc.UnderParallel,
		// Transitive invocation: workflow sub-steps are never the
		// user's top-level command, so suppress notifications even if
		// the referenced CommandDef opted in via notify: true.
		SkipNotify: true,
		Translator: rc.Translator,
		Locale:     rc.Locale,
	}

	if err := RunCommandFn(ctx, subCtx); err != nil {
		return fmt.Errorf("workflow %q step[%d] %q: %w", rc.Cmd.ID, stepIdx, step.Command, err)
	}

	return nil
}

// evalWorkflowStepWhen evaluates a workflow sub-step's `when:` expression.
// Used by parallel preflight, which evaluates all `when:` conditions once
// before the goroutines start so that side-effectful shell predicates run
// exactly once per group execution regardless of concurrency.
func evalWorkflowStepWhen(expr string, rc spec.RunContext) (bool, error) {
	return tpl.EvalCommandCondition(expr, rc.Render, rc.ProjectRoot)
}
