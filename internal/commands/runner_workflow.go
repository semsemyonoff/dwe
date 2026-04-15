package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"devbox-cli/internal/tpl"
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
		if step.Confirm != "" {
			if err := r.runConfirm(ctx, step.Confirm); err != nil {
				return fmt.Errorf("workflow %q step[%d] confirm: %w", ctx.Cmd.ID, i, err)
			}
			continue
		}

		if err := r.runCommandStep(ctx, i, step); err != nil {
			return err
		}
	}

	return nil
}

// runConfirm handles a confirm step.  In non-interactive mode the prompt is
// skipped (auto-confirmed).  Otherwise the user must enter "y" or "Y" to
// continue; any other input aborts the workflow.
func (r *WorkflowRunner) runConfirm(ctx RunContext, message string) error {
	if isNonInteractive() {
		return nil
	}

	out := stdout(ctx)
	_, _ = fmt.Fprintf(out, "%s [y/N] ", message)

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("aborted (no input)")
	}
	answer := strings.TrimSpace(scanner.Text())
	if answer != "y" && answer != "Y" {
		return fmt.Errorf("aborted by user")
	}
	return nil
}

// runCommandStep resolves and executes a single command-reference step.
func (r *WorkflowRunner) runCommandStep(ctx RunContext, stepIdx int, step WorkflowStep) error {
	cmd, err := ctx.Registry.Get(step.Command)
	if err != nil {
		return fmt.Errorf("workflow %q step[%d]: %w", ctx.Cmd.ID, stepIdx, err)
	}

	// Build params map from step-level `with` values.
	// Render ${...} templates in with: values using the parent render context so that
	// config references like "${db.database}" resolve to their actual values.
	provided := make(map[string]string, len(step.With))
	for k, v := range step.With {
		rendered, err := tpl.RenderCommand(v, ctx.Render)
		if err != nil {
			return fmt.Errorf("workflow %q step[%d]: render with[%q]: %w", ctx.Cmd.ID, stepIdx, k, err)
		}
		provided[k] = rendered
	}

	// Resolve params and context for the sub-command.
	resolvedParams, err := ResolveParams(cmd.Params, provided, ctx.Config)
	if err != nil {
		return fmt.Errorf("workflow %q step[%d] %q: resolve params: %w",
			ctx.Cmd.ID, stepIdx, step.Command, err)
	}

	resolvedCtx, err := ResolveContext(cmd.Context, ctx.Config)
	if err != nil {
		return fmt.Errorf("workflow %q step[%d] %q: resolve context: %w",
			ctx.Cmd.ID, stepIdx, step.Command, err)
	}

	// Build the render context for template interpolation.
	renderCtx := &tpl.RenderContext{
		Params:  resolvedParams,
		Context: resolvedCtx,
		Host:    tpl.CurrentHostInfo(),
	}
	if ctx.Config != nil {
		renderCtx.Raw = ctx.Config.Raw
	}

	subCtx := RunContext{
		Cmd:          cmd,
		Params:       resolvedParams,
		Context:      resolvedCtx,
		Render:       renderCtx,
		Config:       ctx.Config,
		DockerConfig: ctx.DockerConfig,
		Registry:     ctx.Registry,
		ProjectRoot:  ctx.ProjectRoot,
		Stdout:       ctx.Stdout,
		Stderr:       ctx.Stderr,
	}

	runner, err := NewRunner(cmd)
	if err != nil {
		return fmt.Errorf("workflow %q step[%d] %q: %w", ctx.Cmd.ID, stepIdx, step.Command, err)
	}

	if err := runner.Run(subCtx); err != nil {
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
