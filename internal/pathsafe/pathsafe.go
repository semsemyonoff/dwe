package pathsafe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CheckNoSymlinks verifies that no existing path component between absRoot and absDir
// is a symlink. It stops at the first non-existent component (which cannot be a symlink).
// The label parameter appears in the error message to identify what is being checked.
func CheckNoSymlinks(absRoot, absDir, label string) error {
	rel, err := filepath.Rel(absRoot, absDir)
	if err != nil {
		return fmt.Errorf("relative path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path %q is not under root %q", absDir, absRoot)
	}
	current := absRoot
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		fi, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("stat %s: %w", current, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s contains symlink at %q; symlinked paths are not supported", label, current)
		}
	}
	return nil
}

// ContainedRel returns the cleaned relative path from absRoot to absChild,
// rejecting any path that escapes absRoot or equals/collapses to . or ..
// This is used for validating user-specified paths (like template destinations)
// that must be contained within a directory.
func ContainedRel(absRoot, absChild string) (string, error) {
	rel, err := filepath.Rel(absRoot, absChild)
	if err != nil {
		return "", fmt.Errorf("relative path: %w", err)
	}

	// Reject absolute or escaping paths
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path is absolute")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes root")
	}

	// Clean the path
	cleanRel := filepath.Clean(rel)

	// Reject if cleaning produces . or .. or escaping path
	if cleanRel == "." || cleanRel == ".." {
		return "", fmt.Errorf("path cleans to %q", cleanRel)
	}
	if strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("cleaned path escapes root")
	}

	return cleanRel, nil
}

// EnsureRealUnder validates that realDir (already symlink-resolved via filepath.EvalSymlinks)
// is equal to or nested under all provided roots (also pre-resolved).
// This is used for post-MkdirAll boundary checks where symlinks have been followed.
// All arguments must already be resolved by the caller via filepath.EvalSymlinks.
// The semantics allow equality (realDir == root is a pass), which is necessary for
// operations that write files at a boundary (e.g. AGENTS.md at the hub root).
func EnsureRealUnder(realDir string, roots ...string) error {
	sep := string(filepath.Separator)
	for _, root := range roots {
		// HasPrefix with separator is true when realDir == root (equality allowed)
		// and true when realDir is strictly under root.
		if !strings.HasPrefix(realDir+sep, root+sep) {
			return fmt.Errorf("directory %q is not under %q", realDir, root)
		}
	}
	return nil
}
