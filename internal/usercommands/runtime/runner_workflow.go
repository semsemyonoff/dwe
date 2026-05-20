package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sync/errgroup"

	"devbox-cli/internal/liveui"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/resolve"
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
	if ctx.SkipConfirm || ctx.NonInteractive || isNonInteractive() {
		return nil
	}

	if ctx.UnderParallel {
		return fmt.Errorf("%w: confirm step %q in workflow %q", ErrConfirmInsideParallel, message, ctx.Cmd.ID)
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
// Used by parallel preflight, which evaluates all `when:` conditions once
// before the goroutines start so that side-effectful shell predicates run
// exactly once per group execution regardless of concurrency.
func evalWorkflowStepWhen(expr string, rc RunContext) (bool, error) {
	return tpl.EvalCommandCondition(expr, rc.Render, rc.ProjectRoot)
}

// subResult collects the outcome of one parallel sub-step. Each goroutine
// writes to its own results[i] index; the post-Wait emit pass reads sequentially.
type subResult struct {
	sub             model.WorkflowStep
	err             error
	output          string
	skipped         bool
	cancelled       bool // true when the sub-step never ran due to parent context cancellation
	continueOnError bool
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

	// LiveLine: when stdout is a TTY paint block rows directly to os.Stdout;
	// otherwise disable so the post-Wait text emit pass remains the sole output
	// channel (CI / piped invocations). All live.* methods are no-ops when
	// disabled, so the same code path covers both modes.
	live := newWorkflowParallelLiveLine(rc.Cmd.ID)
	live.Start()
	defer live.Stop()
	live.StartBlock(n)

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

			if gctx.Err() != nil {
				// Group already cancelled (fail_fast sibling failed or SIGINT).
				// Mark as cancelled so the post-Wait emit shows ◎ Cancelled
				// rather than ✓ Done (which would be misleading for work that
				// never ran). The initiating error is still reported by the
				// goroutine that caused the cancellation.
				results[i].cancelled = true
				live.SetBlockRowFinal(i, liveui.BlockRowSkipped,
					fmt.Sprintf("[%d/%d] Cancelled: %s", i+1, n, sub.Command))
				return nil
			}

			d := whenDecisions[i]
			if d.err != nil {
				wrapped := fmt.Errorf("workflow sub-step %q: when: %w", sub.Command, d.err)
				results[i].err = wrapped
				live.SetBlockRowFinal(i, liveui.BlockRowFailed,
					fmt.Sprintf("[%d/%d] Failed: %s", i+1, n, sub.Command))
				return emit(wrapped)
			}
			if d.skip {
				results[i].skipped = true
				live.SetBlockRowFinal(i, liveui.BlockRowSkipped,
					fmt.Sprintf("[%d/%d] Skipped: %s (when=false)", i+1, n, sub.Command))
				return nil
			}

			live.SetBlockRowRunning(i, fmt.Sprintf("[%d/%d] %s", i+1, n, sub.Command))

			gRC := rc
			gRC.UnderParallel = true
			// Never let parallel sub-steps read from shared stdin. Interactive
			// commands are rejected at plan time, but shell scripts or builtin
			// confirm (without SkipConfirm) could still block on an unexpected
			// read without this isolation.
			gRC.Stdin = strings.NewReader("")

			subFile, _, openErr := openWorkflowSubStepLog(rc.ProjectRoot, workflowID, sub.Command, rc.ProjectRoot != "")
			if openErr != nil {
				wrapped := fmt.Errorf("workflow sub-step %q: open log: %w", sub.Command, openErr)
				results[i].err = wrapped
				live.SetBlockRowFinal(i, liveui.BlockRowFailed,
					fmt.Sprintf("[%d/%d] Failed: %s", i+1, n, sub.Command))
				return emit(wrapped)
			}
			if subFile != nil {
				defer func() { _ = subFile.Close() }()
			}

			var buf bytes.Buffer
			tee := liveui.NewLineTee(func(frame string, final bool) {
				buf.WriteString(frame)
				buf.WriteByte('\n')
				if subFile != nil {
					_, _ = fmt.Fprintln(subFile, frame)
				}
				if final {
					live.SetBlockRowRunning(i,
						fmt.Sprintf("[%d/%d] %s: %s", i+1, n, sub.Command, frame))
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
					results[i].continueOnError = true
					live.SetBlockRowFinal(i, liveui.BlockRowSkipped,
						fmt.Sprintf("[%d/%d] Failed (continue_on_error): %s", i+1, n, sub.Command))
					return nil
				}
				live.SetBlockRowFinal(i, liveui.BlockRowFailed,
					fmt.Sprintf("[%d/%d] Failed: %s", i+1, n, sub.Command))
				return emit(wrapped)
			}
			live.SetBlockRowFinal(i, liveui.BlockRowDone,
				fmt.Sprintf("[%d/%d] Done: %s", i+1, n, sub.Command))
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

	// If no sub-step produced an error but the parent context was cancelled
	// (e.g. SIGINT before the group started, or a sibling pipeline step
	// triggering cancellation), surface that cancellation rather than
	// returning success. Sibling fail-fast errors are already captured in
	// groupErr by the errgroup, so this branch only fires when groupErr==nil.
	if groupErr == nil && parentCtx.Err() != nil {
		groupErr = parentCtx.Err()
	}

	// End the live block before the post-Wait emit so the ticker is no longer
	// active while we write to stderr. Deferring EndBlock would leave the
	// ticker running during the emit loop, causing cursor-sequence interleaving
	// on TTY terminals where os.Stdout and os.Stderr share the same fd.
	live.EndBlock()

	for i, res := range results {
		switch {
		case res.cancelled:
			_, _ = fmt.Fprintf(stderr(rc), "  ◎ [%d/%d] Cancelled: %s\n", i+1, n, res.sub.Command)
		case res.skipped:
			_, _ = fmt.Fprintf(stderr(rc), "  ◎ [%d/%d] Skipped: %s (when=false)\n", i+1, n, res.sub.Command)
		case res.err != nil && res.continueOnError:
			_, _ = fmt.Fprintf(stderr(rc), "  ◎ [%d/%d] Failed (continue_on_error): %s\n", i+1, n, res.sub.Command)
			if res.output != "" {
				_, _ = fmt.Fprintln(stderr(rc), "  ───── output ─────")
				_, _ = fmt.Fprint(stderr(rc), res.output)
				_, _ = fmt.Fprintln(stderr(rc), "  ──────────────────")
			}
		case res.err != nil:
			_, _ = fmt.Fprintf(stderr(rc), "  ✗ [%d/%d] Failed: %s\n", i+1, n, res.sub.Command)
			if res.output != "" {
				_, _ = fmt.Fprintln(stderr(rc), "  ───── output ─────")
				_, _ = fmt.Fprint(stderr(rc), res.output)
				_, _ = fmt.Fprintln(stderr(rc), "  ──────────────────")
			}
		default:
			_, _ = fmt.Fprintf(stderr(rc), "  ✓ [%d/%d] Done: %s\n", i+1, n, res.sub.Command)
		}
	}

	return groupErr
}
