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
// It is a thin compatibility wrapper over the comment-preserving node writer: the
// map is applied as an overlay onto a FRESH empty document and routed through
// ApplyOverlayToNode / WriteLocalYAMLNode, so there is a SINGLE on-disk write
// path. Use it only when there is nothing to preserve (a brand-new file built
// from a plain map); a caller editing an existing local.yml must build an overlay
// and apply it onto a LOADED node instead, or comments and formatting are lost.
func WriteLocalYAML(localPath string, local map[string]any) error {
	doc := emptyMappingDoc()
	if err := ApplyOverlayToNode(doc, local); err != nil {
		return fmt.Errorf("marshal local config: %w", err)
	}
	return WriteLocalYAMLNode(localPath, doc)
}

// WritePolicy decides the on-disk mode of a node write. The two modes are
// explicit on purpose: local.yml is gitignored developer state and is FORCED to
// 0o600 (it may hold credentials), while the tracked layer files (workspace.yml,
// workspace/defaults.yml) keep whatever mode the repo gave them — forcing 0o600
// on a tracked file would surprise git and editors.
type WritePolicy struct {
	mode  os.FileMode
	force bool
}

// ForceMode always writes the target with mode.
func ForceMode(mode os.FileMode) WritePolicy {
	return WritePolicy{mode: mode, force: true}
}

// PreserveOrDefault keeps an existing target's mode and uses mode for a file
// that does not exist yet.
func PreserveOrDefault(mode os.FileMode) WritePolicy {
	return WritePolicy{mode: mode}
}

// resolve returns the mode to apply to the temp file before the rename.
func (p WritePolicy) resolve(path string) os.FileMode {
	if p.force {
		return p.mode
	}
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return info.Mode().Perm()
	}
	return p.mode
}

// writeFileAtomic writes data to localPath atomically via write-temp + rename.
// Ensures the parent directory exists with mode 0o755 and the file ends up with
// the mode the policy resolves to. Shared by WriteLocalYAML (map-based) and
// WriteYAMLNode (node-based).
func writeFileAtomic(localPath string, data []byte, policy WritePolicy) error {
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", localPath, err)
	}

	// Write to temp file. The suffix stays off `.yml` so a leftover temp file
	// from a crashed write is never picked up by a config-file glob.
	tmpFile, err := os.CreateTemp(dir, ".dwe-yaml-*.tmp")
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
	if err := os.Chmod(tmpPath, policy.resolve(localPath)); err != nil {
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
