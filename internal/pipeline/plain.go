package pipeline

import (
	"fmt"

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
	w *render.Writer
}

// NewPlainReporter creates a PlainReporter that writes to w.
func NewPlainReporter(w *render.Writer) *PlainReporter {
	return &PlainReporter{w: w}
}

// StartPipeline is a no-op for PlainReporter; the current deploy output does
// not print a pipeline header.
func (r *PlainReporter) StartPipeline(_ string, _ int) {}

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
//	  Skipping phase <phaseKey> (<reason>)
func (r *PlainReporter) SkipPhase(phaseKey string, _ config.DeployPhase, reason string) {
	r.w.Warning(fmt.Sprintf("  Skipping phase %s (%s)", phaseKey, reason))
}

// StartStep prints the step-start info line:
//
//	  [N/M] <stepAddr>[: <description>]
func (r *PlainReporter) StartStep(stepAddr string, step config.DeployStep, index int, total int) {
	label := stepAddr
	if step.Description != "" {
		label += ": " + step.Description
	}
	r.w.Info(fmt.Sprintf("  [%d/%d] %s", index, total, label))
}

// SkipStep prints a warning when a step is skipped due to a when condition:
//
//	  [N/M] Skipped: <stepAddr> (<reason>)
func (r *PlainReporter) SkipStep(stepAddr string, _ config.DeployStep, index int, total int, reason string) {
	r.w.Warning(fmt.Sprintf("  [%d/%d] Skipped: %s (%s)", index, total, stepAddr, reason))
}

// FinishStep prints a success line when a step completes:
//
//	  [N/M] Done: <stepAddr>
func (r *PlainReporter) FinishStep(stepAddr string, _ config.DeployStep, index int, total int) {
	r.w.Success(fmt.Sprintf("  [%d/%d] Done: %s", index, total, stepAddr))
}

// FailStep prints error lines when a step fails:
//
//	Deploy failed at step "<stepAddr>"
//	  <error message>
func (r *PlainReporter) FailStep(stepAddr string, _ config.DeployStep, _ int, _ int, err error) {
	r.w.Error(fmt.Sprintf("Deploy failed at step %q", stepAddr))
	r.w.Error("  " + err.Error())
}

// FinishPipeline is a no-op for PlainReporter; callers print their own
// completion messages (e.g. log path) after the pipeline loop finishes.
func (r *PlainReporter) FinishPipeline(_ bool) {}

// SuspendForExec is a no-op for PlainReporter.
func (r *PlainReporter) SuspendForExec() {}

// ResumeAfterExec is a no-op for PlainReporter.
func (r *PlainReporter) ResumeAfterExec() {}
