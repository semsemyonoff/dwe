package loader

import "github.com/semsemyonoff/dwe/internal/core/usercommands/model"

// type aliases for test files that reference model types without the model. prefix
type (
	CommandDef   = model.CommandDef
	CommandFile  = model.CommandFile
	FileSpec     = model.FileSpec
	GroupMeta    = model.GroupMeta
	ParamDef     = model.ParamDef
	ScriptDef    = model.ScriptDef
	WorkflowStep = model.WorkflowStep
)

const (
	CommandTypeShell       = model.CommandTypeShell
	CommandTypeScript      = model.CommandTypeScript
	CommandTypeServiceExec = model.CommandTypeServiceExec
	CommandTypeServiceRun  = model.CommandTypeServiceRun
	CommandTypeWorkflow    = model.CommandTypeWorkflow
	CommandTypeDwe         = model.CommandTypeDwe
	FileAccessWrite        = model.FileAccessWrite
	FileAccessRead         = model.FileAccessRead
	FileAccessReadWrite    = model.FileAccessReadWrite
	FileOnErrorRemove      = model.FileOnErrorRemove
	FileOnErrorKeep        = model.FileOnErrorKeep
	FileSortModtimeDesc    = model.FileSortModtimeDesc
	FileSortNameDesc       = model.FileSortNameDesc
	FileSortNameAsc        = model.FileSortNameAsc
)
