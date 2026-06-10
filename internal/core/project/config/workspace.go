// Package config provides loading and validation of dwe configuration files.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"

	"gopkg.in/yaml.v3"
)

// legacyComposeOverlaysMsg is the base migration message for the removed
// compose.overlays key. No trailing period — callers append context.
const legacyComposeOverlaysMsg = "compose.overlays is no longer supported; move overlay files to individual services (type: tool): services.<name>.compose instead. See docs/reference/config/workspace.md for migration details"

// binOverride returns the user-configured override for the named binary, or
// def when cfg is nil, cfg.userConfig is nil, or no override is present.
func binOverride(cfg *DweConfig, key, def string) string {
	if cfg != nil && cfg.userConfig != nil {
		if path, ok := cfg.userConfig.BinaryOverride(key); ok {
			return path
		}
	}
	return def
}

// DweBin returns the configured dwe binary name (default: "dwe").
// Checks user-config overrides first, then falls back to the built-in default.
// Safe when cfg is nil.
func DweBin(cfg *DweConfig) string { return binOverride(cfg, "dwe", "dwe") }

// DockerBin returns the configured docker binary name (default: "docker").
// Checks user-config overrides first, then falls back to the built-in default.
// Safe when cfg is nil.
func DockerBin(cfg *DweConfig) string { return binOverride(cfg, "docker", "docker") }

// ShellBin returns the configured shell binary name (default: "sh").
// Checks user-config overrides first, then falls back to the built-in default.
// Safe when cfg is nil.
func ShellBin(cfg *DweConfig) string { return binOverride(cfg, "shell", "sh") }

// GitBin returns the configured git binary name (default: "git").
// Checks user-config overrides first, then falls back to the built-in default.
// Safe when cfg is nil.
func GitBin(cfg *DweConfig) string { return binOverride(cfg, "git", "git") }

// MmdcBin returns the configured mmdc binary name (default: "mmdc").
// Checks user-config overrides first, then falls back to the built-in default.
// Safe when cfg is nil.
func MmdcBin(cfg *DweConfig) string { return binOverride(cfg, "mmdc", "mmdc") }

// DweConfig is the merged top-level dwe configuration.
// It is produced by layering workspace.yml → workspace/defaults.yml → workspace/local.yml.
type DweConfig struct {
	Project ProjectConfig        `yaml:"project"`
	Runtime RuntimeConfig        `yaml:"runtime"`
	State   string               `yaml:"state"`
	Exports ExportsConfig        `yaml:"exports"`
	Compose ComposeConfig        `yaml:"compose"`
	Deploy  *ProjectDeployConfig `yaml:"-"`
	UI      UIConfig             `yaml:"ui"`
	Docs    DocsConfig           `yaml:"docs"`

	// Services holds the fully resolved service definitions loaded from
	// workspace/services/<name>/service.yml with Enabled populated from the 3-layer config merge.
	// Not unmarshalled from the merge — built by LoadConfig.
	Services map[string]ServiceConfig `yaml:"-"`

	// Raw holds the merged config as a plain map, used for dot-path resolution
	// in export rules. Populated only by LoadConfig; not serialized.
	Raw map[string]any `yaml:"-"`

	// userConfig holds user-level preferences loaded from ~/.config/dwe/config
	// and .dwe/config. Used by binary accessors to resolve engine binary overrides.
	// Nil if load failed (graceful degradation).
	userConfig *userpkg.Config `yaml:"-"`
}

// ProjectDeployConfig holds the project-wide deploy pipeline loaded from workspace/deploy.yml.
// It is loaded separately and not part of the 3-layer config merge.
//
// Log enables/disables file logging at .dwe/logs/<pipeline>.log for the pipeline run.
// nil means "use loader default": LoadProjectDeployConfig defaults to true.
// Set explicitly via top-level `log: true|false`.
type ProjectDeployConfig struct {
	Log    *bool         `yaml:"log"`
	Phases []DeployPhase `yaml:"phases"`
}

// LogEnabled reports whether file logging is enabled. It assumes the loader
// has normalized the Log pointer; nil is treated as false defensively.
func (c *ProjectDeployConfig) LogEnabled() bool {
	return c != nil && c.Log != nil && *c.Log
}

// ServiceDeployConfig holds a per-service deploy pipeline loaded from workspace/services/<name>/deploy.yml.
// It is loaded separately and not part of the 3-layer config merge.
//
// Log enables/disables file logging at .dwe/logs/<pipeline>.log for the pipeline run.
// nil means "use loader default": LoadServiceDeployConfig defaults to true.
// Set explicitly via top-level `log: true|false`.
//
// After specifies deploy-time ordering: the list of service names this service
// deploys after. Only valid in per-service deploy.yml, not in project-wide deploy.yml.
type ServiceDeployConfig struct {
	After  []string      `yaml:"after,omitempty"` // deploy-time ordering: service names this service deploys after
	Log    *bool         `yaml:"log"`
	Phases []DeployPhase `yaml:"phases"`
}

// LogEnabled reports whether file logging is enabled. It assumes the loader
// has normalized the Log pointer; nil is treated as false defensively.
func (c *ServiceDeployConfig) LogEnabled() bool {
	return c != nil && c.Log != nil && *c.Log
}

// DeployConfig is a union of project and service deploy configs.
// Used by validators that parse either type without prior context.
// New code should use ProjectDeployConfig or ServiceDeployConfig.
type DeployConfig struct {
	After  []string      `yaml:"after,omitempty"`
	Log    *bool         `yaml:"log"`
	Phases []DeployPhase `yaml:"phases"`
}

// LogEnabled reports whether file logging is enabled. It assumes the loader
// has normalized the Log pointer; nil is treated as false defensively.
func (c *DeployConfig) LogEnabled() bool {
	return c != nil && c.Log != nil && *c.Log
}

// ServiceDeployConfigsToGeneric converts a map of ServiceDeployConfig to generic DeployConfig.
// Used when internal functions expect the generic type.
func ServiceDeployConfigsToGeneric(src map[string]*ServiceDeployConfig) map[string]*DeployConfig {
	if len(src) == 0 {
		return make(map[string]*DeployConfig)
	}
	result := make(map[string]*DeployConfig, len(src))
	for name, sdc := range src {
		result[name] = &DeployConfig{
			After:  sdc.After,
			Log:    sdc.Log,
			Phases: sdc.Phases,
		}
	}
	return result
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
//   - Type            — executor: shell, dwe, command, or builtin
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
	// Untracked excludes this step from the [N/M] step counter and suppresses
	// its lifecycle output (start/done lines). Mirrors DeployPhase.Untracked
	// but at step granularity so a single stack-up / wait-healthy step can be
	// hidden without moving it into a dedicated untracked phase. Failures are
	// still surfaced.
	Untracked bool           `yaml:"untracked,omitempty"`
	Parallel  *ParallelGroup `yaml:"parallel,omitempty"`
	// SubStepOverrides applies pipeline-side orchestration directives (currently
	// files_gate) to named sub-steps of the workflow referenced by Cmd. Keys are
	// the sub-step Name (or, when absent, the sub-step's referenced Command).
	// Only valid when Type == "command" and the target command is a workflow.
	// Plan-time validation lives in internal/core/execution/pipeline.ResolvePhaseSteps.
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
	"untracked":          true,
	"parallel":           true,
	"sub_step_overrides": true,
}

// deployStepLeafOnlyFields are keys that may appear only on a leaf step
// (Parallel == nil). Their presence alongside parallel: is a hard error.
var deployStepLeafOnlyFields = []string{
	"type", "cmd", "with", "check", "files_gate", "continue_on_error", "sub_step_overrides",
}

// checkKnownFields enforces that value is a YAML mapping whose keys all appear
// in allowed. It compensates for yaml.v3 bypassing KnownFields(true) inside
// custom UnmarshalYAML implementations. mappingDesc names the mapping in the
// wrong-kind error ("<mappingDesc> must be a mapping, got kind N"); typeName is
// the Go type named in the unknown-field error ("field X not found in type
// <typeName>"). The returned map records which allowed keys were seen so callers
// can run follow-up cross-field checks.
func checkKnownFields(value *yaml.Node, mappingDesc, typeName string, allowed map[string]bool) (map[string]bool, error) {
	if value.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s must be a mapping, got kind %d", mappingDesc, value.Kind)
	}
	seen := make(map[string]bool, len(value.Content)/2)
	for i := 0; i < len(value.Content)-1; i += 2 {
		key := value.Content[i].Value
		if !allowed[key] {
			return nil, fmt.Errorf("field %s not found in type %s", key, typeName)
		}
		seen[key] = true
	}
	return seen, nil
}

// UnmarshalYAML enforces:
//   - mapping shape only
//   - explicit known-field allow-list (compensates for yaml.v3 bypass of KnownFields(true)
//     inside custom UnmarshalYAML implementations)
//   - mutual exclusion between parallel: and leaf-only fields (type/cmd/with/check/
//     files_gate/continue_on_error)
//
// Length of parallel.steps is NOT enforced here; that check lives in
// internal/core/execution/pipeline.ResolvePhaseSteps so it can return a typed sentinel
// (ErrEmptyParallelSteps) without an import cycle.
func (s *DeployStep) UnmarshalYAML(value *yaml.Node) error {
	seen, err := checkKnownFields(value, "deploy step", "config.DeployStep", deployStepKnownFields)
	if err != nil {
		return err
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
	if _, err := checkKnownFields(value, "parallel", "config.ParallelGroup", parallelGroupKnownFields); err != nil {
		return err
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
//
// Extra carries project-wide per-developer overlay files appended LAST to the
// `-f` chain in [DweConfig.ComposeFiles]/[DweConfig.ComposeFilesAll]. It is
// populated only by explicit post-decode injection from `workspace/local.yml`
// (`compose.extra`) — the `yaml:"-"` tag makes it unreachable from any other
// YAML source.
type ComposeConfig struct {
	Base  string   `yaml:"base"`
	Extra []string `yaml:"-"`
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

// AppServices returns the subset of c.Services whose Type is "app".
// The returned map is a fresh allocation so callers may mutate it freely.
func (c *DweConfig) AppServices() map[string]ServiceConfig {
	return filterServicesByType(c.Services, ServiceTypeApp)
}

// ToolServices returns the subset of c.Services whose Type is "tool".
// The name deliberately does not shadow the deleted .Tools field so the
// acceptance grep can still flag stale `.Tools` references.
func (c *DweConfig) ToolServices() map[string]ServiceConfig {
	return filterServicesByType(c.Services, ServiceTypeTool)
}

// InfraServices returns the subset of c.Services whose Type is "infra".
func (c *DweConfig) InfraServices() map[string]ServiceConfig {
	return filterServicesByType(c.Services, ServiceTypeInfra)
}

func filterServicesByType(svcs map[string]ServiceConfig, t ServiceType) map[string]ServiceConfig {
	out := make(map[string]ServiceConfig, len(svcs))
	for name, svc := range svcs {
		if svc.Type == t {
			out[name] = svc
		}
	}
	return out
}

// BridgeOverlayRelPath is the project-root-relative path of the generated
// host-bridge compose overlay (design D8). Slash-separated and stored as
// written in the -f chain like every other compose path; the file itself is
// dwe-owned machine state written by internal/core/bridge.
const BridgeOverlayRelPath = ".dwe/compose.bridge.yml"

// ComposeFiles returns the ordered list of compose files for the project:
// base file first, then enabled tool overlays (sorted by key), then enabled
// service overlays (sorted by service name). Per-service local overlays from
// workspace/local.yml (services.<name>.compose.extra) are emitted immediately
// after each service's own compose files, inside the same enabled-gate.
// The generated host-bridge overlay (BridgeOverlayRelPath) follows the
// service groups when it exists on disk. Project-wide local overlays from
// workspace/local.yml (compose.extra) are appended last so last-wins compose
// semantics let a single local file patch anything. This is the canonical
// file list used by all compose-aware CLI operations.
func (c *DweConfig) ComposeFiles() []string {
	return c.composeFiles(false)
}

// ComposeFilesAll returns the ordered list of all configured compose files,
// regardless of whether overlays are enabled: base file first, then all tool
// overlays (sorted by key), then all service overlays (sorted by service name).
// Per-service local overlays from workspace/local.yml are included for every
// service regardless of its enabled state; project-wide local overlays are
// always appended last. Used by --all flags to override the active set.
func (c *DweConfig) ComposeFilesAll() []string {
	return c.composeFiles(true)
}

// composeFiles assembles the ordered -f chain for docker compose. Per-service
// local overlays (svc.LocalComposeExtra) reuse the same `all || svc.Enabled`
// gate as svc.Compose. Project-wide local overlays (c.Compose.Extra) are
// appended unconditionally at the very end.
func (c *DweConfig) composeFiles(all bool) []string {
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
			if all || svc.Enabled {
				if len(svc.Compose) > 0 {
					files = append(files, svc.Compose...)
				}
				if len(svc.LocalComposeExtra) > 0 {
					files = append(files, svc.LocalComposeExtra...)
				}
			}
		}
	}
	emitGroup(func(t ServiceType) bool { return t == ServiceTypeTool })
	emitGroup(func(t ServiceType) bool { return t == ServiceTypeInfra })
	emitGroup(func(t ServiceType) bool { return t == ServiceTypeApp || t == "" })

	// Generated host-bridge overlay: after the service overlays, BEFORE the
	// project-wide local.yml overlays — local.yml stays the user
	// customization channel and keeps the last word over anything the bridge
	// overlay sets (design D8 chain position).
	if c.bridgeOverlayExists() {
		files = append(files, BridgeOverlayRelPath)
	}

	if len(c.Compose.Extra) > 0 {
		files = append(files, c.Compose.Extra...)
	}

	return files
}

// bridgeOverlayExists reports whether the generated host-bridge overlay is
// present for this config's project root. The check runs at call time — the
// bridge prepare hook regenerates or deletes the file after LoadConfig and
// before compose args are built, so a load-time snapshot would go stale
// within the very command that mutates it. Configs built without LoadConfig
// (no __configPath in Raw) never include the overlay.
func (c *DweConfig) bridgeOverlayExists() bool {
	cfgPath, ok := c.Raw["__configPath"].(string)
	if !ok || cfgPath == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(cfgPath), filepath.FromSlash(BridgeOverlayRelPath)))
	return err == nil
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
// is the primary AI feature in dwe. Future AI subsystems should be nested
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

// RenderConfigSection holds config-file rendering settings for a service. An
// optional Template pins a specific template pack; otherwise the pack is
// resolved by convention (service name → extends chain → default) plus an
// optional `.local` sibling override.
type RenderConfigSection struct {
	Template string `yaml:"template"`
}

// ServiceRenderConfig holds all rendering-related configuration for a service.
type ServiceRenderConfig struct {
	IDE    ServiceIDEConfig      `yaml:"ide"`
	AI     ServiceAIConfig       `yaml:"ai"`
	Git    ServiceGitHooksConfig `yaml:"git"`
	Config *RenderConfigSection  `yaml:"config"`
}

// DefaultBridgeShimPath is the container path the host-bridge shim binary is
// mounted at unless bridge.shim_path overrides it (base-image collision).
const DefaultBridgeShimPath = "/usr/local/bin/dwe"

// Bridge on_unreachable policy values: fail makes the shim exit 1 when the
// host daemon is unreachable (a hook must block the commit), warn makes it
// print a warning and exit 0.
const (
	BridgeOnUnreachableFail = "fail"
	BridgeOnUnreachableWarn = "warn"
)

// ServiceBridgeConfig holds host-bridge settings for a service: whether the
// shim binary and bridge mounts are injected into the container (enabled —
// tristate, defaults on for type app), where the shim is mounted (shim_path),
// and the shim's policy when the host daemon is unreachable (on_unreachable:
// fail | warn).
type ServiceBridgeConfig struct {
	Enabled       *bool  `yaml:"enabled"`
	ShimPath      string `yaml:"shim_path"`
	OnUnreachable string `yaml:"on_unreachable"`
}

// StatusColumn declares one custom column rendered in the status table for a
// service or tool. Value is a hermetic Go template evaluated via tpl.Render.
type StatusColumn struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// ServiceType is the type discriminator for entries in workspace/services/<name>/service.yml.
// Decoded natively by gopkg.in/yaml.v3 since it's a named string type.
type ServiceType string

// Service type discriminator values.
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
	ErrDependsOnTool           = errors.New("config: depends_on target must not be a tool service")
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

// ToggleRequires declares what must happen when a service is enabled or disabled.
type ToggleRequires string

// ToggleRequires values.
const (
	RequiresUnspecified     ToggleRequires = "" // zero value when field omitted in YAML
	RequiresNone            ToggleRequires = "none"
	RequiresRestart         ToggleRequires = "restart"
	RequiresDeploy          ToggleRequires = "deploy"
	RequiresDeployOrRestart ToggleRequires = "deploy-or-restart" // resolves to deploy if never deployed, else restart
)

// IsKnown reports whether r is a recognized ToggleRequires value (including the zero value).
func (r ToggleRequires) IsKnown() bool {
	switch r {
	case RequiresUnspecified, RequiresNone, RequiresRestart, RequiresDeploy, RequiresDeployOrRestart:
		return true
	}
	return false
}

// OrDefault returns RequiresRestart when r is RequiresUnspecified; otherwise r.
func (r ToggleRequires) OrDefault() ToggleRequires {
	if r == RequiresUnspecified {
		return RequiresRestart
	}
	return r
}

// Resolve collapses a possibly-virtual ToggleRequires to a concrete one
// understood by the apply executor and the pending-journal writer:
//
//   - RequiresDeployOrRestart → RequiresDeploy when the service has never
//     been successfully deployed (deployed=false), else RequiresRestart.
//   - Any other value is returned unchanged.
//
// Callers MUST funnel both the apply plan and the pending op through this
// method so the two stay in sync.
func (r ToggleRequires) Resolve(deployed bool) ToggleRequires {
	if r == RequiresDeployOrRestart {
		if deployed {
			return RequiresRestart
		}
		return RequiresDeploy
	}
	return r
}

// ServiceToggleHooks holds hooks that fire when a service is enabled or disabled.
type ServiceToggleHooks struct {
	Requires ToggleRequires `yaml:"requires,omitempty"`
	Before   []string       `yaml:"before,omitempty"`
	After    []string       `yaml:"after,omitempty"`
}

// ServiceNotes holds human-readable notes shown during enable/disable.
type ServiceNotes struct {
	Enable  string `yaml:"enable,omitempty"`
	Disable string `yaml:"disable,omitempty"`
}

// ServiceInfoBlock holds display metadata for a service's dashboard entry.
//
// Scheme overrides the global runtime.use_https for this service's URLs.
// Precedence for the effective scheme of a service URL: per-port .Scheme
// (set via the rich `{port,scheme}` form on a ports entry) → ServiceInfoBlock.Scheme
// → Runtime.UseHTTPS. Allowed values: "", "http", "https".
type ServiceInfoBlock struct {
	Title       string            `yaml:"title,omitempty"`
	PrimaryHost string            `yaml:"primary_host,omitempty"`
	PrimaryPort string            `yaml:"primary_port,omitempty"`
	Scheme      string            `yaml:"scheme,omitempty"`
	Paths       []ServiceInfoPath `yaml:"paths,omitempty"`
}

// ServicePortSpec is one entry of ServiceConfig.Ports. The bare-int shorthand
// (`http: 5173`) decodes to ServicePortSpec{Port: 5173, Scheme: ""}; the rich
// object form (`http: {port: 5173, scheme: https}`) carries an optional
// per-port scheme override. Scheme is one of "", "http", "https".
type ServicePortSpec struct {
	Port   int    `yaml:"port" json:"port"`
	Scheme string `yaml:"scheme,omitempty" json:"scheme,omitempty"`
}

// UnmarshalYAML accepts either a bare integer (scheme inherited) or a mapping
// {port: int, scheme: string}. Other shapes return an error so the loader's
// strict decode surfaces a precise diagnostic.
//
// The mapping branch enforces strictness directly on the yaml.Node — it walks
// Content pairs and rejects any key outside {port, scheme}, plus rejects an
// empty/null `port:` field. yaml.Node.Decode does NOT inherit a parent
// decoder's KnownFields(true) setting, so this check must live here (not just
// in a raw-map pre-validator) for strictness to hold regardless of caller.
func (p *ServicePortSpec) UnmarshalYAML(value *yaml.Node) error {
	// Scalar — bare int shorthand.
	if value.Kind == yaml.ScalarNode {
		// Reject null explicitly: yaml.Decode would silently coerce !!null
		// to the zero value, masking what is almost certainly a typo.
		if value.Tag == "!!null" {
			return fmt.Errorf("port: must be an integer or a mapping {port, scheme}, got null")
		}
		var n int
		if err := value.Decode(&n); err != nil {
			return fmt.Errorf("port: must be an integer or a mapping {port, scheme}: %w", err)
		}
		p.Port = n
		p.Scheme = ""
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("port: must be an integer or a mapping {port, scheme}")
	}
	// Mapping form — walk pairs to enforce the allowed-fields contract before
	// decoding into the typed shadow struct.
	var rawPort, rawScheme *yaml.Node
	for i := 0; i+1 < len(value.Content); i += 2 {
		keyNode := value.Content[i]
		valNode := value.Content[i+1]
		switch keyNode.Value {
		case "port":
			rawPort = valNode
		case "scheme":
			rawScheme = valNode
		default:
			return fmt.Errorf("port: unknown field %q (allowed: port, scheme)", keyNode.Value)
		}
	}
	if rawPort == nil {
		return fmt.Errorf("port: missing required field \"port\" in mapping form")
	}
	if rawPort.Tag == "!!null" {
		return fmt.Errorf("port: \"port\" cannot be null")
	}
	var portN int
	if err := rawPort.Decode(&portN); err != nil {
		return fmt.Errorf("port: %w", err)
	}
	p.Port = portN
	if rawScheme != nil && rawScheme.Tag != "!!null" {
		var s string
		if err := rawScheme.Decode(&s); err != nil {
			return fmt.Errorf("port.scheme: %w", err)
		}
		p.Scheme = s
	} else {
		p.Scheme = ""
	}
	return nil
}

// MarshalJSON serializes a port spec uniformly as `{port, scheme}`. The
// scheme field is omitted when empty (no per-port override) so consumers that
// only care about the port number can read `.port` without branching, while
// scheme-aware consumers see the override when it is set. The shape is
// intentionally non-polymorphic — every port entry is an object, never a bare
// integer — so external tooling can rely on a stable schema.
func (p ServicePortSpec) MarshalJSON() ([]byte, error) {
	type rich struct {
		Port   int    `json:"port"`
		Scheme string `json:"scheme,omitempty"`
	}
	return json.Marshal(rich(p))
}

// ServiceInfoPath describes a sub-URL under a service's main URL.
type ServiceInfoPath struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
	Icon string `yaml:"icon,omitempty"`
}

// allowedFieldsFor returns the set of YAML field names permitted for entries
// of the given service type. Used by validators and loader strict-decode error
// messages as the single source of truth for per-type field allowlists.
// Returns a fresh map each call so callers cannot corrupt shared state.
func allowedFieldsFor(t ServiceType) map[string]bool {
	// Fields permitted for every service type.
	common := []string{
		"type", "container", "required", "compose",
		"ports", "hosts", "icon", "info", "status",
		"on_enable", "on_disable", "notes", "bridge",
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
			"configs", "dirs", "extends", "cli", "render", "generated",
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

// GeneratedField declares one service-minted value that DWE harvests and
// replays. File is the output file (relative to the service hub dir) the
// service writes the value into; Pattern is a regex whose first capture group
// extracts the value (e.g. `^APP_KEY=(.*)$`).
type GeneratedField struct {
	File    string `yaml:"file"`
	Pattern string `yaml:"pattern"`
}

// ServiceConfig describes a single service entry in workspace/services/<name>/service.yml.
// The Type field discriminates the per-entry shape (app / tool / infra).
// The Enabled flag is resolved from the 3-layer config merge (mandatory
// services are always enabled).
type ServiceConfig struct {
	Type            ServiceType                `yaml:"type"`
	Container       string                     `yaml:"container"`
	Required        bool                       `yaml:"required"`
	Enabled         bool                       `yaml:"-"` // computed: required || services.<name>.enabled
	Ports           map[string]ServicePortSpec `yaml:"ports,omitempty"`
	Hosts           map[string]string          `yaml:"hosts,omitempty"`
	Icon            string                     `yaml:"icon,omitempty"`
	Info            ServiceInfoBlock           `yaml:"info,omitempty"`
	Dir             string                     `yaml:"dir"`
	DirInternal     string                     `yaml:"dir_internal"`
	WorkDirInternal string                     `yaml:"work_dir_internal"`
	Configs         []ServiceConfigEntry       `yaml:"configs"`
	Dirs            []string                   `yaml:"dirs"`
	Extends         string                     `yaml:"extends"`
	DependsOn       []string                   `yaml:"depends_on"`
	Compose         []string                   `yaml:"compose"`
	// LocalComposeExtra carries per-developer overlay files appended right after
	// svc.Compose in [DweConfig.composeFiles] under the same `all || svc.Enabled`
	// gate. Populated only by explicit post-decode injection from
	// `workspace/local.yml` (`services.<name>.compose.extra`) — the `yaml:"-"`
	// tag makes it unreachable from any git-tracked service.yml.
	LocalComposeExtra []string            `yaml:"-"`
	CLI               ServiceCLIConfig    `yaml:"cli"`
	Render            ServiceRenderConfig `yaml:"render"`
	Bridge            ServiceBridgeConfig `yaml:"bridge"`
	// Generated declares per-service values that the service itself mints (e.g.
	// Laravel APP_KEY) and DWE harvests back into a durable store
	// (.dwe/generated.yml) to replay on subsequent renders. Keyed by field name.
	Generated map[string]GeneratedField `yaml:"generated,omitempty"`
	Status    []StatusColumn            `yaml:"status,omitempty"`
	OnEnable  *ServiceToggleHooks       `yaml:"on_enable,omitempty"`
	OnDisable *ServiceToggleHooks       `yaml:"on_disable,omitempty"`
	Notes     *ServiceNotes             `yaml:"notes,omitempty"`
}

// IsApp reports whether this service has type "app".
func (s ServiceConfig) IsApp() bool { return s.Type.IsApp() }

// IsTool reports whether this service has type "tool".
func (s ServiceConfig) IsTool() bool { return s.Type.IsTool() }

// IsInfra reports whether this service has type "infra".
func (s ServiceConfig) IsInfra() bool { return s.Type.IsInfra() }

// Port returns the host port for the named entry in s.Ports, or 0 if absent.
// Use PortScheme for the optional per-port scheme override.
func (s ServiceConfig) Port(name string) int { return s.Ports[name].Port }

// PortScheme returns the scheme override declared on the named port entry,
// or "" if none was set. "" means the caller should fall back to
// s.Info.Scheme, then Runtime.UseHTTPS. Use EffectiveScheme to resolve the
// full precedence chain.
func (s ServiceConfig) PortScheme(name string) string { return s.Ports[name].Scheme }

// EffectiveScheme resolves the URL scheme for the named port using the
// precedence: per-port override → service.Info.Scheme → runtimeUseHTTPS.
// Returns "http" or "https".
func (s ServiceConfig) EffectiveScheme(portName string, runtimeUseHTTPS bool) string {
	if sch := s.PortScheme(portName); sch != "" {
		return sch
	}
	if s.Info.Scheme != "" {
		return s.Info.Scheme
	}
	if runtimeUseHTTPS {
		return "https"
	}
	return "http"
}

// PortNumbers returns a flattened map of port name → port number, discarding
// any per-port scheme overrides. Useful for downstream consumers (status
// rows, conflict probes, simple JSON outputs) that don't carry scheme metadata.
func (s ServiceConfig) PortNumbers() map[string]int {
	if len(s.Ports) == 0 {
		return nil
	}
	out := make(map[string]int, len(s.Ports))
	for k, v := range s.Ports {
		out[k] = v.Port
	}
	return out
}

// Host returns the hostname for the named entry in s.Hosts, or "" if absent.
func (s ServiceConfig) Host(name string) string { return s.Hosts[name] }

// IDERenderEnabledExplicit returns the IDE render enabled state and whether it was explicitly set.
// If Enabled is non-nil, returns its value and true.
// If Enabled is nil, returns true for type "app" (default) or false for other types, and false (not explicit).
func (s ServiceConfig) IDERenderEnabledExplicit() (enabled bool, explicit bool) {
	return s.renderEnabledExplicit(s.Render.IDE.Enabled)
}

// renderEnabledExplicit resolves a render toggle: if the explicit pointer is
// non-nil it is authoritative; otherwise app services default on, others off.
// Shared by the IDE/AI/Git per-kind RenderEnabledExplicit accessors. The
// bridge toggle (BridgeEnabledExplicit) shares the tristate shape but NOT the
// app default — the bridge is strictly opt-in.
func (s ServiceConfig) renderEnabledExplicit(enabled *bool) (bool, bool) {
	if enabled != nil {
		return *enabled, true
	}
	return s.IsApp(), false
}

// IDERenderEnabled returns whether this service should participate in IDE rendering.
// It's a simple wrapper around IDERenderEnabledExplicit that discards the explicit flag.
func (s ServiceConfig) IDERenderEnabled() bool {
	enabled, _ := s.IDERenderEnabledExplicit()
	return enabled
}

// AIRenderEnabledExplicit returns the AI docs render enabled state and whether it was explicitly set.
// If Enabled is non-nil, returns its value and true.
// If Enabled is nil, returns true for type "app" (default) or false for other types, and false (not explicit).
// Only app services have a dedicated source directory to host hub-level agent
// docs; tools/infra opt in explicitly via render.ai.enabled: true.
func (s ServiceConfig) AIRenderEnabledExplicit() (enabled bool, explicit bool) {
	return s.renderEnabledExplicit(s.Render.AI.Enabled)
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
	return s.renderEnabledExplicit(s.Render.Git.Enabled)
}

// GitRenderEnabled returns whether this service should participate in git-hooks rendering.
// It's a simple wrapper around GitRenderEnabledExplicit that discards the explicit flag.
func (s ServiceConfig) GitRenderEnabled() bool {
	enabled, _ := s.GitRenderEnabledExplicit()
	return enabled
}

// BridgeEnabledExplicit returns the host-bridge enabled state and whether it was explicitly set.
// If Enabled is non-nil, returns its value and true.
// If Enabled is nil, returns false for EVERY service type, and false (not
// explicit) — unlike the render toggles there is no app default: the bridge
// mounts a host-controlled binary into the container and opens a
// container→host command channel, so it is strictly opt-in per service.
func (s ServiceConfig) BridgeEnabledExplicit() (enabled bool, explicit bool) {
	if s.Bridge.Enabled != nil {
		return *s.Bridge.Enabled, true
	}
	return false, false
}

// BridgeEnabled returns whether this service should receive the host-bridge
// shim mount and env block in the generated compose overlay.
// It's a simple wrapper around BridgeEnabledExplicit that discards the explicit flag.
func (s ServiceConfig) BridgeEnabled() bool {
	enabled, _ := s.BridgeEnabledExplicit()
	return enabled
}

// BridgeShimPath returns the container path the shim binary is mounted at:
// bridge.shim_path when set, DefaultBridgeShimPath otherwise.
func (s ServiceConfig) BridgeShimPath() string {
	if s.Bridge.ShimPath != "" {
		return s.Bridge.ShimPath
	}
	return DefaultBridgeShimPath
}

// BridgeOnUnreachable returns the shim's unreachable-daemon policy:
// bridge.on_unreachable when set, BridgeOnUnreachableFail otherwise.
func (s ServiceConfig) BridgeOnUnreachable() string {
	if s.Bridge.OnUnreachable != "" {
		return s.Bridge.OnUnreachable
	}
	return BridgeOnUnreachableFail
}

// DisplayIcon returns the resolved icon for this service.
// If Icon is non-empty, returns it; otherwise returns the type-default icon.
// app -> "📦", tool -> "🔧", infra -> "🧱", unknown -> "".
//
// The tool default is intentionally 🔧 (wrench) rather than ⚙ (gear): gear's
// base codepoint (U+2699) has Emoji_Presentation = No, so terminals disagree
// on its width even with VS16 — see styles.IsAmbiguousWidthIcon. Wrench renders
// reliably as 2 cells everywhere.
func (s ServiceConfig) DisplayIcon() string {
	if s.Icon != "" {
		return s.Icon
	}
	switch s.Type {
	case ServiceTypeApp:
		return "📦"
	case ServiceTypeTool:
		return "🔧"
	case ServiceTypeInfra:
		return "🧱"
	default:
		return ""
	}
}

// DisplayTitle returns the resolved display title for this service.
// If Info.Title is non-empty, returns it; otherwise returns the folder key
// title-cased with underscores and hyphens replaced by spaces.
func (s ServiceConfig) DisplayTitle(folderKey string) string {
	if s.Info.Title != "" {
		return s.Info.Title
	}
	// Title-case the folder key, replacing underscores and hyphens with spaces.
	title := strings.NewReplacer("_", " ", "-", " ").Replace(folderKey)
	// Capitalize each word.
	words := strings.Fields(title)
	for i, word := range words {
		if len(word) > 0 {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

// DisplayHostKey returns the resolved primary host key for this service.
// If Info.PrimaryHost is non-empty, returns it; otherwise returns "web".
func (s ServiceConfig) DisplayHostKey() string {
	if s.Info.PrimaryHost != "" {
		return s.Info.PrimaryHost
	}
	return "web"
}

// DisplayPortKey returns the resolved primary port key for this service.
// If Info.PrimaryPort is non-empty, returns it; otherwise returns "http".
func (s ServiceConfig) DisplayPortKey() string {
	if s.Info.PrimaryPort != "" {
		return s.Info.PrimaryPort
	}
	return "http"
}

// DisplayIcon returns the resolved icon for this path.
// If Icon is non-empty, returns it; otherwise returns "🔗".
func (p ServiceInfoPath) DisplayIcon() string {
	if p.Icon != "" {
		return p.Icon
	}
	return "🔗"
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
//   - From     — dot-path into the effective config (e.g. services.app.ports.http)
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

// ValidIdentifierKey reports whether s matches the Go identifier regex:
// ^[A-Za-z_][A-Za-z0-9_]*$. These keys can be used safely with Go template dot syntax.
func ValidIdentifierKey(s string) bool {
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
func validateConfigKeys(cfg *DweConfig) error {
	for _, svcName := range slices.Sorted(maps.Keys(cfg.Services)) {
		svc := cfg.Services[svcName]
		if err := validateIdentifierKeys(svcName, "ports", svc.Ports); err != nil {
			return err
		}
		if err := validateIdentifierKeys(svcName, "hosts", svc.Hosts); err != nil {
			return err
		}
	}
	return nil
}

// validateIdentifierKeys checks that every key in keys is identifier-safe for
// Go template dot syntax. kind ("ports"/"hosts") names the map in the error.
func validateIdentifierKeys[V any](svcName, kind string, keys map[string]V) error {
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		if !ValidIdentifierKey(k) {
			return fmt.Errorf("service %q: invalid %s key %q: must match ^[A-Za-z_][A-Za-z0-9_]*$ (identifier-safe for template dot syntax)", svcName, kind, k)
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
		return fmt.Errorf("%s", legacyComposeOverlaysMsg)
	}
	var keys []string
	for k := range overlays {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Errorf("%s. Found overlays: %v", legacyComposeOverlaysMsg, keys)
}

// LoadConfigOrWrap loads the project config and, on failure, returns the error
// wrapped with the canonical "loading config: %w" prefix used across the CLI
// and workflow layers. It centralizes the ~29 identical load-and-wrap blocks so
// the prefix stays consistent. Use this instead of hand-wrapping; the typed
// project_invalid_config contract (cmdctx.ErrWrap) is a separate path.
func LoadConfigOrWrap(workspacePath string) (*DweConfig, error) {
	cfg, err := LoadConfig(workspacePath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	return cfg, nil
}

// LoadConfig loads the merged DweConfig by layering:
//
//  1. workspacePath (required)
//  2. <dir>/workspace/defaults.yml (optional, versioned project defaults)
//  3. <dir>/workspace/local.yml   (optional, local overrides, gitignored)
//
// Later layers win on conflict; maps are merged recursively.
// The merged raw map is stored in DweConfig.Raw for dot-path resolution.
//
// Sequencing:
//  1. Load workspace/services/<name>/service.yml (canonical service declarations).
//  2. Validate each overlay layer against the declared service set
//     (only services.<name>.enabled permitted in overlays).
//  3. Merge the raw YAML layers.
//  4. Resolve per-service Enabled from the merged overlay.
//  5. Inject services into Raw for dot-path resolution.
func LoadConfig(workspacePath string) (*DweConfig, error) {
	baseDir := filepath.Dir(workspacePath)

	// Read each layer separately so the cross-layer overlay validator can
	// attribute errors to a specific source file.
	type rawLayer struct {
		path string
		data map[string]any
	}
	var layers []rawLayer

	// Layer 1: workspace.yml (required)
	base, err := loadRawYAML(workspacePath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", workspacePath, err)
	}
	layers = append(layers, rawLayer{path: workspacePath, data: base})

	// Layer 2: workspace/defaults.yml (optional)
	defaultsPath := filepath.Join(baseDir, "workspace", "defaults.yml")
	if defaults, err := loadRawYAML(defaultsPath); err == nil {
		layers = append(layers, rawLayer{path: defaultsPath, data: defaults})
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", defaultsPath, err)
	}

	// Layer 3: workspace/local.yml (optional)
	localPath := filepath.Join(baseDir, "workspace", "local.yml")
	var localRaw map[string]any
	if local, err := loadRawYAML(localPath); err == nil {
		layers = append(layers, rawLayer{path: localPath, data: local})
		localRaw = local
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", localPath, err)
	}

	// Step 1: load per-service folders — the canonical service declarations.
	// Use the raw loader here (no extends inheritance yet) so per-service
	// overlay merges in Step 4 can mutate parent values before children
	// inherit them in Step 5.
	services, err := loadServiceFolders(baseDir)
	if err != nil {
		return nil, err
	}

	// Step 2: validate overlay shape against the declared services set
	// before merging. This is the ordering that catches "silently wrong"
	// overlays that would otherwise be tolerated by the deep merge.
	for _, layer := range layers {
		isLocal := layer.path == localPath
		if err := validateServicesOverlay(layer.path, layer.data, services, isLocal); err != nil {
			return nil, err
		}
		if isLocal {
			if err := validateLocalCompose(layer.path, layer.data); err != nil {
				return nil, err
			}
		} else {
			if err := validateNonLocalCompose(layer.path, layer.data); err != nil {
				return nil, err
			}
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
	var cfg DweConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal merged config: %w", err)
	}

	// Reject binaries: blocks — they've moved to user-config
	if _, ok := merged["binaries"]; ok {
		return nil, fmt.Errorf("binaries: moved to ~/.config/dwe/config — use binary_docker=/path, binary_git=/path, etc. See docs/reference/config/workspace.md")
	}

	// Reject tools: blocks — replaced by services with type:tool
	if _, ok := merged["tools"]; ok {
		return nil, fmt.Errorf("tools: no longer supported — define tool entries as services with type: tool in workspace/services/. See docs/reference/config/services/index.md")
	}

	// Load user-config for binary overrides. On error, log warning and continue
	// (graceful degradation — a malformed user pref file doesn't break project loading).
	userCfg, err := userpkg.Load(baseDir)
	if err != nil {
		slog.Warn("userconfig load failed; binary overrides will fall back to PATH defaults", "err", err)
	}
	cfg.userConfig = userCfg

	for i, rule := range cfg.Exports.Env {
		if IsReservedExportName(rule.Name) {
			return nil, fmt.Errorf("exports.env[%d]: %q is a reserved system variable and cannot be redeclared as an export rule (reserved: %s)",
				i, rule.Name, strings.Join(ReservedExportNames, ", "))
		}
	}

	cfg.Raw = merged
	// Store config path so deploy resolution can find service deploy files.
	cfg.Raw["__configPath"] = workspacePath

	// Step 4: resolve per-service Enabled and per-developer port/host overrides
	// from the merged overlay (workspace/defaults.yml + workspace/local.yml). The
	// overlay validator (validateServicesOverlay) has already enforced shape;
	// here we deep-merge entries by port-name / host-name so a partial override
	// only touches the listed keys.
	for name, svc := range services {
		if svc.Required {
			svc.Enabled = true
		} else {
			val, ok := ResolvePath(merged, "services."+name+".enabled")
			if ok {
				svc.Enabled = isTruthy(val)
			}
		}
		applyOverlayPorts(merged, name, &svc)
		applyOverlayHosts(merged, name, &svc)
		// Per-service local compose overlays are sourced ONLY from local.yml
		// (not the merged map) so a stray compose:extra in workspace.yml /
		// defaults.yml — which validateNonLocalCompose rejects — cannot leak
		// in via a future loader bug. Injected BEFORE ResolveServiceExtends so
		// children inherit the parent's populated LocalComposeExtra.
		applyLocalComposeExtra(localRaw, name, &svc)
		services[name] = svc
	}

	// Project-wide local compose overlays: sibling of per-service injection,
	// source-gated to local.yml. Populates cfg.Compose.Extra; later appended
	// strictly last by composeFiles().
	cfg.Compose.Extra = extractProjectLocalComposeExtra(localRaw)

	// Resolve `extends:` inheritance AFTER per-service overlay merges so children
	// inherit the already-overlaid parent values (e.g. local.yml overrides on the
	// parent's hosts/ports propagate into children that don't override themselves).
	if err := ResolveServiceExtends(services); err != nil {
		return nil, err
	}

	// Validate all local.yml overlay paths (project-wide + per-service) for
	// absolute-rejection, containment, and existence. Runs AFTER
	// ResolveServiceExtends so enabled children that inherit LocalComposeExtra
	// from a disabled parent (skipped in validation) are still checked.
	if err := validateLocalComposeExtraPaths(baseDir, cfg.Compose.Extra, services); err != nil {
		return nil, err
	}
	// Phase 2: apply scheme-only port overlays now that every service has its
	// final inherited port numbers. Scheme-only overlays must reference an
	// existing port — see applyDeferredOverlaySchemes for the contract.
	if err := applyDeferredOverlaySchemes(merged, services); err != nil {
		return nil, err
	}
	cfg.Services = services

	// Step 5: inject service definitions into the raw map so dot-paths like
	// `services.main.ports.http` resolve via ResolvePath for export rules,
	// info.yml, docker.yml templates, and user command default_from.
	injectServicesIntoRaw(merged, services)

	// Type-semantics gates: depends_on targets must not be tool-typed; deploy
	// files may only exist for app-typed services. These rules fire on every
	// LoadConfig path (deploy, lifecycle, compose) — not just `dwe validate`.
	if err := validateDependsOnTypes(services); err != nil {
		return nil, err
	}
	// Load workspace/deploy.yml separately (not merged with config layers).
	deployPath := filepath.Join(baseDir, "workspace", "deploy.yml")
	if deployCfg, err := LoadProjectDeployConfig(deployPath); err == nil {
		cfg.Deploy = deployCfg
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", deployPath, err)
	}
	// Ensure cfg.Deploy is always non-nil (empty by default if deploy.yml is absent)
	if cfg.Deploy == nil {
		cfg.Deploy = &ProjectDeployConfig{}
	}

	// Validate config keys and detect legacy compose.overlays.
	if err := detectLegacyComposeOverlays(merged); err != nil {
		return nil, err
	}
	if err := validateConfigKeys(&cfg); err != nil {
		return nil, err
	}

	// Debug firehose: summarise the resolved config. Visible only when the
	// trace slog handler is installed (dwe --debug / DWE_DEBUG); otherwise this
	// is a no-op Debug record dropped by Go's default handler.
	enabled := 0
	for _, svc := range services {
		if svc.Enabled {
			enabled++
		}
	}
	slog.Debug("config loaded",
		"path", workspacePath,
		"services", len(services),
		"enabled", enabled,
		"layers", len(layers))

	return &cfg, nil
}

// OverlayAllowedKeys is the closed set of per-developer overridable keys
// under a layer's services.<name> mapping. Structural fields (container,
// dir, configs, compose, extends, etc.) belong in workspace/services/<name>/service.yml and
// are rejected by validateServicesOverlay.
//
// Ports and hosts are deliberately overridable: a developer commonly needs
// to change a port that clashes with something already bound on their host,
// or switch the `*.local` hostname they use, without editing the shared
// workspace/services.yml. Each is deep-merged by port-name / host-name on top
// of the declared map so a partial override only touches the listed entries.
var OverlayAllowedKeys = map[string]bool{
	"enabled": true,
	"ports":   true,
	"hosts":   true,
}

// validateServicesOverlay rejects any non-overlay-allowed field under a
// layer's services.<name> mapping, any services.<name> entry naming a
// service not declared in workspace/services.yml, and malformed ports/hosts
// blocks. layerPath is included in error messages so the user knows which
// file to edit.
//
// isLocal is true when this layer is `workspace/local.yml`. It gates the
// per-service `compose:` block to local.yml only — that key is per-developer
// overlay extra; allowing it in defaults.yml or workspace.yml would silently
// pass validation but never be injected (post-decode injection is source-gated
// to local.yml), which is confusing.
func validateServicesOverlay(layerPath string, raw map[string]any, declared map[string]ServiceConfig, isLocal bool) error {
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
			return fmt.Errorf("%s: services.%s: unknown service (declared services live in workspace/services/<name>/service.yml)", layerPath, name)
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
			if key == "compose" && isLocal {
				// Local-only per-service overlay: defer to shape check below.
				continue
			}
			if key == "compose" {
				return fmt.Errorf("%s: services.%s.compose: per-developer compose overlays (compose.extra) belong in workspace/local.yml, not in shared overlay files", layerPath, name)
			}
			if !OverlayAllowedKeys[key] {
				return fmt.Errorf("%s: services.%s.%s: service definitions belong in workspace/services/<name>/service.yml; overlays may only set %s",
					layerPath, name, key, overlayAllowedKeysList())
			}
		}
		if err := validateOverlayPorts(layerPath, name, entry["ports"]); err != nil {
			return err
		}
		if err := validateOverlayHosts(layerPath, name, entry["hosts"]); err != nil {
			return err
		}
		if isLocal {
			if err := validateOverlayCompose(layerPath, name, entry["compose"]); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateOverlayCompose rejects malformed `compose:` blocks under a service
// entry in workspace/local.yml. Accepts nil (key absent) and the shape
// `{extra: [<string>, ...]}`; rejects unknown subkeys, non-list `extra`,
// and non-string entries.
func validateOverlayCompose(layerPath, svcName string, raw any) error {
	if raw == nil {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: services.%s.compose: must be a mapping with key 'extra'", layerPath, svcName)
	}
	for _, key := range slices.Sorted(maps.Keys(m)) {
		if key != "extra" {
			return fmt.Errorf("%s: services.%s.compose.%s: unknown field (only 'extra' is allowed)", layerPath, svcName, key)
		}
	}
	extraRaw, ok := m["extra"]
	if !ok || extraRaw == nil {
		return nil
	}
	return validateComposeExtraList(fmt.Sprintf("%s: services.%s.compose.extra", layerPath, svcName), extraRaw)
}

// validateLocalCompose validates the SHAPE of `raw["compose"]` in
// workspace/local.yml. It does NOT whitelist other top-level keys: local.yml
// legitimately carries `state:`, `runtime:`, etc. so rejecting unknown
// top-level keys would break existing files. Under `compose` it accepts only
// `extra: [<string>, ...]` — `compose.base` belongs in workspace.yml and
// must not be overridden per-developer.
func validateLocalCompose(layerPath string, raw map[string]any) error {
	composeRaw, ok := raw["compose"]
	if !ok || composeRaw == nil {
		return nil
	}
	m, ok := composeRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: compose: must be a mapping", layerPath)
	}
	for _, key := range slices.Sorted(maps.Keys(m)) {
		if key != "extra" {
			return fmt.Errorf("%s: compose.%s: unknown field (only 'extra' is allowed in local.yml)", layerPath, key)
		}
	}
	extraRaw, ok := m["extra"]
	if !ok || extraRaw == nil {
		return nil
	}
	return validateComposeExtraList(fmt.Sprintf("%s: compose.extra", layerPath), extraRaw)
}

// validateNonLocalCompose rejects `compose.extra` in any layer OTHER than
// workspace/local.yml. `compose.extra` is per-developer overlay configuration
// and belongs in local.yml — silently accepting it in workspace.yml /
// defaults.yml would never trigger injection (which is source-gated to
// local.yml), confusing users. `compose.base` is unaffected.
func validateNonLocalCompose(layerPath string, raw map[string]any) error {
	composeRaw, ok := raw["compose"]
	if !ok || composeRaw == nil {
		return nil
	}
	m, ok := composeRaw.(map[string]any)
	if !ok {
		return nil
	}
	if _, hasExtra := m["extra"]; hasExtra {
		return fmt.Errorf("%s: compose.extra: per-developer overlays belong in workspace/local.yml, not in this file", layerPath)
	}
	return nil
}

// validateComposeExtraList checks the shared shape of a `compose.extra` list:
// must be a non-nil sequence of non-empty strings. fieldPath is used as the
// error prefix (e.g. "workspace/local.yml: services.dev.compose.extra").
func validateComposeExtraList(fieldPath string, raw any) error {
	list, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("%s: must be a list of strings", fieldPath)
	}
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			return fmt.Errorf("%s[%d]: must be a string (got %T)", fieldPath, i, item)
		}
		if s == "" {
			return fmt.Errorf("%s[%d]: must be a non-empty string", fieldPath, i)
		}
	}
	return nil
}

// overlayAllowedKeysList returns a sorted, human-friendly comma-separated
// list of overlay-allowed keys for use in error hints.
func overlayAllowedKeysList() string {
	keys := slices.Sorted(maps.Keys(OverlayAllowedKeys))
	return strings.Join(keys, ", ")
}

// validateOverlayPorts rejects malformed `ports:` blocks in overlay layers.
// Accepts nil (key absent or null) and map[string]any where every value is
// either:
//   - an integer in the 1..65535 range, or
//   - a mapping {port: int, scheme: "http"|"https"} (per-port scheme override).
func validateOverlayPorts(layerPath, svcName string, raw any) error {
	if raw == nil {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: services.%s.ports: %w", layerPath, svcName, ErrServicePortsShape)
	}
	for _, portName := range slices.Sorted(maps.Keys(m)) {
		switch v := m[portName].(type) {
		case int:
			if v < 1 || v > 65535 {
				return fmt.Errorf("%s: services.%s.ports.%s = %d: %w", layerPath, svcName, portName, v, ErrServicePortOutOfRange)
			}
		case map[string]any:
			if err := validatePortObject(v, portObjectOverlay); err != nil {
				return fmt.Errorf("%s: services.%s.ports.%s: %w", layerPath, svcName, portName, err)
			}
		default:
			return fmt.Errorf("%s: services.%s.ports.%s: %w (must be an integer or a mapping {port, scheme})", layerPath, svcName, portName, ErrServicePortsShape)
		}
	}
	return nil
}

// portObjectMode tunes validatePortObject for the two contexts the rich form
// can appear in. Service-yml requires `port:` (every service must declare its
// own port number). Overlay allows port to be omitted so a developer can
// override scheme alone for an inherited port — symmetric with the bare-int
// overlay form which preserves any existing scheme.
type portObjectMode int

const (
	portObjectRequirePort portObjectMode = iota // service.yml decode
	portObjectOverlay                           // local.yml / defaults.yml
)

// validatePortObject checks the rich `ports.<key>` object form. Allowed fields
// are {port, scheme}; when port is present it must be int in range;
// scheme (when present and non-null) must be "http" / "https" / "".
func validatePortObject(obj map[string]any, mode portObjectMode) error {
	for k := range obj {
		if k != "port" && k != "scheme" {
			return fmt.Errorf("%w (unknown field %q; allowed: port, scheme)", ErrServicePortsShape, k)
		}
	}
	portRaw, hasPort := obj["port"]
	if !hasPort && mode == portObjectRequirePort {
		return fmt.Errorf("%w (missing port)", ErrServicePortsShape)
	}
	if hasPort {
		// Explicit null is rejected — it is ambiguous between "no override"
		// (which the developer should express by omitting `port:` entirely)
		// and "set port to nothing", and silently accepting it manufactures
		// a Port: 0 entry that bypasses Phase 2's scheme-only handling.
		if portRaw == nil {
			return fmt.Errorf("%w (port cannot be null; omit the key to inherit, or set an integer)", ErrServicePortsShape)
		}
		n, ok := portRaw.(int)
		if !ok {
			return fmt.Errorf("%w (port is not an integer)", ErrServicePortsShape)
		}
		if n < 1 || n > 65535 {
			return fmt.Errorf("port = %d: %w", n, ErrServicePortOutOfRange)
		}
	}
	if schRaw, ok := obj["scheme"]; ok && schRaw != nil {
		s, ok := schRaw.(string)
		if !ok {
			return fmt.Errorf("%w (scheme is not a string)", ErrServicePortsShape)
		}
		if s != "" && s != "http" && s != "https" {
			return fmt.Errorf("%w (scheme %q is not one of: http, https)", ErrServicePortsShape, s)
		}
	}
	// Overlay must specify at least one field; an entirely empty `{}` is a
	// no-op that would silently mask developer intent.
	if mode == portObjectOverlay && !hasPort {
		if _, hasScheme := obj["scheme"]; !hasScheme {
			return fmt.Errorf("%w (overlay must set at least one of: port, scheme)", ErrServicePortsShape)
		}
	}
	return nil
}

// validateOverlayHosts rejects malformed `hosts:` blocks in overlay layers.
// Accepts nil (key absent or null) and map[string]any where every value is
// a string.
func validateOverlayHosts(layerPath, svcName string, raw any) error {
	if raw == nil {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: services.%s.hosts: %w", layerPath, svcName, ErrServiceHostsShape)
	}
	for _, hostName := range slices.Sorted(maps.Keys(m)) {
		if _, ok := m[hostName].(string); !ok {
			return fmt.Errorf("%s: services.%s.hosts.%s: %w (not a string)", layerPath, svcName, hostName, ErrServiceHostsShape)
		}
	}
	return nil
}

// LoadServiceFolder loads a single service definition from
// workspace/services/<name>/service.yml. The service fields are top-level (no
// wrapper key). Returns an error wrapping the name if the file is missing or
// invalid.
func LoadServiceFolder(baseDir, name string) (*ServiceConfig, error) {
	svcFile := filepath.Join(baseDir, "workspace", "services", name, "service.yml")
	data, err := os.ReadFile(svcFile)
	if err != nil {
		return nil, fmt.Errorf("loading service %q definition: %w", name, err)
	}

	// First pass: parse to raw map to inspect actual YAML keys and reject
	// disallowed-shape values before strict decode produces opaque errors.
	var entry map[string]any
	if err := yaml.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("loading service %q definition: parse: %w", name, err)
	}
	if entry == nil {
		entry = map[string]any{}
	}

	var diags []error

	// Type required.
	typeRaw, hasType := entry["type"]
	if !hasType {
		diags = append(diags, fmt.Errorf("%w: service %q", ErrServiceTypeMissing, name))
	} else {
		typeStr, _ := typeRaw.(string)
		svcType := ServiceType(typeStr)
		if err := svcType.Validate(); err != nil {
			diags = append(diags, fmt.Errorf("service %q: %w", name, err))
		} else {
			// Extends only permitted between app services.
			if extRaw, ok := entry["extends"]; ok && extRaw != nil && !svcType.IsApp() {
				diags = append(diags, fmt.Errorf("%w: service %q (type %s)", ErrServiceExtendsCrossType, name, svcType))
			}
			// Per-type field allowlist.
			allowed := allowedFieldsFor(svcType)
			for _, key := range slices.Sorted(maps.Keys(entry)) {
				if key == "extends" && !svcType.IsApp() {
					continue
				}
				if !allowed[key] {
					diags = append(diags, fmt.Errorf("%w: service %q (type %s): field %q", ErrServiceFieldNotAllowed, name, svcType, key))
				}
			}
			// Ports shape + range. Accepts bare int or mapping {port, scheme}.
			if v, ok := entry["ports"]; ok && v != nil {
				if m, isMap := v.(map[string]any); !isMap {
					diags = append(diags, fmt.Errorf("%w: service %q", ErrServicePortsShape, name))
				} else {
					for _, portName := range slices.Sorted(maps.Keys(m)) {
						switch pv := m[portName].(type) {
						case int:
							if pv < 1 || pv > 65535 {
								diags = append(diags, fmt.Errorf("%w: service %q port %q = %d", ErrServicePortOutOfRange, name, portName, pv))
							}
						case map[string]any:
							if err := validatePortObject(pv, portObjectRequirePort); err != nil {
								diags = append(diags, fmt.Errorf("service %q port %q: %w", name, portName, err))
							}
						default:
							diags = append(diags, fmt.Errorf("%w: service %q port %q is not an integer or a mapping {port, scheme}", ErrServicePortsShape, name, portName))
						}
					}
				}
			}
			// Hosts shape.
			if v, ok := entry["hosts"]; ok && v != nil {
				if _, isMap := v.(map[string]any); !isMap {
					diags = append(diags, fmt.Errorf("%w: service %q", ErrServiceHostsShape, name))
				}
			}
		}
	}

	if len(diags) > 0 {
		return nil, fmt.Errorf("loading service %q definition: %w", name, errors.Join(diags...))
	}

	// Second pass: strict typed decode.
	var svc ServiceConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&svc); err != nil {
		return nil, fmt.Errorf("loading service %q definition: parse: %w", name, err)
	}

	// Post-decode validation for fields whose allowed-values contract isn't
	// expressed in the type system. EffectiveScheme trusts svc.Info.Scheme
	// directly, so a typo like `info.scheme: ftp` must be caught at load
	// time — relying solely on `dwe validate` would let runtime commands
	// silently emit non-http schemes.
	if s := svc.Info.Scheme; s != "" && s != "http" && s != "https" {
		return nil, fmt.Errorf("loading service %q definition: info.scheme %q is not allowed (must be \"http\" or \"https\")", name, s)
	}

	// Apply folder-name default to container field if not explicitly set.
	if svc.Container == "" {
		svc.Container = name
	}

	return &svc, nil
}

// LoadServices loads all service definitions from workspace/services/<name>/service.yml
// per-folder files and resolves `extends:` inheritance. Missing directory →
// empty map (not an error). Each folder entry is loaded independently and
// errors are aggregated. For callers that need to apply per-service overlays
// (e.g. local.yml host/port overrides) BEFORE inheritance — so children
// inherit overlaid parent values — use [loadServiceFolders] followed by
// [ResolveServiceExtends].
func LoadServices(baseDir string) (map[string]ServiceConfig, error) {
	services, err := loadServiceFolders(baseDir)
	if err != nil {
		return nil, err
	}
	if err := ResolveServiceExtends(services); err != nil {
		return nil, err
	}
	return services, nil
}

// loadServiceFolders is the bare per-folder file loader used by [LoadServices]
// and by [LoadConfig] when overlays must be applied before extends inheritance.
// It does NOT resolve `extends:` — callers must follow up with
// [ResolveServiceExtends] when extends-aware data is needed.
func loadServiceFolders(baseDir string) (map[string]ServiceConfig, error) {
	return walkServiceFolders(baseDir, "loading services", func(name string) (ServiceConfig, bool, error) {
		svc, err := LoadServiceFolder(baseDir, name)
		if err != nil {
			return ServiceConfig{}, false, err
		}
		return *svc, true, nil
	})
}

// walkServiceFolders iterates each subdirectory of workspace/services/, calling
// load for each service name. Results where load reports keep=true are collected
// into a map keyed by service name (keep=false silently drops the entry). A
// missing services directory yields an empty map and nil error. Per-folder load
// errors are collected and returned joined; errLabel prefixes both the ReadDir
// failure and the joined per-folder errors.
func walkServiceFolders[T any](baseDir, errLabel string, load func(name string) (val T, keep bool, err error)) (map[string]T, error) {
	servicesDir := filepath.Join(baseDir, "workspace", "services")
	entries, err := os.ReadDir(servicesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]T{}, nil
		}
		return nil, fmt.Errorf("%s: %w", errLabel, err)
	}

	result := make(map[string]T)
	var loadErrs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		val, keep, err := load(name)
		if err != nil {
			loadErrs = append(loadErrs, err)
			continue
		}
		if keep {
			result[name] = val
		}
	}
	if len(loadErrs) > 0 {
		return nil, fmt.Errorf("%s: %w", errLabel, errors.Join(loadErrs...))
	}

	return result, nil
}

// ResolveServiceExtends resolves `extends:` inheritance across the given
// services map in place. Must be called AFTER per-service overlay merges
// (applyOverlayPorts / applyOverlayHosts) so children inherit the
// already-overlaid parent values rather than the pre-overlay defaults from
// service.yml.
func ResolveServiceExtends(services map[string]ServiceConfig) error {
	order, err := topoSortServices(services)
	if err != nil {
		return fmt.Errorf("loading services: %w", err)
	}

	var crossTypeDiags []error
	for name, svc := range services {
		if svc.Extends == "" {
			continue
		}
		parent := services[svc.Extends]
		if parent.Type != svc.Type {
			crossTypeDiags = append(crossTypeDiags, fmt.Errorf("%w: service %q (type %s) extends %q (type %s)", ErrServiceExtendsCrossType, name, svc.Type, svc.Extends, parent.Type))
		}
	}
	if len(crossTypeDiags) > 0 {
		return fmt.Errorf("loading services: %w", errors.Join(crossTypeDiags...))
	}

	for _, name := range order {
		svc := services[name]
		if svc.Extends == "" {
			continue
		}
		parent := services[svc.Extends]
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
		if len(svc.LocalComposeExtra) == 0 && len(parent.LocalComposeExtra) > 0 {
			svc.LocalComposeExtra = slices.Clone(parent.LocalComposeExtra)
		}
		if len(svc.Ports) == 0 && len(parent.Ports) > 0 {
			svc.Ports = maps.Clone(parent.Ports)
		}
		if len(svc.Hosts) == 0 && len(parent.Hosts) > 0 {
			svc.Hosts = maps.Clone(parent.Hosts)
		}
		// Inherit Info.Scheme — the service-wide scheme override is semantic,
		// not cosmetic, so a child that doesn't redeclare it should still get
		// the parent's URL scheme. Title / primary_host / primary_port are
		// display-only and deliberately NOT inherited.
		if svc.Info.Scheme == "" && parent.Info.Scheme != "" {
			svc.Info.Scheme = parent.Info.Scheme
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
		if len(parent.CLI.Env) > 0 {
			merged := maps.Clone(parent.CLI.Env)
			maps.Copy(merged, svc.CLI.Env)
			svc.CLI.Env = merged
		}
		svc.Dirs = mergeDeduplicatedStrings(parent.Dirs, svc.Dirs)
		if svc.Render.IDE.Enabled == nil && parent.Render.IDE.Enabled != nil {
			v := *parent.Render.IDE.Enabled
			svc.Render.IDE.Enabled = &v
		}
		if svc.Render.IDE.Template == "" {
			svc.Render.IDE.Template = parent.Render.IDE.Template
		}
		if svc.Render.AI.Enabled == nil && parent.Render.AI.Enabled != nil {
			v := *parent.Render.AI.Enabled
			svc.Render.AI.Enabled = &v
		}
		if svc.Render.AI.Template == "" {
			svc.Render.AI.Template = parent.Render.AI.Template
		}
		if svc.Render.Git.Enabled == nil && parent.Render.Git.Enabled != nil {
			v := *parent.Render.Git.Enabled
			svc.Render.Git.Enabled = &v
		}
		if svc.Render.Git.Template == "" {
			svc.Render.Git.Template = parent.Render.Git.Template
		}
		if svc.Render.Config == nil && parent.Render.Config != nil {
			cfg := *parent.Render.Config
			svc.Render.Config = &cfg
		}
		if svc.Bridge.Enabled == nil && parent.Bridge.Enabled != nil {
			v := *parent.Bridge.Enabled
			svc.Bridge.Enabled = &v
		}
		if svc.Bridge.ShimPath == "" {
			svc.Bridge.ShimPath = parent.Bridge.ShimPath
		}
		if svc.Bridge.OnUnreachable == "" {
			svc.Bridge.OnUnreachable = parent.Bridge.OnUnreachable
		}
		// Distinguish an omitted `generated:` (nil → inherit) from an explicitly
		// empty `generated: {}` (non-nil → child wholly replaces with nothing).
		// Using len()==0 would conflate the two and make a child that
		// deliberately cleared the map silently inherit the parent's keys.
		if svc.Generated == nil && len(parent.Generated) > 0 {
			svc.Generated = maps.Clone(parent.Generated)
		}
		services[name] = svc
	}

	return nil
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

// applyOverlayPorts deep-merges any ports defined under
// services.<name>.ports in the merged overlay onto svc.Ports. Existing
// declared ports are preserved unless the overlay names the same key; new
// keys are added. A nil or non-map overlay value is a no-op (the validator
// would have rejected a malformed shape already).
//
// Overlays are FIELD-LEVEL merges, not atomic-entry replacements. Each
// overlay value may be either:
//   - a bare int — replaces svc.Ports[name].Port; preserves any existing
//     Scheme so a developer's local override of just the port number doesn't
//     accidentally drop a scheme declared in service.yml;
//   - a mapping `{port?, scheme?}` — replaces only the fields that are
//     present. Either field may be omitted; omitted fields inherit from the
//     existing spec. This lets a developer override only the scheme for an
//     inherited port (`local.yml: {scheme: https}`) without re-typing the
//     port number, mirroring how bare-int already preserves scheme.
//   - the literal null value for `scheme:` is treated as "no override", same
//     as omitting the key — so `{port: 9090, scheme: null}` is identical to
//     `{port: 9090}` for both validator and loader.
//
// **Two-phase apply.** Overlay entries that set a port number (bare-int or
// rich-form with `port:`) are applied in phase 1 (this function), BEFORE
// `extends:` inheritance runs — so a parent-level port override propagates
// to children that don't declare their own ports. Scheme-only entries
// (rich-form `{scheme: ...}` with no `port:`) are DEFERRED to phase 2
// (applyDeferredOverlaySchemes) which runs AFTER extends resolution; that
// way a child can override the scheme of a port it inherited from its
// parent without manufacturing a `Port: 0` entry that would also block
// inheritance of the parent's port number.
func applyOverlayPorts(merged map[string]any, name string, svc *ServiceConfig) {
	raw, ok := ResolvePath(merged, "services."+name+".ports")
	if !ok {
		return
	}
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return
	}
	for portName, portVal := range m {
		switch v := portVal.(type) {
		case int:
			if svc.Ports == nil {
				svc.Ports = make(map[string]ServicePortSpec, len(m))
			}
			existing := svc.Ports[portName]
			existing.Port = v
			svc.Ports[portName] = existing
		case map[string]any:
			_, hasPort := v["port"]
			if !hasPort {
				// Scheme-only override. Apply immediately when the target
				// port is already declared on THIS service (so children
				// extending it see the overridden scheme via the normal
				// extends-clone); defer to Phase 2 when the port would
				// arrive via inheritance from a parent. Distinguishing the
				// two cases here means scheme-only overlays on parent
				// services propagate through `extends:` like every other
				// per-service field, instead of mutating the parent after
				// the child has already taken a clone.
				existing, present := svc.Ports[portName]
				if !present || existing.Port == 0 {
					continue
				}
				if schRaw, hasScheme := v["scheme"]; hasScheme && schRaw != nil {
					if s, ok := schRaw.(string); ok {
						existing.Scheme = s
						svc.Ports[portName] = existing
					}
				}
				continue
			}
			if svc.Ports == nil {
				svc.Ports = make(map[string]ServicePortSpec, len(m))
			}
			spec := svc.Ports[portName]
			if p, ok := v["port"].(int); ok {
				spec.Port = p
			}
			if schRaw, hasScheme := v["scheme"]; hasScheme && schRaw != nil {
				if s, ok := schRaw.(string); ok {
					spec.Scheme = s
				}
			}
			svc.Ports[portName] = spec
		}
	}
}

// applyDeferredOverlaySchemes runs after `extends:` inheritance and finalises
// scheme-only port overlays (rich-form entries with no `port:` field). For
// each such entry the target port must already exist on the service — either
// declared in service.yml or inherited from a parent in the extends chain.
// If the port is missing, the overlay is rejected: silently materialising a
// `Port: 0` entry would leak an invalid port into status JSON, env conflict
// probes, and `cfg.Raw`, and the developer's intent (override the scheme of
// an existing port) is unambiguously different from "create a new port with
// no number". Bare-int / full-rich entries handled by applyOverlayPorts are
// skipped here.
func applyDeferredOverlaySchemes(merged map[string]any, services map[string]ServiceConfig) error {
	for _, name := range slices.Sorted(maps.Keys(services)) {
		raw, ok := ResolvePath(merged, "services."+name+".ports")
		if !ok {
			continue
		}
		m, ok := raw.(map[string]any)
		if !ok || len(m) == 0 {
			continue
		}
		svc := services[name]
		var mutated bool
		for portName, portVal := range m {
			v, isMap := portVal.(map[string]any)
			if !isMap {
				continue
			}
			if _, hasPort := v["port"]; hasPort {
				continue
			}
			// Scheme-only entry. Require an existing port to attach to.
			existing, present := svc.Ports[portName]
			if !present || existing.Port == 0 {
				return fmt.Errorf("services.%s.ports.%s: scheme-only overlay targets a port that is neither declared nor inherited", name, portName)
			}
			if schRaw, hasScheme := v["scheme"]; hasScheme && schRaw != nil {
				if s, ok := schRaw.(string); ok {
					existing.Scheme = s
					svc.Ports[portName] = existing
					mutated = true
				}
			}
		}
		if mutated {
			services[name] = svc
		}
	}
	return nil
}

// rawExtraStrings extracts a compose.extra list from localRaw at path and
// returns the non-empty string entries. Returns nil when localRaw is nil,
// the path is absent, or the value is not a []any.
func rawExtraStrings(localRaw map[string]any, path string) []string {
	if localRaw == nil {
		return nil
	}
	raw, ok := ResolvePath(localRaw, path)
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	extra := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok && s != "" {
			extra = append(extra, s)
		}
	}
	return extra
}

// applyLocalComposeExtra copies any `services.<name>.compose.extra` list from
// the workspace/local.yml raw map onto svc.LocalComposeExtra. Source-gated to
// the local layer (caller passes localRaw, which is nil when local.yml does
// not exist). validateOverlayCompose has already enforced shape — every entry
// is a non-empty string. A nil/missing path is a no-op.
func applyLocalComposeExtra(localRaw map[string]any, name string, svc *ServiceConfig) {
	svc.LocalComposeExtra = rawExtraStrings(localRaw, "services."+name+".compose.extra")
}

// validateLocalComposeExtraPaths walks every path in projectExtra and each
// services[name].LocalComposeExtra and enforces rules in order:
//
//  1. Absolute paths are rejected (filepath.Join(baseDir, "/x") returns "/x",
//     bypassing containment — so this check MUST come first).
//  2. Containment under baseDir via pathsafe.ContainedRel.
//  3. Symlink safety via pathsafe.CheckNoSymlinks.
//  4. Existence via os.Stat (skipped for disabled services — developers may
//     stage overlay entries before creating the file, and disabled services
//     are excluded from ComposeFiles(); structural checks 1-3 still run
//     because ComposeFilesAll includes disabled-service paths for --all).
//
// Errors carry the local.yml field path and, for missing files, both the
// as-written and resolved absolute paths for easy debugging.
func validateLocalComposeExtraPaths(baseDir string, projectExtra []string, services map[string]ServiceConfig) error {
	for i, p := range projectExtra {
		field := fmt.Sprintf("workspace/local.yml: compose.extra[%d]", i)
		if err := validateComposeExtraPath(baseDir, p, field); err != nil {
			return err
		}
	}
	for _, name := range slices.Sorted(maps.Keys(services)) {
		svc := services[name]
		for i, p := range svc.LocalComposeExtra {
			field := fmt.Sprintf("workspace/local.yml: services.%s.compose.extra[%d]", name, i)
			if svc.Enabled {
				if err := validateComposeExtraPath(baseDir, p, field); err != nil {
					return err
				}
			} else {
				if err := validateComposeExtraPathSafety(baseDir, p, field); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// validateComposeExtraPath runs the full four-step path safety check against a
// single local.yml overlay entry. See validateLocalComposeExtraPaths for the
// ordering rationale.
func validateComposeExtraPath(baseDir, p, field string) error {
	if err := validateComposeExtraPathSafety(baseDir, p, field); err != nil {
		return err
	}
	abs := filepath.Join(baseDir, p)
	if _, err := os.Stat(abs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s: file not found: %q (resolved to %q)", field, p, abs)
		}
		return fmt.Errorf("%s: stat %q (resolved to %q): %w", field, p, abs, err)
	}
	return nil
}

// validateComposeExtraPathSafety runs structural safety checks (steps 1-3) for
// a single compose overlay path without requiring the file to exist. Used for
// disabled-service overlays that appear in ComposeFilesAll but not ComposeFiles.
func validateComposeExtraPathSafety(baseDir, p, field string) error {
	if filepath.IsAbs(p) {
		return fmt.Errorf("%s: absolute paths are not permitted (got %q); use a path relative to the project root", field, p)
	}
	// Reject unclean paths (containing "..", ".", or redundant separators) before
	// filepath.Join, which would clean them away and cause CheckNoSymlinks to walk a
	// different path than Docker Compose receives (e.g. "link/../evil.yml" erases the
	// "link" symlink component, bypassing the symlink check).
	if filepath.Clean(p) != p {
		return fmt.Errorf("%s: path %q must be clean (no \"..\", \".\", or redundant separators); use a canonical relative path", field, p)
	}
	abs := filepath.Join(baseDir, p)
	if _, err := pathsafe.ContainedRel(baseDir, abs); err != nil {
		return fmt.Errorf("%s: %w (path %q escapes project root)", field, err, p)
	}
	if err := pathsafe.CheckNoSymlinks(baseDir, abs, "compose overlay"); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

// extractProjectLocalComposeExtra returns the project-wide compose.extra list
// from workspace/local.yml's raw map, or nil if absent. validateLocalCompose
// has already enforced shape. Source-gated to the local layer (caller passes
// localRaw, which is nil when local.yml does not exist).
func extractProjectLocalComposeExtra(localRaw map[string]any) []string {
	return rawExtraStrings(localRaw, "compose.extra")
}

// applyOverlayHosts deep-merges any hosts defined under
// services.<name>.hosts in the merged overlay onto svc.Hosts. Same
// semantics as applyOverlayPorts.
func applyOverlayHosts(merged map[string]any, name string, svc *ServiceConfig) {
	raw, ok := ResolvePath(merged, "services."+name+".hosts")
	if !ok {
		return
	}
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return
	}
	if svc.Hosts == nil {
		svc.Hosts = make(map[string]string, len(m))
	}
	for hostName, hostVal := range m {
		if s, ok := hostVal.(string); ok {
			svc.Hosts[hostName] = s
		}
	}
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
		entry["required"] = svc.Required
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
			// Inject the bare port number under services.<name>.ports.<key>
			// so dot-path resolution (`from: services.main.ports.http`) and
			// templates (`(index .Services "X").Port "http"`) keep returning
			// an int — the contract those export rules and templates rely on.
			//
			// Per-port scheme overrides are surfaced as a sibling map under
			// services.<name>.port_schemes.<key> so dot-paths and templates
			// can read them too (`from: services.main.port_schemes.http`,
			// `${services.main.port_schemes.admin}`). The sibling is omitted
			// entirely when no port has a scheme override, so it never appears
			// for services that opt out of the new feature.
			ports := make(map[string]any, len(svc.Ports))
			var schemes map[string]any
			for k, v := range svc.Ports {
				ports[k] = v.Port
				if v.Scheme != "" {
					if schemes == nil {
						schemes = make(map[string]any)
					}
					schemes[k] = v.Scheme
				}
			}
			entry["ports"] = ports
			if schemes != nil {
				entry["port_schemes"] = schemes
			}
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

// LifecycleConfig holds the full lifecycle pipeline loaded from workspace/lifecycle.yml.
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
// Precedence: missing block → off; update block present with empty mode → on;
// update block present with mode set → that value (on or off).
// CLI flags (--no-update, --update) override this.
func (cfg *LifecycleRunConfig) EffectiveMode() string {
	if cfg == nil {
		return "off"
	}
	if cfg.Update == nil {
		return "off"
	}
	if cfg.Update.Mode == "" {
		return "on"
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

// LifecycleUpdate configures the optional git update probe run at the start of dwe run.
// Mode must be one of: on, off.
type LifecycleUpdate struct {
	Mode string `yaml:"mode"`
}

// LoadLifecycleConfig loads the lifecycle pipeline from workspace/lifecycle.yml.
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
	// An empty document (a file that is entirely blank lines / comments, like a
	// freshly-scaffolded inert pipeline) decodes to io.EOF with a zero-valued
	// cfg. Treat that exactly like an absent file: callers already default the
	// pipeline, so the built-in default stays active until the user uncomments.
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
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
			if cfg.Run.Update.Mode != "" {
				if !ValidUpdateMode(cfg.Run.Update.Mode) {
					return nil, fmt.Errorf("lifecycle run: update.mode %q is invalid; must be one of: on, off", cfg.Run.Update.Mode)
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

// ValidUpdateMode reports whether s is one of the allowed update mode values.
func ValidUpdateMode(s string) bool {
	switch s {
	case "on", "off":
		return true
	}
	return false
}

// loadDeployConfigDecode does the strict YAML decode + shape validation for any
// pipeline file. It permits all fields including After; context-specific callers
// enforce restrictions on top of this. NOT exported.
func loadProjectDeployConfigDecode(path string, defaultLog bool) (*ProjectDeployConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg ProjectDeployConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// An empty document (a file that is entirely blank lines / comments, like a
	// freshly-scaffolded inert pipeline) decodes to io.EOF with a zero-valued
	// cfg. Treat that exactly like an absent file: callers already default the
	// pipeline, so the built-in default stays active until the user uncomments.
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validatePhaseSteps(cfg.Phases, true); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Log == nil {
		v := defaultLog
		cfg.Log = &v
	}
	return &cfg, nil
}

func loadServiceDeployConfigDecode(path string, defaultLog bool) (*ServiceDeployConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg ServiceDeployConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// An empty document (a file that is entirely blank lines / comments, like a
	// freshly-scaffolded inert pipeline) decodes to io.EOF with a zero-valued
	// cfg. Treat that exactly like an absent file: callers already default the
	// pipeline, so the built-in default stays active until the user uncomments.
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validatePhaseSteps(cfg.Phases, false); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Log == nil {
		v := defaultLog
		cfg.Log = &v
	}
	return &cfg, nil
}

func loadDeployConfigDecode(path string, allowDeployServices bool, defaultLog bool) (*DeployConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg DeployConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// An empty document (a file that is entirely blank lines / comments, like a
	// freshly-scaffolded inert pipeline) decodes to io.EOF with a zero-valued
	// cfg. Treat that exactly like an absent file: callers already default the
	// pipeline, so the built-in default stays active until the user uncomments.
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
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

// ParseDeployConfigForValidation parses any pipeline file without enforcing
// context-specific restrictions (e.g. the after: field is not rejected here).
// Intended for use by validators that need to inspect the full parsed config
// and emit per-file diagnostics. Runtime callers must use the context-specific
// loaders (LoadProjectDeployConfig, LoadServiceDeployConfig, LoadResetConfig).
func ParseDeployConfigForValidation(path string) (*DeployConfig, error) {
	return loadDeployConfigDecode(path, true, true)
}

// LoadProjectDeployConfig loads the project-wide deploy pipeline from workspace/deploy.yml.
// The file is loaded standalone — it is not merged with the 3-layer config.
// Returns os.ErrNotExist when the file is absent (callers may treat it as optional).
// The after: field is structurally not allowed in project-wide deploy configs.
//
// File logging defaults to enabled (Log=true) when unset. Override with
// `log: false` at the top of deploy.yml.
func LoadProjectDeployConfig(deployPath string) (*ProjectDeployConfig, error) {
	return loadProjectDeployConfigDecode(deployPath, true)
}

// LoadServiceDeployConfig loads a per-service deploy pipeline from
// workspace/services/<name>/deploy.yml. Permits the after: field (deploy-time ordering).
// Returns os.ErrNotExist when the file is absent (callers may treat it as optional).
//
// File logging defaults to enabled (Log=true) when unset. Override with
// `log: false` at the top of deploy.yml.
func LoadServiceDeployConfig(deployPath string) (*ServiceDeployConfig, error) {
	return loadServiceDeployConfigDecode(deployPath, true)
}

// LoadResetConfig loads the reset pipeline from a reset.yml file.
// The file is loaded standalone — it is not merged with the 3-layer config.
// Returns os.ErrNotExist when the file is absent (callers may treat it as optional).
// Reset pipelines must not contain deploy_services phases or after: fields.
//
// File logging defaults to disabled (Log=false). Enable with `log: true` at the top.
func LoadResetConfig(resetPath string) (*ProjectDeployConfig, error) {
	return loadProjectDeployConfigDecode(resetPath, false)
}

// validatePhaseSteps validates a slice of DeployPhase values.
// When allowDeployServices is false, deploy_services phases are rejected.
func validatePhaseSteps(phases []DeployPhase, allowDeployServices bool) error {
	for pi := range phases {
		phase := &phases[pi]
		// Phase names starting with "_" are reserved for engine-synthetic phases
		// (e.g. _auto_reap_daemons injected by lifecycle.EnsureStopConfig after load).
		// User-authored YAML must not use underscore-prefixed phase names.
		if strings.HasPrefix(phase.Name, "_") {
			return fmt.Errorf("phase %q: phase names starting with \"_\" are reserved for engine-synthetic phases", phase.Name)
		}
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
	case "shell", "dwe":
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

// validateDependsOnTypes returns an error if any service's depends_on list
// names a service whose type is tool. Unknown depends_on targets are tolerated
// here — TopoSortServices already validates them at plan time.
func validateDependsOnTypes(services map[string]ServiceConfig) error {
	var diags []error
	for _, name := range slices.Sorted(maps.Keys(services)) {
		svc := services[name]
		for _, dep := range svc.DependsOn {
			target, ok := services[dep]
			if !ok {
				continue
			}
			if target.IsTool() {
				diags = append(diags, fmt.Errorf("%w: service %q depends_on %q (type tool)", ErrDependsOnTool, name, dep))
			}
		}
	}
	if len(diags) == 0 {
		return nil
	}
	return errors.Join(diags...)
}

// LoadServiceDeployConfigs loads per-service deploy pipelines from workspace/services/<name>/deploy.yml.
// Only services present in the services map AND having a corresponding deploy file are returned.
// Missing deploy files are silently skipped (not every service needs a deploy pipeline).
func LoadServiceDeployConfigs(baseDir string, services map[string]ServiceConfig) (map[string]*ServiceDeployConfig, error) {
	result := make(map[string]*ServiceDeployConfig)

	for name := range services {
		path := filepath.Join(baseDir, "workspace", "services", name, "deploy.yml")
		cfg, err := LoadServiceDeployConfig(path)
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

// LoadServiceResetConfig loads the reset pipeline for a single named service
// from workspace/services/<name>/reset.yml. Returns (nil, nil) when the file is
// absent (the service simply has no reset pipeline). Reset pipelines structurally
// do not support the after: field (reset is per-service or full, not ordered).
func LoadServiceResetConfig(baseDir, name string) (*ProjectDeployConfig, error) {
	path := filepath.Join(baseDir, "workspace", "services", name, "reset.yml")
	cfg, err := loadProjectDeployConfigDecode(path, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("loading service %q reset: %w", name, err)
	}
	return cfg, nil
}

// LoadServiceResetConfigs loads all per-service reset pipelines from
// workspace/services/*/reset.yml. Services without a reset.yml are silently
// omitted from the result (nil entry would not be useful to callers).
// A missing workspace/services/ directory returns an empty map and nil error.
// Per-folder decode failures are collected and returned via errors.Join.
func LoadServiceResetConfigs(baseDir string) (map[string]*ProjectDeployConfig, error) {
	return walkServiceFolders(baseDir, "loading service reset configs", func(name string) (*ProjectDeployConfig, bool, error) {
		cfg, err := LoadServiceResetConfig(baseDir, name)
		if err != nil {
			return nil, false, err
		}
		return cfg, cfg != nil, nil
	})
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

// LoadDweConfig reads and parses a single workspace.yml file at the given path.
// Prefer LoadConfig for full layered loading.
func LoadDweConfig(path string) (*DweConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg DweConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// LookupDotPath resolves a dot-separated path (e.g. "services.main.work_dir_internal")
// against cfg.Raw and returns the value. Returns (nil, nil) when cfg is nil or the
// path is missing. Returns an error when the resolved value is not a string — the
// only currently-supported leaf type for dot-path lookups in user-facing config.
func LookupDotPath(cfg *DweConfig, path string) (any, error) {
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

// ResolvePath resolves a dot-separated path (e.g. "services.app.ports.http") in a
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
