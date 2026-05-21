package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// StylesConfig is the top-level structure of devbox/styles.yml.
// It controls colors, the ASCII header, and separator character for all UI output.
type StylesConfig struct {
	Header    StylesHeader `yaml:"header"`
	Colors    StylesColors `yaml:"colors"`
	Separator string       `yaml:"separator"`
}

// StylesHeader describes the ASCII art banner rendered at startup.
type StylesHeader struct {
	Lines []string `yaml:"lines"`
	Font  string   `yaml:"font"`
	Color string   `yaml:"color"`
}

// StylesColors holds ANSI 256-color codes (as strings) for all UI elements.
// Empty string means "use default hardcoded value".
type StylesColors struct {
	// Info dashboard
	Label        string `yaml:"label"`
	SectionTitle string `yaml:"section_title"`
	SubHeader    string `yaml:"subheader"`
	Muted        string `yaml:"muted"`
	Warning      string `yaml:"warning"`
	Info         string `yaml:"info"`

	// Semantic status colors (used in tables/status)
	Enabled   string `yaml:"enabled"`
	Disabled  string `yaml:"disabled"`
	Mandatory string `yaml:"mandatory"`
	Partial   string `yaml:"partial"`

	// Table colors (shared across all Lipgloss tables)
	TableBorder string `yaml:"table_border"`
	TableHeader string `yaml:"table_header"`

	// Command browser (cmdbrowser TUI) — consumed via ui.Color*() string accessors.
	FocusBorder        string `yaml:"focus_border"`
	Description        string `yaml:"description"`
	TreeCount          string `yaml:"tree_count"`
	TreeArrow          string `yaml:"tree_arrow"`
	FilterMatch        string `yaml:"filter_match"`
	PaginationActive   string `yaml:"pagination_active"`
	PaginationInactive string `yaml:"pagination_inactive"`

	// Help/Fang color scheme (CLI help output)
	Help StylesHelpColors `yaml:"help"`
}

// StylesHelpColors holds ANSI 256-color codes for CLI help output elements
// rendered by Fang. Empty string means "use Fang default".
type StylesHelpColors struct {
	Title       string `yaml:"title"`       // section headers (USAGE, COMMANDS, etc.)
	Command     string `yaml:"command"`     // command names
	Flag        string `yaml:"flag"`        // flag names
	Program     string `yaml:"program"`     // program name in usage line
	Description string `yaml:"description"` // command/flag descriptions
	Argument    string `yaml:"argument"`    // arguments like [command]
}

// LoadStylesConfig reads and parses a styles.yml file at the given path.
// If the file does not exist, it returns an empty StylesConfig (defaults apply).
func LoadStylesConfig(path string) (*StylesConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &StylesConfig{}, nil
		}
		return &StylesConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg StylesConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return &StylesConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}
