package runtime

import (
	"errors"
	"fmt"
	"os"

	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/resolve"
)

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

// Run executes the workflow described by ctx.
func (r *WorkflowRunner) Run(ctx RunContext) error {
	if ctx.Registry == nil {
		return fmt.Errorf("workflow runner: registry is required but not set in RunContext")
	}

	for i, step := range ctx.Cmd.Steps {
		if step.When != "" {
			ok, err := tpl.EvalCommandCondition(step.When, ctx.Render, ctx.ProjectRoot)
			if err != nil {
				return fmt.Errorf("workflow %q step[%d]: %w", ctx.Cmd.ID, i, err)
			}
			if !ok {
				_, _ = fmt.Fprintf(stderr(ctx), "  ◎ workflow %q step[%d]: skipped (when: %s)\n",
					ctx.Cmd.ID, i, step.When)
				continue
			}
		}

		if step.Confirm != "" {
			if err := r.runConfirmStep(ctx, step.Confirm); err != nil {
				return fmt.Errorf("workflow %q step[%d] confirm: %w", ctx.Cmd.ID, i, err)
			}
			continue
		}

		if err := r.runCommandStep(ctx, i, step); err != nil {
			if step.ContinueOnError {
				_, _ = fmt.Fprintf(stderr(ctx), "  ⚠ workflow %q step[%d] %q: continue_on_error: %v\n",
					ctx.Cmd.ID, i, step.Command, err)
				continue
			}
			return err
		}
	}

	return nil
}

// runConfirmStep handles a confirm step.
func (r *WorkflowRunner) runConfirmStep(ctx RunContext, message string) error {
	if ctx.NonInteractive || isNonInteractive() {
		return nil
	}

	stdin := ctx.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	if ui.IsInteractiveFn(stdin) {
		confirmed, err := runConfirm(message, "Yes", "No")
		if err != nil {
			if errors.Is(err, ui.ErrCancelled) {
				return fmt.Errorf("aborted by user")
			}
			return err
		}
		if !confirmed {
			return fmt.Errorf("aborted by user")
		}
		return nil
	}

	if render.NewWriter(stdout(ctx)).Confirm(message, stdin) {
		return nil
	}
	return fmt.Errorf("aborted by user")
}

// runCommandStep resolves and executes a single command-reference step.
func (r *WorkflowRunner) runCommandStep(ctx RunContext, stepIdx int, step model.WorkflowStep) error {
	cmd, err := ctx.Registry.Get(step.Command)
	if err != nil {
		return fmt.Errorf("workflow %q step[%d]: %w", ctx.Cmd.ID, stepIdx, err)
	}

	provided := make(map[string]string, len(step.With))
	for k, v := range step.With {
		rendered, err := tpl.RenderCommand(v, ctx.Render)
		if err != nil {
			return fmt.Errorf("workflow %q step[%d]: render with[%q]: %w", ctx.Cmd.ID, stepIdx, k, err)
		}
		provided[k] = rendered
	}

	resolvedParams, err := resolve.Params(cmd.Params, provided, ctx.Config)
	if err != nil {
		return fmt.Errorf("workflow %q step[%d] %q: resolve params: %w",
			ctx.Cmd.ID, stepIdx, step.Command, err)
	}

	resolvedCtx, err := resolve.Context(cmd.Context, ctx.Config)
	if err != nil {
		return fmt.Errorf("workflow %q step[%d] %q: resolve context: %w",
			ctx.Cmd.ID, stepIdx, step.Command, err)
	}

	renderCtx := &tpl.RenderContext{
		Params:  resolvedParams,
		Context: resolvedCtx,
		Host:    tpl.CurrentHostInfo(),
	}
	if ctx.Config != nil {
		renderCtx.Raw = ctx.Config.Raw
	}

	subCtx := RunContext{
		Cmd:            cmd,
		Params:         resolvedParams,
		Context:        resolvedCtx,
		Render:         renderCtx,
		Config:         ctx.Config,
		DockerConfig:   ctx.DockerConfig,
		Registry:       ctx.Registry,
		ProjectRoot:    ctx.ProjectRoot,
		Stdout:         ctx.Stdout,
		Stderr:         ctx.Stderr,
		Stdin:          ctx.Stdin,
		SkipConfirm:    ctx.SkipConfirm,
		NonInteractive: ctx.NonInteractive,
	}

	if err := RunCommand(subCtx); err != nil {
		return fmt.Errorf("workflow %q step[%d] %q: %w", ctx.Cmd.ID, stepIdx, step.Command, err)
	}

	return nil
}

// isNonInteractive returns true when the DEVBOX_NONINTERACTIVE environment
// variable is set to "1" or "true".
func isNonInteractive() bool {
	v := os.Getenv("DEVBOX_NONINTERACTIVE")
	return v == "1" || v == "true"
}
