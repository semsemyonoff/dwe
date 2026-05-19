package pipeline

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// Icons used in step output lines.
const (
	iconDone    = "✓"
	iconFailed  = "✗"
	iconSkipped = "◎"
	iconRunning = "·"
)

// timestampLayout is the per-line clock prefix format (YY-MM-DD HH:MM:SS).
const timestampLayout = "06-01-02 15:04:05"

// PlainReporter implements Reporter with line-by-line text output.
// Every emitted line is prefixed with a gray "[YY-MM-DD HH:MM:SS] "
// timestamp followed by the colored message.
//
// Output format:
//
//	[ts] Phase: <phaseKey>[: <description>]
//	[ts]   · [N/M] <stepAddr>[: <description>]
//	[ts]   ✓ [N/M] Done: <stepAddr>
//	[ts]   ◎ [N/M] Skipped: <stepAddr> (<reason>)
//	[ts] ✗ Deploy failed at step "<stepAddr>"
//	[ts]   <error message>
//	[ts] ✓ Done (1m 23s)
//
// SuspendForExec and ResumeAfterExec are no-ops: plain text output does not
// need to yield or reclaim the terminal.
type PlainReporter struct {
	mu        sync.Mutex // guards every write to w and any future shared state
	w         *render.Writer
	name      string           // pipeline name set by StartPipeline (e.g. "deploy", "reset")
	startTime time.Time        // recorded by StartPipeline for elapsed time in FinishPipeline
	now       func() time.Time // injectable clock; defaults to time.Now
}

// NewPlainReporter creates a PlainReporter that writes to w.
func NewPlainReporter(w *render.Writer) *PlainReporter {
	return &PlainReporter{w: w, now: time.Now}
}

// StartPipeline stores the pipeline name and records the start time for
// elapsed time reporting. It does not print a header; the current deploy/reset
// output has no pipeline banner.
func (r *PlainReporter) StartPipeline(name string, _ int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.name = name
	r.startTime = r.now()
}

// EnterPhase prints the phase label line:
//
//	[ts] Phase: <phaseKey>[: <description>]
//
// Untracked phases produce no output.
func (r *PlainReporter) EnterPhase(phaseKey string, phase config.DeployPhase) {
	if phase.Untracked {
		return
	}
	label := "Phase: " + phaseKey
	if phase.Description != "" {
		label += ": " + phase.Description
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emit(render.Blue, label)
}

// SkipPhase prints a warning when an entire phase is skipped:
//
//	[ts] Skipping phase <phaseKey> (<reason>)
//
// Untracked phases produce no output.
func (r *PlainReporter) SkipPhase(phaseKey string, phase config.DeployPhase, reason string) {
	if phase.Untracked {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emit(render.Yellow, fmt.Sprintf("  Skipping phase %s (%s)", phaseKey, reason))
}

// StartStep prints the step-start info line:
//
//	[ts]   · [N/M] <stepAddr>[: <description>]
//
// Untracked steps (index == 0, total == 0) produce no output.
func (r *PlainReporter) StartStep(stepAddr string, step config.DeployStep, index int, total int) {
	if index == 0 && total == 0 {
		return
	}
	label := stepAddr
	if step.Description != "" {
		label += ": " + step.Description
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if index > 0 {
		r.emit(render.Blue, fmt.Sprintf("  %s [%d/%d] %s", iconRunning, index, total, label))
	} else {
		r.emit(render.Blue, fmt.Sprintf("  %s %s", iconRunning, label))
	}
}

// SkipStep prints a warning when a step is skipped due to a when condition:
//
//	[ts]   ◎ [N/M] Skipped: <stepAddr> (<reason>)
//
// Untracked steps (index == 0, total == 0) produce no output.
func (r *PlainReporter) SkipStep(stepAddr string, _ config.DeployStep, index int, total int, reason string) {
	if index == 0 && total == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if index > 0 {
		r.emit(render.Yellow, fmt.Sprintf("  %s [%d/%d] Skipped: %s (%s)", iconSkipped, index, total, stepAddr, reason))
	} else {
		r.emit(render.Yellow, fmt.Sprintf("  %s Skipped: %s (%s)", iconSkipped, stepAddr, reason))
	}
}

// FinishStep prints a success line when a step completes:
//
//	[ts]   ✓ [N/M] Done: <stepAddr>
//
// Untracked steps (index == 0, total == 0) produce no output.
func (r *PlainReporter) FinishStep(stepAddr string, _ config.DeployStep, index int, total int) {
	if index == 0 && total == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if index > 0 {
		r.emit(render.Green, fmt.Sprintf("  %s [%d/%d] Done: %s", iconDone, index, total, stepAddr))
	} else {
		r.emit(render.Green, fmt.Sprintf("  %s Done: %s", iconDone, stepAddr))
	}
}

// FailStep prints error lines when a step fails:
//
//	[ts] ✗ <Name> failed at step "<stepAddr>"
//	[ts]   <error message>
//
// The label is derived from the pipeline name set by StartPipeline (e.g.
// "deploy" → "Deploy failed…", "reset" → "Reset failed…"). Falls back to
// "Pipeline" if StartPipeline was not called.
func (r *PlainReporter) FailStep(stepAddr string, _ config.DeployStep, _ int, _ int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	label := r.name
	if label == "" {
		label = "pipeline"
	}
	label = strings.ToUpper(label[:1]) + label[1:]
	r.emit(render.Red, fmt.Sprintf("%s %s failed at step %q", iconFailed, label, stepAddr))
	if err != nil {
		r.emit(render.Red, "  "+err.Error())
	}
}

// FinishPipeline prints a Done message with elapsed time on success:
//
//	[ts] ✓ Done (1m 23s)
//
// On failure it is silent; the failure is already reported by FailStep.
func (r *PlainReporter) FinishPipeline(success bool) {
	if !success {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	elapsed := formatElapsed(r.now().Sub(r.startTime))
	_, _ = fmt.Fprintf(r.w.Writer(), "%s%s %s Done%s %s(%s)%s\n",
		r.timestampPrefix(),
		render.Green, iconDone, render.Reset,
		render.Gray, elapsed, render.Reset,
	)
}

// emit writes a single line with the timestamp prefix and a colored body.
// Format: "<gray>[ts]<reset> <color>msg<reset>\n".
func (r *PlainReporter) emit(color, msg string) {
	_, _ = fmt.Fprintf(r.w.Writer(), "%s%s%s%s\n", r.timestampPrefix(), color, msg, render.Reset)
}

// timestampPrefix returns the gray "[YY-MM-DD HH:MM:SS] " prefix for the
// current line. Always ends with a trailing space.
func (r *PlainReporter) timestampPrefix() string {
	return fmt.Sprintf("%s[%s]%s ", render.Gray, r.now().Format(timestampLayout), render.Reset)
}

// formatElapsed formats a duration as a human-readable elapsed time string.
// Examples: "5s", "1m 23s", "2h 5m".
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// SuspendForExec is a no-op for PlainReporter.
func (r *PlainReporter) SuspendForExec() {}

// ResumeAfterExec is a no-op for PlainReporter.
func (r *PlainReporter) ResumeAfterExec() {}

// StartGroup prints a single header line announcing a parallel group:
//
//	[ts]   · Parallel group: <groupAddr> (<n> steps)
//
// Per-sub-step lifecycle events still flow through StartStep / FinishStep /
// FailStep / SkipStep; this method only signals the group boundary. Task 8/9
// will replace this stub with buffered / live rendering.
func (r *PlainReporter) StartGroup(groupAddr string, group config.DeployStep, subIndices []int, _ int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	label := groupAddr
	if group.Description != "" {
		label += ": " + group.Description
	}
	r.emit(render.Blue, fmt.Sprintf("  %s Parallel group: %s (%d steps)", iconRunning, label, len(subIndices)))
}

// FinishGroup prints a one-line footer for a parallel group. success is true
// when every sub-step succeeded (after accounting for continue_on_error).
func (r *PlainReporter) FinishGroup(groupAddr string, _ config.DeployStep, success bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if success {
		r.emit(render.Green, fmt.Sprintf("  %s Parallel group done: %s", iconDone, groupAddr))
	} else {
		r.emit(render.Red, fmt.Sprintf("  %s Parallel group failed: %s", iconFailed, groupAddr))
	}
}

// SubStepOutput is a no-op in the Task 5 stub. Task 8/9 will route per-sub-step
// output through this method into buffered/live displays.
func (r *PlainReporter) SubStepOutput(_ string, _ string) {}
