package spec

import (
	"time"

	"github.com/semsemyonoff/devbox/internal/core/usercommands/model"
)

// StepStatus enumerates the terminal states a workflow step can finish in.
type StepStatus int

const (
	// StepStatusDone indicates the step completed without error.
	StepStatusDone StepStatus = iota
	// StepStatusFailed indicates the step returned an error (whether the
	// workflow then aborts or absorbs it via continue_on_error).
	StepStatusFailed
	// StepStatusSkipped indicates the step never ran (when: false or
	// files_gate override).
	StepStatusSkipped
)

// StepResult carries the outcome of one workflow step.
type StepResult struct {
	Status     StepStatus
	Duration   time.Duration
	Err        error  // populated when Status == StepStatusFailed
	SkipReason string // populated when Status == StepStatusSkipped
}

// WorkflowStepObserver receives lifecycle events for top-level sequential
// workflow steps. Parallel sub-step events are not surfaced here — a parallel
// block is a single step from the observer's point of view.
//
// Nil observer => observer calls are skipped entirely, preserving the
// pre-observer plain-stdout output.
type WorkflowStepObserver interface {
	OnStepStart(idx, total int, step model.WorkflowStep)
	OnStepEnd(idx int, step model.WorkflowStep, result StepResult)
}

// StepIOSuspender is an optional capability an observer can implement so the
// workflow runner can hide its live UI footer while a child process writes
// directly to the terminal. The runner type-asserts on this interface around
// each sequential command step; implementations must make both methods
// idempotent enough to compose with nested suspends from `huh` prompts (see
// the depth-counted bridge in the snapshot CLI observer).
type StepIOSuspender interface {
	SuspendForExec()
	ResumeAfterExec()
}
