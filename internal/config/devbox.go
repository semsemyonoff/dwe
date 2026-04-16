// Package config provides loading and validation of devbox configuration files.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DevboxConfig is the merged top-level devbox configuration.
// It is produced by layering devbox.yml → devbox/defaults.yml → devbox/local.yml.
type DevboxConfig struct {
	SchemaVersion string        `yaml:"schema_version"`
	Project       ProjectConfig `yaml:"project"`
	Tools         ToolsConfig   `yaml:"tools"`
	Runtime       RuntimeConfig `yaml:"runtime"`
	State         string        `yaml:"state"`
	Exports       ExportsConfig `yaml:"exports"`
	Compose       ComposeConfig `yaml:"compose"`
	IDE           IDEConfig     `yaml:"ide"`
	Deploy        DeployConfig  `yaml:"-"`

	// Services holds the fully resolved service definitions loaded from
	// devbox/services.yml with Enabled populated from the 3-layer config merge.
	// Not unmarshalled from the merge — built by LoadConfig.
	Services map[string]ServiceConfig `yaml:"-"`

	// Raw holds the merged config as a plain map, used for dot-path resolution
	// in export rules. Populated only by LoadConfig; not serialized.
	Raw map[string]any `yaml:"-"`
}

// IDEConfig holds per-editor IDE config generation settings.
// Used by `devbox render ide` to determine which editor configs to generate.
type IDEConfig struct {
	VSCode       IDEEditorConfig `yaml:"vscode"`
	JetBrains    IDEEditorConfig `yaml:"jetbrains"`
	Devcontainer IDEEditorConfig `yaml:"devcontainer"`
}

// IDEEditorConfig holds the enabled flag for a single editor target.
type IDEEditorConfig struct {
	Enabled bool `yaml:"enabled"`
}

// DeployConfig holds the full deploy pipeline loaded from devbox/deploy.yml.
// It is loaded separately and not part of the 3-layer config merge.
type DeployConfig struct {
	Phases []DeployPhase `yaml:"phases"`
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
type DeployPhase struct {
	Name           string       `yaml:"name"`
	Description    string       `yaml:"description"`
	When           string       `yaml:"when"`
	Steps          []DeployStep `yaml:"steps"`
	DeployServices bool         `yaml:"deploy_services"`
}

// DeployStep is a single atomic pipeline action.
// Exactly one of Run, Devbox, Command, or Builtin must be set.
//
//   - Run     — shell command executed directly via os/exec
//   - Devbox  — public devbox CLI subcommand (e.g. "docker down"); invoked as "devbox <subcommand>"
//   - Command — devbox command ID (e.g. "services.main.migrate"); dispatched via command runner
//   - Builtin — engine-internal action name (e.g. "confirm", "remove_paths"); executed directly in Go
//   - With    — parameters for Command or Builtin steps (string values for Command, any type for Builtin)
//   - When    — skip condition; three expression kinds are supported:
//     1. Go template (contains "{{") — evaluated against DevboxConfig at plan-resolution time;
//     step is excluded from the plan when the rendered result is falsy ("", "false", "0").
//     2. Builtin predicate — evaluated at step-execution time:
//     "dir-exists <path>", "dir-missing <path>",
//     "dir-empty <path>", "dir-not-empty <path>",
//     "file-exists <path>", "file-missing <path>".
//     3. Shell command prefixed "cmd: <command>" — evaluated at step-execution time;
//     exits 0 → true (run step), non-zero → false (skip step).
//   - Check — post-condition evaluated after the step succeeds; same expression kinds
//     as When (builtin predicates and "cmd: <command>"), but no Go templates.
//     Pipeline is aborted with an error when the check returns false.
type DeployStep struct {
	Name        string         `yaml:"name"`
	Run         string         `yaml:"run"`
	Devbox      string         `yaml:"devbox"`
	Command     string         `yaml:"command"`
	Builtin     string         `yaml:"builtin"`
	With        map[string]any `yaml:"with"`
	Description string         `yaml:"description"`
	When        string         `yaml:"when"`
	Check       string         `yaml:"check"`

	// Deprecated: use builtin: service_configs_copy with with.service and with.mode.
	// Automatically converted to Builtin at load time for backward compatibility.
	ServiceConfigsCopy string `yaml:"service_configs_copy"`
	// Deprecated: paired with ServiceConfigsCopy; use with.mode in the builtin form.
	Mode string `yaml:"mode"`
}

// ComposeConfig holds Docker Compose file declarations.
// Base is always included; Overlays are optional and keyed by a short name.
type ComposeConfig struct {
	Base     string            `yaml:"base"`
	Overlays map[string]string `yaml:"overlays"`
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
	var files []string
	if c.Compose.Base != "" {
		files = append(files, c.Compose.Base)
	}

	// Tool overlays from compose.overlays in sorted key order.
	keys := make([]string, 0, len(c.Compose.Overlays))
	for k := range c.Compose.Overlays {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if c.toolOverlayEnabled(key) {
			files = append(files, c.Compose.Overlays[key])
		}
	}

	// Service overlays from services with compose_overlay set.
	svcNames := make([]string, 0, len(c.Services))
	for name := range c.Services {
		svcNames = append(svcNames, name)
	}
	sort.Strings(svcNames)
	for _, name := range svcNames {
		svc := c.Services[name]
		if svc.Enabled && len(svc.Compose) > 0 {
			files = append(files, svc.Compose...)
		}
	}

	return files
}

// toolOverlayEnabled reports whether the overlay with the given key is active.
func (c *DevboxConfig) toolOverlayEnabled(key string) bool {
	switch key {
	case "adminer":
		return c.Tools.Adminer.Enabled
	case "redis_insight":
		return c.Tools.RedisInsight.Enabled
	case "mailpit":
		return c.Tools.Mailpit.Enabled
	default:
		return false
	}
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
	Extends         string               `yaml:"extends"`
	DependsOn       []string             `yaml:"depends_on"`
	Compose         []string             `yaml:"compose"`
	CLI             ServiceCLIConfig     `yaml:"cli"`
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
	Env map[string]string `yaml:"env"`
}

// ToolsConfig holds the set of optional development tools.
type ToolsConfig struct {
	Adminer      ToolConfig `yaml:"adminer"`
	RedisInsight ToolConfig `yaml:"redis_insight"`
	Mailpit      ToolConfig `yaml:"mailpit"`
}

// AnyEnabled returns true when at least one tool is enabled.
func (t ToolsConfig) AnyEnabled() bool {
	return t.Adminer.Enabled || t.RedisInsight.Enabled || t.Mailpit.Enabled
}

// ToolConfig holds enabled flag for a single tool.
type ToolConfig struct {
	Enabled bool `yaml:"enabled"`
}

// RuntimeConfig describes ports, hostnames, and other runtime settings.
type RuntimeConfig struct {
	UseHTTPS bool         `yaml:"use_https"`
	Ports    RuntimePorts `yaml:"ports"`
	Hosts    RuntimeHosts `yaml:"hosts"`
	SPX      SPXConfig    `yaml:"spx"`
}

// RuntimePorts maps service roles to host ports.
type RuntimePorts struct {
	App          int `yaml:"app"`
	Db           int `yaml:"db"`
	Redis        int `yaml:"redis"`
	Adminer      int `yaml:"adminer"`
	RedisInsight int `yaml:"redis_insight"`
	Mailpit      int `yaml:"mailpit"`
}

// RuntimeHosts maps service roles to virtual hostnames.
type RuntimeHosts struct {
	Main         string `yaml:"main"`
	Adminer      string `yaml:"adminer"`
	RedisInsight string `yaml:"redis_insight"`
	Mailpit      string `yaml:"mailpit"`
}

// SPXConfig holds SPX profiler settings.
type SPXConfig struct {
	Path string `yaml:"path"`
}

// ExportsConfig groups export targets.
type ExportsConfig struct {
	Env []ExportRule `yaml:"env"`
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

// LoadConfig loads the merged DevboxConfig by layering:
//
//  1. devboxPath (required)
//  2. <dir>/devbox/defaults.yml (optional, versioned project defaults)
//  3. <dir>/devbox/local.yml   (optional, local overrides, gitignored)
//
// Later layers win on conflict; maps are merged recursively.
// The merged raw map is stored in DevboxConfig.Raw for dot-path resolution.
func LoadConfig(devboxPath string) (*DevboxConfig, error) {
	baseDir := filepath.Dir(devboxPath)
	merged := make(map[string]any)

	// Layer 1: devbox.yml (required)
	base, err := loadRawYAML(devboxPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", devboxPath, err)
	}
	deepMerge(merged, base)

	// Layer 2: devbox/defaults.yml (optional)
	defaultsPath := filepath.Join(baseDir, "devbox", "defaults.yml")
	if defaults, err := loadRawYAML(defaultsPath); err == nil {
		deepMerge(merged, defaults)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", defaultsPath, err)
	}

	// Layer 3: devbox/local.yml (optional)
	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	if local, err := loadRawYAML(localPath); err == nil {
		deepMerge(merged, local)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", localPath, err)
	}

	data, err := yaml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal merged config: %w", err)
	}
	var cfg DevboxConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal merged config: %w", err)
	}
	cfg.Raw = merged
	// Store config path so deploy resolution can find service deploy files.
	cfg.Raw["__configPath"] = devboxPath

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

	return &cfg, nil
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

	// Resolve extends.
	for name, svc := range f.Services {
		if svc.Extends == "" {
			continue
		}
		parent, ok := f.Services[svc.Extends]
		if !ok {
			return nil, fmt.Errorf("service %q extends unknown service %q", name, svc.Extends)
		}
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
		if len(svc.CLI.Env) == 0 && len(parent.CLI.Env) > 0 {
			svc.CLI.Env = parent.CLI.Env
		}
		f.Services[name] = svc
	}

	return f.Services, nil
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

// LoadDeployConfig loads the deploy pipeline from a deploy.yml file.
// The file is loaded standalone — it is not merged with the 3-layer config.
// Returns os.ErrNotExist when the file is absent (callers may treat it as optional).
func LoadDeployConfig(deployPath string) (*DeployConfig, error) {
	return loadPipelineConfig(deployPath, true)
}

// LoadResetConfig loads the reset pipeline from a reset.yml file.
// The file is loaded standalone — it is not merged with the 3-layer config.
// Returns os.ErrNotExist when the file is absent (callers may treat it as optional).
// Reset pipelines must not contain deploy_services phases.
func LoadResetConfig(resetPath string) (*DeployConfig, error) {
	return loadPipelineConfig(resetPath, false)
}

// loadPipelineConfig is the shared loader for deploy and reset pipelines.
// When allowDeployServices is false, deploy_services phases are rejected.
func loadPipelineConfig(path string, allowDeployServices bool) (*DeployConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg DeployConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for pi := range cfg.Phases {
		phase := &cfg.Phases[pi]
		if phase.DeployServices {
			if !allowDeployServices {
				return nil, fmt.Errorf("phase %q: deploy_services is not allowed in this pipeline type", phase.Name)
			}
			if len(phase.Steps) > 0 {
				return nil, fmt.Errorf("phase %q: deploy_services phase must not contain steps", phase.Name)
			}
			continue
		}
		for si := range phase.Steps {
			step := &phase.Steps[si]
			// Normalize deprecated service_configs_copy into builtin form.
			if step.ServiceConfigsCopy != "" {
				if step.Builtin != "" || step.Run != "" || step.Devbox != "" || step.Command != "" {
					return nil, fmt.Errorf("step %q (phase %q): service_configs_copy cannot be combined with other step types", step.Name, phase.Name)
				}
				mode := step.Mode
				if mode == "" {
					mode = "replace"
				}
				if step.With == nil {
					step.With = make(map[string]any)
				}
				step.With["service"] = step.ServiceConfigsCopy
				step.With["mode"] = mode
				step.Builtin = "service_configs_copy"
				step.ServiceConfigsCopy = ""
				step.Mode = ""
			}
			set := 0
			if step.Run != "" {
				set++
			}
			if step.Devbox != "" {
				set++
			}
			if step.Command != "" {
				set++
			}
			if step.Builtin != "" {
				set++
			}
			if set > 1 {
				return nil, fmt.Errorf("step %q (phase %q): only one of run, devbox, command, or builtin may be set", step.Name, phase.Name)
			}
			if set == 0 {
				return nil, fmt.Errorf("step %q (phase %q): exactly one of run, devbox, command, or builtin must be set", step.Name, phase.Name)
			}
		}
	}
	return &cfg, nil
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

// deepMerge merges src into dst in place.
// For keys where both values are maps, it recurses. Otherwise src wins.
func deepMerge(dst, src map[string]any) {
	for k, sv := range src {
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
