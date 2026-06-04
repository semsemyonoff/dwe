package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

// File and directory permissions for scaffolded output. Generated files are
// plain config/source (0644); created directories are 0755.
const (
	filePerm = 0o644
	dirPerm  = 0o755
)

// writeFile writes data to path atomically (temp-file + rename), creating any
// missing parent directories. It returns written=false (and no error) when the
// file already exists and force is not set — the fill-gaps idempotency contract.
//
// The atomic write means a failure mid-write never leaves a partially written
// file at the destination: the temp file is removed on any error and the
// destination only ever sees the complete content via rename.
func writeFile(path string, data []byte, force bool) (written bool, err error) {
	if !force {
		if _, statErr := os.Stat(path); statErr == nil {
			return false, nil
		} else if !os.IsNotExist(statErr) {
			return false, fmt.Errorf("scaffold: stat %s: %w", path, statErr)
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return false, fmt.Errorf("scaffold: create dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".scaffold-*.tmp")
	if err != nil {
		return false, fmt.Errorf("scaffold: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return false, fmt.Errorf("scaffold: write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return false, fmt.Errorf("scaffold: close temp file for %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, filePerm); err != nil {
		_ = os.Remove(tmpName)
		return false, fmt.Errorf("scaffold: chmod %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return false, fmt.Errorf("scaffold: rename into place %s: %w", path, err)
	}
	return true, nil
}
