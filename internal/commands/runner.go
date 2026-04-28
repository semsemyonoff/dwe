package commands

import (
	"fmt"
	"io"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/render"
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

	// DockerConfig is the Docker Compose execution policy. When set, service
	// runners use it to apply global args and project naming from the policy.
	// May be nil — runners fall back to config-only defaults.
	DockerConfig *config.DockerConfig

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

	// Stdin is the reader used for interactive prompts. When nil, os.Stdin is used.
	Stdin io.Reader
}

// NewRunner returns the appropriate Runner implementation for the given command type.
// An error is returned for unknown command types.
func NewRunner(cmd *CommandDef) (Runner, error) {
	switch cmd.Type {
	case CommandTypeCommand:
		return &HostRunner{}, nil
	case CommandTypeDevbox:
		return &DevboxRunner{}, nil
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

// RunCommand executes a command definition, applying command-level pre-run
// behavior such as confirmation prompts before dispatching to the concrete
// runner for the command type.
func RunCommand(ctx RunContext) error {
	if err := ConfirmCommand(ctx); err != nil {
		return err
	}
	runner, err := NewRunner(ctx.Cmd)
	if err != nil {
		return err
	}
	if err := runner.Run(ctx); err != nil {
		if msgErr := emitCommandMessage(ctx, ctx.Cmd.Messages.Error, false); msgErr != nil {
			return fmt.Errorf("%w; render error message: %v", err, msgErr)
		}
		return err
	}
	if err := emitCommandMessage(ctx, ctx.Cmd.Messages.Success, true); err != nil {
		return err
	}
	return nil
}

func emitCommandMessage(ctx RunContext, message string, success bool) error {
	if message == "" {
		return nil
	}
	if ctx.Render != nil {
		rendered, err := tpl.RenderCommand(message, ctx.Render)
		if err != nil {
			return err
		}
		message = rendered
	}
	if success {
		render.NewWriter(stdout(ctx)).Success(message)
		return nil
	}
	render.NewWriter(stderr(ctx)).Error(message)
	return nil
}

// ErrUnsupportedType is returned when no runner exists for a given command type.
type ErrUnsupportedType struct {
	Type CommandType
}

func (e *ErrUnsupportedType) Error() string {
	return "no runner for command type: " + string(e.Type)
}

// Compose builds a *docker.Compose from the context's config and docker policy.
// When DockerConfig is nil a minimal Compose is returned with just the project
// name and file list derived from the devbox config.
func (ctx RunContext) Compose() *docker.Compose {
	if ctx.DockerConfig != nil && ctx.Config != nil {
		return docker.NewCompose(ctx.Config, ctx.DockerConfig)
	}
	// Fallback: build a minimal Compose from config only.
	c := &docker.Compose{
		CommandArgs: map[string][]string{},
	}
	if ctx.Config != nil {
		c.ProjectName = ctx.Config.Project.FullName()
		c.Files = ctx.Config.ComposeFiles()
	}
	return c
}
