package setup

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadSetupYAML reads and parses devbox/setup.yml with strict field decoding.
// A missing file is not an error and returns (nil, nil).
// Unknown YAML fields cause a parse error.
// Unknown Type values are permitted here (caught by validators).
func LoadSetupYAML(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("load %s: %w", path, err)
	}

	// Handle empty file case
	if len(data) == 0 {
		return &Config{}, nil
	}

	cfg := &Config{}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}

	return cfg, nil
}
