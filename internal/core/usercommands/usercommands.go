// Package usercommands is the public API for the dwe command system.
// It re-exports types, constants, and functions from the model, loader,
// registry, resolve, and runtime subpackages so that callers can use a single
// import path ("github.com/semsemyonoff/dwe/internal/core/usercommands") without knowing which
// subpackage owns each symbol.
package usercommands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/loader"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/resolve"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// ---- Type aliases (model) ----

type CommandType = model.CommandType
type ParamType = model.ParamType
type UserMode = model.UserMode
type ExecMode = model.ExecMode
type FileAccess = model.FileAccess
type FileSort = model.FileSort
type FileOnError = model.FileOnError
type FileCandidate = model.FileCandidate
type FileSpec = model.FileSpec
type GroupMeta = model.GroupMeta
type ParamDef = model.ParamDef
type ArgsSpec = model.ArgsSpec
type ContextDef = model.ContextDef
type ScriptDef = model.ScriptDef
type WorkflowStep = model.WorkflowStep
type WorkflowParallel = model.WorkflowParallel
type RunnerDef = model.RunnerDef
type CommandMessages = model.CommandMessages
type CommandDef = model.CommandDef
type CommandFile = model.CommandFile

// ---- Type aliases (registry) ----

type GroupNode = registry.GroupNode
type Registry = registry.Registry

// ---- Type aliases (runtime) ----

type Runner = runtime.Runner
type RunContext = runtime.RunContext
type ErrUnsupportedType = runtime.ErrUnsupportedType
type FileProbeResult = runtime.FileProbeResult

// ---- Error sentinels (runtime) ----

// ErrWorkflowNestedParallel is returned when a workflow containing a
// parallel: block is invoked from another parallel context (pipeline or
// workflow). Callers can use errors.Is against this variable without
// importing the runtime subpackage directly.
var ErrWorkflowNestedParallel = runtime.ErrWorkflowNestedParallel

// ErrConfirmInsideParallel is returned when an interactive confirmation is
// reached inside a parallel group. Callers can use errors.Is against this
// variable without importing the runtime subpackage directly.
var ErrConfirmInsideParallel = runtime.ErrConfirmInsideParallel

// ---- CommandType constants ----

const (
	CommandTypeShell       = model.CommandTypeShell
	CommandTypeScript      = model.CommandTypeScript
	CommandTypeServiceExec = model.CommandTypeServiceExec
	CommandTypeServiceRun  = model.CommandTypeServiceRun
	CommandTypeWorkflow    = model.CommandTypeWorkflow
	CommandTypeDwe         = model.CommandTypeDwe
	CommandTypeBuiltin     = model.CommandTypeBuiltin
)

// ---- ParamType constants ----

const (
	ParamTypeString = model.ParamTypeString
	ParamTypeBool   = model.ParamTypeBool
	ParamTypeInt    = model.ParamTypeInt
	ParamTypePath   = model.ParamTypePath
)

// ---- UserMode constants ----

const (
	UserModeCurrent  = model.UserModeCurrent
	UserModeRoot     = model.UserModeRoot
	UserModeInternal = model.UserModeInternal
)

// ---- ExecMode constants ----

const (
	ExecModeExec      = model.ExecModeExec
	ExecModeRun       = model.ExecModeRun
	ExecModeExecOrRun = model.ExecModeExecOrRun
)

// ---- FileAccess constants ----

const (
	FileAccessRead      = model.FileAccessRead
	FileAccessWrite     = model.FileAccessWrite
	FileAccessReadWrite = model.FileAccessReadWrite
)

// ---- FileSort constants ----

const (
	FileSortNameAsc     = model.FileSortNameAsc
	FileSortNameDesc    = model.FileSortNameDesc
	FileSortModtimeAsc  = model.FileSortModtimeAsc
	FileSortModtimeDesc = model.FileSortModtimeDesc
)

// ---- FileOnError constants ----

const (
	FileOnErrorKeep   = model.FileOnErrorKeep
	FileOnErrorRemove = model.FileOnErrorRemove
)

// ---- Other constants ----

const DefaultConfirmationText = model.DefaultConfirmationText

// ---- Runner type aliases ----

type DweRunner = runtime.DweRunner
type HostRunner = runtime.HostRunner
type ScriptRunner = runtime.ScriptRunner
type ServiceExecRunner = runtime.ServiceExecRunner
type ServiceRunRunner = runtime.ServiceRunRunner
type WorkflowRunner = runtime.WorkflowRunner
type BuiltinRunner = runtime.BuiltinRunner

// ---- Functions (registry) ----

// NewEmptyRegistry returns an empty Registry with no commands.
func NewEmptyRegistry() *Registry {
	return registry.NewEmptyRegistry()
}

// LoadRegistry discovers all command files under baseDir, loads them, and
// assembles a Registry.
func LoadRegistry(baseDir string) (*Registry, error) {
	return registry.LoadRegistry(baseDir)
}

// LoadRegistryFromConfigPath loads the command registry from workspace/commands/
// relative to configPath. Returns an empty registry when the directory does not
// exist. Validates the registry before returning.
func LoadRegistryFromConfigPath(configPath string) (*Registry, error) {
	commandsDir := filepath.Join(filepath.Dir(configPath), "workspace", "commands")
	if _, statErr := os.Stat(commandsDir); errors.Is(statErr, os.ErrNotExist) {
		return registry.NewEmptyRegistry(), nil
	}
	reg, err := registry.LoadRegistry(commandsDir)
	if err != nil {
		return nil, fmt.Errorf("loading command registry: %w", err)
	}
	if err := reg.Validate(); err != nil {
		return nil, fmt.Errorf("command registry validation: %w", err)
	}
	return reg, nil
}

// CommandIndex projects reg into the shared agent-facing command and group
// summaries, both sorted by ID. A nil registry yields two nil slices.
//
// It never evaluates `hide:`: a caller rendering a live document (llms-txt)
// runs ApplyVisibility first, while a caller writing a file to disk (template
// packs) must not, or the file would flip with the state of the Docker stack.
//
// Group counts come from reg.List, not GroupNode.Commands, so they inherit
// List's Private/BridgeHidden (and, after ApplyVisibility, Hidden) filters.
// Nodes with no printable text and no direct commands are skipped: ensureGroup
// materializes every dotted ancestor, and a header-only file (e.g. only
// `group: {bridge: …}`) leaves meta non-zero without giving the node a title.
func CommandIndex(reg *Registry, tr i18n.Translator, locale string) ([]model.CommandSummary, []model.CommandGroupSummary) {
	if reg == nil {
		return nil, nil
	}

	defs := reg.List("")
	commands := make([]model.CommandSummary, 0, len(defs))
	for _, def := range defs {
		commands = append(commands, model.CommandSummary{
			ID:          def.ID,
			Group:       def.Group,
			Description: tr.CommandDescription(locale, def.ID, def.Description),
			Type:        string(def.Type),
			Service:     def.DeclaredService(),
		})
	}

	var groups []model.CommandGroupSummary
	var walk func(gn *GroupNode)
	walk = func(gn *GroupNode) {
		for _, child := range gn.Children {
			walk(child)
		}
		if gn.ID == "" {
			return
		}
		if gn.Meta.Title == "" && gn.Meta.Description == "" && len(gn.Commands) == 0 {
			return
		}
		count := len(reg.List(gn.ID))
		if count == 0 {
			return
		}
		groups = append(groups, model.CommandGroupSummary{
			ID:          gn.ID,
			Title:       tr.GroupTitle(locale, gn.ID, gn.Name),
			Description: tr.GroupDescription(locale, gn.ID, gn.Meta.Description),
			Count:       count,
		})
	}
	walk(reg.Groups())
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })

	return commands, groups
}

// ---- Functions (loader) ----

// DiscoverCommandFiles walks baseDir recursively and returns *.yml file paths.
func DiscoverCommandFiles(baseDir string) ([]string, error) {
	return loader.DiscoverCommandFiles(baseDir)
}

// ComputeGroup derives the dot-separated group ID from a relative path.
func ComputeGroup(relPath string) string {
	return loader.ComputeGroup(relPath)
}

// ComputeCommandID builds the fully-qualified command ID.
func ComputeCommandID(group, localName string) string {
	return loader.ComputeCommandID(group, localName)
}

// LoadCommandFile reads, parses, and validates a single command YAML file.
func LoadCommandFile(absPath, baseDir string) (*CommandFile, error) {
	return loader.LoadCommandFile(absPath, baseDir)
}

// ---- Functions (resolve) ----

// ResolveParams resolves parameter values for a command invocation.
func ResolveParams(defs map[string]ParamDef, provided map[string]string, cfg *config.DweConfig) (map[string]any, error) {
	return resolve.Params(defs, provided, cfg)
}

// ResolveContext resolves context values for a command invocation.
func ResolveContext(defs map[string]ContextDef, cfg *config.DweConfig) (map[string]any, error) {
	return resolve.Context(defs, cfg)
}

// BuildEnv constructs the environment variable map for a command execution.
func BuildEnv(cmd *CommandDef, params map[string]any, ctx map[string]any, files map[string]tpl.ResolvedFile) (map[string]string, error) {
	return resolve.BuildEnv(cmd, params, ctx, files)
}

// ParseCommandFile unmarshals YAML bytes into a CommandFile.
func ParseCommandFile(data []byte) (*CommandFile, error) {
	return model.ParseCommandFile(data)
}

// ---- Functions (runtime) ----

// NewRunner returns the appropriate Runner implementation for the given command type.
func NewRunner(cmd *CommandDef) (Runner, error) {
	return runtime.NewRunner(cmd)
}

// RunCommand executes a command definition. The supplied ctx is threaded
// through to the runner so child processes are cancelled when ctx is cancelled.
func RunCommand(ctx context.Context, rc RunContext) error {
	return runtime.RunCommand(ctx, rc)
}

// ConfirmCommand prompts before running confirmation-enabled commands.
func ConfirmCommand(ctx RunContext) error {
	return runtime.ConfirmCommand(ctx)
}

// BuildRunContext constructs a RunContext for command execution by resolving
// params, context, and docker config.
func BuildRunContext(
	cfg *config.DweConfig,
	reg *Registry,
	def *CommandDef,
	with map[string]any,
	workDir string,
) (RunContext, error) {
	return runtime.BuildRunContext(cfg, reg, def, with, workDir)
}

// BuildPreRenderedRunContext is BuildRunContext for a with: map that has
// already been rendered through tpl.RenderCommand — the pipeline, which
// renders step cmd/with/check leaves once at resolve time. See
// runtime.BuildPreRenderedRunContext for why the second render must not run.
func BuildPreRenderedRunContext(
	cfg *config.DweConfig,
	reg *Registry,
	def *CommandDef,
	with map[string]any,
	workDir string,
) (RunContext, error) {
	return runtime.BuildPreRenderedRunContext(cfg, reg, def, with, workDir)
}

// ComputeFilePathsProbe probes a subset of files to check for existence without
// side effects. Configuration errors produce an error; missing files produce
// Resolved: false with no error.
func ComputeFilePathsProbe(ctx RunContext, only []string) (map[string]FileProbeResult, error) {
	return runtime.ComputeFilePathsProbe(ctx, only)
}
