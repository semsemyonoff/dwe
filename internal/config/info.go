package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// InfoConfig is the top-level structure of devbox/info.yml.
// It describes what to render when showing project info/help.
type InfoConfig struct {
	Settings InfoSettings  `yaml:"settings"`
	Sections []InfoSection `yaml:"sections"`
	// Footer controls whether a closing table header line is printed after all sections.
	Footer bool `yaml:"footer"`
}

// InfoSettings controls global display parameters for the info output.
type InfoSettings struct {
	// LineWidth is the inner width (in characters) used for table header padding
	// and text truncation. Default (0) falls back to 76.
	LineWidth int `yaml:"line_width"`
}

// InfoSection is a named block of info content, optionally enclosed by a table header.
type InfoSection struct {
	// ID is a machine-readable section identifier (used for referencing/overriding).
	ID string `yaml:"id"`
	// Title, when non-empty, prints a TableHeader line before the section items.
	Title string `yaml:"title"`
	// Items is the ordered list of content entries.
	Items []InfoItem `yaml:"items"`
}

// InfoIndent represents an optional indent value for an InfoItem.
// It distinguishes between "not set" (use default), an explicit int (use that),
// and false (force zero indent).
type InfoIndent struct {
	set   bool
	value int
}

// IsSet reports whether the indent was explicitly provided in YAML.
func (h InfoIndent) IsSet() bool { return h.set }

// Value returns the explicit indent value (0 when indent: false).
func (h InfoIndent) Value() int { return h.value }

// UnmarshalYAML accepts an integer value (indent: 0, indent: 4, etc.).
// Any other type returns an error.
func (h *InfoIndent) UnmarshalYAML(node *yaml.Node) error {
	if node.Tag != "!!int" {
		return fmt.Errorf("indent: expected int, got %s", node.Tag)
	}
	var n int
	if err := node.Decode(&n); err != nil {
		return err
	}
	if n < 0 {
		return fmt.Errorf("indent: negative value %d is not allowed", n)
	}
	h.value = n
	h.set = true
	return nil
}

// InfoItem is a single renderable element within a section.
type InfoItem struct {
	// Type selects the rendering function:
	//   subheader  — yellow "---Title---" line
	//   definition — "  name — value" definition line
	//   warning    — yellow warning message
	//   info       — blue info message
	//   separator  — blank line
	Type string `yaml:"type"`

	// Text is the content for: subheader, warning, info.
	// Supports Go template expressions evaluated against DevboxConfig.
	Text string `yaml:"text"`

	// Name is the left-hand label for definition items.
	Name string `yaml:"name"`

	// Value is the right-hand content for definition items.
	// Supports Go template expressions evaluated against DevboxConfig.
	Value string `yaml:"value"`

	// Indent controls leading whitespace for definition and info items (number of spaces).
	// When omitted, renderItem applies a default of 2 for definition items.
	Indent InfoIndent `yaml:"indent"`

	// Icon is an optional symbol (emoji or character) displayed before the name
	// in definition items. Empty means no icon.
	Icon string `yaml:"icon"`

	// When is an optional Go template expression evaluated against DevboxConfig.
	// The item is shown only when the rendered result is truthy
	// (non-empty, not "false", not "0").
	// Empty When always shows the item.
	When string `yaml:"when"`
}

// LoadInfoConfig reads and parses an info.yml file at the given path.
func LoadInfoConfig(path string) (*InfoConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg InfoConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}
