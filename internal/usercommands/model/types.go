// Package model contains the core types and constants for the devbox command system.
package model

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	reFileID   = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	rePosixEnv = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// CommandType identifies the execution strategy for a command.
type CommandType string

const (
	// CommandTypeShell runs a shell command or argv on the host.
	CommandTypeShell CommandType = "shell"
	// CommandTypeScript runs one or more script files.
	CommandTypeScript CommandType = "script"
	// CommandTypeServiceExec runs inside an existing container via docker compose exec.
	CommandTypeServiceExec CommandType = "service_exec"
	// CommandTypeServiceRun starts a one-off container via docker compose run.
	CommandTypeServiceRun CommandType = "service_run"
	// CommandTypeWorkflow executes a sequence of command references.
	CommandTypeWorkflow CommandType = "workflow"
	// CommandTypeDevbox runs a devbox subcommand using the current executable.
	// The cmd: field contains the subcommand and its arguments (without the binary path).
	CommandTypeDevbox CommandType = "devbox"
	// CommandTypeBuiltin invokes an engine-internal builtin action by name.
	// The cmd: field holds the builtin name; with: holds its parameters.
	CommandTypeBuiltin CommandType = "builtin"
)

// ParamType describes the expected value type of a command parameter.
type ParamType string

const (
	// ParamTypeString is the default param type: a plain text value.
	ParamTypeString ParamType = "string"
	// ParamTypeBool is a boolean param (true/false).
	ParamTypeBool ParamType = "bool"
	// ParamTypeInt is an integer param.
	ParamTypeInt ParamType = "int"
	// ParamTypePath is a file-system path param.
	ParamTypePath ParamType = "path"
)

// UserMode specifies which user to use inside a container.
// Special values: "current" (pass --user with host UID:GID), "root" (--user root),
// "internal" (no --user flag, use the image's built-in USER).
// Any other string value is passed verbatim as --user <value>.
//
// When the field is empty/omitted, the runner falls back to the target service's
// services.<svc>.cli.user; if that is also empty, no --user flag is added.
// Use "internal" to explicitly opt out of the cli.user fallback.
type UserMode string

const (
	// UserModeCurrent passes --user with the host UID:GID.
	UserModeCurrent UserMode = "current"
	// UserModeRoot passes --user root.
	UserModeRoot UserMode = "root"
	// UserModeInternal skips the --user flag and bypasses the cli.user fallback,
	// so the container runs under the image's built-in USER directive.
	UserModeInternal UserMode = "internal"
)

// ExecMode controls how service_exec commands behave when the container state is unknown.
type ExecMode string

const (
	// ExecModeExec always uses docker compose exec (fails if container not running).
	ExecModeExec ExecMode = "exec"
	// ExecModeRun always uses docker compose run --rm.
	ExecModeRun ExecMode = "run"
	// ExecModeExecOrRun checks if container is running; uses exec if so, run otherwise.
	ExecModeExecOrRun ExecMode = "exec-or-run"
)

// FileAccess specifies whether a file is read, written, or both.
type FileAccess string

const (
	// FileAccessRead means the file must exist before the command runs.
	FileAccessRead FileAccess = "read"
	// FileAccessWrite means the command will create or modify the file.
	FileAccessWrite FileAccess = "write"
	// FileAccessReadWrite means the file must pre-exist and the command may modify it.
	FileAccessReadWrite FileAccess = "read_write"
)

// FileSort specifies the sort order for glob-matched file candidates.
type FileSort string

const (
	// FileSortNameAsc sorts candidates by basename, ascending (A-Z).
	FileSortNameAsc FileSort = "name_asc"
	// FileSortNameDesc sorts candidates by basename, descending (Z-A).
	FileSortNameDesc FileSort = "name_desc"
	// FileSortModtimeAsc sorts candidates by modification time, oldest first.
	FileSortModtimeAsc FileSort = "modtime_asc"
	// FileSortModtimeDesc sorts candidates by modification time, newest first.
	FileSortModtimeDesc FileSort = "modtime_desc"
)

// FileOnError specifies what happens to a file if the command fails.
type FileOnError string

const (
	// FileOnErrorKeep preserves the file even if the command fails.
	FileOnErrorKeep FileOnError = "keep"
	// FileOnErrorRemove deletes the file if the command fails (only if not pre-existing).
	FileOnErrorRemove FileOnError = "remove"
)

// FileCandidate is one candidate path or glob in a file spec's fallback chain.
type FileCandidate struct {
	// Path is a literal file path (mutually exclusive with Glob).
	Path string `yaml:"path"`
	// Glob is a glob pattern to match multiple files (mutually exclusive with Path).
	Glob string `yaml:"glob"`
	// Match is a regex pattern applied to basenames when Glob is used.
	Match string `yaml:"match"`
	// Sort specifies the sort order for glob matches. Only valid with Glob.
	Sort FileSort `yaml:"sort"`
}

// FileSpec declares a file artefact that a command reads or produces.
// Required field stays a plain bool (no *bool) for simplicity; when access is
// read_write, presence is always enforced at runtime regardless of what required says.
type FileSpec struct {
	// Access is the access mode (read, write, read_write). Required.
	Access FileAccess `yaml:"access"`
	// Path is a literal file path (for read/read_write, mutually exclusive with Candidates).
	// For write, this is required and candidates is rejected.
	Path string `yaml:"path"`
	// Candidates is an ordered list of fallback paths/globs (for read/read_write only).
	Candidates []FileCandidate `yaml:"candidates"`
	// Required indicates the file must be resolved. For read: respected.
	// For write: n/a (write always creates). For read_write: always true at runtime.
	Required bool `yaml:"required"`
	// Mkdir creates parent directories for write mode (mkdir only valid for write).
	Mkdir bool `yaml:"mkdir"`
	// Overwrite allows overwriting an existing file in write mode (only valid for write).
	Overwrite bool `yaml:"overwrite"`
	// OnError specifies what to do with the file if the command fails (write/read_write only).
	OnError FileOnError `yaml:"on_error"`
	// Env is the environment variable name to inject with the resolved file path.
	Env string `yaml:"env"`
}

// GroupMeta holds optional metadata about a command group, declared at the top of a
// command file under the `group` key.
type GroupMeta struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
}

// ParamDef defines a single named parameter for a command.
type ParamDef struct {
	// Type is the expected value type. Defaults to "string" when empty.
	Type ParamType `yaml:"type"`
	// Description is human-readable help text shown by devbox command inspect.
	Description string `yaml:"description"`
	// Required indicates the caller must supply this parameter explicitly.
	Required bool `yaml:"required"`
	// Default is a literal fallback value when the parameter is not supplied.
	Default string `yaml:"default"`
	// DefaultFrom is a dot-path into the merged config used as the default
	// when the parameter is not supplied and Default is empty.
	DefaultFrom string `yaml:"default_from"`
	// Env is the environment variable name used to expose this param to the process.
	// When empty the param is not injected as an env var (only available for templating).
	Env string `yaml:"env"`
	// Pattern is an optional anchored regular expression that the resolved string
	// value must fully match. Applied only to string and path params; ignored for
	// bool and int params. An empty Pattern skips validation.
	Pattern string `yaml:"pattern"`
}

// ContextDef defines a single named context value derived from the merged config.
type ContextDef struct {
	// From is a dot-path into the merged DevboxConfig.Raw map.
	From string `yaml:"from"`
	// Required causes an error at resolution time when the path resolves to nil/empty.
	Required bool `yaml:"required"`
	// Env is the environment variable name used to expose this value to the process.
	Env string `yaml:"env"`
}

// ScriptDef describes the script to execute for a command of type "script".
// Exactly one of the two modes must be configured:
//   - Simple mode:  Path is set (a single script file).
//   - Phased mode:  Run (and optionally Plan and Cleanup) are set.
type ScriptDef struct {
	// Shell is the interpreter to use. Defaults to "sh".
	Shell string `yaml:"shell"`

	// --- Simple mode ---
	// Path is the path to a single script file (relative to the project root).
	Path string `yaml:"path"`

	// --- Phased mode ---
	// Plan is an optional script run before Run (setup / dry-run phase).
	Plan string `yaml:"plan"`
	// Run is the main execution script.
	Run string `yaml:"run"`
	// Cleanup is an optional script that is always executed after Run, even on failure.
	Cleanup string `yaml:"cleanup"`
}

// Validate checks that exactly one mode is configured.
func (s *ScriptDef) Validate() error {
	hasSimple := s.Path != ""
	hasPhased := s.Run != ""
	switch {
	case hasSimple && hasPhased:
		return fmt.Errorf("script: path and run are mutually exclusive")
	case hasSimple && (s.Plan != "" || s.Cleanup != ""):
		return fmt.Errorf("script: plan/cleanup may not be combined with path")
	case !hasSimple && !hasPhased:
		return fmt.Errorf("script: one of path or run must be set")
	}
	return nil
}

// WorkflowStep is one step in a workflow command.
// Exactly one of Command, Confirm, or Parallel must be set.
type WorkflowStep struct {
	// Command is the full command ID (e.g. "services.main.migrate") to execute.
	Command string `yaml:"command"`
	// With holds parameter overrides passed to the referenced command.
	With map[string]string `yaml:"with"`
	// Confirm is a message displayed to the user before continuing.
	// The workflow is aborted if the user declines.
	Confirm string `yaml:"confirm"`
	// When is an optional skip condition evaluated before the step runs.
	// Supports ${...} syntax and cmd: / builtin predicates.
	When string `yaml:"when"`
	// ContinueOnError allows a command step to fail without aborting the workflow.
	// Not valid on confirm steps (a confirmation that is ignored is meaningless).
	ContinueOnError bool `yaml:"continue_on_error"`
	// Parallel, when non-nil, marks this step as a parallel container holding
	// a group of leaf sub-steps to run concurrently. Mutually exclusive with
	// Command, Confirm, and With. When and ContinueOnError remain valid at
	// the container level.
	Parallel *WorkflowParallel `yaml:"parallel,omitempty"`
}

// WorkflowParallel declares a group of workflow sub-steps to be executed concurrently.
// Mirrors the pipeline ParallelGroup shape so deploy/lifecycle/reset and workflow
// share the same concurrency semantics.
type WorkflowParallel struct {
	// MaxConcurrent caps how many sub-steps run at once.
	// When <= 0, the runner picks min(runtime.NumCPU(), len(Steps)).
	MaxConcurrent int `yaml:"max_concurrent,omitempty"`
	// FailFast controls whether the first sub-step error cancels siblings.
	// nil (default) means true; explicit false aggregates errors via errors.Join.
	FailFast *bool `yaml:"fail_fast,omitempty"`
	// Steps holds the leaf sub-steps to execute concurrently.
	// Must contain at least 2 entries; nested parallel and confirm sub-steps are rejected.
	Steps []WorkflowStep `yaml:"steps"`
}

// Validate checks that exactly one of Command, Confirm, or Parallel is set, and
// that container-only fields aren't combined incompatibly.
func (s *WorkflowStep) Validate() error {
	hasCommand := s.Command != ""
	hasConfirm := s.Confirm != ""
	hasParallel := s.Parallel != nil
	set := 0
	if hasCommand {
		set++
	}
	if hasConfirm {
		set++
	}
	if hasParallel {
		set++
	}
	switch {
	case set > 1:
		return fmt.Errorf("workflow step: command, confirm, and parallel are mutually exclusive")
	case set == 0:
		return fmt.Errorf("workflow step: one of command, confirm, or parallel must be set")
	case hasConfirm && len(s.With) > 0:
		return fmt.Errorf("workflow step: with may not be combined with confirm")
	case hasConfirm && s.ContinueOnError:
		return fmt.Errorf("workflow step: continue_on_error is not valid on confirm steps")
	case hasParallel && len(s.With) > 0:
		return fmt.Errorf("workflow step: with may not be combined with parallel")
	}
	if hasParallel {
		if len(s.Parallel.Steps) < 2 {
			return fmt.Errorf("workflow step: parallel.steps must contain at least 2 sub-steps")
		}
		for i, sub := range s.Parallel.Steps {
			if sub.Parallel != nil {
				return fmt.Errorf("workflow step: parallel.steps[%d]: nested parallel is not supported", i)
			}
			if sub.Confirm != "" {
				return fmt.Errorf("workflow step: parallel.steps[%d]: confirm is not allowed inside a parallel group", i)
			}
			if sub.Command == "" {
				return fmt.Errorf("workflow step: parallel.steps[%d]: command is required", i)
			}
			if err := sub.Validate(); err != nil {
				return fmt.Errorf("workflow step: parallel.steps[%d]: %w", i, err)
			}
		}
	}
	return nil
}

// RunnerDef allows a command to override service/user/workdir fields without
// duplicating the full command definition.  When present on a CommandDef these
// fields take precedence over the top-level service/user/workdir fields.
type RunnerDef struct {
	Service     string   `yaml:"service"`
	User        UserMode `yaml:"user"`
	Workdir     string   `yaml:"workdir"`
	WorkdirFrom string   `yaml:"workdir_from"`
	Mode        ExecMode `yaml:"mode"`
}

// CommandMessages defines optional command-level messages emitted by the
// shared runner after command execution.
type CommandMessages struct {
	Success string `yaml:"success"`
	Error   string `yaml:"error"`
}

// CommandDef is a single command entry inside a command file.
type CommandDef struct {
	// Type is the execution strategy. Required.
	Type CommandType `yaml:"type"`
	// Description is human-readable help text.
	Description string `yaml:"description"`
	// Private hides the command from `devbox command list` but allows
	// it to be referenced from workflows.
	Private bool `yaml:"private"`
	// Confirmation asks the user to confirm before the command is executed.
	Confirmation bool `yaml:"confirmation"`
	// ConfirmationText is the prompt shown when Confirmation is true.
	// Defaults to DefaultConfirmationText when empty.
	ConfirmationText string `yaml:"confirmation_text"`

	// Params declares named parameters accepted by the command.
	Params map[string]ParamDef `yaml:"params"`
	// Context declares named values derived from the merged config.
	Context map[string]ContextDef `yaml:"context"`
	// Env is additional environment variables injected into the process,
	// supporting ${...} template interpolation.
	Env map[string]string `yaml:"env"`
	// Files declares file artefacts the command reads or produces.
	Files map[string]FileSpec `yaml:"files"`
	// Messages holds optional centralized success/error output for the command.
	Messages CommandMessages `yaml:"messages"`

	// --- type=shell fields ---
	// Cmd is a shell command string executed via `sh -c`.
	// Mutually exclusive with Argv.
	Cmd string `yaml:"cmd"`
	// Argv is the raw argument vector (no shell quoting).
	// Mutually exclusive with Cmd.
	Argv []string `yaml:"argv"`

	// --- type=service_exec / service_run fields ---
	// Service is the Docker Compose service name.
	Service string `yaml:"service"`
	// User specifies the container user (current, root, or a literal string).
	User UserMode `yaml:"user"`
	// Workdir is the working directory inside the container.
	Workdir string `yaml:"workdir"`
	// WorkdirFrom is a dot-path into the merged config resolving to a workdir string.
	WorkdirFrom string `yaml:"workdir_from"`
	// Mode controls exec vs run behaviour for service_exec commands.
	Mode ExecMode `yaml:"mode"`
	// ComposeArgs is a list of arbitrary flags to pass to docker compose exec/run,
	// inserted before --user/--workdir/-e flags. Supports ${...} template interpolation.
	ComposeArgs []string `yaml:"compose_args"`

	// --- type=script fields ---
	Script *ScriptDef `yaml:"script"`

	// --- type=workflow fields ---
	Steps []WorkflowStep `yaml:"steps"`

	// --- type=builtin fields ---
	// With holds the parameters passed to the builtin (e.g. timeout, services).
	With map[string]any `yaml:"with"`

	// Runner is an optional override block for service/user/workdir/mode.
	// When set, its non-zero fields take precedence over the top-level fields.
	Runner *RunnerDef `yaml:"runner"`

	// Computed fields — not part of YAML, populated by the loader.

	// ID is the full qualified command ID, e.g. "services.main.migrate".
	ID string `yaml:"-"`
	// Group is the dot-separated group prefix, e.g. "services.main".
	Group string `yaml:"-"`
	// LocalName is the command name within its group, e.g. "migrate".
	LocalName string `yaml:"-"`
}

// Validate checks that the CommandDef is internally consistent.
// It enforces mutually exclusive field combinations per type.
func (c *CommandDef) Validate() error {
	if c.Type == "" {
		return fmt.Errorf("command %q: type is required", c.ID)
	}

	switch c.Type {
	case CommandTypeShell, CommandTypeDevbox:
		if err := c.validateCommandType(); err != nil {
			return fmt.Errorf("command %q: %w", c.ID, err)
		}
	case CommandTypeScript:
		if err := c.validateScriptType(); err != nil {
			return fmt.Errorf("command %q: %w", c.ID, err)
		}
	case CommandTypeServiceExec, CommandTypeServiceRun:
		if err := c.validateServiceType(); err != nil {
			return fmt.Errorf("command %q: %w", c.ID, err)
		}
	case CommandTypeWorkflow:
		if err := c.validateWorkflowType(); err != nil {
			return fmt.Errorf("command %q: %w", c.ID, err)
		}
	case CommandTypeBuiltin:
		if err := c.validateBuiltinType(); err != nil {
			return fmt.Errorf("command %q: %w", c.ID, err)
		}
	default:
		if c.Type == "command" {
			return fmt.Errorf("command %q: unknown type %q (use type: shell for host shell commands)", c.ID, c.Type)
		}
		return fmt.Errorf("command %q: unknown type %q", c.ID, c.Type)
	}

	// Validate env name conflicts across params, context, env block, and files.
	if err := c.validateEnvConflicts(); err != nil {
		return fmt.Errorf("command %q: %w", c.ID, err)
	}

	// Validate file specs (shapes, IDs, access modes, etc.).
	if err := c.validateFiles(); err != nil {
		return fmt.Errorf("command %q: %w", c.ID, err)
	}

	return nil
}

// EffectiveConfirmationText returns the prompt text for confirmation-enabled
// commands, applying the documented default.
func (c *CommandDef) EffectiveConfirmationText() string {
	if c != nil && c.ConfirmationText != "" {
		return c.ConfirmationText
	}
	return DefaultConfirmationText
}

// DefaultConfirmationText is the fallback prompt for confirmation-enabled commands.
const DefaultConfirmationText = "Are you sure?"

func (c *CommandDef) validateCommandType() error {
	if c.Type == CommandTypeShell {
		hasCmd := c.Cmd != ""
		hasArgv := len(c.Argv) > 0
		if hasCmd && hasArgv {
			return fmt.Errorf("cmd and argv are mutually exclusive")
		}
		if !hasCmd && !hasArgv {
			return fmt.Errorf("one of cmd or argv must be set")
		}
	}
	if c.Type == CommandTypeDevbox {
		if c.Cmd == "" {
			return fmt.Errorf("cmd is required for type=devbox")
		}
		if len(c.Argv) > 0 {
			return fmt.Errorf("argv is not valid for type=devbox; use cmd")
		}
	}

	if c.Script != nil {
		return fmt.Errorf("script field is not valid for type=%s", c.Type)
	}
	if len(c.Steps) > 0 {
		return fmt.Errorf("steps field is not valid for type=%s", c.Type)
	}
	if c.Service != "" {
		return fmt.Errorf("service field is not valid for type=%s", c.Type)
	}
	if len(c.ComposeArgs) > 0 {
		return fmt.Errorf("compose_args field is not valid for type=%s", c.Type)
	}
	if c.WorkdirFrom != "" {
		return fmt.Errorf("workdir_from is not valid for type=%s", c.Type)
	}
	if c.Type == CommandTypeDevbox && c.Workdir != "" {
		return fmt.Errorf("workdir is not valid for type=devbox")
	}
	return nil
}

func (c *CommandDef) validateScriptType() error {
	if c.Script == nil {
		return fmt.Errorf("script block is required for type=script")
	}
	if err := c.Script.Validate(); err != nil {
		return err
	}
	if c.Cmd != "" || len(c.Argv) > 0 {
		return fmt.Errorf("cmd/argv fields are not valid for type=script")
	}
	if len(c.Steps) > 0 {
		return fmt.Errorf("steps field is not valid for type=script")
	}
	if len(c.ComposeArgs) > 0 {
		return fmt.Errorf("compose_args field is not valid for type=script")
	}
	if c.WorkdirFrom != "" {
		return fmt.Errorf("workdir_from is not valid for type=script")
	}
	return nil
}

func (c *CommandDef) validateServiceType() error {
	effectiveService := c.Service
	if c.Runner != nil && c.Runner.Service != "" {
		effectiveService = c.Runner.Service
	}
	if effectiveService == "" {
		return fmt.Errorf("service is required for type=%s", c.Type)
	}
	if c.Script != nil {
		return fmt.Errorf("script field is not valid for type=%s", c.Type)
	}
	if len(c.Steps) > 0 {
		return fmt.Errorf("steps field is not valid for type=%s", c.Type)
	}
	hasCmd := c.Cmd != ""
	hasArgv := len(c.Argv) > 0
	if hasCmd && hasArgv {
		return fmt.Errorf("cmd and argv are mutually exclusive")
	}
	if !hasCmd && !hasArgv {
		return fmt.Errorf("one of cmd or argv must be set")
	}
	if c.Type == CommandTypeServiceRun && c.Mode != "" && c.Mode != ExecModeRun {
		return fmt.Errorf("mode is not applicable for type=service_run (always runs a new container)")
	}
	return nil
}

func (c *CommandDef) validateWorkflowType() error {
	if len(c.Steps) == 0 {
		return fmt.Errorf("steps is required and must be non-empty for type=workflow")
	}
	if c.Cmd != "" || len(c.Argv) > 0 {
		return fmt.Errorf("cmd/argv fields are not valid for type=workflow")
	}
	if c.Script != nil {
		return fmt.Errorf("script field is not valid for type=workflow")
	}
	if c.Service != "" {
		return fmt.Errorf("service field is not valid for type=workflow")
	}
	if len(c.ComposeArgs) > 0 {
		return fmt.Errorf("compose_args field is not valid for type=workflow")
	}
	if c.Workdir != "" {
		return fmt.Errorf("workdir is not valid for type=workflow")
	}
	if c.WorkdirFrom != "" {
		return fmt.Errorf("workdir_from is not valid for type=workflow")
	}
	for i, step := range c.Steps {
		if err := step.Validate(); err != nil {
			return fmt.Errorf("step[%d]: %w", i, err)
		}
	}
	return nil
}

func (c *CommandDef) validateBuiltinType() error {
	if c.Cmd == "" {
		return fmt.Errorf("cmd is required for type=builtin (builtin name)")
	}
	if len(c.Argv) > 0 {
		return fmt.Errorf("argv is not valid for type=builtin; use cmd for builtin name")
	}
	if c.Script != nil {
		return fmt.Errorf("script field is not valid for type=builtin")
	}
	if len(c.Steps) > 0 {
		return fmt.Errorf("steps field is not valid for type=builtin")
	}
	if c.Service != "" {
		return fmt.Errorf("service field is not valid for type=builtin")
	}
	if len(c.ComposeArgs) > 0 {
		return fmt.Errorf("compose_args field is not valid for type=builtin")
	}
	if c.Workdir != "" || c.WorkdirFrom != "" {
		return fmt.Errorf("workdir is not valid for type=builtin")
	}
	if c.User != "" {
		return fmt.Errorf("user is not valid for type=builtin")
	}
	if c.Mode != "" {
		return fmt.Errorf("mode is not valid for type=builtin")
	}
	if c.Runner != nil {
		return fmt.Errorf("runner is not valid for type=builtin")
	}
	return nil
}

func (c *CommandDef) validateEnvConflicts() error {
	allEnvNames := make(map[string]string)

	for name := range c.Env {
		allEnvNames[name] = "env block"
	}

	for pname, pdef := range c.Params {
		if pdef.Env != "" {
			if existing, ok := allEnvNames[pdef.Env]; ok {
				return fmt.Errorf("env conflict: %q declared by params.%s and %s", pdef.Env, pname, existing)
			}
			allEnvNames[pdef.Env] = fmt.Sprintf("params.%s", pname)
		}
	}

	for cname, cdef := range c.Context {
		if cdef.Env != "" {
			if existing, ok := allEnvNames[cdef.Env]; ok {
				return fmt.Errorf("env conflict: %q declared by context.%s and %s", cdef.Env, cname, existing)
			}
			allEnvNames[cdef.Env] = fmt.Sprintf("context.%s", cname)
		}
	}

	for fid, fspec := range c.Files {
		if fspec.Env != "" {
			if existing, ok := allEnvNames[fspec.Env]; ok {
				return fmt.Errorf("env conflict: %q declared by files.%s and %s", fspec.Env, fid, existing)
			}
			allEnvNames[fspec.Env] = fmt.Sprintf("files.%s", fid)
		}
	}

	return nil
}

func (c *CommandDef) validateFiles() error {
	if len(c.Files) == 0 {
		return nil
	}

	for fid, fspec := range c.Files {
		if !reFileID.MatchString(fid) {
			return fmt.Errorf("files.%s: id must match ^[a-zA-Z_][a-zA-Z0-9_]*$ (got %q)", fid, fid)
		}

		if fspec.Access == "" {
			return fmt.Errorf("files.%s: access is required", fid)
		}
		if fspec.Access != FileAccessRead && fspec.Access != FileAccessWrite && fspec.Access != FileAccessReadWrite {
			return fmt.Errorf("files.%s: access must be one of read, write, read_write (got %q)", fid, fspec.Access)
		}

		hasPath := fspec.Path != ""
		hasCandidates := len(fspec.Candidates) > 0

		switch fspec.Access {
		case FileAccessWrite:
			if !hasPath {
				return fmt.Errorf("files.%s: path is required for access=write", fid)
			}
			if hasCandidates {
				return fmt.Errorf("files.%s: candidates are not allowed for access=write", fid)
			}

		case FileAccessRead, FileAccessReadWrite:
			if hasPath && hasCandidates {
				return fmt.Errorf("files.%s: path and candidates are mutually exclusive", fid)
			}
			if !hasPath && !hasCandidates {
				return fmt.Errorf("files.%s: one of path or candidates must be set for access=%s", fid, fspec.Access)
			}
		}

		for i, cand := range fspec.Candidates {
			candHasPath := cand.Path != ""
			candHasGlob := cand.Glob != ""

			if candHasPath && candHasGlob {
				return fmt.Errorf("files.%s: candidates[%d]: path and glob are mutually exclusive", fid, i)
			}
			if !candHasPath && !candHasGlob {
				return fmt.Errorf("files.%s: candidates[%d]: one of path or glob must be set", fid, i)
			}

			if candHasPath && (cand.Match != "" || cand.Sort != "") {
				return fmt.Errorf("files.%s: candidates[%d]: match and sort are only valid with glob", fid, i)
			}

			if candHasGlob && cand.Sort != "" {
				if cand.Sort != FileSortNameAsc && cand.Sort != FileSortNameDesc &&
					cand.Sort != FileSortModtimeAsc && cand.Sort != FileSortModtimeDesc {
					return fmt.Errorf("files.%s: candidates[%d]: sort must be one of name_asc, name_desc, modtime_asc, modtime_desc (got %q)", fid, i, cand.Sort)
				}
			}
		}

		if fspec.Mkdir && fspec.Access != FileAccessWrite {
			return fmt.Errorf("files.%s: mkdir is only valid for access=write", fid)
		}
		if fspec.Overwrite && fspec.Access != FileAccessWrite {
			return fmt.Errorf("files.%s: overwrite is only valid for access=write", fid)
		}

		if fspec.OnError != "" && fspec.OnError != FileOnErrorKeep && fspec.OnError != FileOnErrorRemove {
			return fmt.Errorf("files.%s: on_error must be one of keep, remove (got %q)", fid, fspec.OnError)
		}
		if fspec.OnError != "" && fspec.Access == FileAccessRead {
			return fmt.Errorf("files.%s: on_error is not valid for access=read", fid)
		}

		if fspec.Env != "" {
			if !rePosixEnv.MatchString(fspec.Env) {
				return fmt.Errorf("files.%s: env must be a valid POSIX env name like MY_VAR (got %q)", fid, fspec.Env)
			}
		}
	}

	return nil
}

// CommandFile is the top-level structure of a command YAML file.
// The file may declare a group metadata block and a map of named commands.
type CommandFile struct {
	// Group holds optional metadata about the group this file defines.
	Group GroupMeta `yaml:"group"`
	// Commands maps local command names to their definitions.
	Commands map[string]CommandDef `yaml:"commands"`

	// Computed fields set by the loader.

	// FilePath is the absolute path of the source file.
	FilePath string `yaml:"-"`
	// GroupID is the computed dot-separated group prefix, e.g. "services.main".
	GroupID string `yaml:"-"`
}

// Validate validates all command definitions in the file.
func (f *CommandFile) Validate() error {
	var errs []string
	for name, cmd := range f.Commands {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, "command name must not be empty")
			continue
		}
		if err := cmd.Validate(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("validation errors in %s:\n  %s", f.FilePath, strings.Join(errs, "\n  "))
	}
	return nil
}

// ParseCommandFile unmarshals YAML bytes into a CommandFile and runs basic field validation.
func ParseCommandFile(data []byte) (*CommandFile, error) {
	var cf CommandFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cf); err != nil {
		return nil, fmt.Errorf("YAML parse error: %w", err)
	}
	return &cf, nil
}
