package pipeline

import (
	"fmt"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// PlainReporter implements Reporter with the same line-by-line text output
// that the deploy/reset runner produced before the reporter abstraction was
// introduced. Output format:
//
//	Phase: <phaseKey>[: <description>]
//	  [N/M] <stepAddr>[: <description>]
//	  [N/M] Done: <stepAddr>
//	  [N/M] Skipped: <stepAddr> (<reason>)
//	Deploy failed at step "<stepAddr>"
//	  <error message>
//
// SuspendForExec and ResumeAfterExec are no-ops: plain text output does not
// need to yield or reclaim the terminal.
type PlainReporter struct {
	w    *render.Writer
	name string // pipeline name set by StartPipeline (e.g. "deploy", "reset")
}

// NewPlainReporter creates a PlainReporter that writes to w.
func NewPlainReporter(w *render.Writer) *PlainReporter {
	return &PlainReporter{w: w}
}

// StartPipeline stores the pipeline name for use in failure messages. It does
// not print a header; the current deploy/reset output has no pipeline banner.
func (r *PlainReporter) StartPipeline(name string, _ int) {
	r.name = name
}

// EnterPhase prints the phase label line:
//
//	Phase: <phaseKey>[: <description>]
func (r *PlainReporter) EnterPhase(phaseKey string, phase config.DeployPhase) {
	label := "Phase: " + phaseKey
	if phase.Description != "" {
		label += ": " + phase.Description
	}
	r.w.Info(label)
}

// SkipPhase prints a warning when an entire phase is skipped:
//
//	Skipping phase <phaseKey> (<reason>)
func (r *PlainReporter) SkipPhase(phaseKey string, _ config.DeployPhase, reason string) {
	r.w.Warning(fmt.Sprintf("  Skipping phase %s (%s)", phaseKey, reason))
}

// StartStep prints the step-start info line:
//
//	[N/M] <stepAddr>[: <description>]
//
// For untracked steps (index == 0), the [N/M] counter is omitted.
func (r *PlainReporter) StartStep(stepAddr string, step config.DeployStep, index int, total int) {
	label := stepAddr
	if step.Description != "" {
		label += ": " + step.Description
	}
	if index > 0 {
		r.w.Info(fmt.Sprintf("  [%d/%d] %s", index, total, label))
	} else {
		r.w.Info(fmt.Sprintf("  %s", label))
	}
}

// SkipStep prints a warning when a step is skipped due to a when condition:
//
//	[N/M] Skipped: <stepAddr> (<reason>)
//
// For untracked steps (index == 0), the [N/M] counter is omitted.
func (r *PlainReporter) SkipStep(stepAddr string, _ config.DeployStep, index int, total int, reason string) {
	if index > 0 {
		r.w.Warning(fmt.Sprintf("  [%d/%d] Skipped: %s (%s)", index, total, stepAddr, reason))
	} else {
		r.w.Warning(fmt.Sprintf("  Skipped: %s (%s)", stepAddr, reason))
	}
}

// FinishStep prints a success line when a step completes:
//
//	[N/M] Done: <stepAddr>
//
// For untracked steps (index == 0), the [N/M] counter is omitted.
func (r *PlainReporter) FinishStep(stepAddr string, _ config.DeployStep, index int, total int) {
	if index > 0 {
		r.w.Success(fmt.Sprintf("  [%d/%d] Done: %s", index, total, stepAddr))
	} else {
		r.w.Success(fmt.Sprintf("  Done: %s", stepAddr))
	}
}

// FailStep prints error lines when a step fails:
//
//	<Name> failed at step "<stepAddr>"
//	  <error message>
//
// The label is derived from the pipeline name set by StartPipeline (e.g.
// "deploy" → "Deploy failed…", "reset" → "Reset failed…"). Falls back to
// "Pipeline" if StartPipeline was not called.
func (r *PlainReporter) FailStep(stepAddr string, _ config.DeployStep, _ int, _ int, err error) {
	label := r.name
	if label == "" {
		label = "pipeline"
	}
	label = strings.ToUpper(label[:1]) + label[1:]
	r.w.Error(fmt.Sprintf("%s failed at step %q", label, stepAddr))
	if err != nil {
		r.w.Error("  " + err.Error())
	}
}

// FinishPipeline is a no-op for PlainReporter; callers print their own
// completion messages (e.g. log path) after the pipeline loop finishes.
func (r *PlainReporter) FinishPipeline(_ bool) {}

// SuspendForExec is a no-op for PlainReporter.
func (r *PlainReporter) SuspendForExec() {}

// ResumeAfterExec is a no-op for PlainReporter.
func (r *PlainReporter) ResumeAfterExec() {}
