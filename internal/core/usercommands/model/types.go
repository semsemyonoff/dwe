// Package model contains the core types and constants for the devbox command system.
package model

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

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
	// CommandTypeDwe runs a dwe subcommand using the current executable.
	// The cmd: field contains the subcommand and its arguments (without the binary path).
	CommandTypeDwe CommandType = "dwe"
	// CommandTypeBuiltin invokes an engine-internal builtin action by name.
	// The cmd: field holds the builtin name; with: holds its parameters.
	CommandTypeBuiltin CommandType = "builtin"
	// CommandTypeDaemon is a declarative sugar type expanded at registry-load time
	// into four virtual commands: <base>.start, <base>.logs, <base>.stop, <base>.restart.
	// Daemon-typed commands are never executed directly — see internal/shared/daemon/.
	CommandTypeDaemon CommandType = "daemon"
)

// DaemonControlStart, etc. are the four control names that may appear in
// DaemonSpec.Controls. They map to the four synthesized virtual commands.
const (
	DaemonControlStart   = "start"
	DaemonControlLogs    = "logs"
	DaemonControlStop    = "stop"
	DaemonControlRestart = "restart"
)

// allowedFieldsFor returns the set of top-level field names allowed for a given CommandType.
// All types share a common set of fields; per-type allowlists extend that common set.
// The allowlist is derived from what the validate*Type functions explicitly reject.
func allowedFieldsFor(t CommandType) map[string]bool {
	common := map[string]bool{
		"type":              true,
		"description":       true,
		"private":           true,
		"confirmation":      true,
		"confirmation_text": true,
		"notify":            true,
		"params":            true,
		"context":           true,
		"env":               true,
		"files":             true,
		"messages":          true,
	}

	switch t {
	case CommandTypeShell:
		common["cmd"] = true
		common["argv"] = true
		common["workdir"] = true
	case CommandTypeDwe:
		common["cmd"] = true
		// workdir is explicitly rejected for devbox, NOT in allowed set
	case CommandTypeScript:
		common["script"] = true
		common["workdir"] = true
		// compose_args is rejected for script
		// workdir_from is rejected for script
	case CommandTypeServiceExec:
		common["service"] = true
		common["user"] = true
		common["workdir"] = true
		common["workdir_from"] = true
		common["mode"] = true
		common["compose_args"] = true
		common["runner"] = true
		common["cmd"] = true
		common["argv"] = true
	case CommandTypeServiceRun:
		common["service"] = true
		common["user"] = true
		common["workdir"] = true
		common["workdir_from"] = true
		common["compose_args"] = true
		common["runner"] = true
		common["cmd"] = true
		common["argv"] = true
	case CommandTypeWorkflow:
		common["steps"] = true
		// workdir is rejected for workflow
	case CommandTypeBuiltin:
		common["cmd"] = true
		common["with"] = true
		// workdir, user, and runner are not in common and not added here — rejected by allowlist
	case CommandTypeDaemon:
		common["daemon"] = true
		common["service"] = true
		common["user"] = true
		common["workdir"] = true
		common["workdir_from"] = true
		common["compose_args"] = true
		common["runner"] = true
		common["argv"] = true
	}

	return common
}

// DaemonSpec is the YAML schema block under a type=daemon command's daemon: key.
// It describes how the source command expands into virtual .start/.logs/.stop/.restart
// commands at registry-load time. All four virtual commands are always generated.
type DaemonSpec struct {
	// ContainerTemplate is a template literal rendered at runtime to produce the
	// container name (after the project prefix). May reference ${param.X}.
	ContainerTemplate string `yaml:"container_template"`
	// OnAlreadyRunning controls .start behaviour when a container with the resolved
	// name is already running. Values: "error" (default), "noop".
	OnAlreadyRunning string `yaml:"on_already_running"`
	// AutoRemove, when not explicitly false, adds --rm to docker compose run.
	// Default true.
	AutoRemove *bool `yaml:"auto_remove"`
	// StopTimeout is the raw YAML duration string (e.g. "10s", "500ms") passed
	// to docker stop -t at runtime. Empty → runtime uses 10s default. Stored
	// as raw string so validate/commands can surface parseable diagnostics
	// instead of YAML decode errors.
	StopTimeout string `yaml:"stop_timeout"`
}

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
	// ExecModeExec calls docker compose exec directly. If the container is not
	// running docker emits its own (cryptic) error. Prefer ExecModeExecOrFail
	// for clearer diagnostics; this mode exists for callers that need the raw
	// behaviour.
	ExecModeExec ExecMode = "exec"
	// ExecModeRun always uses docker compose run --rm.
	ExecModeRun ExecMode = "run"
	// ExecModeExecOrRun checks if container is running; uses exec if so, run otherwise.
	// Emits a warning when the fallback to run actually triggers — ephemeral run
	// containers are an easy source of confusion (state not persisted, no shared
	// network with other compose services, etc.).
	ExecModeExecOrRun ExecMode = "exec-or-run"
	// ExecModeExecOrFail (default) pre-checks the target service and refuses
	// with a clear devbox-level error when the container is not running.
	// Prevents the silent compose-fallback that ExecModeExecOrRun does for
	// tools that legitimately work as ephemeral runs (mc, composer, etc.).
	ExecModeExecOrFail ExecMode = "exec-or-fail"
)

// DefaultExecMode is the ExecMode used when a service_exec command does not
// specify mode. It is exec-or-fail rather than exec so that a "service not
// running" condition surfaces as an actionable devbox error rather than a raw
// compose stderr trace.
const DefaultExecMode = ExecModeExecOrFail

// IsValid reports whether m is one of the canonical ExecMode values.
// The empty string is considered valid (interpreted as the default at the
// dispatch site); use this for schema validation, not for switch coverage.
func (m ExecMode) IsValid() bool {
	switch m {
	case "", ExecModeExec, ExecModeRun, ExecModeExecOrRun, ExecModeExecOrFail:
		return true
	}
	return false
}

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

// ParamWidget identifies the form widget type for prompting a parameter.
type ParamWidget string

const (
	// WidgetInput displays a text input field.
	WidgetInput ParamWidget = "input"
	// WidgetSelect displays a single-choice selector.
	WidgetSelect ParamWidget = "select"
	// WidgetMultiselect displays a multi-choice selector.
	WidgetMultiselect ParamWidget = "multiselect"
	// WidgetConfirm displays a yes/no confirmation.
	WidgetConfirm ParamWidget = "confirm"
)

// OptionItem is a single option in a ParamOptions list, with an optional label.
type OptionItem struct {
	Value string `yaml:"value"`
	Label string `yaml:"label"`
}

// ParamOptions holds the source of options for select/multiselect widgets.
// Either Static (literal list) or From (dot-path reference) is populated, never both.
type ParamOptions struct {
	Static []OptionItem // literal option list
	From   string       // dot-path reference (canonical form, no ${...} wrapper)
}

// Compile-time interface check — keeps the contract honest.
var _ yaml.Unmarshaler = (*ParamOptions)(nil)

// UnmarshalYAML unmarshals a ParamOptions from YAML.
// Accepts: null/missing (zero), sequence of scalars, sequence of maps,
// or a scalar ${...} reference. Errors on invalid forms.
func (p *ParamOptions) UnmarshalYAML(node *yaml.Node) error {
	if node == nil || node.Tag == "!!null" {
		return nil
	}

	switch node.Kind {
	case yaml.ScalarNode:
		// Scalar must be a ${...} reference.
		s := strings.TrimSpace(node.Value)
		if !strings.HasPrefix(s, "${") || !strings.HasSuffix(s, "}") {
			return fmt.Errorf("options: expected `${...}` reference or sequence, got plain scalar %q", node.Value)
		}
		inner := strings.TrimSpace(s[2 : len(s)-1]) // strip ${ and }
		if inner == "" {
			return fmt.Errorf("options: `${}` is empty; expected a dot-path")
		}
		p.From = inner
		return nil

	case yaml.SequenceNode:
		// Sequence of scalars or maps.
		if len(node.Content) == 0 {
			p.Static = []OptionItem{}
			return nil
		}

		// Detect the element type from the first element.
		firstElem := node.Content[0]
		switch firstElem.Kind {
		case yaml.ScalarNode:
			// Scalar sequence: each element becomes {Value: s, Label: s}.
			for i, elem := range node.Content {
				if elem.Kind != yaml.ScalarNode {
					return fmt.Errorf("options[%d]: mixed scalar and non-scalar sequence not allowed", i)
				}
				p.Static = append(p.Static, OptionItem{
					Value: elem.Value,
					Label: elem.Value,
				})
			}
			return nil
		case yaml.MappingNode:
			// Map sequence: decode each as OptionItem.
			for i, elem := range node.Content {
				if elem.Kind != yaml.MappingNode {
					return fmt.Errorf("options[%d]: mixed scalar and non-scalar sequence not allowed", i)
				}
				var item OptionItem
				if err := elem.Decode(&item); err != nil {
					return fmt.Errorf("options[%d]: %w", i, err)
				}
				if item.Value == "" {
					return fmt.Errorf("options[%d]: 'value' field is required and must be non-empty (check for typos)", i)
				}
				p.Static = append(p.Static, item)
			}
			return nil
		default:
			return fmt.Errorf("options[0]: sequence element must be scalar or mapping, got %v", firstElem.Kind)
		}

	case yaml.MappingNode:
		return fmt.Errorf("options: expected sequence or `${...}` reference, got mapping")

	default:
		return fmt.Errorf("options: unexpected node kind %v", node.Kind)
	}
}

// IsZero reports whether the ParamOptions is empty (no static list and no reference).
func (p *ParamOptions) IsZero() bool {
	return p == nil || (len(p.Static) == 0 && p.From == "")
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
	// Widget specifies the form widget type. When empty, inferred from Type and Options.
	Widget ParamWidget `yaml:"widget"`
	// Options provides the list of choices for select/multiselect widgets.
	Options *ParamOptions `yaml:"options"`
	// Separator is the string used to join multiselect values. Defaults to " ".
	Separator string `yaml:"separator"`
}

// EffectiveWidget returns the widget to use for this parameter, applying inference rules.
// If Widget is explicitly set, returns it. Otherwise infers based on Type and Options:
//   - bool type → confirm
//   - non-empty Options → select
//   - string/int/path type, empty Options → input
func (pd *ParamDef) EffectiveWidget() ParamWidget {
	if pd.Widget != "" {
		return pd.Widget
	}

	// Infer from Type and Options.
	if pd.Type == ParamTypeBool {
		return WidgetConfirm
	}
	if pd.Options != nil && len(pd.Options.Static) > 0 {
		return WidgetSelect
	}
	if pd.Options != nil && pd.Options.From != "" {
		return WidgetSelect
	}
	// Default: string, int, path with no options → input.
	return WidgetInput
}

// ContextDef defines a single named context value derived from the merged config.
type ContextDef struct {
	// From is a dot-path into the merged DweConfig.Raw map.
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
	// Name is an optional identifier for the step within its enclosing workflow.
	// When omitted on a Command step it defaults to Command at lookup time
	// (StepName()); pipeline-side sub_step_overrides target this name.
	// Names must be unique across all sub-steps of a workflow (top-level + nested
	// parallel container leaves combined); collisions are rejected at load time.
	Name string `yaml:"name,omitempty"`
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

// StepName returns the effective sub-step identifier used for pipeline
// sub_step_overrides lookups: the explicit Name when set, otherwise the
// referenced Command. Returns an empty string for confirm/parallel containers
// that carry neither Name nor Command.
func (s WorkflowStep) StepName() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Command
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
	// AlwaysShowOutput, when true, dumps each successful sub-step's captured
	// stdout/stderr between separator bars after the group completes. The
	// default (false) keeps the failure-only behaviour. Skipped and cancelled
	// sub-steps never produce output and are unaffected.
	AlwaysShowOutput bool `yaml:"always_show_output,omitempty"`
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
	// Private hides the command from `dwe command list` but allows
	// it to be referenced from workflows.
	Private bool `yaml:"private"`
	// Confirmation asks the user to confirm before the command is executed.
	Confirmation bool `yaml:"confirmation"`
	// ConfirmationText is the prompt shown when Confirmation is true.
	// Defaults to DefaultConfirmationText when empty.
	ConfirmationText string `yaml:"confirmation_text"`
	// Notify opts the command in to a desktop notification when it
	// finishes. Only fires when the command is the top-level invocation —
	// transitive invocations (workflow sub-steps, pipeline actions) are
	// suppressed at runtime regardless of this field.
	Notify bool `yaml:"notify,omitempty"`

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

	// --- type=daemon fields ---
	// Daemon is the YAML schema block for type=daemon commands.
	// Forbidden on any other type by validateDaemonType.
	Daemon *DaemonSpec `yaml:"daemon"`
	// SourceDaemon is expansion-time metadata populated by the registry
	// expander on synthetic .start/.logs/.stop/.restart commands. It carries
	// the source daemon's *DaemonSpec so inspect can render structural fields
	// without round-tripping `with:`. Never loaded from YAML; never validated
	// by validateBuiltinType/validateWorkflowType.
	SourceDaemon *DaemonSpec `yaml:"-"`
	// DerivedFromDaemon, when non-empty, is the base ID of the source daemon
	// command this synthetic was expanded from. Populated by the registry
	// expander; used by inspect to render the "derived from" line.
	DerivedFromDaemon string `yaml:"-"`

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

	if c.Type != CommandTypeDaemon && c.Daemon != nil {
		return fmt.Errorf("command %q: %w (got type=%s)", c.ID, ErrDaemonLeakedOnNonDaemon, c.Type)
	}

	switch c.Type {
	case CommandTypeShell, CommandTypeDwe:
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
	case CommandTypeDaemon:
		if err := c.validateDaemonType(); err != nil {
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

	// Validate param widgets and options.
	if err := c.validateParams(); err != nil {
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
	if c.Type == CommandTypeDwe {
		if c.Cmd == "" {
			return fmt.Errorf("cmd is required for type=dwe")
		}
		if len(c.Argv) > 0 {
			return fmt.Errorf("argv is not valid for type=dwe; use cmd")
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
	if c.Type == CommandTypeDwe && c.Workdir != "" {
		return fmt.Errorf("workdir is not valid for type=dwe")
	}
	if c.User != "" {
		return fmt.Errorf("user is not valid for type=%s", c.Type)
	}
	if c.Runner != nil {
		return fmt.Errorf("runner is not valid for type=%s", c.Type)
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
	if c.User != "" {
		return fmt.Errorf("user is not valid for type=script")
	}
	if c.Runner != nil {
		return fmt.Errorf("runner is not valid for type=script")
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
	// Runtime safety net: service_run should never have top-level mode (enforced at parse time via allowlist)
	if c.Type == CommandTypeServiceRun && c.Mode != "" && c.Mode != ExecModeRun {
		return fmt.Errorf("mode is not applicable for type=service_run (always runs a new container)")
	}
	if !c.Mode.IsValid() {
		return fmt.Errorf("mode %q is invalid (must be one of: exec, run, exec-or-run, exec-or-fail)", c.Mode)
	}
	if c.Runner != nil && !c.Runner.Mode.IsValid() {
		return fmt.Errorf("runner.mode %q is invalid (must be one of: exec, run, exec-or-run, exec-or-fail)", c.Runner.Mode)
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
	if c.User != "" {
		return fmt.Errorf("user is not valid for type=workflow")
	}
	if c.Runner != nil {
		return fmt.Errorf("runner is not valid for type=workflow")
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

// Sentinel errors emitted by validateDaemonType. validate/commands/daemon.go
// uses errors.Is to detect them when suppressing duplicate fallback diagnostics.
var (
	ErrDaemonServiceRequired           = errors.New("daemon: service required")
	ErrDaemonServiceNotLiteral         = errors.New("daemon: service must be literal (no ${...} or {{...}})")
	ErrDaemonBlockRequired             = errors.New("daemon: daemon block required")
	ErrDaemonContainerTemplateRequired = errors.New("daemon: container_template required")
	ErrDaemonOnAlreadyRunningInvalid   = errors.New("daemon: on_already_running must be \"error\" or \"noop\"")
	ErrDaemonStopTimeoutInvalid        = errors.New("daemon: stop_timeout invalid")
	ErrDaemonLeakedOnNonDaemon         = errors.New("daemon: daemon block is only valid on type=daemon")
)

// validateDaemonType enforces runtime-critical checks on a type=daemon command.
// Param-reference walks (every ${param.X} in container_template references a
// declared param with a pattern: set) stay in validate/commands/daemon.go.
//
// Uses errors.Join to surface every field error rather than short-circuiting,
// so users see all problems in a single cmd.Validate() pass.
func (c *CommandDef) validateDaemonType() error {
	var errs []error

	// Type-foreign fields should not appear on daemon.
	if c.Cmd != "" {
		errs = append(errs, fmt.Errorf("cmd is not valid for type=daemon (use argv)"))
	}
	if c.Script != nil {
		errs = append(errs, fmt.Errorf("script field is not valid for type=daemon"))
	}
	if len(c.Steps) > 0 {
		errs = append(errs, fmt.Errorf("steps field is not valid for type=daemon"))
	}

	// service: required, literal.
	effectiveService := c.Service
	if c.Runner != nil && c.Runner.Service != "" {
		effectiveService = c.Runner.Service
	}
	if effectiveService == "" {
		errs = append(errs, ErrDaemonServiceRequired)
	} else if strings.Contains(effectiveService, "${") || strings.Contains(effectiveService, "{{") {
		errs = append(errs, ErrDaemonServiceNotLiteral)
	}

	// daemon block.
	if c.Daemon == nil {
		errs = append(errs, ErrDaemonBlockRequired)
		return errors.Join(errs...)
	}

	if strings.TrimSpace(c.Daemon.ContainerTemplate) == "" {
		errs = append(errs, ErrDaemonContainerTemplateRequired)
	}

	switch c.Daemon.OnAlreadyRunning {
	case "", "error", "noop":
	default:
		errs = append(errs, fmt.Errorf("%w (got %q)", ErrDaemonOnAlreadyRunningInvalid, c.Daemon.OnAlreadyRunning))
	}

	if s := strings.TrimSpace(c.Daemon.StopTimeout); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: parse %q: %v", ErrDaemonStopTimeoutInvalid, s, err))
		} else if d <= 0 {
			errs = append(errs, fmt.Errorf("%w: must be positive (got %q)", ErrDaemonStopTimeoutInvalid, s))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
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

func (c *CommandDef) validateParams() error {
	for pname, pdef := range c.Params {
		// Check Widget is a valid enum value if set.
		if pdef.Widget != "" {
			switch pdef.Widget {
			case WidgetInput, WidgetSelect, WidgetMultiselect, WidgetConfirm:
				// Valid.
			default:
				return fmt.Errorf("params.%s: widget must be one of input, select, multiselect, confirm (got %q)", pname, pdef.Widget)
			}
		}

		// Determine the effective widget.
		effective := pdef.EffectiveWidget()

		// Widget = select/multiselect requires non-empty options.
		if effective == WidgetSelect || effective == WidgetMultiselect {
			if pdef.Options == nil || (len(pdef.Options.Static) == 0 && pdef.Options.From == "") {
				return fmt.Errorf("params.%s: widget %s requires non-empty options", pname, effective)
			}
		}

		// Widget = input/confirm must have empty options.
		if effective == WidgetInput || effective == WidgetConfirm {
			if pdef.Options != nil && (len(pdef.Options.Static) > 0 || pdef.Options.From != "") {
				return fmt.Errorf("params.%s: widget %s does not accept options", pname, effective)
			}
		}

		// Pattern + Options is not allowed.
		if pdef.Pattern != "" && pdef.Options != nil && (len(pdef.Options.Static) > 0 || pdef.Options.From != "") {
			return fmt.Errorf("params.%s: pattern and options are mutually exclusive", pname)
		}

		// Separator only valid on multiselect.
		if pdef.Separator != "" && effective != WidgetMultiselect {
			return fmt.Errorf("params.%s: separator is only valid for multiselect widgets", pname)
		}

		// Static options: check for empty and duplicate values.
		if pdef.Options != nil && len(pdef.Options.Static) > 0 {
			seen := make(map[string]bool)
			for i, item := range pdef.Options.Static {
				if item.Value == "" {
					return fmt.Errorf("params.%s: options[%d] has empty value (check for typos in the 'value' key)", pname, i)
				}
				if seen[item.Value] {
					return fmt.Errorf("params.%s: duplicate option value %q", pname, item.Value)
				}
				seen[item.Value] = true
			}
		}

		// Validate default literal against static options.
		if pdef.Default != "" && pdef.Options != nil && len(pdef.Options.Static) > 0 {
			optionSet := make(map[string]bool, len(pdef.Options.Static))
			for _, item := range pdef.Options.Static {
				optionSet[item.Value] = true
			}
			candidates := []string{pdef.Default}
			if effective == WidgetMultiselect {
				sep := pdef.Separator
				if sep == "" {
					sep = " "
				}
				candidates = strings.Split(pdef.Default, sep)
			}
			for _, candidate := range candidates {
				if !optionSet[candidate] {
					return fmt.Errorf("params.%s: default %q not found in static options", pname, candidate)
				}
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
	// First pass: decode to raw map to check per-type allowlists.
	var rawData map[string]any
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&rawData); err != nil {
		return nil, fmt.Errorf("YAML parse error: %w", err)
	}

	// Validate per-type allowlists on commands.
	if cmdMap, ok := rawData["commands"].(map[string]any); ok {
		for cmdName, cmdVal := range cmdMap {
			if cmdObj, ok := cmdVal.(map[string]any); ok {
				// Extract type to determine allowlist.
				typeVal, hasType := cmdObj["type"]
				if !hasType {
					// Type will be caught as required by Validate(), skip allowlist check.
					continue
				}
				typeStr, ok := typeVal.(string)
				if !ok {
					continue
				}
				ct := CommandType(typeStr)

				// Validate top-level fields against allowlist.
				allowed := allowedFieldsFor(ct)
				for fieldName := range cmdObj {
					if !allowed[fieldName] {
						return nil, fmt.Errorf("command %q: field %q not allowed for type %q", cmdName, fieldName, typeStr)
					}
				}

				// Special validation: service_run runner cannot have mode.
				if ct == CommandTypeServiceRun {
					if runnerVal, hasRunner := cmdObj["runner"]; hasRunner {
						if runnerObj, ok := runnerVal.(map[string]any); ok {
							if _, hasRunnerMode := runnerObj["mode"]; hasRunnerMode {
								return nil, fmt.Errorf("command %q: runner.mode is not allowed for type service_run (always uses docker compose run)", cmdName)
							}
						}
					}
				}
			}
		}
	}

	// Second pass: standard YAML decode with KnownFields validation.
	var cf CommandFile
	dec = yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cf); err != nil {
		return nil, fmt.Errorf("YAML parse error: %w", err)
	}
	return &cf, nil
}
