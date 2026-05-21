// Package config provides loading and validation of devbox configuration files.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"devbox-cli/internal/condition"
	"devbox-cli/internal/filesgate"

	"gopkg.in/yaml.v3"
)

// BinariesConfig holds binary overrides for engine policy.
//
// Fields are read from the top-level devbox.yml only — not layered with
// defaults.yml or local.yml. Empty fields fall back to built-in defaults
// (devbox, docker, sh). Use DevboxBin/DockerBin/ShellBin accessors to read.
type BinariesConfig struct {
	Devbox string `yaml:"devbox"`
	Docker string `yaml:"docker"`
	Shell  string `yaml:"shell"`
}

// DevboxBin returns the configured devbox binary name (default: "devbox").
// Safe when cfg is nil.
func DevboxBin(cfg *DevboxConfig) string {
	if cfg == nil || cfg.Binaries.Devbox == "" {
		return "devbox"
	}
	return cfg.Binaries.Devbox
}

// DockerBin returns the configured docker binary name (default: "docker").
// Safe when cfg is nil.
func DockerBin(cfg *DevboxConfig) string {
	if cfg == nil || cfg.Binaries.Docker == "" {
		return "docker"
	}
	return cfg.Binaries.Docker
}

// ShellBin returns the configured shell binary name (default: "sh").
// Safe when cfg is nil.
func ShellBin(cfg *DevboxConfig) string {
	if cfg == nil || cfg.Binaries.Shell == "" {
		return "sh"
	}
	return cfg.Binaries.Shell
}

// applyBinariesDefaults fills empty BinariesConfig fields with built-in defaults.
func applyBinariesDefaults(b *BinariesConfig) {
	if b.Devbox == "" {
		b.Devbox = "devbox"
	}
	if b.Docker == "" {
		b.Docker = "docker"
	}
	if b.Shell == "" {
		b.Shell = "sh"
	}
}

// DevboxConfig is the merged top-level devbox configuration.
// It is produced by layering devbox.yml → devbox/defaults.yml → devbox/local.yml.
//
// Binaries is engine policy read from the top-level devbox.yml only — it is
// not layered with defaults.yml or local.yml. See BinariesConfig for details.
type DevboxConfig struct {
	SchemaVersion string         `yaml:"schema_version"`
	Project       ProjectConfig  `yaml:"project"`
	Tools         ToolsConfig    `yaml:"tools"`
	Runtime       RuntimeConfig  `yaml:"runtime"`
	State         string         `yaml:"state"`
	Exports       ExportsConfig  `yaml:"exports"`
	Compose       ComposeConfig  `yaml:"compose"`
	Deploy        DeployConfig   `yaml:"-"`
	Binaries      BinariesConfig `yaml:"binaries"`
	UI            UIConfig       `yaml:"ui"`

	// Services holds the fully resolved service definitions loaded from
	// devbox/services.yml with Enabled populated from the 3-layer config merge.
	// Not unmarshalled from the merge — built by LoadConfig.
	Services map[string]ServiceConfig `yaml:"-"`

	// Raw holds the merged config as a plain map, used for dot-path resolution
	// in export rules. Populated only by LoadConfig; not serialized.
	Raw map[string]any `yaml:"-"`
}

// DeployConfig holds the full deploy pipeline loaded from devbox/deploy.yml.
// It is loaded separately and not part of the 3-layer config merge.
//
// Log enables/disables file logging at .devbox/logs/<pipeline>.log for the pipeline run.
// nil means "use loader default": LoadDeployConfig defaults to true,
// LoadResetConfig defaults to false. Set explicitly via top-level `log: true|false`.
type DeployConfig struct {
	Log    *bool         `yaml:"log"`
	Phases []DeployPhase `yaml:"phases"`
}

// LogEnabled reports whether file logging is enabled. It assumes the loader
// has normalized the Log pointer; nil is treated as false defensively.
func (c *DeployConfig) LogEnabled() bool {
	return c != nil && c.Log != nil && *c.Log
}

// DeployPhase groups a set of sequential deploy steps.
// A phase with DeployServices=true is a marker: CLI resolves it by inlining
// the deploy pipelines of all enabled services in dependency order.
// Such phases must not contain Steps.
//
// When is an optional skip condition evaluated before any steps in the phase run.
// It supports the same three expression kinds as DeployStep.When:
//   - Go template "{{...}}"         — evaluated against config at plan time; phase excluded when false
//   - Builtin predicate             — e.g. "dir-empty services/main/src" (runtime)
//   - Shell command "cmd: <cmd>"    — evaluated at step-execution time (runtime)
//
// For runtime conditions the phase condition is applied to each step in the phase
// that does not already carry its own runtime when condition.
//
// Untracked marks the phase as excluded from the step counter.
// Steps in untracked phases receive index=0 and total=0 in reporter calls, and
// the pipeline StartPipeline total does not include them. PlainReporter suppresses
// all output for untracked steps (except failures, which are always printed).
// Useful for post-deploy summary phases that run after the main work is done.
type DeployPhase struct {
	Name           string               `yaml:"name"`
	Description    string               `yaml:"description"`
	When           *condition.Condition `yaml:"when,omitempty"`
	Untracked      bool                 `yaml:"untracked"`
	Steps          []DeployStep         `yaml:"steps"`
	DeployServices bool                 `yaml:"deploy_services"`
}

// DeployStep is a single atomic pipeline action.
// DeployStep is a step in a phase, using the typed action model.
//
//   - Type            — executor: shell, devbox, command, or builtin
//   - Cmd             — the action payload (command string or builtin name)
//   - With            — optional parameters for command or builtin actions
//   - When            — optional skip condition (type: template|builtin|shell)
//   - Check           — optional post-execution action (same type/cmd/with shape)
//   - FilesGate       — optional pre-step file-existence gate (decides run/skip based on files:)
//   - ContinueOnError — when true, a failed step or check is reported but pipeline continues
//   - SkipConfirm     — when true, bypasses confirmation prompts for this step only
//     (equivalent to a per-step -y / --yes); ORed with the pipeline-wide skip-confirm flag
type DeployStep struct {
	Name            string               `yaml:"name"`
	Type            string               `yaml:"type"`
	Cmd             string               `yaml:"cmd"`
	With            map[string]any       `yaml:"with,omitempty"`
	Description     string               `yaml:"description,omitempty"`
	When            *condition.Condition `yaml:"when,omitempty"`
	Check           *Action              `yaml:"check,omitempty"`
	FilesGate       *filesgate.FilesGate `yaml:"files_gate,omitempty"`
	ContinueOnError bool                 `yaml:"continue_on_error,omitempty"`
	SkipConfirm     bool                 `yaml:"skip_confirm,omitempty"`
	Parallel        *ParallelGroup       `yaml:"parallel,omitempty"`
	// SubStepOverrides applies pipeline-side orchestration directives (currently
	// files_gate) to named sub-steps of the workflow referenced by Cmd. Keys are
	// the sub-step Name (or, when absent, the sub-step's referenced Command).
	// Only valid when Type == "command" and the target command is a workflow.
	// Plan-time validation lives in internal/pipeline.ResolvePhaseSteps.
	SubStepOverrides map[string]SubStepOverride `yaml:"sub_step_overrides,omitempty"`
}

// SubStepOverride is the pipeline-side overlay for a single workflow sub-step.
// It carries per-sub-step orchestration directives (currently files_gate) from
// the originating DeployStep through to the workflow runner via
// RunContext.WorkflowSubStepOverrides. Workflow YAML never declares these —
// they live exclusively on the pipeline side so workflows stay opaque.
type SubStepOverride struct {
	FilesGate *filesgate.FilesGate `yaml:"files_gate,omitempty"`
}

// ParallelGroup represents a step-group container that runs sub-steps concurrently.
// A DeployStep with non-nil Parallel is a group step; all leaf-only fields
// (type, cmd, with, check, files_gate, continue_on_error) must be absent on
// the parent step. Allowed group-level fields: name, description, when, skip_confirm.
type ParallelGroup struct {
	MaxConcurrent int          `yaml:"max_concurrent,omitempty"`
	FailFast      *bool        `yaml:"fail_fast,omitempty"`
	Steps         []DeployStep `yaml:"steps"`
}

// deployStepKnownFields is the explicit allow-list of keys recognised on a
// DeployStep YAML mapping. A custom UnmarshalYAML bypasses the parent decoder's
// KnownFields(true), so we hand-validate keys here to preserve strict-decode
// semantics. Keep in sync with the DeployStep field tags above.
var deployStepKnownFields = map[string]bool{
	"name":               true,
	"description":        true,
	"type":               true,
	"cmd":                true,
	"with":               true,
	"when":               true,
	"check":              true,
	"files_gate":         true,
	"continue_on_error":  true,
	"skip_confirm":       true,
	"parallel":           true,
	"sub_step_overrides": true,
}

// deployStepLeafOnlyFields are keys that may appear only on a leaf step
// (Parallel == nil). Their presence alongside parallel: is a hard error.
var deployStepLeafOnlyFields = []string{
	"type", "cmd", "with", "check", "files_gate", "continue_on_error", "sub_step_overrides",
}

// UnmarshalYAML enforces:
//   - mapping shape only
//   - explicit known-field allow-list (compensates for yaml.v3 bypass of KnownFields(true)
//     inside custom UnmarshalYAML implementations)
//   - mutual exclusion between parallel: and leaf-only fields (type/cmd/with/check/
//     files_gate/continue_on_error)
//
// Length of parallel.steps is NOT enforced here; that check lives in
// internal/pipeline.ResolvePhaseSteps so it can return a typed sentinel
// (ErrEmptyParallelSteps) without an import cycle.
func (s *DeployStep) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("deploy step must be a mapping, got kind %d", value.Kind)
	}
	seen := make(map[string]bool, len(value.Content)/2)
	for i := 0; i < len(value.Content)-1; i += 2 {
		key := value.Content[i].Value
		if !deployStepKnownFields[key] {
			return fmt.Errorf("field %s not found in type config.DeployStep", key)
		}
		seen[key] = true
	}
	if seen["parallel"] {
		for _, leaf := range deployStepLeafOnlyFields {
			if seen[leaf] {
				return fmt.Errorf("parallel step %q must not set leaf-only field %q", nameOfNode(value), leaf)
			}
		}
	}
	type rawDeployStep DeployStep
	var raw rawDeployStep
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*s = DeployStep(raw)
	return nil
}

// parallelGroupKnownFields is the allow-list for the nested parallel: mapping.
// Mirrors the rationale on deployStepKnownFields.
var parallelGroupKnownFields = map[string]bool{
	"max_concurrent": true,
	"fail_fast":      true,
	"steps":          true,
}

// UnmarshalYAML enforces the known-field allow-list on the parallel: mapping.
func (p *ParallelGroup) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("parallel must be a mapping, got kind %d", value.Kind)
	}
	for i := 0; i < len(value.Content)-1; i += 2 {
		key := value.Content[i].Value
		if !parallelGroupKnownFields[key] {
			return fmt.Errorf("field %s not found in type config.ParallelGroup", key)
		}
	}
	type rawParallelGroup ParallelGroup
	var raw rawParallelGroup
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*p = ParallelGroup(raw)
	return nil
}

// nameOfNode tries to extract the "name" field value from a mapping node for
// inclusion in error messages. Returns "<unnamed>" if absent.
func nameOfNode(value *yaml.Node) string {
	for i := 0; i < len(value.Content)-1; i += 2 {
		if value.Content[i].Value == "name" && value.Content[i+1].Kind == yaml.ScalarNode {
			return value.Content[i+1].Value
		}
	}
	return "<unnamed>"
}

// Action returns the action-shaped representation of this step for ExecAction callers.
func (s DeployStep) Action() Action {
	return Action{Type: s.Type, Cmd: s.Cmd, With: s.With}
}

// ComposeConfig holds Docker Compose file declarations.
// Base is always included; tool and service overlays live inside each tool/service entry.
type ComposeConfig struct {
	Base string `yaml:"base"`
}

// ProjectConfig holds project identity fields.
type ProjectConfig struct {
	Name   string `yaml:"name"`
	Prefix string `yaml:"prefix"`
}

// FullName returns "<prefix>-<name>" or just "<name>" if prefix is empty.
func (p ProjectConfig) FullName() string {
	if p.Prefix != "" {
		return p.Prefix + "-" + p.Name
	}
	return p.Name
}

// ComposeFiles returns the ordered list of compose files for the project:
// base file first, then enabled tool overlays (sorted by key), then enabled
// service overlays (sorted by service name). This is the canonical file list
// used by all compose-aware CLI operations.
func (c *DevboxConfig) ComposeFiles() []string {
	return c.composeFiles(false)
}

// ComposeFilesAll returns the ordered list of all configured compose files,
// regardless of whether overlays are enabled: base file first, then all tool
// overlays (sorted by key), then all service overlays (sorted by service name).
// This is used by --all flags to override the active set.
func (c *DevboxConfig) ComposeFilesAll() []string {
	return c.composeFiles(true)
}

func (c *DevboxConfig) composeFiles(all bool) []string {
	files := make([]string, 0, 1+len(c.Tools)+len(c.Services))
	if c.Compose.Base != "" {
		files = append(files, c.Compose.Base)
	}

	for _, name := range slices.Sorted(maps.Keys(c.Tools)) {
		tool := c.Tools[name]
		if tool.Compose == "" {
			continue
		}
		if all || tool.Enabled {
			files = append(files, tool.Compose)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(c.Services)) {
		svc := c.Services[name]
		if (all || svc.Enabled) && len(svc.Compose) > 0 {
			files = append(files, svc.Compose...)
		}
	}

	return files
}

// ServiceConfigEntry represents one config file declared under a service's configs list.
// It supports a string shorthand ("- .env") and an explicit struct form:
//
//   - file: .env
//     mountpoint: src/.env
//
// The mountpoint field is optional. When set, deploy config touches that path
// (relative to the service dir) after copying to configs/, so that Docker Desktop
// virtiofs can create a nested file bind mount over it.
type ServiceConfigEntry struct {
	// File is the config filename. It is the source (configs/services/<svc>/<file>)
	// and the default destination basename under services/<svc>/configs/.
	File string
	// Mountpoint is an optional path relative to the service dir (e.g. "src/.env").
	// When set, deploy config touches this file after copying to configs/ so that
	// Docker Desktop virtiofs can create a nested file bind mount over it.
	Mountpoint string
}

// UnmarshalYAML supports both the string shorthand ("- .env") and the explicit
// struct form ("- file: .env\n  mountpoint: src/.env").
func (e *ServiceConfigEntry) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		e.File = value.Value
		return nil
	}
	// Struct form: decode into a temporary alias to avoid recursion.
	type entryAlias struct {
		File       string `yaml:"file"`
		Mountpoint string `yaml:"mountpoint"`
	}
	var a entryAlias
	if err := value.Decode(&a); err != nil {
		return err
	}
	e.File = a.File
	e.Mountpoint = a.Mountpoint
	return nil
}

// ServiceIDEConfig holds IDE rendering settings for a service.
type ServiceIDEConfig struct {
	Enabled  *bool  `yaml:"enabled"`
	Template string `yaml:"template"`
}

// ServiceAIConfig holds AI-related settings for a service. The current keys
// (Enabled, Template) control hub-level agent documentation rendering, which
// is the primary AI feature in devbox. Future AI subsystems should be nested
// here as sub-blocks (e.g. ai.shell, ai.commands) rather than added as new
// top-level keys.
type ServiceAIConfig struct {
	Enabled  *bool  `yaml:"enabled"`
	Template string `yaml:"template"`
}

// ServiceGitHooksConfig holds git-hooks rendering settings for a service.
type ServiceGitHooksConfig struct {
	Enabled  *bool  `yaml:"enabled"`
	Template string `yaml:"template"`
}

// ServiceRenderConfig holds all rendering-related configuration for a service.
type ServiceRenderConfig struct {
	IDE ServiceIDEConfig      `yaml:"ide"`
	AI  ServiceAIConfig       `yaml:"ai"`
	Git ServiceGitHooksConfig `yaml:"git"`
}

// StatusColumn declares one custom column rendered in the status table for a
// service or tool. Value is a hermetic Go template evaluated via tpl.Render.
type StatusColumn struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// ServiceConfig describes a single application service.
// Definitions are loaded from devbox/services.yml; the Enabled flag is resolved
// from the 3-layer config merge (mandatory services are always enabled).
type ServiceConfig struct {
	Type            string               `yaml:"type"`
	Container       string               `yaml:"container"`
	Mandatory       bool                 `yaml:"mandatory"`
	Enabled         bool                 `yaml:"-"` // computed: mandatory || services.<name>.enabled
	Dir             string               `yaml:"dir"`
	DirInternal     string               `yaml:"dir_internal"`
	WorkDirInternal string               `yaml:"work_dir_internal"`
	Configs         []ServiceConfigEntry `yaml:"configs"`
	Dirs            []string             `yaml:"dirs"`
	Extends         string               `yaml:"extends"`
	DependsOn       []string             `yaml:"depends_on"`
	Compose         []string             `yaml:"compose"`
	CLI             ServiceCLIConfig     `yaml:"cli"`
	Render          ServiceRenderConfig  `yaml:"render"`
	Status          []StatusColumn       `yaml:"status,omitempty"`
}

// IDERenderEnabledExplicit returns the IDE render enabled state and whether it was explicitly set.
// If Enabled is non-nil, returns its value and true.
// If Enabled is nil, returns true for type "app" (default) or false for other types, and false (not explicit).
func (s ServiceConfig) IDERenderEnabledExplicit() (enabled bool, explicit bool) {
	if s.Render.IDE.Enabled != nil {
		return *s.Render.IDE.Enabled, true
	}
	return s.Type == "app", false
}

// IDERenderEnabled returns whether this service should participate in IDE rendering.
// It's a simple wrapper around IDERenderEnabledExplicit that discards the explicit flag.
func (s ServiceConfig) IDERenderEnabled() bool {
	enabled, _ := s.IDERenderEnabledExplicit()
	return enabled
}

// AIRenderEnabledExplicit returns the AI docs render enabled state and whether it was explicitly set.
// If Enabled is non-nil, returns its value and true.
// If Enabled is nil, returns true (default enabled for all service types) and false (not explicit).
func (s ServiceConfig) AIRenderEnabledExplicit() (enabled bool, explicit bool) {
	if s.Render.AI.Enabled != nil {
		return *s.Render.AI.Enabled, true
	}
	return true, false
}

// AIRenderEnabled returns whether this service should participate in AI docs rendering.
// It's a simple wrapper around AIRenderEnabledExplicit that discards the explicit flag.
func (s ServiceConfig) AIRenderEnabled() bool {
	enabled, _ := s.AIRenderEnabledExplicit()
	return enabled
}

// GitRenderEnabledExplicit returns the git-hooks render enabled state and whether it was explicitly set.
// If Enabled is non-nil, returns its value and true.
// If Enabled is nil, returns true for type "app" (default) or false for other types, and false (not explicit).
func (s ServiceConfig) GitRenderEnabledExplicit() (enabled bool, explicit bool) {
	if s.Render.Git.Enabled != nil {
		return *s.Render.Git.Enabled, true
	}
	return s.Type == "app", false
}

// GitRenderEnabled returns whether this service should participate in git-hooks rendering.
// It's a simple wrapper around GitRenderEnabledExplicit that discards the explicit flag.
func (s ServiceConfig) GitRenderEnabled() bool {
	enabled, _ := s.GitRenderEnabledExplicit()
	return enabled
}

// ServiceCLIConfig holds defaults for the `services cli` command.
// All fields are optional; empty values fall back to built-in defaults.
type ServiceCLIConfig struct {
	// Mode controls how the shell session is started: "auto", "exec", or "run".
	// auto (default): running->exec, absent->run, stopped->error.
	// exec: always docker exec, error if not running.
	// run: always docker compose run --rm.
	Mode string `yaml:"mode"`
	// Shell is the shell binary to invoke inside the container (default: bash).
	Shell string `yaml:"shell"`
	// User is the container user to run as (default: current OS user UID).
	// Overridden by --root flag at runtime.
	User string `yaml:"user"`
	// WorkDir is the working directory inside the container.
	// Falls back to service.work_dir_internal, then dir_internal.
	WorkDir string `yaml:"workdir"`
	// Env is a map of environment variables to pass into the container session.
	// Accepts both map form (KEY: VALUE) and list form (- KEY=VALUE).
	Env map[string]string `yaml:"env"`
}

// UnmarshalYAML supports both map and list forms for the Env field.
// Map form:   env: { KEY: VALUE }
// List form:  env: [ "KEY=VALUE" ]
func (c *ServiceCLIConfig) UnmarshalYAML(value *yaml.Node) error {
	// Decode using a type alias to avoid infinite recursion.
	type cliAlias struct {
		Mode    string    `yaml:"mode"`
		Shell   string    `yaml:"shell"`
		User    string    `yaml:"user"`
		WorkDir string    `yaml:"workdir"`
		Env     yaml.Node `yaml:"env"`
	}
	var a cliAlias
	if err := value.Decode(&a); err != nil {
		return err
	}
	c.Mode = a.Mode
	c.Shell = a.Shell
	c.User = a.User
	c.WorkDir = a.WorkDir

	if a.Env.Kind == 0 {
		// env key was not present.
		return nil
	}

	switch a.Env.Kind {
	case yaml.MappingNode:
		// Map form: env: { KEY: VALUE }
		if err := a.Env.Decode(&c.Env); err != nil {
			return fmt.Errorf("cli.env: %w", err)
		}
	case yaml.SequenceNode:
		// List form: env: [ "KEY=VALUE" ]
		c.Env = make(map[string]string)
		for _, item := range a.Env.Content {
			k, v, found := strings.Cut(item.Value, "=")
			if !found || k == "" {
				return fmt.Errorf("cli.env: %q is not in KEY=VALUE format", item.Value)
			}
			c.Env[k] = v
		}
	default:
		return fmt.Errorf("cli.env: must be a map or a list of KEY=VALUE strings")
	}
	return nil
}

// ToolConfig holds configuration for a single optional tool.
// Keys in ToolsConfig are constrained to match the regex ^[A-Za-z_][A-Za-z0-9_]*$
// (Go identifier safe) so they can be used with Go template dot syntax.
// Container, Compose, and Status are loaded from devbox/tools.yml.
// Enabled is resolved programmatically from the 3-layer overlay merge.
// Host and Port are resolved from the 3-layer runtime.hosts.<name> /
// runtime.ports.<name> merge — they live in defaults.yml so they can be
// overridden in local.yml without touching tools.yml.
type ToolConfig struct {
	Enabled   bool           `yaml:"-"` // resolved from tools.<name>.enabled overlay
	Container string         `yaml:"container"`
	Host      string         `yaml:"-"` // resolved from runtime.hosts.<name>
	Port      int            `yaml:"-"` // resolved from runtime.ports.<name>
	Compose   string         `yaml:"compose"`
	Status    []StatusColumn `yaml:"status,omitempty"`
}

// ToolsConfig is a map of tool names to their configurations.
// Keys are constrained to match the regex ^[A-Za-z_][A-Za-z0-9_]*$
// (Go identifier safe) so they can be used with Go template dot syntax.
// Examples: map[string]ToolConfig{"adminer": {...}, "elasticvue": {...}}
type ToolsConfig map[string]ToolConfig

// AnyEnabled returns true when at least one tool is enabled.
// Safe on a nil map receiver; range over a nil map runs zero iterations.
func (t ToolsConfig) AnyEnabled() bool {
	for _, tool := range t {
		if tool.Enabled {
			return true
		}
	}
	return false
}

// RuntimeConfig describes ports, hostnames, and other runtime settings.
type RuntimeConfig struct {
	UseHTTPS bool         `yaml:"use_https"`
	Ports    RuntimePorts `yaml:"ports"`
	Hosts    RuntimeHosts `yaml:"hosts"`
	SPX      SPXConfig    `yaml:"spx"`
}

// RuntimePorts maps runtime role names (e.g. "app", "db", "redis", "main") to host ports.
// Keys are constrained to match the regex ^[A-Za-z_][A-Za-z0-9_]*$ (Go identifier safe)
// so they can be used with Go template dot syntax.
// Non-tool roles live here (app, db, redis, main); tool port references live under Tools.<name>.Port.
type RuntimePorts map[string]int

// RuntimeHosts maps runtime role names (e.g. "main", "app") to virtual hostnames.
// Keys are constrained to match the regex ^[A-Za-z_][A-Za-z0-9_]*$ (Go identifier safe)
// so they can be used with Go template dot syntax.
// Non-tool roles live here (main, app); tool host references live under Tools.<name>.Host.
type RuntimeHosts map[string]string

// SPXConfig holds SPX profiler settings.
type SPXConfig struct {
	Path string `yaml:"path"`
}

// ExportsConfig groups export targets.
type ExportsConfig struct {
	Env []ExportRule `yaml:"env"`
}

// ReservedExportNames lists env variable names that the renderer always emits
// itself before any user-defined export rule runs. User rules are forbidden
// from redeclaring them: the rendering layer reads the system values from the
// project config and host environment, and a duplicate line in the output
// .env would have parser-defined precedence.
var ReservedExportNames = []string{"PROJECT", "UID", "GID"}

// IsReservedExportName reports whether name is reserved by the system and
// therefore cannot be used as an ExportRule.Name.
func IsReservedExportName(name string) bool {
	return slices.Contains(ReservedExportNames, name)
}

// ExportRule describes a single env variable to render.
//
// Fields:
//   - Name     — env variable name (e.g. APP_PORT)
//   - From     — dot-path into the effective config (e.g. runtime.ports.app)
//   - Default  — fallback value when From is missing or resolves to zero
//   - Required — error if From is missing and Default is empty
//   - Format   — output format: "string" (default), "bool", "int"
//   - When     — dot-path; rule is skipped when the value at this path is falsy
//   - Comment  — written as "# comment" above the variable in the generated file
type ExportRule struct {
	Name     string `yaml:"name"`
	From     string `yaml:"from"`
	Default  string `yaml:"default"`
	Required bool   `yaml:"required"`
	Format   string `yaml:"format"`
	When     string `yaml:"when"`
	Comment  string `yaml:"comment"`
}

// validIdentifierKey reports whether s matches the Go identifier regex:
// ^[A-Za-z_][A-Za-z0-9_]*$. These keys can be used safely with Go template dot syntax.
func validIdentifierKey(s string) bool {
	if len(s) == 0 {
		return false
	}
	if (s[0] < 'a' || s[0] > 'z') && (s[0] < 'A' || s[0] > 'Z') && s[0] != '_' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// validateConfigKeys checks that all keys in Tools, Runtime.Ports, and Runtime.Hosts
// are identifier-safe (^[A-Za-z_][A-Za-z0-9_]*$), and that every declared tool entry
// (enabled or disabled) has non-empty container plus a corresponding
// runtime.hosts.<name> and positive runtime.ports.<name>.
// Tool host/port live in the shared runtime.{hosts,ports} collections alongside
// service roles — overrideable via the 3-layer merge.
func validateConfigKeys(cfg *DevboxConfig) error {
	for _, key := range slices.Sorted(maps.Keys(cfg.Tools)) {
		if !validIdentifierKey(key) {
			return fmt.Errorf("invalid tool key %q: must match ^[A-Za-z_][A-Za-z0-9_]*$ (identifier-safe for template dot syntax)", key)
		}
		tool := cfg.Tools[key]
		if tool.Container == "" {
			return fmt.Errorf("tool %q: container is required (set in devbox/tools.yml)", key)
		}
		if tool.Host == "" {
			return fmt.Errorf("tool %q: host is required (set runtime.hosts.%s in devbox/defaults.yml)", key, key)
		}
		if tool.Port <= 0 {
			return fmt.Errorf("tool %q: port must be positive (set runtime.ports.%s in devbox/defaults.yml), got %d", key, key, tool.Port)
		}
	}

	for _, key := range slices.Sorted(maps.Keys(cfg.Runtime.Ports)) {
		if !validIdentifierKey(key) {
			return fmt.Errorf("invalid runtime.ports key %q: must match ^[A-Za-z_][A-Za-z0-9_]*$ (identifier-safe for template dot syntax)", key)
		}
	}

	for _, key := range slices.Sorted(maps.Keys(cfg.Runtime.Hosts)) {
		if !validIdentifierKey(key) {
			return fmt.Errorf("invalid runtime.hosts key %q: must match ^[A-Za-z_][A-Za-z0-9_]*$ (identifier-safe for template dot syntax)", key)
		}
	}

	return nil
}

// detectLegacyComposeOverlays checks the merged raw YAML for a compose.overlays block.
// If found, returns a migration error pointing users to the new per-tool compose field.
func detectLegacyComposeOverlays(raw map[string]any) error {
	compose, ok := raw["compose"].(map[string]any)
	if !ok {
		return nil
	}
	overlaysRaw, exists := compose["overlays"]
	if !exists || overlaysRaw == nil {
		return nil
	}
	overlays, ok := overlaysRaw.(map[string]any)
	if !ok || len(overlays) == 0 {
		return fmt.Errorf("compose.overlays is no longer supported; move overlay files to individual tools: tools.<name>.compose instead. See docs/reference/config/devbox.md for migration details")
	}
	var keys []string
	for k := range overlays {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Errorf("compose.overlays is no longer supported; move overlay files to individual tools: tools.<name>.compose instead. See docs/reference/config/devbox.md for migration details. Found overlays: %v", keys)
}

// LoadConfig loads the merged DevboxConfig by layering:
//
//  1. devboxPath (required)
//  2. <dir>/devbox/defaults.yml (optional, versioned project defaults)
//  3. <dir>/devbox/local.yml   (optional, local overrides, gitignored)
//
// Later layers win on conflict; maps are merged recursively.
// The merged raw map is stored in DevboxConfig.Raw for dot-path resolution.
//
// Tool definitions live in devbox/tools.yml; the three layers above may carry
// only `tools.<name>.enabled` overlays.
func LoadConfig(devboxPath string) (*DevboxConfig, error) {
	baseDir := filepath.Dir(devboxPath)

	// Read each layer separately so the cross-layer tools overlay validator can
	// attribute errors to a specific source file.
	type rawLayer struct {
		path string
		data map[string]any
	}
	var layers []rawLayer

	// Layer 1: devbox.yml (required)
	base, err := loadRawYAML(devboxPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", devboxPath, err)
	}
	layers = append(layers, rawLayer{path: devboxPath, data: base})

	// Layer 2: devbox/defaults.yml (optional)
	defaultsPath := filepath.Join(baseDir, "devbox", "defaults.yml")
	if defaults, err := loadRawYAML(defaultsPath); err == nil {
		layers = append(layers, rawLayer{path: defaultsPath, data: defaults})
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", defaultsPath, err)
	}

	// Layer 3: devbox/local.yml (optional)
	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	if local, err := loadRawYAML(localPath); err == nil {
		layers = append(layers, rawLayer{path: localPath, data: local})
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", localPath, err)
	}

	// Load devbox/tools.yml — the only source of typed tool definitions.
	toolsPath := filepath.Join(baseDir, "devbox", "tools.yml")
	tools, err := LoadToolsConfig(toolsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", toolsPath, err)
	}
	if tools == nil {
		tools = ToolsConfig{}
	}

	// Cross-layer validate: each layer may only carry tools.<name>.enabled
	// against the declared tool set.
	for _, layer := range layers {
		if err := validateToolsOverlay(layer.path, layer.data, tools); err != nil {
			return nil, err
		}
	}

	// Merge the layers.
	merged := make(map[string]any)
	for _, layer := range layers {
		deepMerge(merged, layer.data)
	}

	data, err := yaml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal merged config: %w", err)
	}
	var cfg DevboxConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal merged config: %w", err)
	}

	// Binaries are engine policy: read from top-level devbox.yml only, never layered.
	// Re-parse the base file to get the raw binaries block, overwriting whatever the
	// merged unmarshal produced. Defaults are applied after so a partial top-level
	// block (e.g. only docker: podman) still gets sensible values for the other fields.
	var topView struct {
		Binaries BinariesConfig `yaml:"binaries"`
	}
	if topBytes, err := os.ReadFile(devboxPath); err == nil {
		_ = yaml.Unmarshal(topBytes, &topView) // best-effort; parse errors fall back to defaults
	}
	cfg.Binaries = topView.Binaries
	applyBinariesDefaults(&cfg.Binaries)

	for i, rule := range cfg.Exports.Env {
		if IsReservedExportName(rule.Name) {
			return nil, fmt.Errorf("exports.env[%d]: %q is a reserved system variable and cannot be redeclared as an export rule (reserved: %s)",
				i, rule.Name, strings.Join(ReservedExportNames, ", "))
		}
	}

	// Authoritative tool assignment: tools.yml is the only source for typed tool
	// definitions. The marshal/unmarshal above may have populated cfg.Tools with
	// zero-value entries from overlay tools.<name>.enabled blocks; replace it now.
	// Host and Port are resolved from the merged runtime.hosts.<name> /
	// runtime.ports.<name> so user-tunable values live in defaults.yml / local.yml.
	cfg.Tools = tools
	for name, tool := range cfg.Tools {
		val, ok := ResolvePath(merged, "tools."+name+".enabled")
		if ok {
			tool.Enabled = isTruthy(val)
		} else {
			tool.Enabled = false
		}
		tool.Host = cfg.Runtime.Hosts[name]
		tool.Port = cfg.Runtime.Ports[name]
		cfg.Tools[name] = tool
	}

	cfg.Raw = merged
	// Store config path so deploy resolution can find service deploy files.
	cfg.Raw["__configPath"] = devboxPath

	// Inject tool definitions into the raw map so dot-paths like
	// tools.<name>.port resolve via ResolvePath for export rules, info.yml,
	// docker.yml template expressions, and user command default_from.
	injectToolsIntoRaw(merged, cfg.Tools)

	// Normalize Raw["binaries"] so dot-path lookups (e.g. ${binaries.docker} in
	// export rules) see the same effective values as cfg.Binaries.* Go callers.
	// Any binaries: block from defaults.yml or local.yml is silently discarded here.
	cfg.Raw["binaries"] = map[string]any{
		"devbox": cfg.Binaries.Devbox,
		"docker": cfg.Binaries.Docker,
		"shell":  cfg.Binaries.Shell,
	}

	// Load devbox/services.yml separately (not merged with config layers).
	servicesPath := filepath.Join(baseDir, "devbox", "services.yml")
	if services, err := LoadServicesConfig(servicesPath); err == nil {
		// Resolve enabled state from the 3-layer merge.
		for name, svc := range services {
			if svc.Mandatory {
				svc.Enabled = true
			} else {
				val, ok := ResolvePath(merged, "services."+name+".enabled")
				if ok {
					svc.Enabled = isTruthy(val)
				}
			}
			services[name] = svc
		}
		cfg.Services = services
		// Inject service definitions into the raw map so export rules
		// like "from: services.main.container" resolve via dot-path.
		injectServicesIntoRaw(merged, services)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", servicesPath, err)
	}

	// Load devbox/deploy.yml separately (not merged with config layers).
	deployPath := filepath.Join(baseDir, "devbox", "deploy.yml")
	if deployCfg, err := LoadDeployConfig(deployPath); err == nil {
		cfg.Deploy = *deployCfg
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", deployPath, err)
	}

	// Validate config keys and detect legacy compose.overlays.
	if err := detectLegacyComposeOverlays(merged); err != nil {
		return nil, err
	}
	if err := validateConfigKeys(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// toolsFile is the top-level structure of devbox/tools.yml.
type toolsFile struct {
	Tools ToolsConfig `yaml:"tools"`
}

// LoadToolsConfig loads tool definitions from devbox/tools.yml using strict
// known-field decoding. A missing file returns (nil, os.ErrNotExist) — callers
// must test with errors.Is(err, os.ErrNotExist).
func LoadToolsConfig(path string) (ToolsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f toolsFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Tools == nil {
		f.Tools = ToolsConfig{}
	}
	return f.Tools, nil
}

// validateToolsOverlay rejects any field other than `enabled:` under a layer's
// tools.<name> mapping and any tools.<name> entry naming a tool not declared in
// devbox/tools.yml. The layerPath is included in the error message so the user
// knows which file to edit.
func validateToolsOverlay(layerPath string, raw map[string]any, declared ToolsConfig) error {
	toolsRaw, ok := raw["tools"]
	if !ok || toolsRaw == nil {
		return nil
	}
	toolsMap, ok := toolsRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: tools: must be a mapping", layerPath)
	}
	for _, name := range slices.Sorted(maps.Keys(toolsMap)) {
		if _, declaredOK := declared[name]; !declaredOK {
			return fmt.Errorf("%s: tools.%s: unknown tool (declared tools live in devbox/tools.yml)", layerPath, name)
		}
		entryRaw := toolsMap[name]
		if entryRaw == nil {
			continue
		}
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: tools.%s: must be a mapping", layerPath, name)
		}
		for _, key := range slices.Sorted(maps.Keys(entry)) {
			if key != "enabled" {
				return fmt.Errorf("%s: tools.%s.%s: tool definitions belong in devbox/tools.yml; overlays may only set enabled", layerPath, name, key)
			}
		}
	}
	return nil
}

// injectToolsIntoRaw merges tool definitions into raw["tools"] so dot-path
// lookups (e.g. tools.adminer.container, tools.adminer.enabled) resolve
// against the merged map. Host and port live under runtime.hosts.<name> /
// runtime.ports.<name> and are NOT mirrored here — there is one canonical
// dot-path per value.
func injectToolsIntoRaw(raw map[string]any, tools ToolsConfig) {
	toolsMap, ok := raw["tools"].(map[string]any)
	if !ok {
		toolsMap = make(map[string]any)
		raw["tools"] = toolsMap
	}
	for name, tool := range tools {
		entry, ok := toolsMap[name].(map[string]any)
		if !ok {
			entry = make(map[string]any)
			toolsMap[name] = entry
		}
		entry["enabled"] = tool.Enabled
		entry["container"] = tool.Container
		if tool.Compose != "" {
			entry["compose"] = tool.Compose
		}
	}
}

// servicesFile is the top-level structure of devbox/services.yml.
type servicesFile struct {
	Services map[string]ServiceConfig `yaml:"services"`
}

// LoadServicesConfig loads service definitions from devbox/services.yml.
// It resolves `extends` inheritance: a service with extends=<parent> inherits
// all zero-value fields from the parent.
func LoadServicesConfig(path string) (map[string]ServiceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f servicesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// Resolve extends in topological order so that multi-level chains (C→B→A)
	// are processed parent-first regardless of map iteration order.
	order, err := topoSortServices(f.Services)
	if err != nil {
		return nil, err
	}
	for _, name := range order {
		svc := f.Services[name]
		if svc.Extends == "" {
			continue
		}
		// Parent is guaranteed to be fully resolved already.
		parent := f.Services[svc.Extends]
		if svc.Type == "" {
			svc.Type = parent.Type
		}
		if svc.Dir == "" {
			svc.Dir = parent.Dir
		}
		if svc.DirInternal == "" {
			svc.DirInternal = parent.DirInternal
		}
		if svc.WorkDirInternal == "" {
			svc.WorkDirInternal = parent.WorkDirInternal
		}
		if len(svc.Configs) == 0 {
			svc.Configs = parent.Configs
		}
		if svc.CLI.Mode == "" {
			svc.CLI.Mode = parent.CLI.Mode
		}
		if svc.CLI.Shell == "" {
			svc.CLI.Shell = parent.CLI.Shell
		}
		if svc.CLI.User == "" {
			svc.CLI.User = parent.CLI.User
		}
		if svc.CLI.WorkDir == "" {
			svc.CLI.WorkDir = parent.CLI.WorkDir
		}
		// Merge parent CLI env into child: parent provides defaults, child overrides.
		// This mirrors the recursive map merge used throughout the 3-layer config system.
		if len(parent.CLI.Env) > 0 {
			merged := maps.Clone(parent.CLI.Env)
			maps.Copy(merged, svc.CLI.Env) // child wins on conflicts
			svc.CLI.Env = merged
		}
		// Merge dirs: parent dirs come first, child dirs appended (deduplicated).
		svc.Dirs = mergeDeduplicatedStrings(parent.Dirs, svc.Dirs)
		// IDE block inheritance: child inherits from parent if not explicitly set.
		if svc.Render.IDE.Enabled == nil && parent.Render.IDE.Enabled != nil {
			v := *parent.Render.IDE.Enabled
			svc.Render.IDE.Enabled = &v
		}
		if svc.Render.IDE.Template == "" {
			svc.Render.IDE.Template = parent.Render.IDE.Template
		}
		// AI block inheritance: child inherits from parent if not explicitly set.
		if svc.Render.AI.Enabled == nil && parent.Render.AI.Enabled != nil {
			v := *parent.Render.AI.Enabled
			svc.Render.AI.Enabled = &v
		}
		if svc.Render.AI.Template == "" {
			svc.Render.AI.Template = parent.Render.AI.Template
		}
		// Git block inheritance: child inherits from parent if not explicitly set.
		if svc.Render.Git.Enabled == nil && parent.Render.Git.Enabled != nil {
			v := *parent.Render.Git.Enabled
			svc.Render.Git.Enabled = &v
		}
		if svc.Render.Git.Template == "" {
			svc.Render.Git.Template = parent.Render.Git.Template
		}
		f.Services[name] = svc
	}

	return f.Services, nil
}

// topoSortServices returns service names in topological order (parents before
// children) so that multi-level extends chains are resolved correctly.
// Returns an error if a cycle or unknown parent is detected.
func topoSortServices(services map[string]ServiceConfig) ([]string, error) {
	// Validate all extends references first.
	for name, svc := range services {
		if svc.Extends != "" {
			if _, ok := services[svc.Extends]; !ok {
				return nil, fmt.Errorf("service %q extends unknown service %q", name, svc.Extends)
			}
		}
	}

	// Kahn's algorithm: count in-degree (number of parents) for each node.
	inDegree := make(map[string]int, len(services))
	children := make(map[string][]string, len(services))
	for name, svc := range services {
		if _, exists := inDegree[name]; !exists {
			inDegree[name] = 0
		}
		if svc.Extends != "" {
			inDegree[name]++
			children[svc.Extends] = append(children[svc.Extends], name)
		}
	}

	// Start with nodes that have no parent (roots).
	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue) // deterministic order

	result := make([]string, 0, len(services))
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		result = append(result, name)
		kids := children[name]
		sort.Strings(kids)
		for _, child := range kids {
			inDegree[child]--
			if inDegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}

	if len(result) != len(services) {
		return nil, fmt.Errorf("services config contains a circular extends dependency")
	}
	return result, nil
}

// injectServicesIntoRaw merges service definitions into raw["services"] so that
// export rules can resolve dot-paths like "services.main.container".
func injectServicesIntoRaw(raw map[string]any, services map[string]ServiceConfig) {
	svcMap, ok := raw["services"].(map[string]any)
	if !ok {
		svcMap = make(map[string]any)
		raw["services"] = svcMap
	}
	for name, svc := range services {
		entry, ok := svcMap[name].(map[string]any)
		if !ok {
			entry = make(map[string]any)
			svcMap[name] = entry
		}
		entry["type"] = svc.Type
		entry["container"] = svc.Container
		entry["mandatory"] = svc.Mandatory
		entry["enabled"] = svc.Enabled
		entry["dir"] = svc.Dir
		entry["dir_internal"] = svc.DirInternal
		entry["work_dir_internal"] = svc.WorkDirInternal
		if len(svc.Compose) > 0 {
			compose := make([]any, len(svc.Compose))
			for i, c := range svc.Compose {
				compose[i] = c
			}
			entry["compose"] = compose
		}
		if len(svc.Configs) > 0 {
			configs := make([]any, len(svc.Configs))
			for i, c := range svc.Configs {
				configs[i] = c.File
			}
			entry["configs"] = configs
		}
	}
}

// isTruthy returns true for values that represent an enabled/truthy state.
func isTruthy(v any) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val != "" && val != "false" && val != "0"
	case int:
		return val != 0
	case float64:
		return val != 0
	default:
		return v != nil
	}
}

// LifecycleConfig holds the full lifecycle pipeline loaded from devbox/lifecycle.yml.
// It is loaded separately and not part of the 3-layer config merge.
type LifecycleConfig struct {
	Run  *LifecycleRunConfig  `yaml:"run"`
	Stop *LifecycleStopConfig `yaml:"stop"`
}

// LifecycleRunConfig holds the run lifecycle pipeline configuration.
// Update is a pointer so a missing block (nil) is distinguishable from a present
// block with defaults — writing the update: key is itself the opt-in.
type LifecycleRunConfig struct {
	Update       *LifecycleUpdate `yaml:"update"`
	ShowInfo     bool             `yaml:"show_info"`
	FinalMessage string           `yaml:"final_message"`
	Log          *bool            `yaml:"log"`
	Phases       []DeployPhase    `yaml:"phases"`
}

// LogEnabled reports whether file logging is enabled for the run pipeline.
// Defaults to false when unset; loader normalizes nil to false.
func (cfg *LifecycleRunConfig) LogEnabled() bool {
	return cfg != nil && cfg.Log != nil && *cfg.Log
}

// EffectiveMode returns the resolved update mode before any CLI flag is applied.
// Precedence: missing block → off; enabled:false → off; enabled:true+no mode → prompt;
// enabled:true+mode set → that value. CLI flags (--no-update, --update) override this.
func (cfg *LifecycleRunConfig) EffectiveMode() string {
	if cfg == nil {
		return "off"
	}
	if cfg.Update == nil {
		return "off"
	}
	if cfg.Update.Enabled == nil {
		// Loader is responsible for setting Enabled when the block is present;
		// this branch only fires if a caller bypasses LoadLifecycleConfig.
		return "off"
	}
	if !*cfg.Update.Enabled {
		return "off"
	}
	if cfg.Update.Mode == "" {
		return "prompt"
	}
	return cfg.Update.Mode
}

// LifecycleStopConfig holds the stop lifecycle pipeline configuration.
type LifecycleStopConfig struct {
	FinalMessage string        `yaml:"final_message"`
	Log          *bool         `yaml:"log"`
	Phases       []DeployPhase `yaml:"phases"`
}

// LogEnabled reports whether file logging is enabled for the stop pipeline.
// Defaults to false when unset; loader normalizes nil to false.
func (cfg *LifecycleStopConfig) LogEnabled() bool {
	return cfg != nil && cfg.Log != nil && *cfg.Log
}

// LifecycleUpdate configures the optional git update probe run at the start of devbox run.
// Enabled is a pointer so absent (nil) is distinguishable from explicit false at load time.
// Mode must be one of: prompt, auto, check, off.
type LifecycleUpdate struct {
	Enabled *bool  `yaml:"enabled"`
	Mode    string `yaml:"mode"`
}

// LoadLifecycleConfig loads the lifecycle pipeline from devbox/lifecycle.yml.
// The file is loaded standalone — it is not merged with the 3-layer config.
// Returns os.ErrNotExist when the file is absent (callers may treat it as optional).
// Lifecycle pipelines must not use deploy_services phases.
func LoadLifecycleConfig(path string) (*LifecycleConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg LifecycleConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Run != nil {
		if err := validatePhaseSteps(cfg.Run.Phases, false); err != nil {
			return nil, fmt.Errorf("lifecycle run: %w", err)
		}
		if cfg.Run.FinalMessage == "" {
			cfg.Run.FinalMessage = "Project is ready for work!"
		}
		if cfg.Run.Log == nil {
			f := false
			cfg.Run.Log = &f
		}
		if cfg.Run.Update != nil {
			if cfg.Run.Update.Enabled == nil {
				t := true
				cfg.Run.Update.Enabled = &t
			}
			if cfg.Run.Update.Mode != "" {
				if !ValidUpdateMode(cfg.Run.Update.Mode) {
					return nil, fmt.Errorf("lifecycle run: update.mode %q is invalid; must be one of: prompt, auto, check, off", cfg.Run.Update.Mode)
				}
			}
		}
	}
	if cfg.Stop != nil {
		if err := validatePhaseSteps(cfg.Stop.Phases, false); err != nil {
			return nil, fmt.Errorf("lifecycle stop: %w", err)
		}
		if cfg.Stop.FinalMessage == "" {
			cfg.Stop.FinalMessage = "Project is stopped. Have a nice day!"
		}
		if cfg.Stop.Log == nil {
			f := false
			cfg.Stop.Log = &f
		}
	}
	return &cfg, nil
}

// ValidUpdateMode reports whether s is one of the four allowed update mode values.
func ValidUpdateMode(s string) bool {
	switch s {
	case "prompt", "auto", "check", "off":
		return true
	}
	return false
}

// LoadDeployConfig loads the deploy pipeline from a deploy.yml file.
// The file is loaded standalone — it is not merged with the 3-layer config.
// Returns os.ErrNotExist when the file is absent (callers may treat it as optional).
//
// File logging defaults to enabled (Log=true) when unset. Override with
// `log: false` at the top of deploy.yml.
func LoadDeployConfig(deployPath string) (*DeployConfig, error) {
	return loadPipelineConfig(deployPath, true, true)
}

// LoadResetConfig loads the reset pipeline from a reset.yml file.
// The file is loaded standalone — it is not merged with the 3-layer config.
// Returns os.ErrNotExist when the file is absent (callers may treat it as optional).
// Reset pipelines must not contain deploy_services phases.
//
// File logging defaults to disabled (Log=false). Enable with `log: true` at the top.
func LoadResetConfig(resetPath string) (*DeployConfig, error) {
	return loadPipelineConfig(resetPath, false, false)
}

// loadPipelineConfig is the shared loader for deploy and reset pipelines.
// When allowDeployServices is false, deploy_services phases are rejected.
// defaultLog is applied when the YAML omits the top-level log: field.
func loadPipelineConfig(path string, allowDeployServices bool, defaultLog bool) (*DeployConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg DeployConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validatePhaseSteps(cfg.Phases, allowDeployServices); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Log == nil {
		v := defaultLog
		cfg.Log = &v
	}
	return &cfg, nil
}

// validatePhaseSteps validates a slice of DeployPhase values.
// When allowDeployServices is false, deploy_services phases are rejected.
func validatePhaseSteps(phases []DeployPhase, allowDeployServices bool) error {
	for pi := range phases {
		phase := &phases[pi]
		if phase.DeployServices {
			if !allowDeployServices {
				return fmt.Errorf("phase %q: deploy_services is not allowed in this pipeline type", phase.Name)
			}
			if len(phase.Steps) > 0 {
				return fmt.Errorf("phase %q: deploy_services phase must not contain steps", phase.Name)
			}
			if phase.When != nil {
				return fmt.Errorf("phase %q: deploy_services phase does not support when", phase.Name)
			}
			continue
		}
		if phase.When != nil {
			if err := phase.When.Validate(); err != nil {
				return fmt.Errorf("phase %q when: %w", phase.Name, err)
			}
		}
		for si := range phase.Steps {
			step := &phase.Steps[si]
			if err := validateStepShape(step, phase.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateStepShape validates a single DeployStep, dispatching to either the
// leaf-step rules (type/cmd required) or the parallel-group rules (recurse into
// sub-steps; leaf-only directives on the group are already rejected by
// DeployStep.UnmarshalYAML).
func validateStepShape(step *DeployStep, phaseName string) error {
	if step.Parallel != nil {
		// Group-level when is optional and validated like any other when.
		if step.When != nil {
			if err := step.When.Validate(); err != nil {
				return fmt.Errorf("step %q (phase %q) when: %w", step.Name, phaseName, err)
			}
		}
		for si := range step.Parallel.Steps {
			sub := &step.Parallel.Steps[si]
			// Nested parallel is rejected at plan time with a typed sentinel;
			// here we only validate shape, so recurse uniformly.
			if err := validateStepShape(sub, phaseName); err != nil {
				return err
			}
		}
		return nil
	}
	if step.Type == "" {
		return fmt.Errorf("step %q (phase %q): type is required", step.Name, phaseName)
	}
	if step.Cmd == "" {
		return fmt.Errorf("step %q (phase %q): cmd is required", step.Name, phaseName)
	}
	switch step.Type {
	case "shell", "devbox":
		if len(step.With) > 0 {
			return fmt.Errorf("step %q (phase %q): type %q does not accept with", step.Name, phaseName, step.Type)
		}
	case "command", "builtin":
	default:
		return fmt.Errorf("step %q (phase %q): unknown type %q", step.Name, phaseName, step.Type)
	}
	if step.Check != nil {
		if err := step.Check.Validate(); err != nil {
			return fmt.Errorf("step %q (phase %q) check: %w", step.Name, phaseName, err)
		}
	}
	if step.When != nil {
		if err := step.When.Validate(); err != nil {
			return fmt.Errorf("step %q (phase %q) when: %w", step.Name, phaseName, err)
		}
	}
	return nil
}

// LoadServiceDeployConfigs loads per-service deploy pipelines from devbox/deploy/<name>.yml.
// Only services present in the services map AND having a corresponding deploy file are returned.
// Missing deploy files are silently skipped (not every service needs a deploy pipeline).
func LoadServiceDeployConfigs(baseDir string, services map[string]ServiceConfig) (map[string]*DeployConfig, error) {
	deployDir := filepath.Join(baseDir, "devbox", "deploy")
	result := make(map[string]*DeployConfig)

	for name := range services {
		path := filepath.Join(deployDir, name+".yml")
		cfg, err := LoadDeployConfig(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("load deploy config for service %q: %w", name, err)
		}
		result[name] = cfg
	}
	return result, nil
}

// TopoSortServices returns service names in dependency order (dependencies first).
// Only services present in the names slice are included. Returns an error on
// cycles or references to unknown services.
func TopoSortServices(names []string, services map[string]ServiceConfig) ([]string, error) {
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	// States: 0 = unvisited, 1 = in-progress, 2 = done.
	state := make(map[string]int, len(names))
	var order []string

	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 2 {
			return nil
		}
		if state[name] == 1 {
			return fmt.Errorf("circular dependency detected involving service %q", name)
		}
		state[name] = 1

		svc, ok := services[name]
		if !ok {
			return fmt.Errorf("service %q not found", name)
		}
		for _, dep := range svc.DependsOn {
			if !nameSet[dep] {
				// Dependency exists in services but not in the set we're sorting —
				// it's either not enabled or has no deploy file. Still validate it exists.
				if _, ok := services[dep]; !ok {
					return fmt.Errorf("service %q depends on unknown service %q", name, dep)
				}
				continue
			}
			if err := visit(dep); err != nil {
				return err
			}
		}

		state[name] = 2
		order = append(order, name)
		return nil
	}

	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// LoadDevboxConfig reads and parses a single devbox.yml file at the given path.
// Prefer LoadConfig for full layered loading.
func LoadDevboxConfig(path string) (*DevboxConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg DevboxConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// LookupDotPath resolves a dot-separated path (e.g. "services.main.work_dir_internal")
// against cfg.Raw and returns the value. Returns (nil, nil) when cfg is nil or the
// path is missing. Returns an error when the resolved value is not a string — the
// only currently-supported leaf type for dot-path lookups in user-facing config.
func LookupDotPath(cfg *DevboxConfig, path string) (any, error) {
	if cfg == nil || path == "" {
		return nil, nil
	}
	v, found := ResolvePath(cfg.Raw, path)
	if !found {
		return nil, nil
	}
	if _, ok := v.(string); !ok {
		return nil, fmt.Errorf("dot-path %q: value is not a string", path)
	}
	return v, nil
}

// ResolvePath resolves a dot-separated path (e.g. "runtime.ports.app") in a
// nested map and returns the value and whether it was found.
func ResolvePath(m map[string]any, path string) (any, bool) {
	if path == "" || m == nil {
		return nil, false
	}
	head, tail, _ := strings.Cut(path, ".")
	v, ok := m[head]
	if !ok {
		return nil, false
	}
	if tail == "" {
		return v, true
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	return ResolvePath(sub, tail)
}

// loadRawYAML reads a YAML file into a raw map. Returns os.ErrNotExist when
// the file does not exist so callers can treat it as optional.
func loadRawYAML(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

// mergeDeduplicatedStrings returns a slice containing all elements of a followed
// by any elements of b that are not already present in a. Order is preserved.
func mergeDeduplicatedStrings(a, b []string) []string {
	if len(a) == 0 {
		return append([]string(nil), b...)
	}
	seen := make(map[string]bool, len(a))
	result := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// deepMerge merges src into dst in place.
// For keys where both values are maps, it recurses. Otherwise src wins.
func deepMerge(dst, src map[string]any) {
	for k, sv := range src {
		if sv == nil {
			continue
		}
		dv, exists := dst[k]
		if !exists {
			dst[k] = sv
			continue
		}
		dsm, dIsMap := dv.(map[string]any)
		ssm, sIsMap := sv.(map[string]any)
		if dIsMap && sIsMap {
			deepMerge(dsm, ssm)
		} else {
			dst[k] = sv
		}
	}
}
