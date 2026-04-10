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

	// Raw holds the merged config as a plain map, used for dot-path resolution
	// in export rules. Populated only by LoadConfig; not serialized.
	Raw map[string]any `yaml:"-"`
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
	Type string `yaml:"type"`
	Dir  string `yaml:"dir"`
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
