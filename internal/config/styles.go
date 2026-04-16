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
}

// LoadStylesConfig reads and parses a styles.yml file at the given path.
// If the file does not exist, it returns an empty StylesConfig (defaults apply).
func LoadStylesConfig(path string) (*StylesConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &StylesConfig{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg StylesConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}
