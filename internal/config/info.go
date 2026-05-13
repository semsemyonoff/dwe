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
	// HideOnEmpty, when true, the section (including its title) is omitted if no item survives when: filtering.
	// Default false preserves legacy rendering.
	HideOnEmpty bool `yaml:"hide_on_empty"`
}

// InfoIndent represents an optional indent value for an InfoItem.
// It distinguishes between "not set" (use default) and an explicit non-negative int.
type InfoIndent struct {
	set   bool
	value int
}

// IsSet reports whether the indent was explicitly provided in YAML.
func (h InfoIndent) IsSet() bool { return h.set }

// Value returns the explicit indent value, or 0 if not set.
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
// Five types are valid: info, warning, definition, separator, subgroup.
// Type-specific fields:
//   - info, warning: Text (message body), Indent, Icon, When.
//   - definition: Name, Value, Indent, Icon, When.
//   - separator: When.
//   - subgroup: Title (header text), Items (children), When, HideOnEmpty, Decorative.
// The Title field (for subgroups) is distinct from Text (for info/warning).
// All types support the Decorative flag to override the type's default visibility.
type InfoItem struct {
	// Type selects the rendering function: info, warning, definition, separator, subgroup.
	Type string `yaml:"type"`

	// Text is the content for: warning, info.
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

	// Title is the subgroup header text (subgroup items only).
	// Mirrors InfoSection.Title and supports Go template expressions.
	// Empty means the subgroup has no header.
	Title string `yaml:"title,omitempty"`

	// Items are the children of a subgroup (subgroup items only).
	// Subgroups nest recursively.
	Items []InfoItem `yaml:"items,omitempty"`

	// HideOnEmpty controls whether a subgroup (and its title) is omitted
	// when no item survives when: filtering. Defaults to true for subgroups.
	HideOnEmpty *bool `yaml:"hide_on_empty,omitempty"`

	// Decorative indicates whether this item counts as content for its parent's
	// hide_on_empty check. When nil, defaults are: separator → true, all others → false.
	// An explicit override applies to any type.
	Decorative *bool `yaml:"decorative,omitempty"`
}

// IsDecorative returns whether this item is decorative (does not count as content).
// When Decorative is nil, the type default is used: separator items are decorative,
// all others are not. When Decorative is set, the explicit value is returned.
func (i InfoItem) IsDecorative() bool {
	if i.Decorative != nil {
		return *i.Decorative
	}
	return i.Type == "separator"
}

// SubgroupHideOnEmpty returns whether a subgroup should be hidden when empty.
// Defaults to true if HideOnEmpty is nil (subgroup default).
func (i InfoItem) SubgroupHideOnEmpty() bool {
	if i.HideOnEmpty != nil {
		return *i.HideOnEmpty
	}
	return true
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
	if err := ValidateInfoConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ValidateInfoConfig checks that all item types are valid and subgroups declare items.
// It walks sections and subgroup children recursively, building error messages with
// paths like "section[tools].items[0]" for easy YAML location.
func ValidateInfoConfig(cfg *InfoConfig) error {
	for i, section := range cfg.Sections {
		secPath := sectionPath(i, section.ID)
		if err := validateItems(section.Items, secPath); err != nil {
			return err
		}
	}
	return nil
}

func validateItems(items []InfoItem, pathPrefix string) error {
	validTypes := map[string]bool{
		"info":       true,
		"warning":    true,
		"definition": true,
		"separator":  true,
		"subgroup":   true,
	}

	for i, item := range items {
		itemPath := fmt.Sprintf("%s.items[%d]", pathPrefix, i)

		// Validate type is known
		if !validTypes[item.Type] {
			return fmt.Errorf("info: %s: unknown type %q; valid types: info, warning, definition, separator, subgroup", itemPath, item.Type)
		}

		// Subgroup-specific validation
		if item.Type == "subgroup" {
			if len(item.Items) == 0 {
				return fmt.Errorf("info: %s: subgroup must declare items", itemPath)
			}
			// Recurse into subgroup children
			if err := validateItems(item.Items, itemPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func sectionPath(index int, id string) string {
	if id != "" {
		return fmt.Sprintf("section[%s]", id)
	}
	return fmt.Sprintf("section[%d]", index)
}
