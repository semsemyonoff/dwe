// Package runtime contains runners and the RunContext for command execution.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/notify"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/registry"
)

// Runner is the interface implemented by each command type executor.
// Run carries out the command described by rc and reports any error. The
// supplied context.Context controls cancellation of any child processes
// spawned by the runner (via exec.CommandContext).
type Runner interface {
	Run(ctx context.Context, rc RunContext) error
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

	// UnderParallel is set when this RunContext executes a sub-step of a
	// parallel group (pipeline or workflow). Runners use it to reject
	// operations that need exclusive ownership of the terminal — interactive
	// confirmations and nested parallel blocks. Task 6 adds the guards;
	// Task 5 only propagates the flag.
	UnderParallel bool

	// SkipNotify suppresses the end-of-command desktop notification for
	// this invocation. Always set true when one runtime invokes another:
	// only the top-level user-invoked command should fire notifications.
	// The workflow runner, pipeline executor action dispatch, and any
	// future internal call site must set this to true on the inner
	// RunContext they build. Top-level entry points leave the zero value
	// (false), enabling notification when Cmd.Notify is true.
	SkipNotify bool

	// WorkflowSubStepOverrides carries pipeline-side per-sub-step directives
	// (currently files_gate) for the workflow being invoked. Set by the
	// pipeline executor when the originating DeployStep declares
	// sub_step_overrides; nil for ad-hoc workflow invocations. The workflow
	// runner consumes it inside both sequential and parallel dispatch paths.
	// Keys are sub-step names (StepName()); values are the matching override.
	WorkflowSubStepOverrides map[string]config.SubStepOverride
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

// ErrNilCmd is returned by RunCommand when rc.Cmd is nil. Real call sites
// resolve the CommandDef from the registry before invoking RunCommand, so
// a nil Cmd indicates a programmer error in a caller, not a runtime
// condition. The explicit error avoids a nil-deref panic deeper in the
// runner pipeline and lets tests assert the contract directly.
var ErrNilCmd = errors.New("runtime: RunCommand called with nil Cmd")

// RunCommand executes a command definition, applying command-level pre-run
// behavior such as file preparation and confirmation prompts before dispatching
// to the concrete runner for the command type. The supplied ctx is threaded
// through to the runner so child processes can be cancelled.
func RunCommand(ctx context.Context, rc RunContext) (err error) {
	if rc.Cmd == nil {
		return ErrNilCmd
	}
	if TestSnapshotRC != nil {
		TestSnapshotRC(rc)
	}
	if rc.Render == nil {
		rc.Render = &tpl.RenderContext{}
	}

	if rc.Render.Raw == nil && rc.Config != nil {
		rc.Render.Raw = rc.Config.Raw
	}

	if rc.Render.Params == nil {
		rc.Render.Params = make(map[string]any)
	}
	if rc.Render.Context == nil {
		rc.Render.Context = make(map[string]any)
	}

	// Conditional notifier install — only when this is the top-level user
	// invocation of a command opted into notifications. Workflow sub-steps
	// and pipeline-invoked commands have SkipNotify=true and skip this
	// block entirely, avoiding the per-sub-step userconfig.Load.
	if rc.Cmd != nil && rc.Cmd.Notify && !rc.SkipNotify {
		start := time.Now()
		var projectName string
		if rc.Config != nil {
			projectName = rc.Config.Project.Name
		}
		ucfg, ucfgErr := userconfigLoadFunc(rc.ProjectRoot)
		if ucfgErr != nil {
			slog.Warn("userconfig load failed; notifications disabled for this run", "err", ucfgErr)
			ucfg = nil
		}
		n := newNotifier(ucfg)
		cmdID := rc.Cmd.ID
		defer func() {
			// User explicitly declined the confirmation prompt — not a failure.
			if errors.As(err, new(*commandAbortedError)) {
				return
			}
			n.Notify(context.Background(), notify.Event{
				Kind:      notify.OpCommand,
				Operation: "command:" + cmdID,
				Outcome:   notify.OutcomeFromErr(err),
				Duration:  time.Since(start),
				Err:       err,
				Project:   projectName,
			})
		}()
	}

	paths, err := ComputeFilePaths(rc)
	if err != nil {
		return err
	}

	rc.Render.Files = paths

	if err := ConfirmCommand(rc); err != nil {
		return err
	}

	cleanups, err := PrepareFileEffects(rc, paths)
	if err != nil {
		return err
	}

	runner, err := NewRunner(rc.Cmd)
	if err != nil {
		return err
	}

	if err := runner.Run(ctx, rc); err != nil {
		for _, cleanup := range slices.Backward(cleanups) {
			cleanup()
		}
		if msgErr := emitCommandMessage(rc, rc.Cmd.Messages.Error, false); msgErr != nil {
			return fmt.Errorf("%w; render error message: %v", err, msgErr)
		}
		return err
	}

	if err := emitCommandMessage(rc, rc.Cmd.Messages.Success, true); err != nil {
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
