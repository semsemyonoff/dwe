package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"devbox-cli/internal/core/usercommands/model"
	"devbox-cli/internal/core/usercommands/runtime/internal/runio"
	"devbox-cli/internal/core/usercommands/runtime/spec"
	"devbox-cli/internal/shared/liveui"
	"devbox-cli/internal/shared/render"
)

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
func (r *Runner) runParallelGroup(parentCtx context.Context, rc spec.RunContext, group *model.WorkflowParallel, _ int) error {
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

	if !rc.SkipConfirm && !rc.NonInteractive && !runio.IsNonInteractive() {
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
	//
	// isTTY mirrors the factory's decision so the post-Wait emit can suppress
	// per-sub-step status lines when the user already saw them in the live
	// block (TTY mode), or print them as the sole channel (non-TTY / CI).
	isTTY := workflowParallelStdoutIsTTY()
	live := newWorkflowParallelLiveLine(rc.Cmd.ID)
	live.Start()
	defer live.Stop()
	live.StartBlock(n)
	groupStart := time.Now()

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

			gateSkip, gateReason, gateErr := evalSubStepOverrideGate(rc, sub)
			if gateErr != nil {
				wrapped := fmt.Errorf("workflow sub-step %q: %w", sub.Command, gateErr)
				results[i].err = wrapped
				live.SetBlockRowFinal(i, liveui.BlockRowFailed,
					fmt.Sprintf("[%d/%d] Failed: %s", i+1, n, sub.Command))
				return emit(wrapped)
			}
			if gateSkip {
				results[i].skipped = true
				results[i].output = gateReason
				live.SetBlockRowFinal(i, liveui.BlockRowSkipped,
					fmt.Sprintf("[%d/%d] Skipped: %s (%s)", i+1, n, sub.Command, gateReason))
				return nil
			}

			live.SetBlockRowRunning(i, fmt.Sprintf("[%d/%d] %s", i+1, n, sub.Command))

			gRC := rc
			gRC.UnderParallel = true
			gRC.SkipNotify = true
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
			tee := liveui.NewLineTeePreserveANSI(func(frame string, final bool) {
				stripped := liveui.ANSIOnlyRe.ReplaceAllString(frame, "")
				live.SetBlockRowRunning(i,
					fmt.Sprintf("[%d/%d] %s: %s", i+1, n, sub.Command, stripped))
				if !final {
					return
				}
				buf.WriteString(frame)
				buf.WriteByte('\n')
				if subFile != nil {
					_, _ = fmt.Fprintln(subFile, stripped)
				}
			})

			gRC.Stdout = tee
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

	if groupErr == nil && parentCtx.Err() != nil {
		groupErr = parentCtx.Err()
	}

	live.EndBlock()
	live.Stop()

	emitStatus := !isTTY
	alwaysShowOutput := group.AlwaysShowOutput
	failures := 0
	for i, res := range results {
		switch {
		case res.cancelled:
			if emitStatus {
				_, _ = fmt.Fprintf(runio.StderrOf(rc), "  ◎ [%d/%d] Cancelled: %s\n", i+1, n, res.sub.Command)
			}
		case res.skipped:
			if emitStatus {
				reason := "when=false"
				if res.output != "" {
					reason = res.output
				}
				_, _ = fmt.Fprintf(runio.StderrOf(rc), "  ◎ [%d/%d] Skipped: %s (%s)\n", i+1, n, res.sub.Command, reason)
			}
		case res.err != nil && res.continueOnError:
			failures++
			if emitStatus {
				_, _ = fmt.Fprintf(runio.StderrOf(rc), "  ◎ [%d/%d] Failed (continue_on_error): %s\n", i+1, n, res.sub.Command)
			}
			dumpSubStepOutput(runio.StderrOf(rc), res.sub.Command, res.output)
		case res.err != nil:
			failures++
			if emitStatus {
				_, _ = fmt.Fprintf(runio.StderrOf(rc), "  ✗ [%d/%d] Failed: %s\n", i+1, n, res.sub.Command)
			}
			dumpSubStepOutput(runio.StderrOf(rc), res.sub.Command, res.output)
		default:
			if emitStatus {
				_, _ = fmt.Fprintf(runio.StderrOf(rc), "  ✓ [%d/%d] Done: %s\n", i+1, n, res.sub.Command)
			}
			if alwaysShowOutput {
				dumpSubStepOutput(runio.StderrOf(rc), res.sub.Command, res.output)
			}
		}
	}

	writeParallelSummary(runio.StderrOf(rc), rc.Cmd.ID, time.Since(groupStart), failures > 0, isTTY)

	return groupErr
}

// writeParallelSummary prints a single-line summary footer for a workflow
// parallel block, replacing the live header "parallel: <id>" with a final
// ✓/✗ glyph + elapsed time. Colors are ANSI-coded only when colored is true
// (TTY mode); non-TTY callers (CI / piped stdout) get a plain-text line so
// log scrapers do not see escape sequences.
func writeParallelSummary(w io.Writer, workflowID string, elapsed time.Duration, failed, colored bool) {
	icon := liveui.IconDone
	color := render.Green
	if failed {
		icon = liveui.IconFailed
		color = render.Red
	}
	if !colored {
		_, _ = fmt.Fprintf(w, "%s [%s] parallel: %s\n", icon, liveui.FormatElapsed(elapsed), workflowID)
		return
	}
	elapsedText := render.Gray + "[" + liveui.FormatElapsed(elapsed) + "]" + render.Reset
	_, _ = fmt.Fprintf(w, "%s%s%s %s parallel: %s\n", color, icon, render.Reset, elapsedText, workflowID)
}
