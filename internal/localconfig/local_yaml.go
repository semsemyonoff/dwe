package localconfig

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadLocalYAML reads and parses devbox/local.yml. A missing file is not an error.
func LoadLocalYAML(localPath string) (map[string]any, error) {
	local := make(map[string]any)
	data, err := os.ReadFile(localPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return local, nil
		}
		return nil, fmt.Errorf("read %s: %w", localPath, err)
	}
	if err := yaml.Unmarshal(data, &local); err != nil {
		return nil, fmt.Errorf("parse %s: %w", localPath, err)
	}
	if local == nil {
		local = make(map[string]any)
	}
	return local, nil
}

// WriteLocalYAML marshals and writes the local config map.
func WriteLocalYAML(localPath string, local map[string]any) error {
	data, err := yaml.Marshal(local)
	if err != nil {
		return fmt.Errorf("marshal local config: %w", err)
	}
	if err := os.WriteFile(localPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", localPath, err)
	}
	return nil
}

// SetLocalEntryEnabled sets the "enabled" field on the named entry within a
// services/tools subtree, creating the entry map if absent.
func SetLocalEntryEnabled(subtree map[string]any, name string, enabled bool) {
	entry, ok := subtree[name].(map[string]any)
	if !ok {
		entry = make(map[string]any)
		subtree[name] = entry
	}
	entry["enabled"] = enabled
}
