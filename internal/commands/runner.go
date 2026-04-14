package commands

import (
	"io"

	"devbox-cli/internal/config"
	"devbox-cli/internal/tpl"
)

// Runner is the interface implemented by each command type executor.
// Run carries out the command described by ctx and reports any error.
type Runner interface {
	Run(ctx RunContext) error
}

// RunContext holds all data needed to execute a single command invocation.
type RunContext struct {
	// Cmd is the command definition being executed.
	Cmd *CommandDef

	// Params holds resolved parameter values keyed by param name.
	Params map[string]any

	// Context holds resolved context values keyed by context name.
	Context map[string]any

	// Render is the template evaluation context used for ${...} interpolation.
	Render *tpl.RenderContext

	// Config is the merged devbox configuration.
	Config *config.DevboxConfig

	// Registry is the loaded command registry, used by WorkflowRunner to look
	// up referenced commands. May be nil when workflows are not expected.
	Registry *Registry

	// ProjectRoot is the absolute path to the devbox project root directory.
	// Runners use it to resolve relative cwd paths and script paths.
	ProjectRoot string

	// Stdout and Stderr receive process output. When nil, os.Stdout/os.Stderr
	// are used instead.
	Stdout io.Writer
	Stderr io.Writer
}

// NewRunner returns the appropriate Runner implementation for the given command type.
// An error is returned for unknown command types.
func NewRunner(cmd *CommandDef) (Runner, error) {
	switch cmd.Type {
	case CommandTypeCommand:
		return &HostRunner{}, nil
	case CommandTypeServiceExec:
		return &ServiceExecRunner{}, nil
	case CommandTypeServiceRun:
		return &ServiceRunRunner{}, nil
	case CommandTypeScript:
		return &ScriptRunner{}, nil
	case CommandTypeWorkflow:
		return &WorkflowRunner{}, nil
	default:
		return nil, &ErrUnsupportedType{Type: cmd.Type}
	}
}

// ErrUnsupportedType is returned when no runner exists for a given command type.
type ErrUnsupportedType struct {
	Type CommandType
}

func (e *ErrUnsupportedType) Error() string {
	return "no runner for command type: " + string(e.Type)
}
