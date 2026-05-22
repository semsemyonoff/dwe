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
// (devbox, docker, sh, git). Use DevboxBin/DockerBin/ShellBin/GitBin accessors to read.
type BinariesConfig struct {
	Devbox string `yaml:"devbox"`
	Docker string `yaml:"docker"`
	Shell  string `yaml:"shell"`
	Git    string `yaml:"git"`
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

// GitBin returns the configured git binary name (default: "git").
// Safe when cfg is nil.
func GitBin(cfg *DevboxConfig) string {
	if cfg == nil || cfg.Binaries.Git == "" {
		return "git"
	}
	return cfg.Binaries.Git
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
	if b.Git == "" {
		b.Git = "git"
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
	files := make([]string, 0, 1+len(c.Services))
	if c.Compose.Base != "" {
		files = append(files, c.Compose.Base)
	}

	// Group by type: tools, then infra, then apps; sorted by name within each group.
	// Services with an empty Type are emitted last in the same pass as apps so
	// tests that build ServiceConfig literals without setting Type still work.
	// Order is part of the public surface — see Task 6 in
	// docs/plans/2026-05-22-unified-services-schema.md.
	emitGroup := func(match func(ServiceType) bool) {
		for _, name := range slices.Sorted(maps.Keys(c.Services)) {
			svc := c.Services[name]
			if !match(svc.Type) {
				continue
			}
			if (all || svc.Enabled) && len(svc.Compose) > 0 {
				files = append(files, svc.Compose...)
			}
		}
	}
	emitGroup(func(t ServiceType) bool { return t == ServiceTypeTool })
	emitGroup(func(t ServiceType) bool { return t == ServiceTypeInfra })
	emitGroup(func(t ServiceType) bool { return t == ServiceTypeApp || t == "" })

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

// ServiceType is the type discriminator for entries in devbox/services.yml.
// Decoded natively by gopkg.in/yaml.v3 since it's a named string type.
type ServiceType string

const (
	ServiceTypeApp   ServiceType = "app"
	ServiceTypeTool  ServiceType = "tool"
	ServiceTypeInfra ServiceType = "infra"
)

// Sentinel errors for ServiceType validation and unified-services-schema enforcement.
var (
	ErrServiceTypeMissing      = errors.New("config: service type missing")
	ErrServiceTypeUnknown      = errors.New("config: unknown service type")
	ErrServiceFieldNotAllowed  = errors.New("config: field not allowed for service type")
	ErrServiceExtendsCrossType = errors.New("config: extends only permitted for type app")
	ErrServicePortsShape       = errors.New("config: ports must be a map of name to port number")
	ErrServiceHostsShape       = errors.New("config: hosts must be a map of name to hostname")
	ErrServicePortOutOfRange   = errors.New("config: port value out of range 1..65535")
)

// validServiceTypes is the closed set of allowed ServiceType values.
var validServiceTypes = map[ServiceType]bool{
	ServiceTypeApp:   true,
	ServiceTypeTool:  true,
	ServiceTypeInfra: true,
}

// IsValid reports whether t is one of the recognised service types.
func (t ServiceType) IsValid() bool { return validServiceTypes[t] }

// Validate returns nil for valid service types; ErrServiceTypeMissing for the
// empty string; ErrServiceTypeUnknown (wrapped with the offending value) for
// anything else.
func (t ServiceType) Validate() error {
	if t == "" {
		return ErrServiceTypeMissing
	}
	if !validServiceTypes[t] {
		return fmt.Errorf("%w: %q", ErrServiceTypeUnknown, string(t))
	}
	return nil
}

// IsApp reports whether t == ServiceTypeApp.
func (t ServiceType) IsApp() bool { return t == ServiceTypeApp }

// IsTool reports whether t == ServiceTypeTool.
func (t ServiceType) IsTool() bool { return t == ServiceTypeTool }

// IsInfra reports whether t == ServiceTypeInfra.
func (t ServiceType) IsInfra() bool { return t == ServiceTypeInfra }

// allowedFieldsFor returns the set of YAML field names permitted for entries
// of the given service type. Used by validators and loader strict-decode error
// messages as the single source of truth for per-type field allowlists.
// Returns a fresh map each call so callers cannot corrupt shared state.
func allowedFieldsFor(t ServiceType) map[string]bool {
	// Fields permitted for every service type.
	common := []string{
		"type", "container", "mandatory", "compose",
		"ports", "hosts", "status",
	}
	switch t {
	case ServiceTypeApp:
		out := make(map[string]bool, len(common)+10)
		for _, k := range common {
			out[k] = true
		}
		for _, k := range []string{
			"depends_on",
			"dir", "dir_internal", "work_dir_internal",
			"configs", "dirs", "extends", "cli", "render",
		} {
			out[k] = true
		}
		return out
	case ServiceTypeInfra:
		out := make(map[string]bool, len(common)+1)
		for _, k := range common {
			out[k] = true
		}
		out["depends_on"] = true
		return out
	case ServiceTypeTool:
		out := make(map[string]bool, len(common))
		for _, k := range common {
			out[k] = true
		}
		return out
	default:
		return map[string]bool{}
	}
}

// ServiceConfig describes a single service entry in devbox/services.yml.
// The Type field discriminates the per-entry shape (app / tool / infra).
// The Enabled flag is resolved from the 3-layer config merge (mandatory
// services are always enabled).
type ServiceConfig struct {
	Type            ServiceType          `yaml:"type"`
	Container       string               `yaml:"container"`
	Mandatory       bool                 `yaml:"mandatory"`
	Enabled         bool                 `yaml:"-"` // computed: mandatory || services.<name>.enabled
	Ports           map[string]int       `yaml:"ports,omitempty"`
	Hosts           map[string]string    `yaml:"hosts,omitempty"`
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

// IsApp reports whether this service has type "app".
func (s ServiceConfig) IsApp() bool { return s.Type.IsApp() }

// IsTool reports whether this service has type "tool".
func (s ServiceConfig) IsTool() bool { return s.Type.IsTool() }

// IsInfra reports whether this service has type "infra".
func (s ServiceConfig) IsInfra() bool { return s.Type.IsInfra() }

// Port returns the host port for the named entry in s.Ports, or 0 if absent.
func (s ServiceConfig) Port(name string) int { return s.Ports[name] }

// Host returns the hostname for the named entry in s.Hosts, or "" if absent.
func (s ServiceConfig) Host(name string) string { return s.Hosts[name] }

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

// RuntimeConfig describes runtime settings that are not service-specific.
// Per-service host/port have moved to ServiceConfig.Hosts/Ports.
type RuntimeConfig struct {
	UseHTTPS bool      `yaml:"use_https"`
	SPX      SPXConfig `yaml:"spx"`
}

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

// validateConfigKeys checks per-service identifier-safety of Ports/Hosts keys
// so they can be used with Go template dot syntax (^[A-Za-z_][A-Za-z0-9_]*$).
// Top-level service names are checked as well.
func validateConfigKeys(cfg *DevboxConfig) error {
	for _, svcName := range slices.Sorted(maps.Keys(cfg.Services)) {
		svc := cfg.Services[svcName]
		for _, k := range slices.Sorted(maps.Keys(svc.Ports)) {
			if !validIdentifierKey(k) {
				return fmt.Errorf("service %q: invalid ports key %q: must match ^[A-Za-z_][A-Za-z0-9_]*$ (identifier-safe for template dot syntax)", svcName, k)
			}
		}
		for _, k := range slices.Sorted(maps.Keys(svc.Hosts)) {
			if !validIdentifierKey(k) {
				return fmt.Errorf("service %q: invalid hosts key %q: must match ^[A-Za-z_][A-Za-z0-9_]*$ (identifier-safe for template dot syntax)", svcName, k)
			}
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
// Sequencing:
//  1. Load devbox/services.yml (canonical service declarations).
//  2. Validate each overlay layer against the declared service set
//     (only services.<name>.enabled permitted in overlays).
//  3. Merge the raw YAML layers.
//  4. Resolve per-service Enabled from the merged overlay.
//  5. Inject services into Raw for dot-path resolution.
func LoadConfig(devboxPath string) (*DevboxConfig, error) {
	baseDir := filepath.Dir(devboxPath)

	// Read each layer separately so the cross-layer overlay validator can
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

	// Step 1: load devbox/services.yml — the canonical service declarations.
	servicesPath := filepath.Join(baseDir, "devbox", "services.yml")
	services, err := LoadServicesConfig(servicesPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if services == nil {
		services = map[string]ServiceConfig{}
	}

	// Step 2: validate overlay shape against the declared services set
	// before merging. This is the ordering that catches "silently wrong"
	// overlays that would otherwise be tolerated by the deep merge.
	for _, layer := range layers {
		if err := validateServicesOverlay(layer.path, layer.data, services); err != nil {
			return nil, err
		}
	}

	// Step 3: merge the layers.
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

	cfg.Raw = merged
	// Store config path so deploy resolution can find service deploy files.
	cfg.Raw["__configPath"] = devboxPath

	// Normalize Raw["binaries"] so dot-path lookups (e.g. ${binaries.docker} in
	// export rules) see the same effective values as cfg.Binaries.* Go callers.
	// Any binaries: block from defaults.yml or local.yml is silently discarded here.
	cfg.Raw["binaries"] = map[string]any{
		"devbox": cfg.Binaries.Devbox,
		"docker": cfg.Binaries.Docker,
		"shell":  cfg.Binaries.Shell,
		"git":    cfg.Binaries.Git,
	}

	// Step 4: resolve per-service Enabled from the merged overlay.
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

	// Step 5: inject service definitions into the raw map so dot-paths like
	// `services.main.ports.http` resolve via ResolvePath for export rules,
	// info.yml, docker.yml templates, and user command default_from.
	injectServicesIntoRaw(merged, services)

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

// validateServicesOverlay rejects any field other than `enabled:` under a
// layer's services.<name> mapping and any services.<name> entry naming a
// service not declared in devbox/services.yml. layerPath is included in the
// error message so the user knows which file to edit.
func validateServicesOverlay(layerPath string, raw map[string]any, declared map[string]ServiceConfig) error {
	svcRaw, ok := raw["services"]
	if !ok || svcRaw == nil {
		return nil
	}
	svcMap, ok := svcRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: services: must be a mapping", layerPath)
	}
	for _, name := range slices.Sorted(maps.Keys(svcMap)) {
		if _, declaredOK := declared[name]; !declaredOK {
			return fmt.Errorf("%s: services.%s: unknown service (declared services live in devbox/services.yml)", layerPath, name)
		}
		entryRaw := svcMap[name]
		if entryRaw == nil {
			continue
		}
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: services.%s: must be a mapping", layerPath, name)
		}
		for _, key := range slices.Sorted(maps.Keys(entry)) {
			if key != "enabled" {
				return fmt.Errorf("%s: services.%s.%s: service definitions belong in devbox/services.yml; overlays may only set enabled", layerPath, name, key)
			}
		}
	}
	return nil
}

// servicesFile is the top-level structure of devbox/services.yml.
type servicesFile struct {
	Services map[string]ServiceConfig `yaml:"services"`
}

// LoadServicesConfig loads service definitions from devbox/services.yml.
//
// The loader is strict: every entry must declare `type:` (app | tool | infra),
// each entry is checked against the per-type field allowlist
// (allowedFieldsFor), and `extends:` is only permitted between two app
// services. `ports:` and `hosts:` must be maps of named entries (no scalar
// shorthand); port values are validated to 1..65535. Per-file violations are
// aggregated with errors.Join so a single parse pass surfaces every problem.
// Extends inheritance uses defensive copies (slices.Clone / maps.Clone) so
// child mutations cannot corrupt the parent.
func LoadServicesConfig(path string) (map[string]ServiceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// First pass: parse to a raw nested map so we can inspect the actual YAML
	// keys per entry and reject disallowed-shape values before strict decode
	// turns them into opaque type errors.
	var rawFile struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &rawFile); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var diags []error
	for _, name := range slices.Sorted(maps.Keys(rawFile.Services)) {
		entry := rawFile.Services[name]
		// Type required.
		typeRaw, hasType := entry["type"]
		if !hasType {
			diags = append(diags, fmt.Errorf("%w: service %q", ErrServiceTypeMissing, name))
			continue
		}
		typeStr, _ := typeRaw.(string)
		svcType := ServiceType(typeStr)
		if err := svcType.Validate(); err != nil {
			diags = append(diags, fmt.Errorf("service %q: %w", name, err))
			continue
		}
		// Per-type field allowlist.
		allowed := allowedFieldsFor(svcType)
		for _, key := range slices.Sorted(maps.Keys(entry)) {
			if !allowed[key] {
				diags = append(diags, fmt.Errorf("%w: service %q (type %s): field %q", ErrServiceFieldNotAllowed, name, svcType, key))
			}
		}
		// Ports / hosts must be maps when present.
		if v, ok := entry["ports"]; ok && v != nil {
			if _, isMap := v.(map[string]any); !isMap {
				diags = append(diags, fmt.Errorf("%w: service %q", ErrServicePortsShape, name))
			} else {
				for _, portName := range slices.Sorted(maps.Keys(v.(map[string]any))) {
					portVal := v.(map[string]any)[portName]
					n, ok := portVal.(int)
					if !ok {
						diags = append(diags, fmt.Errorf("%w: service %q port %q is not an integer", ErrServicePortsShape, name, portName))
						continue
					}
					if n < 1 || n > 65535 {
						diags = append(diags, fmt.Errorf("%w: service %q port %q = %d", ErrServicePortOutOfRange, name, portName, n))
					}
				}
			}
		}
		if v, ok := entry["hosts"]; ok && v != nil {
			if _, isMap := v.(map[string]any); !isMap {
				diags = append(diags, fmt.Errorf("%w: service %q", ErrServiceHostsShape, name))
			}
		}
		// Extends only permitted between app services. Parent type checked
		// after strict decode (parent existence is checked by topoSort).
		if extRaw, ok := entry["extends"]; ok && extRaw != nil && !svcType.IsApp() {
			diags = append(diags, fmt.Errorf("%w: service %q (type %s)", ErrServiceExtendsCrossType, name, svcType))
		}
	}

	// If any shape/allowlist diagnostics fired, bail before strict decode —
	// otherwise yaml would surface a generic type error on top of ours.
	if len(diags) > 0 {
		return nil, fmt.Errorf("loading %s: %w", path, errors.Join(diags...))
	}

	// Second pass: strict typed decode. Rejects unknown struct fields (typos
	// like `containerr:`) and mistyped scalars after shape pre-validation.
	var f servicesFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// Resolve extends in topological order so multi-level chains (C→B→A) are
	// processed parent-first regardless of map iteration order.
	order, err := topoSortServices(f.Services)
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", path, err)
	}

	// Cross-type extends: child.Type must equal parent.Type. Validated after
	// topoSort guarantees parent exists. (The non-app guard already fired in
	// the first pass; this catches app extends app of a different… well, all
	// extends are app→app today, but check defensively for symmetry and to
	// surface a clear sentinel if the policy ever loosens.)
	var crossTypeDiags []error
	for name, svc := range f.Services {
		if svc.Extends == "" {
			continue
		}
		parent := f.Services[svc.Extends]
		if parent.Type != svc.Type {
			crossTypeDiags = append(crossTypeDiags, fmt.Errorf("%w: service %q (type %s) extends %q (type %s)", ErrServiceExtendsCrossType, name, svc.Type, svc.Extends, parent.Type))
		}
	}
	if len(crossTypeDiags) > 0 {
		return nil, fmt.Errorf("loading %s: %w", path, errors.Join(crossTypeDiags...))
	}

	for _, name := range order {
		svc := f.Services[name]
		if svc.Extends == "" {
			continue
		}
		// Parent is guaranteed to be fully resolved already.
		parent := f.Services[svc.Extends]
		if svc.Dir == "" {
			svc.Dir = parent.Dir
		}
		if svc.DirInternal == "" {
			svc.DirInternal = parent.DirInternal
		}
		if svc.WorkDirInternal == "" {
			svc.WorkDirInternal = parent.WorkDirInternal
		}
		if len(svc.Configs) == 0 && len(parent.Configs) > 0 {
			svc.Configs = slices.Clone(parent.Configs)
		}
		if len(svc.Compose) == 0 && len(parent.Compose) > 0 {
			svc.Compose = slices.Clone(parent.Compose)
		}
		if len(svc.Ports) == 0 && len(parent.Ports) > 0 {
			svc.Ports = maps.Clone(parent.Ports)
		}
		if len(svc.Hosts) == 0 && len(parent.Hosts) > 0 {
			svc.Hosts = maps.Clone(parent.Hosts)
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
		// AI block inheritance.
		if svc.Render.AI.Enabled == nil && parent.Render.AI.Enabled != nil {
			v := *parent.Render.AI.Enabled
			svc.Render.AI.Enabled = &v
		}
		if svc.Render.AI.Template == "" {
			svc.Render.AI.Template = parent.Render.AI.Template
		}
		// Git block inheritance.
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

// injectServicesIntoRaw merges service definitions into raw["services"] so
// that export rules and dot-path templates resolve against the merged map.
// Intermediate maps are lazy-initialized; empty Ports/Hosts/Configs/Compose
// are omitted (no empty-map placeholder) so `(index .Services "X").Ports`
// existence checks behave consistently across services.
func injectServicesIntoRaw(raw map[string]any, services map[string]ServiceConfig) {
	if raw["services"] == nil {
		raw["services"] = map[string]any{}
	}
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
		entry["type"] = string(svc.Type)
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
		if len(svc.Ports) > 0 {
			ports := make(map[string]any, len(svc.Ports))
			for k, v := range svc.Ports {
				ports[k] = v
			}
			entry["ports"] = ports
		}
		if len(svc.Hosts) > 0 {
			hosts := make(map[string]any, len(svc.Hosts))
			for k, v := range svc.Hosts {
				hosts[k] = v
			}
			entry["hosts"] = hosts
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
