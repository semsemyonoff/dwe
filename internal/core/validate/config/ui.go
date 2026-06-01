package config

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/validate"

	"gopkg.in/yaml.v3"
)

// uiCommandsKnownKeys lists the YAML keys recognised under ui.commands.
// Unknown keys are surfaced as warnings by the validator since the
// devbox.yml loader is lenient and silently ignores them.
var uiCommandsKnownKeys = map[string]bool{
	"default_expanded_depth": true,
	"auto_collapse_empty":    true,
	"show_type_badges":       true,
}

type uiValidator struct{}

func (v *uiValidator) ID() string     { return "ui" }
func (v *uiValidator) Domain() string { return "config" }

func (v *uiValidator) Run(ctx validate.Context) []validate.Diagnostic {
	configPath := ctx.ConfigPath
	if configPath == "" {
		configPath = filepath.Join(ctx.ProjectRoot, "workspace.yml")
	}
	file := relPath(ctx.ProjectRoot, configPath)

	data, err := os.ReadFile(configPath)
	if err != nil {
		// devboxValidator already surfaces missing/invalid devbox.yml — stay silent.
		return nil
	}
	var top struct {
		UI yaml.Node `yaml:"ui"`
	}
	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil
	}
	if top.UI.Kind == 0 {
		// Block absent — nothing to validate.
		return nil
	}
	if top.UI.Kind != yaml.MappingNode {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.ui",
			File:     file,
			Line:     top.UI.Line,
			Message:  "ui: must be a YAML mapping",
			Hint:     "Expected:\n  ui:\n    commands:\n      ...",
		}}
	}

	var diags []validate.Diagnostic
	// Warn on unknown keys directly under ui: (e.g. `command` instead of `commands`).
	for i := 0; i+1 < len(top.UI.Content); i += 2 {
		k := top.UI.Content[i].Value
		if k != "commands" {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "config",
				Target:   "config.ui",
				File:     file,
				Line:     top.UI.Content[i].Line,
				Message:  "unknown key under ui: " + k,
				Hint:     "The only supported key under ui: is `commands:`.",
			})
		}
	}

	// Find ui.commands sub-node. Check for a non-mapping scalar before
	// calling findMappingChild, which silently returns nil for non-mappings.
	if raw := rawChild(&top.UI, "commands"); raw != nil && raw.Kind != yaml.MappingNode {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.ui",
			File:     file,
			Line:     raw.Line,
			Message:  "ui.commands must be a YAML mapping",
			Hint:     "Expected:\n  ui:\n    commands:\n      default_expanded_depth: 3",
		})
		return diags
	}
	commands := findMappingChild(&top.UI, "commands")
	if commands == nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "config",
			Target:   "config.ui",
			File:     file,
		})
		return diags
	}

	type unknownEntry struct {
		name string
		line int
	}
	var unknown []unknownEntry
	for i := 0; i+1 < len(commands.Content); i += 2 {
		keyNode := commands.Content[i]
		valNode := commands.Content[i+1]
		key := keyNode.Value
		if !uiCommandsKnownKeys[key] {
			unknown = append(unknown, unknownEntry{name: key, line: keyNode.Line})
			continue
		}
		switch key {
		case "default_expanded_depth":
			if valNode.Kind == yaml.ScalarNode {
				if d, err := strconv.Atoi(valNode.Value); err != nil || d < 0 {
					diags = append(diags, validate.Diagnostic{
						Severity: validate.SeverityError,
						Domain:   "config",
						Target:   "config.ui",
						File:     file,
						Line:     valNode.Line,
						Message:  "ui.commands.default_expanded_depth must be >= 0, got " + valNode.Value,
						Hint:     "Use 0 for all-collapsed or a positive integer to expand to that depth (e.g. 1 expands only top-level groups). Omit the key to use the default depth of 1.",
					})
				}
			} else {
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "config",
					Target:   "config.ui",
					File:     file,
					Line:     valNode.Line,
					Message:  "ui.commands.default_expanded_depth must be an integer",
					Hint:     "Use 0 for all-collapsed or a positive integer to expand to that depth (e.g. 1 expands only top-level groups). Omit the key to use the default depth of 1.",
				})
			}
		case "auto_collapse_empty", "show_type_badges":
			valid := false
			if valNode.Kind == yaml.ScalarNode {
				switch strings.ToLower(valNode.Value) {
				case "true", "false", "yes", "no", "on", "off":
					valid = true
				}
			}
			if !valid {
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "config",
					Target:   "config.ui",
					File:     file,
					Line:     valNode.Line,
					Message:  "ui.commands." + key + " must be a boolean (true or false), got " + valNode.Value,
					Hint:     "Use `true` or `false`.",
				})
			}
		}
	}
	if len(unknown) > 0 {
		sort.Slice(unknown, func(i, j int) bool { return unknown[i].name < unknown[j].name })
		for _, u := range unknown {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "config",
				Target:   "config.ui",
				File:     file,
				Line:     u.line,
				Message:  "unknown key under ui.commands: " + u.name,
				Hint:     "Known keys: default_expanded_depth, auto_collapse_empty, show_type_badges.",
			})
		}
	}
	if len(diags) == 0 {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "config",
			Target:   "config.ui",
			File:     file,
		})
	}
	return diags
}

// findMappingChild returns the mapping node at key under parent, or nil.
// If the key exists but its value is not a mapping, nil is returned.
// Use rawChild to detect and report non-mapping values before calling this.
func findMappingChild(parent *yaml.Node, key string) *yaml.Node {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key && parent.Content[i+1].Kind == yaml.MappingNode {
			return parent.Content[i+1]
		}
	}
	return nil
}

// rawChild returns the value node for key under parent regardless of its kind,
// or nil if the key is absent. Used to detect non-mapping values that
// findMappingChild would silently treat as absent.
func rawChild(parent *yaml.Node, key string) *yaml.Node {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}
