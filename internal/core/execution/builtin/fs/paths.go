package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/core/execution/builtin/spec"
)

// RemovePaths deletes declared relative paths inside the project root.
type RemovePaths struct{}

// Validate checks that 'paths' is a non-empty list of relative paths that do
// not escape the project root.
func (RemovePaths) Validate(with map[string]any) error {
	paths, err := spec.GetStringSlice(with, "paths")
	if err != nil {
		return fmt.Errorf("builtin remove_paths: %w", err)
	}
	if len(paths) == 0 {
		return fmt.Errorf("builtin remove_paths: 'paths' must not be empty")
	}
	for _, p := range paths {
		if p == "" {
			return fmt.Errorf("builtin remove_paths: path must not be empty")
		}
		if filepath.IsAbs(p) {
			return fmt.Errorf("builtin remove_paths: path %q must be relative", p)
		}
		cleaned := filepath.Clean(p)
		if cleaned == "." || strings.HasPrefix(cleaned, "..") {
			return fmt.Errorf("builtin remove_paths: path %q is not allowed (root-equivalent or escaping)", p)
		}
	}
	return nil
}

// Describe returns a human-readable description for plan display.
func (RemovePaths) Describe(with map[string]any) string {
	paths, _ := spec.GetStringSlice(with, "paths")
	return fmt.Sprintf("builtin: remove_paths(paths=%v)", paths)
}

// Run removes each declared path relative to the project root.
func (RemovePaths) Run(ctx context.Context, with map[string]any, ectx spec.ExecContext) error {
	paths, err := spec.GetStringSlice(with, "paths")
	if err != nil {
		return fmt.Errorf("remove_paths: %w", err)
	}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Security: path must be relative and must not escape the project root.
		if filepath.IsAbs(p) {
			return fmt.Errorf("remove_paths: path %q must be relative", p)
		}
		abs := filepath.Join(ectx.ProjectRoot, p)
		rel, err := filepath.Rel(ectx.ProjectRoot, abs)
		if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("remove_paths: path %q is not allowed (must be a non-root relative path inside the project)", p)
		}
		if err := os.RemoveAll(abs); err != nil {
			return fmt.Errorf("remove_paths: removing %q: %w", p, err)
		}
		ectx.Output.Success(fmt.Sprintf("removed %s", p))
	}
	return nil
}
