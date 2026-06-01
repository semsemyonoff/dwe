package workflow

import (
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
)

// Type and constant aliases for test files. Keeps the moved workflow tests
// readable with the same prefix-free names they used at the root package.
type (
	WorkflowRunner       = Runner
	RunContext           = spec.RunContext
	WorkflowStepObserver = spec.WorkflowStepObserver
	StepResult           = spec.StepResult
	StepStatus           = spec.StepStatus
	StepIOSuspender      = spec.StepIOSuspender

	CommandDef       = model.CommandDef
	CommandType      = model.CommandType
	FileSpec         = model.FileSpec
	ParamDef         = model.ParamDef
	WorkflowStep     = model.WorkflowStep
	WorkflowParallel = model.WorkflowParallel
	Registry         = registry.Registry
)

const (
	CommandTypeShell    = model.CommandTypeShell
	CommandTypeDwe   = model.CommandTypeDwe
	CommandTypeScript   = model.CommandTypeScript
	CommandTypeWorkflow = model.CommandTypeWorkflow
	FileAccessRead      = model.FileAccessRead
	FileAccessWrite     = model.FileAccessWrite
	ParamTypeString     = model.ParamTypeString

	StepStatusDone    = spec.StepStatusDone
	StepStatusFailed  = spec.StepStatusFailed
	StepStatusSkipped = spec.StepStatusSkipped
)

// newEmptyRegistry is a helper for tests so they don't need to import registry directly.
func newEmptyRegistry() *Registry {
	return registry.NewEmptyRegistry()
}

// buildWorkflowRegistry creates a Registry with the given commands pre-loaded
// without going through YAML files.
func buildWorkflowRegistry(cmds ...*CommandDef) *Registry {
	reg := newEmptyRegistry()
	for _, cmd := range cmds {
		reg.AddCommandForTest(cmd)
	}
	return reg
}
