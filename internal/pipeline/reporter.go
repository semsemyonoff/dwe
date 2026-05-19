// Package pipeline defines the Reporter interface for deploy/reset pipeline execution
// and provides the PlainReporter implementation.
package pipeline

import "devbox-cli/internal/config"

// Reporter receives lifecycle events from the deploy/reset pipeline executor
// and renders them to the terminal. The only implementation is PlainReporter,
// which produces line-by-line text output.
//
// Event ordering contract:
//
//	StartPipeline
//	  → (EnterPhase | SkipPhase)*
//	      → (StartStep → SuspendForExec → ResumeAfterExec → FinishStep|FailStep|SkipStep)*
//	FinishPipeline
//
// phaseKey is the display key for the phase: "<phase>" for orchestrator phases
// or "<service>/<phase>" for per-service phases.
//
// stepAddr is the full step address: "<phase>/<step>" or "<service>/<phase>/<step>".
type Reporter interface {
	// StartPipeline is called once before any phases execute.
	StartPipeline(name string, totalSteps int)

	// EnterPhase is called when a new phase begins.
	EnterPhase(phaseKey string, phase config.DeployPhase)

	// SkipPhase is called when an entire phase is skipped due to a when condition.
	// reason is a human-readable explanation (e.g. "when: dir-empty services/main").
	SkipPhase(phaseKey string, phase config.DeployPhase, reason string)

	// StartStep is called immediately before a step executes.
	StartStep(stepAddr string, step config.DeployStep, index int, total int)

	// SkipStep is called when a step is skipped due to a when condition.
	// reason is a human-readable explanation (e.g. "when: dir-empty services/main"
	// or "phase when: cmd: ./bin/devbox status").
	SkipStep(stepAddr string, step config.DeployStep, index int, total int, reason string)

	// FinishStep is called after a step completes successfully.
	FinishStep(stepAddr string, step config.DeployStep, index int, total int)

	// FailStep is called when a step returns an error.
	FailStep(stepAddr string, step config.DeployStep, index int, total int, err error)

	// FinishPipeline is called once after all phases complete.
	// success is false if any step failed.
	FinishPipeline(success bool)

	// SuspendForExec is called before an external child process takes the
	// terminal. PlainReporter is a no-op.
	SuspendForExec()

	// ResumeAfterExec is called after the external child process exits.
	// PlainReporter is a no-op.
	ResumeAfterExec()

	// StartGroup is called immediately before a parallel group launches its
	// sub-steps. subIndices is the contiguous block of per-sub-step tracked
	// indices reserved for the group (one per sub-step, in declaration order);
	// total is the pipeline-wide tracked-step total.
	//
	// Implementations MUST be safe for concurrent invocation: once a group is
	// started, StartStep / FinishStep / FailStep / SkipStep / SubStepOutput
	// events for sub-steps arrive from N goroutines.
	StartGroup(groupAddr string, group config.DeployStep, subIndices []int, total int)

	// FinishGroup is called after every sub-step of a parallel group has
	// completed (or been cancelled). success is false if any sub-step failed
	// (after accounting for continue_on_error).
	FinishGroup(groupAddr string, group config.DeployStep, success bool)

	// SubStepOutput streams a single output line from a parallel sub-step to
	// the reporter. Sequential pipelines never call this method.
	SubStepOutput(subAddr string, line string)
}
