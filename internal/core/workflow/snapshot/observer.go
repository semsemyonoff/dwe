package snapshot

import (
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime"
)

// StepObserverCloser bundles a workflow step observer with a Close method so
// the snapshot package can own teardown of the live UI it constructs. The
// runner only sees the embedded WorkflowStepObserver methods; Close is owned
// by the snapshot package (the sole owner of `defer obs.Close()`).
type StepObserverCloser interface {
	runtime.WorkflowStepObserver
	Close()
}

// StepObserverFactory builds a per-workflow observer from the workflow's step
// list. The factory is invoked AFTER SelectWorkflow and AFTER any pre-workflow
// confirmation prompts, so the observer's live UI never overlaps a huh prompt.
// A nil factory disables the observer (preserves the pre-observer plain
// stdout). A factory that returns nil also disables the observer (used by the
// CLI layer when stdout is not a TTY or --no-live is set).
type StepObserverFactory func(steps []model.WorkflowStep) StepObserverCloser
