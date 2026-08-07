// Package runtime contains runners and the RunContext for command execution.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/notify"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/internal/runio"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/runners/builtin"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/runners/host"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/runners/script"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/runners/service"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/runners/workflow"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// Workflow runner callback wiring. The workflow subpackage holds three
// function-var seams (RunCommandFn / BuildRunContextFn /
// ComputeFilePathsProbeFn) so it can dispatch sub-steps and probe files_gate
// overrides without importing this root package. Wiring lives in init().
//
//nolint:gochecknoinits // breaks runtime↔workflow import cycle
func init() {
	workflow.RunCommandFn = RunCommand
	workflow.BuildRunContextFn = BuildRunContext
	workflow.ComputeFilePathsProbeFn = ComputeFilePathsProbe
}

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

// Concrete-runner type aliases. External callers (the `usercommands.HostRunner`
// etc. re-export chain in `internal/core/usercommands/usercommands.go`) continue
// to write `runtime.HostRunner`, `runtime.WorkflowRunner`, … . Subpackage tests
// also resolve their own concrete type from these aliases when needed.
type (
	// HostRunner runs type=shell commands on the host machine.
	HostRunner = host.Runner
	// DweRunner runs type=dwe commands by re-invoking the dwe CLI.
	DweRunner = host.DweRunner
	// ServiceExecRunner runs type=service commands via `docker compose exec`.
	ServiceExecRunner = service.ExecRunner
	// ServiceRunRunner runs type=service commands via `docker compose run --rm`.
	ServiceRunRunner = service.RunRunner
	// ScriptRunner runs type=script commands.
	ScriptRunner = script.Runner
	// BuiltinRunner runs type=builtin commands by dispatching to the engine
	// builtin registry.
	BuiltinRunner = builtin.Runner
	// WorkflowRunner runs type=workflow commands by dispatching each step.
	WorkflowRunner = workflow.Runner
)

// StepStatus enumerates the terminal states a workflow step can finish in.
// Alias for spec.StepStatus so external callers keep writing runtime.StepStatus.
type StepStatus = spec.StepStatus

// StepResult carries the outcome of one workflow step. Alias for spec.StepResult.
type StepResult = spec.StepResult

// WorkflowStepObserver receives lifecycle events for top-level sequential
// workflow steps. Alias for spec.WorkflowStepObserver.
type WorkflowStepObserver = spec.WorkflowStepObserver

// StepIOSuspender is an optional capability an observer can implement so the
// workflow runner can hide its live UI footer while a child process writes
// directly to the terminal. Alias for spec.StepIOSuspender.
type StepIOSuspender = spec.StepIOSuspender

const (
	// StepStatusDone indicates the step completed without error.
	StepStatusDone = spec.StepStatusDone
	// StepStatusFailed indicates the step returned an error.
	StepStatusFailed = spec.StepStatusFailed
	// StepStatusSkipped indicates the step never ran (when: false or files_gate override).
	StepStatusSkipped = spec.StepStatusSkipped
)

// ErrWorkflowNestedParallel is returned when a workflow containing a
// `parallel:` block is invoked from another parallel context. Aliased from
// spec/ so external callers continue to test against runtime.ErrWorkflowNestedParallel.
var ErrWorkflowNestedParallel = spec.ErrWorkflowNestedParallel

// ErrConfirmInsideParallel is returned when an interactive confirmation is
// reached inside a parallel group. Aliased from spec/.
var ErrConfirmInsideParallel = spec.ErrConfirmInsideParallel

// NewRunner returns the appropriate Runner implementation for the given command type.
// An error is returned for unknown command types.
func NewRunner(cmd *model.CommandDef) (Runner, error) {
	switch cmd.Type {
	case model.CommandTypeShell:
		return &host.Runner{}, nil
	case model.CommandTypeDwe:
		return &host.DweRunner{}, nil
	case model.CommandTypeServiceExec:
		return &service.ExecRunner{}, nil
	case model.CommandTypeServiceRun:
		return &service.RunRunner{}, nil
	case model.CommandTypeScript:
		return &script.Runner{}, nil
	case model.CommandTypeWorkflow:
		return &WorkflowRunner{}, nil
	case model.CommandTypeBuiltin:
		return &builtin.Runner{}, nil
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
//
// Hidden gate: when rc.Cmd.Hidden is true (resolved by reg.ApplyVisibility),
// the command is treated as if it does not exist — the runner is not
// invoked, no notifier fires, and a single stderr line records the skip.
// This is the central enforcement point for the `hide:` contract; every
// dispatcher (workflow runner, pipeline executor, reset hooks, validate
// checks, direct invocation) inherits it without per-site duplication.
// Returns nil so callers see a clean "no-op" — the same shape as a
// when:false skip at the workflow level.
func RunCommand(ctx context.Context, rc RunContext) (err error) {
	if rc.Cmd == nil {
		return ErrNilCmd
	}
	if rc.Cmd.Hidden {
		if w := rc.Stderr; w != nil {
			_, _ = fmt.Fprintf(w, "  ◎ command %q: skipped (hidden)\n", rc.Cmd.ID)
		}
		return nil
	}
	if TestSnapshotRC != nil {
		TestSnapshotRC(rc)
	}
	normalizeRenderContext(&rc)

	// Set when argv_append_from produced nothing to process. The notifier defer
	// below reads it: the command did not do the work it would report on, so it
	// is suppressed exactly like a declined confirmation.
	var skippedEmptyAppend bool

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
			// Nothing to process — the command never ran, so there is no
			// outcome to report. (err is nil here, so without this the user
			// would get a "success" notification for work that did not happen.)
			if skippedEmptyAppend {
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
		// argv_append_from produced nothing: the command is skipped, not
		// failed. File effects are rolled back above for the same reason they
		// are on the error path — nothing consumed them. See
		// spec.ErrArgvAppendEmpty for the full contract.
		if errors.Is(err, spec.ErrArgvAppendEmpty) {
			skippedEmptyAppend = true
			_, _ = fmt.Fprintf(runio.StderrOf(rc), "  ◎ command %q: skipped (nothing to process)\n", rc.Cmd.ID)
			return nil
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

// normalizeRenderContext fills in the render-context defaults every dispatcher
// depends on, at the single point every dispatcher passes through.
//
// Doing this per-call-site is what review caught: several dispatchers build
// their RenderContext inline rather than via BuildRunContext (the workflow
// sub-step path builds a fresh one; pipeline actions, reset hooks and validate
// checks go through the constructor), so a per-site rule invites exactly one of
// them to be forgotten — and for ${args} the symptom is silent. A command
// declaring `argv: [go, test, "${args}"]` with args.default ["./..."] renders
// as `go test` on the path that missed it, testing the current directory
// instead of the module: a different command, not a visible failure.
//
// Recomputing the args default over an already-resolved value is idempotent —
// the CLI's Resolve(userArgs) is non-nil whenever the caller supplied anything,
// and Resolve(nil) yields that same default otherwise.
func normalizeRenderContext(rc *RunContext) {
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
	if rc.Render.Args == nil && rc.Cmd != nil {
		rc.Render.Args = rc.Cmd.Args.Resolve(nil)
	}
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
