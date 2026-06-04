package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// StylesConfig is the top-level structure of workspace/styles.yml.
// It controls colors, the ASCII header, and separator character for all UI output.
type StylesConfig struct {
	Header    StylesHeader `yaml:"header"`
	Colors    StylesColors `yaml:"colors"`
	Separator string       `yaml:"separator"`
}

// StylesHeader describes the branded header rendered at startup.
type StylesHeader struct {
	Lines   []string `yaml:"lines"`
	Font    string   `yaml:"font"`
	Tagline string   `yaml:"tagline"`
}

// StylesColors holds the 7 semantic color tokens that drive every UI surface.
// Values are raw hex strings (e.g. "#2EC3EB"). Empty string means "use the
// built-in light/dark default for this token" — defaults are resolved once at
// ApplyStyles time via lipgloss.HasDarkBackground().
type StylesColors struct {
	Accent  string `yaml:"accent"`
	Success string `yaml:"success"`
	Warning string `yaml:"warning"`
	Danger  string `yaml:"danger"`
	Muted   string `yaml:"muted"`
	Border  string `yaml:"border"`
	Text    string `yaml:"text"`
}

// LoadStylesConfig reads and parses a styles.yml file at the given path.
// If the file does not exist, it returns an empty StylesConfig (defaults apply).
func LoadStylesConfig(path string) (*StylesConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
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
