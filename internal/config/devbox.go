// Package config provides loading and validation of devbox configuration files.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/condition"

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
	IDE           IDEConfig      `yaml:"ide"`
	Deploy        DeployConfig   `yaml:"-"`
	Binaries      BinariesConfig `yaml:"binaries"`

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
//
// Log enables/disables file logging at logs/<pipeline>.log for the pipeline run.
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
//   - ContinueOnError — when true, a failed step or check is reported but pipeline continues
type DeployStep struct {
	Name            string               `yaml:"name"`
	Type            string               `yaml:"type"`
	Cmd             string               `yaml:"cmd"`
	With            map[string]any       `yaml:"with,omitempty"`
	Description     string               `yaml:"description,omitempty"`
	When            *condition.Condition `yaml:"when,omitempty"`
	Check           *Action              `yaml:"check,omitempty"`
	ContinueOnError bool                 `yaml:"continue_on_error,omitempty"`
}

// Action returns the action-shaped representation of this step for ExecAction callers.
func (s DeployStep) Action() Action {
	return Action{Type: s.Type, Cmd: s.Cmd, With: s.With}
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
	Dirs            []string             `yaml:"dirs"`
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
			continue
		}
		for si := range phase.Steps {
			step := &phase.Steps[si]
			// Validate step body: exactly one of the four types with non-empty cmd.
			if step.Type == "" {
				return fmt.Errorf("step %q (phase %q): type is required", step.Name, phase.Name)
			}
			if step.Cmd == "" {
				return fmt.Errorf("step %q (phase %q): cmd is required", step.Name, phase.Name)
			}
			switch step.Type {
			case "shell", "devbox":
				// shell and devbox do not accept with
				if len(step.With) > 0 {
					return fmt.Errorf("step %q (phase %q): type %q does not accept with", step.Name, phase.Name, step.Type)
				}
			case "command", "builtin":
				// command and builtin may accept with (optional)
			default:
				return fmt.Errorf("step %q (phase %q): unknown type %q", step.Name, phase.Name, step.Type)
			}
			// Validate check if present.
			if step.Check != nil {
				if err := step.Check.Validate(); err != nil {
					return fmt.Errorf("step %q (phase %q) check: %w", step.Name, phase.Name, err)
				}
			}
			// Validate when condition if present.
			if step.When != nil {
				if err := step.When.Validate(); err != nil {
					return fmt.Errorf("step %q (phase %q) when: %w", step.Name, phase.Name, err)
				}
			}
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
