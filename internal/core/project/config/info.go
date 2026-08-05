package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// InfoConfig is the top-level structure of workspace/info.yml.
// It describes what to render when showing project info/help.
type InfoConfig struct {
	Sections []InfoSection `yaml:"sections"`
	// Footer controls whether a closing table header line is printed after all sections.
	Footer bool `yaml:"footer"`
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

// AutoURLsSpec holds configuration for auto-urls block type.
type AutoURLsSpec struct {
	// Include specifies which service types to include (app, tool, infra).
	// Default: [app, tool].
	Include []string `yaml:"include,omitempty"`
	// Hide specifies service folder keys to exclude entirely.
	Hide []string `yaml:"hide,omitempty"`
	// HidePaths maps service folder keys to lists of path names to exclude.
	HidePaths map[string][]string `yaml:"hide_paths,omitempty"`
	// PortVia specifies the front proxy service name for URL assembly.
	// When empty, auto-detected as the single infra service with ports.http==80 or ports.https==443.
	PortVia string `yaml:"port_via,omitempty"`
}

// AutoHostsSpec holds configuration for auto-hosts block type.
type AutoHostsSpec struct {
	// Include specifies which service types to include (app, tool, infra).
	// Default: [app, tool, infra].
	Include []string `yaml:"include,omitempty"`
	// IP specifies the IP address for the hosts list.
	// Default: 127.0.0.1.
	IP string `yaml:"ip,omitempty"`
	// Hide specifies service folder keys to exclude entirely.
	Hide []string `yaml:"hide,omitempty"`
}

// InfoItem is a single renderable element within a section.
// Types are valid: info, warning, definition, separator, subgroup, auto-urls, auto-hosts.
// Type-specific fields:
//   - info: Text (message body), Indent, When.
//   - warning: Text (message body), When.
//   - definition: Name, Value, Indent, Icon, When.
//   - separator: When.
//   - subgroup: Title (header text), Items (children), When, HideOnEmpty, Decorative.
//   - auto-urls: SourceAutoURLsSpec (via UnmarshalYAML), When.
//   - auto-hosts: SourceAutoHostsSpec (via UnmarshalYAML), When.
//
// The Title field (for subgroups) is distinct from Text (for info/warning).
// All types support the Decorative flag to override the type's default visibility.
type InfoItem struct {
	// Type selects the rendering function: info, warning, definition, separator, subgroup.
	Type string `yaml:"type"`

	// Text is the content for: warning, info.
	// Supports Go template expressions evaluated against DweConfig.
	Text string `yaml:"text"`

	// Name is the left-hand label for definition items.
	Name string `yaml:"name"`

	// Value is the right-hand content for definition items.
	// Supports Go template expressions evaluated against DweConfig.
	Value string `yaml:"value"`

	// Indent controls leading whitespace for definition and info items (number of spaces).
	// When omitted, renderItem applies a default of 2 for definition items.
	Indent InfoIndent `yaml:"indent"`

	// Icon is an optional symbol (emoji or character) displayed before the name
	// in definition items. Empty means no icon.
	Icon string `yaml:"icon"`

	// When is an optional Go template expression evaluated against DweConfig.
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

	// SourceAutoURLsSpec is populated by UnmarshalYAML when Type == "auto-urls".
	// Not decoded from YAML directly (yaml:"-"); the custom unmarshaller sets it.
	SourceAutoURLsSpec *AutoURLsSpec `yaml:"-"`

	// SourceAutoHostsSpec is populated by UnmarshalYAML when Type == "auto-hosts".
	// Not decoded from YAML directly (yaml:"-"); the custom unmarshaller sets it.
	SourceAutoHostsSpec *AutoHostsSpec `yaml:"-"`
}

// Compile-time interface check.
var _ yaml.Unmarshaler = (*InfoItem)(nil)

// UnmarshalYAML decodes an InfoItem from YAML, handling dispatch to type-specific specs.
// Uses a type-alias shadow to avoid infinite recursion when decoding flat fields.
func (i *InfoItem) UnmarshalYAML(value *yaml.Node) error {
	// Use type alias to decode flat fields without re-entering this method.
	type alias InfoItem
	var a alias
	if err := value.Decode(&a); err != nil {
		return err
	}
	*i = InfoItem(a)

	// Dispatch on type to populate type-specific Source* pointers.
	switch i.Type {
	case "auto-urls":
		var spec AutoURLsSpec
		if err := value.Decode(&spec); err != nil {
			return err
		}
		i.SourceAutoURLsSpec = &spec
	case "auto-hosts":
		var spec AutoHostsSpec
		if err := value.Decode(&spec); err != nil {
			return err
		}
		i.SourceAutoHostsSpec = &spec
	}
	return nil
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

// DefaultInfoConfig returns a synthesized InfoConfig with built-in URLs and Hosts sections.
// This is used as a fallback when workspace/info.yml is not present.
// Both auto-blocks have their SourceAutoURLsSpec/SourceAutoHostsSpec pointers populated
// directly (since UnmarshalYAML does not run on Go-constructed configs).
func DefaultInfoConfig() *InfoConfig {
	decorativeTrue := true
	return &InfoConfig{
		Sections: []InfoSection{
			{
				ID:          "urls",
				Title:       "URLs",
				HideOnEmpty: true,
				Items: []InfoItem{
					{
						Type:               "auto-urls",
						SourceAutoURLsSpec: &AutoURLsSpec{},
					},
				},
			},
			{
				ID:          "hosts",
				Title:       "Hosts",
				HideOnEmpty: true,
				Items: []InfoItem{
					{
						Type:       "warning",
						Text:       "Please, add these to your /etc/hosts:",
						Decorative: &decorativeTrue,
					},
					{
						Type:                "auto-hosts",
						SourceAutoHostsSpec: &AutoHostsSpec{},
					},
				},
			},
		},
		Footer: true,
	}
}

// LoadInfoConfig reads and parses an info.yml file at the given path.
// If the file does not exist, returns the built-in default InfoConfig.
func LoadInfoConfig(path string) (*InfoConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultInfoConfig(), nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// An all-comment or empty file decodes no top-level keys at all. Treat that
	// exactly like an absent file so the built-in dashboard stays active until
	// the user uncomments — mirrors the io.EOF tolerance of the strict pipeline
	// loaders, but yaml.Unmarshal never returns io.EOF, so probe the decoded key
	// set instead. Checked on a separate probe decode (not "len(cfg.Sections) ==
	// 0") so a deliberate `sections: []` or a file carrying only `footer: true`
	// is not silently overridden.
	var probe map[string]any
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(probe) == 0 {
		return DefaultInfoConfig(), nil
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

var validInfoTypes = map[string]bool{
	"info":       true,
	"warning":    true,
	"definition": true,
	"separator":  true,
	"subgroup":   true,
	"auto-urls":  true,
	"auto-hosts": true,
}

func validateItems(items []InfoItem, pathPrefix string) error {
	for i, item := range items {
		itemPath := fmt.Sprintf("%s.items[%d]", pathPrefix, i)

		// Validate type is known
		if !validInfoTypes[item.Type] {
			return fmt.Errorf("info: %s: unknown type %q; valid types: info, warning, definition, separator, subgroup, auto-urls, auto-hosts", itemPath, item.Type)
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

		// auto-urls validation
		if item.Type == "auto-urls" {
			if item.SourceAutoURLsSpec == nil {
				return fmt.Errorf("info: %s: auto-urls must have spec populated by unmarshaller", itemPath)
			}
			if err := validateAutoURLsSpec(item.SourceAutoURLsSpec, itemPath); err != nil {
				return err
			}
		}

		// auto-hosts validation
		if item.Type == "auto-hosts" {
			if item.SourceAutoHostsSpec == nil {
				return fmt.Errorf("info: %s: auto-hosts must have spec populated by unmarshaller", itemPath)
			}
			if err := validateAutoHostsSpec(item.SourceAutoHostsSpec, itemPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateAutoURLsSpec(spec *AutoURLsSpec, itemPath string) error {
	return validateIncludeValues(spec.Include, itemPath)
}

func validateAutoHostsSpec(spec *AutoHostsSpec, itemPath string) error {
	// IP validation happens in validator with net.ParseIP (happens only when non-empty)
	return validateIncludeValues(spec.Include, itemPath)
}

// validateIncludeValues enforces that every include[] entry of an auto-urls /
// auto-hosts block names a known service type.
func validateIncludeValues(include []string, itemPath string) error {
	for _, inc := range include {
		if inc != "app" && inc != "tool" && inc != "infra" {
			return fmt.Errorf("info: %s: include value %q not in {app, tool, infra}", itemPath, inc)
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
