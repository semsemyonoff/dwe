package fs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/core/execution/builtin/spec"
)

// FileExists is a predicate builtin that succeeds when the given path exists
// inside the project root.
type FileExists struct{}

// Validate checks that the required 'path' param is present.
func (FileExists) Validate(with map[string]any) error {
	path := spec.GetStringParam(with, "path", "")
	if path == "" {
		return errors.New("missing required param 'path'")
	}
	return nil
}

// Describe returns a human-readable description for plan display.
func (FileExists) Describe(with map[string]any) string {
	path := spec.GetStringParam(with, "path", "")
	return fmt.Sprintf("builtin: file_exists(path=%s)", path)
}

// Run stats the requested path inside the project root and returns an error
// when the file is missing.
func (FileExists) Run(_ context.Context, with map[string]any, ectx spec.ExecContext) error {
	path := spec.GetStringParam(with, "path", "")
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(ectx.ProjectRoot, path)
	}
	if _, err := os.Stat(full); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("file not found: %s", path)
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	return nil
}
