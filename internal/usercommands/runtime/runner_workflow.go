package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"

	"golang.org/x/sync/errgroup"

	"devbox-cli/internal/liveui"
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

// Run executes the workflow described by rc.
func (r *WorkflowRunner) Run(ctx context.Context, rc RunContext) error {
	if rc.Registry == nil {
		return fmt.Errorf("workflow runner: registry is required but not set in RunContext")
	}

	for i, step := range rc.Cmd.Steps {
		if step.When != "" {
			ok, err := tpl.EvalCommandCondition(step.When, rc.Render, rc.ProjectRoot)
			if err != nil {
				return fmt.Errorf("workflow %q step[%d]: %w", rc.Cmd.ID, i, err)
			}
			if !ok {
				_, _ = fmt.Fprintf(stderr(rc), "  ◎ workflow %q step[%d]: skipped (when: %s)\n",
					rc.Cmd.ID, i, step.When)
				continue
			}
		}

		switch {
		case step.Parallel != nil:
			if err := r.runParallelGroup(ctx, rc, step.Parallel, i); err != nil {
				if step.ContinueOnError {
					_, _ = fmt.Fprintf(stderr(rc), "  ⚠ workflow %q step[%d] parallel: continue_on_error: %v\n",
						rc.Cmd.ID, i, err)
					continue
				}
				return err
			}
		case step.Confirm != "":
			if err := r.runConfirmStep(rc, step.Confirm); err != nil {
				return fmt.Errorf("workflow %q step[%d] confirm: %w", rc.Cmd.ID, i, err)
			}
		default:
			if err := r.runCommandStep(ctx, rc, i, step); err != nil {
				if step.ContinueOnError {
					_, _ = fmt.Fprintf(stderr(rc), "  ⚠ workflow %q step[%d] %q: continue_on_error: %v\n",
						rc.Cmd.ID, i, step.Command, err)
					continue
				}
				return err
			}
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
func (r *WorkflowRunner) runCommandStep(ctx context.Context, rc RunContext, stepIdx int, step model.WorkflowStep) error {
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

	renderCtx := &tpl.RenderContext{
		Params:  resolvedParams,
		Context: resolvedCtx,
		Host:    tpl.CurrentHostInfo(),
	}
	if rc.Config != nil {
		renderCtx.Raw = rc.Config.Raw
	}

	subCtx := RunContext{
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
	}

	if err := RunCommand(ctx, subCtx); err != nil {
		return fmt.Errorf("workflow %q step[%d] %q: %w", rc.Cmd.ID, stepIdx, step.Command, err)
	}

	return nil
}

// isNonInteractive returns true when the DEVBOX_NONINTERACTIVE environment
// variable is set to "1" or "true".
func isNonInteractive() bool {
	v := os.Getenv("DEVBOX_NONINTERACTIVE")
	return v == "1" || v == "true"
}

// evalWorkflowStepWhen evaluates a workflow sub-step's `when:` expression.
// Shared by the main sequential loop and parallel preflight so the predicate
// runs through one path. Side-effectful predicates (`cmd:` shells) MUST be
// evaluated exactly once per group execution — parallel preflight caches the
// result for the goroutine to consume.
func evalWorkflowStepWhen(expr string, rc RunContext) (bool, error) {
	return tpl.EvalCommandCondition(expr, rc.Render, rc.ProjectRoot)
}

// subResult collects the outcome of one parallel sub-step. Each goroutine
// writes to its own results[i] index; the post-Wait emit pass reads sequentially.
type subResult struct {
	sub     model.WorkflowStep
	err     error
	output  string
	skipped bool
}

// runParallelGroup executes a workflow parallel sub-step group concurrently.
//
// Phase 1 — Preflight (sequential):
//   - evaluate each sub-step's `when:` exactly once; cache the decision
//   - for non-skipped sub-steps, reject confirmation-required commands when
//     SkipConfirm/NonInteractive are not set (UX preflight; Task 6 adds the
//     transitive-confirmation runtime guard)
//
// Phase 2 — Concurrent execution (errgroup with SetLimit):
//   - per-goroutine RunContext clone with UnderParallel=true, isolated
//     Stdout/Stderr routed through a LineTee that captures frames into a
//     per-sub-step buffer AND writes them to a per-sub-step log file
//   - `fail_fast: true` (default): first error cancels siblings via ctx
//   - `fail_fast: false`: errors aggregate via errors.Join
//
// Phase 3 — Post-Wait emit (sequential, no race):
//   - iterate results in order; write ✓/✗/◎ status lines to rc.Stderr; for
//     failed sub-steps print captured output between separator bars.
func (r *WorkflowRunner) runParallelGroup(parentCtx context.Context, rc RunContext, group *model.WorkflowParallel, _ int) error {
	n := len(group.Steps)
	maxC := group.MaxConcurrent
	if maxC <= 0 {
		if cpu := runtime.NumCPU(); cpu < n {
			maxC = cpu
		} else {
			maxC = n
		}
	}
	failFast := true
	if group.FailFast != nil {
		failFast = *group.FailFast
	}

	type subDecision struct {
		skip bool
		err  error
	}
	whenDecisions := make([]subDecision, n)
	for i, sub := range group.Steps {
		if sub.When == "" {
			continue
		}
		ok, err := evalWorkflowStepWhen(sub.When, rc)
		whenDecisions[i] = subDecision{skip: !ok, err: err}
	}

	if !rc.SkipConfirm && !rc.NonInteractive && !isNonInteractive() {
		for i, sub := range group.Steps {
			d := whenDecisions[i]
			if d.err != nil || d.skip {
				continue
			}
			def, err := rc.Registry.Get(sub.Command)
			if err != nil {
				return fmt.Errorf("parallel preflight: %w", err)
			}
			if def.Confirmation {
				return fmt.Errorf("parallel sub-step %q requires confirmation; rerun with --yes or set DEVBOX_NONINTERACTIVE=1", sub.Command)
			}
		}
	}

	eg, gctx := errgroup.WithContext(parentCtx)
	eg.SetLimit(maxC)

	var (
		errsMu  sync.Mutex
		errs    []error
		results = make([]subResult, n)
	)

	emit := func(wrapped error) error {
		if failFast {
			return wrapped
		}
		errsMu.Lock()
		errs = append(errs, wrapped)
		errsMu.Unlock()
		return nil
	}

	workflowID := sanitizeWorkflowFS(rc.Cmd.ID)

	for i, sub := range group.Steps {
		eg.Go(func() error {
			results[i].sub = sub

			d := whenDecisions[i]
			if d.err != nil {
				wrapped := fmt.Errorf("workflow sub-step %q: when: %w", sub.Command, d.err)
				results[i].err = wrapped
				return emit(wrapped)
			}
			if d.skip {
				results[i].skipped = true
				return nil
			}

			gRC := rc
			gRC.UnderParallel = true

			subFile, _, openErr := openWorkflowSubStepLog(rc.ProjectRoot, workflowID, sub.Command, rc.ProjectRoot != "")
			if openErr != nil {
				wrapped := fmt.Errorf("workflow sub-step %q: open log: %w", sub.Command, openErr)
				results[i].err = wrapped
				return emit(wrapped)
			}
			if subFile != nil {
				defer func() { _ = subFile.Close() }()
			}

			var buf bytes.Buffer
			tee := liveui.NewLineTee(func(frame string, _ bool) {
				buf.WriteString(frame)
				buf.WriteByte('\n')
				if subFile != nil {
					_, _ = fmt.Fprintln(subFile, frame)
				}
			})

			gRC.Stdout = &liveui.ANSIOnlyStripper{W: tee}
			gRC.Stderr = gRC.Stdout

			err := r.runCommandStep(gctx, gRC, i, sub)
			tee.Flush()
			results[i].output = buf.String()

			if err != nil {
				wrapped := fmt.Errorf("workflow sub-step %q: %w", sub.Command, err)
				results[i].err = wrapped
				if sub.ContinueOnError {
					return nil
				}
				return emit(wrapped)
			}
			return nil
		})
	}

	var groupErr error
	if failFast {
		groupErr = eg.Wait()
	} else {
		_ = eg.Wait()
		if len(errs) > 0 {
			groupErr = errors.Join(errs...)
		}
	}

	for i, res := range results {
		switch {
		case res.skipped:
			_, _ = fmt.Fprintf(stderr(rc), "  ◎ [%d/%d] Skipped: %s (when=false)\n", i+1, n, res.sub.Command)
		case res.err != nil:
			_, _ = fmt.Fprintf(stderr(rc), "  ✗ [%d/%d] Failed: %s\n", i+1, n, res.sub.Command)
			_, _ = fmt.Fprintln(stderr(rc), "  ───── output ─────")
			_, _ = fmt.Fprint(stderr(rc), res.output)
			_, _ = fmt.Fprintln(stderr(rc), "  ──────────────────")
		default:
			_, _ = fmt.Fprintf(stderr(rc), "  ✓ [%d/%d] Done: %s\n", i+1, n, res.sub.Command)
		}
	}

	return groupErr
}
