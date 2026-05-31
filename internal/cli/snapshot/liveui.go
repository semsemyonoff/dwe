package snapshot

import (
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/semsemyonoff/devbox/internal/core/ui/widgets"
	"github.com/semsemyonoff/devbox/internal/core/usercommands/model"
	"github.com/semsemyonoff/devbox/internal/core/usercommands/runtime"
	snapshotpkg "github.com/semsemyonoff/devbox/internal/core/workflow/snapshot"
	"github.com/semsemyonoff/devbox/internal/shared/liveui"
	"github.com/semsemyonoff/devbox/internal/shared/render"
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

// snapshotTimestampLayout matches pipeline/plain.go so the visual style of
// snapshot output is identical to deploy/lifecycle pipeline output.
const snapshotTimestampLayout = "06-01-02 15:04:05"

// newSnapshotLiveObserver builds the live-UI observer for one snapshot
// workflow. Returns nil (disabled) when the live UI cannot render — non-TTY
// stdout or the caller opted out via --no-live. A nil result drives the
// runner's nil-observer path so plain stdout is produced.
//
// Lifecycle: the snapshot package owns `defer obs.Close()` (see ExecParams /
// CreateParams / RestoreParams / RemoveParams.StepObserverFactory). The
// command layer never references the observer after handing the factory off.
func newSnapshotLiveObserver(label string, disabled bool, steps []model.WorkflowStep) snapshotpkg.StepObserverCloser {
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
		total:  len(steps),
		now:    time.Now,
	}
	live.Start()
	// Pipe huh-prompt before/after hooks through the depth-counted bridge so
	// any prompt that fires mid-workflow (whether from a confirm: step or from
	// a command-level ConfirmCommand) composes with the StepIOSuspender pause
	// already active around the step.
	widgets.SetHuhHooks(o.pause, o.resume)
	return o
}

// snapshotStepLabel synthesises a one-line label for the step's status line.
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
// events as persistent log lines above a single-line live footer — matching
// the PlainReporter sequential rendering used by `deploy run` / lifecycle
// pipelines. Implements runtime.WorkflowStepObserver, runtime.StepIOSuspender,
// and snapshotpkg.StepObserverCloser.
type snapshotLiveObserver struct {
	live   *liveui.LiveLine
	labels []string
	total  int
	now    func() time.Time
	// pauseDepth counts active pause requests from any source (StepIOSuspender
	// invocations, huh-prompt before/after hooks). Only the 0→1 transition
	// invokes live.Pause(); only the 1→0 transition invokes live.Resume().
	// Atomic because liveui.LiveLine.tickLoop runs on its own goroutine.
	pauseDepth atomic.Int32
}

// Compile-time interface assertions: break the build if either contract drifts.
var (
	_ runtime.WorkflowStepObserver   = (*snapshotLiveObserver)(nil)
	_ runtime.StepIOSuspender        = (*snapshotLiveObserver)(nil)
	_ snapshotpkg.StepObserverCloser = (*snapshotLiveObserver)(nil)
)

// timestampPrefix returns the gray "[YY-MM-DD HH:MM:SS] " prefix matching
// PlainReporter.timestampPrefix.
func (o *snapshotLiveObserver) timestampPrefix() string {
	return fmt.Sprintf("%s[%s]%s ", render.Gray, o.now().Format(snapshotTimestampLayout), render.Reset)
}

// emit writes one persistent log line above the live footer using the same
// color+timestamp shape as PlainReporter.emit.
func (o *snapshotLiveObserver) emit(color, msg string) {
	o.live.Println(fmt.Sprintf("%s%s%s%s", o.timestampPrefix(), color, msg, render.Reset))
}

func (o *snapshotLiveObserver) OnStepStart(idx, _ int, _ model.WorkflowStep) {
	if idx < 0 || idx >= len(o.labels) {
		return
	}
	label := o.labels[idx]
	o.emit(render.Blue, fmt.Sprintf("  %s [%d/%d] %s", liveui.IconRunning, idx+1, o.total, label))
	o.live.SetText(fmt.Sprintf("[%d/%d] %s", idx+1, o.total, label))
}

func (o *snapshotLiveObserver) OnStepEnd(idx int, _ model.WorkflowStep, result runtime.StepResult) {
	if idx < 0 || idx >= len(o.labels) {
		return
	}
	label := o.labels[idx]
	elapsed := liveui.FormatElapsed(result.Duration)
	switch result.Status {
	case runtime.StepStatusDone:
		o.emit(render.Green, fmt.Sprintf("  %s [%d/%d] Done: %s (%s)", liveui.IconDone, idx+1, o.total, label, elapsed))
	case runtime.StepStatusFailed:
		o.emit(render.Red, fmt.Sprintf("  %s [%d/%d] Failed: %s (%s)", liveui.IconFailed, idx+1, o.total, label, elapsed))
		if result.Err != nil {
			o.emit(render.Red, "    "+result.Err.Error())
		}
	case runtime.StepStatusSkipped:
		reason := result.SkipReason
		if reason == "" {
			reason = "skipped"
		}
		o.emit(render.Yellow, fmt.Sprintf("  %s [%d/%d] Skipped: %s (%s)", liveui.IconSkipped, idx+1, o.total, label, reason))
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
	if v := o.pauseDepth.Add(-1); v == 0 {
		o.live.Resume()
	} else if v < 0 {
		o.pauseDepth.Store(0)
	}
}

// Close releases the observer: clear huh hooks first (so any future prompt
// elsewhere in the process does not call back into a stopped LiveLine), then
// stop the live ticker.
func (o *snapshotLiveObserver) Close() {
	widgets.ClearHuhHooks()
	o.live.Stop()
}
