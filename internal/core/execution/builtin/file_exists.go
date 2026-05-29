package builtin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/core/execution/builtin/spec"
)

type fileExistsBuiltin struct{}

func (fileExistsBuiltin) Validate(with map[string]any) error {
	path := spec.GetStringParam(with, "path", "")
	if path == "" {
		return errors.New("missing required param 'path'")
	}
	return nil
}

func (fileExistsBuiltin) Describe(with map[string]any) string {
	path := spec.GetStringParam(with, "path", "")
	return fmt.Sprintf("builtin: file_exists(path=%s)", path)
}

func (fileExistsBuiltin) Run(_ context.Context, with map[string]any, ectx spec.ExecContext) error {
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
