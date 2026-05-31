package runtime

import (
	"github.com/semsemyonoff/devbox/internal/core/usercommands/model"
	"github.com/semsemyonoff/devbox/internal/core/usercommands/registry"
	"github.com/semsemyonoff/devbox/internal/core/usercommands/resolve"
	"github.com/semsemyonoff/devbox/internal/shared/tpl"
)

// type aliases for test files that reference model/registry types without package prefix
type (
	CommandDef       = model.CommandDef
	CommandFile      = model.CommandFile
	CommandMessages  = model.CommandMessages
	CommandType      = model.CommandType
	ContextDef       = model.ContextDef
	ExecMode         = model.ExecMode
	FileAccess       = model.FileAccess
	FileCandidate    = model.FileCandidate
	FileOnError      = model.FileOnError
	FileSort         = model.FileSort
	FileSpec         = model.FileSpec
	GroupMeta        = model.GroupMeta
	ParamDef         = model.ParamDef
	RunnerDef        = model.RunnerDef
	ScriptDef        = model.ScriptDef
	UserMode         = model.UserMode
	WorkflowStep     = model.WorkflowStep
	WorkflowParallel = model.WorkflowParallel
	GroupNode        = registry.GroupNode
	Registry         = registry.Registry
)

const (
	CommandTypeShell        = model.CommandTypeShell
	CommandTypeDevbox       = model.CommandTypeDevbox
	CommandTypeScript       = model.CommandTypeScript
	CommandTypeServiceExec  = model.CommandTypeServiceExec
	CommandTypeServiceRun   = model.CommandTypeServiceRun
	CommandTypeWorkflow     = model.CommandTypeWorkflow
	ExecModeExec            = model.ExecModeExec
	ExecModeRun             = model.ExecModeRun
	ExecModeExecOrRun       = model.ExecModeExecOrRun
	FileAccessRead          = model.FileAccessRead
	FileAccessWrite         = model.FileAccessWrite
	FileAccessReadWrite     = model.FileAccessReadWrite
	FileOnErrorRemove       = model.FileOnErrorRemove
	FileOnErrorKeep         = model.FileOnErrorKeep
	FileSortModtimeAsc      = model.FileSortModtimeAsc
	FileSortModtimeDesc     = model.FileSortModtimeDesc
	FileSortNameAsc         = model.FileSortNameAsc
	FileSortNameDesc        = model.FileSortNameDesc
	ParamTypeString         = model.ParamTypeString
	ParamTypeBool           = model.ParamTypeBool
	ParamTypeInt            = model.ParamTypeInt
	UserModeCurrent         = model.UserModeCurrent
	UserModeRoot            = model.UserModeRoot
	UserModeInternal        = model.UserModeInternal
	DefaultConfirmationText = model.DefaultConfirmationText
)

// newEmptyRegistry is a helper for tests so they don't need to import registry directly.
func newEmptyRegistry() *Registry {
	return registry.NewEmptyRegistry()
}

// BuildEnv is exposed for test files that test file-path resolution and env building together.
func BuildEnv(cmd *CommandDef, params map[string]any, ctx map[string]any, files map[string]tpl.ResolvedFile) (map[string]string, error) {
	return resolve.BuildEnv(cmd, params, ctx, files)
}
