package resolve

import "devbox-cli/internal/usercommands/model"

// type aliases for test files that reference model types without the model. prefix
type (
	CommandDef   = model.CommandDef
	CommandFile  = model.CommandFile
	ContextDef   = model.ContextDef
	FileSpec     = model.FileSpec
	ParamDef     = model.ParamDef
	ScriptDef    = model.ScriptDef
	WorkflowStep = model.WorkflowStep
)

const (
	CommandTypeShell = model.CommandTypeShell
	CommandTypeScript  = model.CommandTypeScript
	ParamTypeString    = model.ParamTypeString
	ParamTypeBool      = model.ParamTypeBool
	ParamTypeInt       = model.ParamTypeInt
	ParamTypePath      = model.ParamTypePath
	FileAccessWrite    = model.FileAccessWrite
	FileAccessRead     = model.FileAccessRead
)
