package docs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DocRoot represents a documentation source (either built-in or project-local).
type DocRoot struct {
	Name        string // "devbox" or "project"
	FS          fs.FS  // The file system to read from
	ProjectPath string // Absolute path to the project root (for project docs only)
}

// Sources returns the available documentation sources:
// - Always includes the built-in "devbox" docs from BuiltinFS.
// - Includes "project" docs if <projectRoot>/docs/ exists and is readable.
func Sources(projectRoot string) []DocRoot {
	roots := []DocRoot{
		{
			Name: "devbox",
			FS:   BuiltinFS,
		},
	}

	// Check for project docs at <projectRoot>/docs
	if projectRoot != "" {
		projectDocsPath := filepath.Join(projectRoot, "docs")
		if info, err := os.Stat(projectDocsPath); err == nil && info.IsDir() {
			// Open the docs directory as an OS filesystem.
			projectFS := os.DirFS(projectDocsPath)
			roots = append(roots, DocRoot{
				Name:        "project",
				FS:          projectFS,
				ProjectPath: projectRoot,
			})
		}
	}

	return roots
}

// RelPath returns the relative path from the documentation root,
// or an error if the path is invalid.
func RelPath(root DocRoot, path string) (string, error) {
	// Normalize the path.
	normalized := filepath.Clean(path)

	// Reject paths that try to escape the root.
	if normalized == "." || normalized == ".." || filepath.IsAbs(normalized) {
		return "", fmt.Errorf("invalid path: %q", path)
	}

	// Ensure the path doesn't contain directory traversal.
	if containsTraversal(normalized) {
		return "", fmt.Errorf("path traversal not allowed: %q", path)
	}

	return normalized, nil
}

func containsTraversal(path string) bool {
	parts := filepath.SplitList(path)
	for _, part := range parts {
		if part == ".." || strings.Contains(part, "..") {
			return true
		}
	}
	return false
}
