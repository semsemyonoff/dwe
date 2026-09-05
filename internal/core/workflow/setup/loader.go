package setup

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/semsemyonoff/dwe/internal/shared/yamlstrict"
)

// LoadSetupYAML reads and parses workspace/setup.yml with strict field decoding.
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

	if len(data) == 0 {
		return &Config{}, nil
	}

	cfg := &Config{}
	// yamlstrict names the file itself, so no "load %s:" prefix on that error;
	// io.EOF (an all-comment file) passes through bare and keeps its old wrap.
	if err := yamlstrict.Decode(data, cfg, path); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("load %s: %w", path, err)
		}
		return nil, err
	}

	return cfg, nil
}
