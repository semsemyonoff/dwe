// Package config provides loading and validation of devbox configuration files.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DevboxConfig is the merged top-level devbox configuration.
// It is produced by layering devbox.yml → devbox/defaults.yml → devbox/local.yml.
type DevboxConfig struct {
	SchemaVersion string                   `yaml:"schema_version"`
	Project       ProjectConfig            `yaml:"project"`
	Services      map[string]ServiceConfig `yaml:"services"`
	Tools         ToolsConfig              `yaml:"tools"`
	Runtime       RuntimeConfig            `yaml:"runtime"`
	State         string                   `yaml:"state"`
	Exports       ExportsConfig            `yaml:"exports"`
	Compose       ComposeConfig            `yaml:"compose"`
	IDE           IDEConfig                `yaml:"ide"`
	Deploy        DeployConfig             `yaml:"-"`

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
type DeployPhase struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Steps       []DeployStep `yaml:"steps"`
}

// DeployStep is a single atomic deploy action.
// Exactly one of Cmd or Make must be set.
//
//   - Cmd  — shell command executed directly via os/exec
//   - Make — Make target executed via `make <target>`
//   - When — skip condition; three expression kinds are supported:
//     1. Go template (contains "{{") — evaluated against DevboxConfig at plan-resolution time;
//     step is excluded from the plan when the rendered result is falsy ("", "false", "0").
//     2. Builtin predicate — evaluated at step-execution time:
//     "dir-exists <path>", "dir-missing <path>",
//     "dir-empty <path>", "dir-not-empty <path>",
//     "file-exists <path>", "file-missing <path>".
//     3. Shell command prefixed "cmd: <command>" — evaluated at step-execution time;
//     exits 0 → true (run step), non-zero → false (skip step).
type DeployStep struct {
	Name        string `yaml:"name"`
	Cmd         string `yaml:"cmd"`
	Make        string `yaml:"make"`
	Description string `yaml:"description"`
	When        string `yaml:"when"`
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

// ServiceConfig describes a single service (app hub).
type ServiceConfig struct {
	Type           string              `yaml:"type"`
	Dir            string              `yaml:"dir"`
	Container      string              `yaml:"container"`
	DirInternal    string              `yaml:"dir_internal"`
	Configs        []ServiceConfigFile `yaml:"configs"`
	InstallerImage string              `yaml:"installer_image"`
}

// ServiceConfigFile declares a template config to copy into a service directory during deploy.
//
// Fields:
//   - Src  — source template path (relative to project root, e.g. configs/app/main/.env)
//   - Dest — destination filename in services/<name>/configs/ (e.g. .env)
//   - Mode — copy mode: "default" (skip if exists), "update" (merge new keys), "replace" (overwrite)
type ServiceConfigFile struct {
	Src  string `yaml:"src"`
	Dest string `yaml:"dest"`
	Mode string `yaml:"mode"`
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
	Debug    DebugConfig  `yaml:"debug"`
}

// DebugConfig holds debug-mode settings (e.g. Xdebug container overlay).
type DebugConfig struct {
	Enabled bool `yaml:"enabled"`
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

	// Load devbox/deploy.yml separately (not merged with config layers).
	deployPath := filepath.Join(baseDir, "devbox", "deploy.yml")
	if deployCfg, err := LoadDeployConfig(deployPath); err == nil {
		cfg.Deploy = *deployCfg
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", deployPath, err)
	}

	return &cfg, nil
}

// LoadDeployConfig loads the deploy pipeline from a deploy.yml file.
// The file is loaded standalone — it is not merged with the 3-layer config.
// Returns os.ErrNotExist when the file is absent (callers may treat it as optional).
func LoadDeployConfig(deployPath string) (*DeployConfig, error) {
	data, err := os.ReadFile(deployPath)
	if err != nil {
		return nil, err
	}
	var cfg DeployConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", deployPath, err)
	}
	for _, phase := range cfg.Phases {
		for _, step := range phase.Steps {
			if step.Cmd != "" && step.Make != "" {
				return nil, fmt.Errorf("deploy step %q (phase %q): only one of cmd or make may be set", step.Name, phase.Name)
			}
			if step.Cmd == "" && step.Make == "" {
				return nil, fmt.Errorf("deploy step %q (phase %q): exactly one of cmd or make must be set", step.Name, phase.Name)
			}
		}
	}
	return &cfg, nil
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
