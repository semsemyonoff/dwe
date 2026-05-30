// Package runtime contains runners and the RunContext for command execution.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"devbox-cli/internal/core/notify"
	"devbox-cli/internal/core/usercommands/model"
	"devbox-cli/internal/core/usercommands/runtime/internal/runio"
	"devbox-cli/internal/core/usercommands/runtime/spec"
	"devbox-cli/internal/shared/render"
	"devbox-cli/internal/shared/tpl"
)

// Runner is the interface implemented by each command type executor. It is
// an alias for spec.Runner so the runtime root package and its callers can
// continue to write runtime.Runner.
type Runner = spec.Runner

// RunContext holds all data needed to execute a single command invocation.
// Alias for spec.RunContext.
type RunContext = spec.RunContext

// FileProbeResult tracks the outcome of a single file probe.
// Alias for spec.FileProbeResult.
type FileProbeResult = spec.FileProbeResult

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
	// block entirely, avoiding the per-sub-step userpkg.Load.
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
	// Consult translator if available, falling back to the raw message
	if ctx.Translator != nil && ctx.Cmd != nil && ctx.Cmd.ID != "" {
		var translated string
		if success {
			translated = ctx.Translator.CommandSuccessMessage(ctx.Locale, ctx.Cmd.ID, "")
		} else {
			translated = ctx.Translator.CommandErrorMessage(ctx.Locale, ctx.Cmd.ID, "")
		}
		if translated != "" {
			message = translated
		}
	}

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
		render.NewWriter(runio.StdoutOf(ctx)).Success(message)
		return nil
	}
	render.NewWriter(runio.StderrOf(ctx)).Error(message)
	return nil
}

// ErrUnsupportedType is returned when no runner exists for a given command type.
type ErrUnsupportedType struct {
	Type model.CommandType
}

func (e *ErrUnsupportedType) Error() string {
	return "no runner for command type: " + string(e.Type)
}
