package snapshot

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ReadCurrent returns the name stored in the current-pointer file, or "" if
// the pointer is absent or empty. Any other I/O error is returned wrapped.
func ReadCurrent(baseDir string) (string, error) {
	data, err := os.ReadFile(CurrentPointer(baseDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read current pointer: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// WriteCurrent atomically writes name to the current-pointer file. The name
// is not validated here — callers must use ValidateName first.
func WriteCurrent(baseDir, name string) error {
	if err := os.MkdirAll(StateDir(baseDir), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	return writeFileAtomic(CurrentPointer(baseDir), []byte(name+"\n"), 0o644)
}

// ClearCurrent removes the current-pointer file. Missing file is a no-op.
func ClearCurrent(baseDir string) error {
	if err := os.Remove(CurrentPointer(baseDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear current pointer: %w", err)
	}
	return nil
}
