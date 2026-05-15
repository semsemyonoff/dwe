// Package runtime contains runners and the RunContext for command execution.
package runtime

import (
	"fmt"
	"io"
	"os"
	"slices"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/registry"
)

// Runner is the interface implemented by each command type executor.
// Run carries out the command described by ctx and reports any error.
type Runner interface {
	Run(ctx RunContext) error
}

// RunContext holds all data needed to execute a single command invocation.
type RunContext struct {
	// Cmd is the command definition being executed.
	Cmd *model.CommandDef

	// Params holds resolved parameter values keyed by param name.
	Params map[string]any

	// Context holds resolved context values keyed by context name.
	Context map[string]any

	// Render is the template evaluation context used for ${...} interpolation.
	// May be nil on entry; RunCommand defensive-inits it if needed.
	Render *tpl.RenderContext

	// Config is the merged devbox configuration.
	Config *config.DevboxConfig

	// DockerConfig is the Docker Compose execution policy. When set, service
	// runners use it to apply global args and project naming from the policy.
	// May be nil — runners fall back to config-only defaults.
	DockerConfig *config.DockerConfig

	// Registry is the loaded command registry, used by WorkflowRunner to look
	// up referenced commands. May be nil when workflows are not expected.
	Registry *registry.Registry

	// ProjectRoot is the absolute path to the devbox project root directory.
	// Runners use it to resolve relative cwd paths and script paths.
	ProjectRoot string

	// Stdout and Stderr receive process output. When nil, os.Stdout/os.Stderr
	// are used instead.
	Stdout io.Writer
	Stderr io.Writer

	// Stdin is the reader used for interactive prompts. When nil, os.Stdin is used.
	Stdin io.Reader

	// SkipConfirm, when true, skips confirmation prompts entirely.
	// Set by command-line flags or inherited from parent context.
	SkipConfirm bool

	// NonInteractive, when true, forces non-interactive code paths
	// regardless of TTY attachment (e.g., in workflow confirm steps and script env).
	NonInteractive bool
}

// stdinOrOS returns ctx.Stdin if set, otherwise os.Stdin.
func stdinOrOS(ctx RunContext) io.Reader {
	if ctx.Stdin != nil {
		return ctx.Stdin
	}
	return os.Stdin
}

// NewRunner returns the appropriate Runner implementation for the given command type.
// An error is returned for unknown command types.
func NewRunner(cmd *model.CommandDef) (Runner, error) {
	switch cmd.Type {
	case model.CommandTypeShell:
		return &HostRunner{}, nil
	case model.CommandTypeDevbox:
		return &DevboxRunner{}, nil
	case model.CommandTypeServiceExec:
		return &ServiceExecRunner{}, nil
	case model.CommandTypeServiceRun:
		return &ServiceRunRunner{}, nil
	case model.CommandTypeScript:
		return &ScriptRunner{}, nil
	case model.CommandTypeWorkflow:
		return &WorkflowRunner{}, nil
	case model.CommandTypeBuiltin:
		return &BuiltinRunner{}, nil
	default:
		return nil, &ErrUnsupportedType{Type: cmd.Type}
	}
}

// RunCommand executes a command definition, applying command-level pre-run
// behavior such as file preparation and confirmation prompts before dispatching
// to the concrete runner for the command type.
func RunCommand(ctx RunContext) error {
	if ctx.Render == nil {
		ctx.Render = &tpl.RenderContext{}
	}

	if ctx.Render.Raw == nil && ctx.Config != nil {
		ctx.Render.Raw = ctx.Config.Raw
	}

	if ctx.Render.Params == nil {
		ctx.Render.Params = make(map[string]any)
	}
	if ctx.Render.Context == nil {
		ctx.Render.Context = make(map[string]any)
	}

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		return err
	}

	ctx.Render.Files = paths

	if err := ConfirmCommand(ctx); err != nil {
		return err
	}

	cleanups, err := PrepareFileEffects(ctx, paths)
	if err != nil {
		return err
	}

	runner, err := NewRunner(ctx.Cmd)
	if err != nil {
		return err
	}

	if err := runner.Run(ctx); err != nil {
		for _, cleanup := range slices.Backward(cleanups) {
			cleanup()
		}
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
	Type model.CommandType
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
	c := &docker.Compose{
		Bin:         config.DockerBin(ctx.Config),
		CommandArgs: map[string][]string{},
	}
	if ctx.Config != nil {
		c.ProjectName = ctx.Config.Project.FullName()
		c.Files = ctx.Config.ComposeFiles()
	}
	return c
}
