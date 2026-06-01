// Package spec defines the contract between the runtime root package and
// the per-runner subpackages: the Runner interface, the RunContext struct
// it consumes, and the FileProbeResult returned by file-probe APIs.
//
// Subpackages (runtime/runners/*) implement Runner by importing this
// package; the root runtime package re-exports these types via aliases so
// external callers can continue to reference runtime.Runner,
// runtime.RunContext, etc.
package spec

import (
	"context"
	"io"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
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
	Config *config.DweConfig

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
	// confirmations and nested parallel blocks.
	UnderParallel bool

	// SkipNotify suppresses the end-of-command desktop notification for
	// this invocation. Always set true when one runtime invokes another:
	// only the top-level user-invoked command should fire notifications.
	// The workflow runner, pipeline executor action dispatch, and any
	// future internal call site must set this to true on the inner
	// RunContext they build. Top-level entry points leave the zero value
	// (false), enabling notification when Cmd.Notify is true.
	SkipNotify bool

	// StepObserver, when non-nil, receives lifecycle events for top-level
	// sequential workflow steps (start / end / skip / fail). Parallel
	// sub-step events are not surfaced; the parallel block as a whole is one
	// step from the observer's point of view. Implementations may also
	// satisfy StepIOSuspender to have the runner pause their live UI while
	// each sequential command step's child writes to the terminal.
	StepObserver WorkflowStepObserver

	// WorkflowSubStepOverrides carries pipeline-side per-sub-step directives
	// (currently files_gate) for the workflow being invoked. Set by the
	// pipeline executor when the originating DeployStep declares
	// sub_step_overrides; nil for ad-hoc workflow invocations. The workflow
	// runner consumes it inside both sequential and parallel dispatch paths.
	// Keys are sub-step names (StepName()); values are the matching override.
	WorkflowSubStepOverrides map[string]config.SubStepOverride

	// Translator provides localized string lookups. When set, display strings
	// (descriptions, confirmation text, param descriptions) are looked up
	// via this Translator. Set by the command layer (e.g., runCommandByID);
	// not populated by BuildRunContext (callers wire it in after construction).
	// Completion paths and other contexts that bypass locale resolution set
	// a NopTranslator to avoid nil checks downstream.
	Translator i18n.Translator

	// Locale is the active locale code (e.g. "ru", "en"). Used alongside
	// Translator to look up localized strings. Set by the command layer.
	Locale string
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

// FileProbeResult tracks the outcome of a single file probe.
type FileProbeResult struct {
	Resolved bool   // true if the file (or a candidate chain) resolved
	Path     string // the resolved path, if Resolved is true
	Err      error  // configuration error, if any (e.g. bad template, bad glob, bad regex)
}
