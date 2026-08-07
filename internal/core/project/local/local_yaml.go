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

// WriteLocalYAML writes the local config map atomically using write-temp + rename.
// Ensures the parent directory exists with mode 0o755 and the file is written with mode 0o600.
//
// This is a thin compatibility wrapper over the comment-preserving node writer:
// it materializes the map as an overlay onto a fresh empty document and routes
// through ApplyOverlayToNode / WriteLocalYAMLNode, so there is a SINGLE on-disk
// write path. No production code calls it anymore — all write callers (the
// `services` toggle and the setup wizard) build an overlay and apply it onto a
// LOADED node so comments/formatting survive. WriteLocalYAML is retained only
// for tests and any caller that genuinely has nothing to preserve (a brand-new
// file from a plain map); it still drops nothing because the source map has no
// comments to begin with.
func WriteLocalYAML(localPath string, local map[string]any) error {
	doc := emptyMappingDoc()
	if err := ApplyOverlayToNode(doc, local); err != nil {
		return fmt.Errorf("marshal local config: %w", err)
	}
	return WriteLocalYAMLNode(localPath, doc)
}

// writeFileAtomic writes data to localPath atomically via write-temp + rename.
// Ensures the parent directory exists with mode 0o755 and the file ends up with
// mode 0o600. Shared by WriteLocalYAML (map-based) and WriteLocalYAMLNode
// (node-based).
func writeFileAtomic(localPath string, data []byte) error {
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
