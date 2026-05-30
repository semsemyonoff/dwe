package runtime

import (
	"devbox-cli/internal/core/usercommands/model"
	"devbox-cli/internal/core/usercommands/runtime/spec"
)

// StepStatus enumerates the terminal states a workflow step can finish in.
// Alias for spec.StepStatus.
type StepStatus = spec.StepStatus

// StepResult carries the outcome of one workflow step.
// Alias for spec.StepResult.
type StepResult = spec.StepResult

// WorkflowStepObserver receives lifecycle events for top-level sequential
// workflow steps. Alias for spec.WorkflowStepObserver.
type WorkflowStepObserver = spec.WorkflowStepObserver

// StepIOSuspender is an optional capability an observer can implement so the
// workflow runner can hide its live UI footer while a child process writes
// directly to the terminal. Alias for spec.StepIOSuspender.
type StepIOSuspender = spec.StepIOSuspender

const (
	// StepStatusDone indicates the step completed without error.
	StepStatusDone = spec.StepStatusDone
	// StepStatusFailed indicates the step returned an error (whether the
	// workflow then aborts or absorbs it via continue_on_error).
	StepStatusFailed = spec.StepStatusFailed
	// StepStatusSkipped indicates the step never ran (when: false or
	// files_gate override).
	StepStatusSkipped = spec.StepStatusSkipped
)

// fireOnStepStart calls the observer's OnStepStart hook when present.
func fireOnStepStart(rc RunContext, idx, total int, step model.WorkflowStep) {
	if rc.StepObserver != nil {
		rc.StepObserver.OnStepStart(idx, total, step)
	}
}

// fireOnStepEnd calls the observer's OnStepEnd hook when present.
func fireOnStepEnd(rc RunContext, idx int, step model.WorkflowStep, result StepResult) {
	if rc.StepObserver != nil {
		rc.StepObserver.OnStepEnd(idx, step, result)
	}
}
