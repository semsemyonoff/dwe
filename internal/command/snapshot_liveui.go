package command

import (
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"github.com/charmbracelet/x/term"

	"devbox-cli/internal/liveui"
	"devbox-cli/internal/snapshot"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/runtime"
)

// snapshotLiveOutputs picks the terminal-control writer (cursor ANSI / spinner
// frames) and the screen writer (data lines) for the snapshot live UI.
// Overridable for tests.
var snapshotLiveOutputs = func() (termOut io.Writer, screen io.Writer, isTTY bool) {
	isTTY = term.IsTerminal(os.Stdout.Fd())
	if isTTY {
		return os.Stdout, os.Stdout, true
	}
	return io.Discard, os.Stdout, false
}

// newSnapshotObserverLiveLine constructs the *liveui.LiveLine that the snapshot
// observer paints into. Exposed as a package-level var so tests can swap in a
// capturing writer (mirrors newWorkflowParallelLiveLine in runner_workflow.go).
var newSnapshotObserverLiveLine = func(workflowLabel string) *liveui.LiveLine {
	termOut, screen, isTTY := snapshotLiveOutputs()
	live := liveui.NewLiveLine(termOut, screen, isTTY)
	live.SetText(workflowLabel)
	return live
}

// newSnapshotLiveObserver builds the live-UI observer for one snapshot
// workflow. Returns nil (disabled) when the live UI cannot render — non-TTY
// stdout or the caller opted out via --no-live. A nil result drives the
// runner's nil-observer path so plain stdout is produced.
//
// Lifecycle: the snapshot package owns `defer obs.Close()` (see ExecParams /
// CreateParams / RestoreParams / RemoveParams.StepObserverFactory). The
// command layer never references the observer after handing the factory off.
func newSnapshotLiveObserver(label string, disabled bool, steps []model.WorkflowStep) snapshot.StepObserverCloser {
	if disabled {
		return nil
	}
	_, _, isTTY := snapshotLiveOutputs()
	if !isTTY {
		return nil
	}
	live := newSnapshotObserverLiveLine(label)

	labels := make([]string, len(steps))
	for i, s := range steps {
		labels[i] = snapshotStepLabel(s)
	}

	o := &snapshotLiveObserver{
		live:   live,
		labels: labels,
	}
	live.Start()
	live.StartBlock(len(steps))
	// Pipe huh-prompt before/after hooks through the depth-counted bridge so
	// any prompt that fires mid-workflow (whether from a confirm: step or from
	// a command-level ConfirmCommand) composes with the StepIOSuspender pause
	// already active around the step.
	ui.SetHuhHooks(o.pause, o.resume)
	return o
}

// snapshotStepLabel synthesises a one-line label for the live block row.
func snapshotStepLabel(s model.WorkflowStep) string {
	switch {
	case s.Parallel != nil:
		return fmt.Sprintf("parallel (%d sub-steps)", len(s.Parallel.Steps))
	case s.Confirm != "":
		return "confirm: " + s.Confirm
	case s.Name != "":
		return s.Name
	default:
		return s.Command
	}
}

// snapshotLiveObserver renders top-level sequential workflow step lifecycle
// events as a block of rows above a live footer. Implements
// runtime.WorkflowStepObserver, runtime.StepIOSuspender, and
// snapshot.StepObserverCloser.
type snapshotLiveObserver struct {
	live   *liveui.LiveLine
	labels []string
	// pauseDepth counts active pause requests from any source (StepIOSuspender
	// invocations, huh-prompt before/after hooks). Only the 0→1 transition
	// invokes live.Pause(); only the 1→0 transition invokes live.Resume().
	// Atomic because liveui.LiveLine.tickLoop runs on its own goroutine.
	pauseDepth atomic.Int32
}

// Compile-time interface assertions: break the build if either contract drifts.
var (
	_ runtime.WorkflowStepObserver = (*snapshotLiveObserver)(nil)
	_ runtime.StepIOSuspender      = (*snapshotLiveObserver)(nil)
	_ snapshot.StepObserverCloser  = (*snapshotLiveObserver)(nil)
)

func (o *snapshotLiveObserver) OnStepStart(idx, _ int, _ model.WorkflowStep) {
	if idx < 0 || idx >= len(o.labels) {
		return
	}
	o.live.SetBlockRowRunning(idx, o.labels[idx])
}

func (o *snapshotLiveObserver) OnStepEnd(idx int, _ model.WorkflowStep, result runtime.StepResult) {
	if idx < 0 || idx >= len(o.labels) {
		return
	}
	label := o.labels[idx]
	switch result.Status {
	case runtime.StepStatusDone:
		o.live.SetBlockRowFinal(idx, liveui.BlockRowDone, label)
	case runtime.StepStatusFailed:
		msg := label
		if result.Err != nil {
			msg = label + ": " + result.Err.Error()
		}
		o.live.SetBlockRowFinal(idx, liveui.BlockRowFailed, msg)
	case runtime.StepStatusSkipped:
		msg := label
		if result.SkipReason != "" {
			msg = label + " — " + result.SkipReason
		}
		o.live.SetBlockRowFinal(idx, liveui.BlockRowSkipped, msg)
	}
}

// SuspendForExec hides the live footer for the duration of a child process.
func (o *snapshotLiveObserver) SuspendForExec() { o.pause() }

// ResumeAfterExec repaints the live footer after a child process returns.
func (o *snapshotLiveObserver) ResumeAfterExec() { o.resume() }

func (o *snapshotLiveObserver) pause() {
	if o.pauseDepth.Add(1) == 1 {
		o.live.Pause()
	}
}

func (o *snapshotLiveObserver) resume() {
	if o.pauseDepth.Add(-1) == 0 {
		o.live.Resume()
	}
}

// Close releases the observer: clear huh hooks first (so any future prompt
// elsewhere in the process does not call back into a stopped LiveLine), then
// end the block, then stop the live ticker.
func (o *snapshotLiveObserver) Close() {
	ui.ClearHuhHooks()
	o.live.EndBlock()
	o.live.Stop()
}
