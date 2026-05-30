package workflow

import (
	"devbox-cli/internal/core/usercommands/model"
	"devbox-cli/internal/core/usercommands/runtime/spec"
)

// fireOnStepStart calls the observer's OnStepStart hook when present.
func fireOnStepStart(rc spec.RunContext, idx, total int, step model.WorkflowStep) {
	if rc.StepObserver != nil {
		rc.StepObserver.OnStepStart(idx, total, step)
	}
}

// fireOnStepEnd calls the observer's OnStepEnd hook when present.
func fireOnStepEnd(rc spec.RunContext, idx int, step model.WorkflowStep, result spec.StepResult) {
	if rc.StepObserver != nil {
		rc.StepObserver.OnStepEnd(idx, step, result)
	}
}
