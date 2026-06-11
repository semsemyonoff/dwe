package local

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadLocalYAML reads and parses workspace/local.yml. A missing file is not an error.
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

// WriteLocalYAML marshals and writes the local config map atomically using write-temp + rename.
// Ensures the parent directory exists with mode 0o755 and the file is written with mode 0o600.
//
// This is the legacy map-based entry point: marshalling a map[string]any drops
// any comments/formatting that were in the source local.yml and emits keys in
// non-deterministic order. New writers should route through the comment-
// preserving node writer (LoadLocalYAMLNode / ApplyOverlayToNode /
// WriteLocalYAMLNode) in local_node.go. WriteLocalYAML is retained only until
// the remaining callers (services toggle, setup wizard) are migrated to the
// node writer in Task 2, after which it can be removed. Both writers share the
// same atomic-write helper so on-disk semantics (temp+rename, 0o755 dir, 0o600
// file) are identical.
func WriteLocalYAML(localPath string, local map[string]any) error {
	data, err := yaml.Marshal(local)
	if err != nil {
		return fmt.Errorf("marshal local config: %w", err)
	}
	return writeFileAtomic(localPath, data)
}

// writeFileAtomic writes data to localPath atomically via write-temp + rename.
// Ensures the parent directory exists with mode 0o755 and the file ends up with
// mode 0o600. Shared by WriteLocalYAML (map-based) and WriteLocalYAMLNode
// (node-based).
func writeFileAtomic(localPath string, data []byte) error {
	// Ensure parent directory exists
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", localPath, err)
	}

	// Write to temp file
	tmpFile, err := os.CreateTemp(dir, ".local-*.yml")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath) // Clean up on error
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// Set file permissions
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("set file permissions: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, localPath); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
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
