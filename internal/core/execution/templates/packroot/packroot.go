// Package packroot resolves per-file template lookups with a universal
// sibling-shadow override convention. For any pack <kind>/<name>, a sibling
// shadow pack at devbox/templates/<kind>/<name>.local/<rel> overrides the
// canonical devbox/templates/<kind>/<name>/<rel> file. The override pack is
// gitignored by the project's `*.local/` pattern; the canonical pack is
// tracked. The .devbox/ runtime directory is never consulted here.
package packroot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
)

// Resolve looks up a single relative path inside the named pack with the
// override convention applied:
//
//  1. <projectRoot>/devbox/templates/<kind>/<packName>.local/<rel>
//  2. <projectRoot>/devbox/templates/<kind>/<packName>/<rel>
//
// Either candidate must, when present, exist as a regular file. A
// non-regular candidate (directory, symlink, device, fifo, ...) is a hard
// error so a malformed override cannot silently mask itself, and a malformed
// canonical pack surfaces a clear message instead of letting downstream
// `os.ReadFile` emit "is a directory" or similar.
//
// When neither candidate exists the returned error wraps os.ErrNotExist so
// callers can branch with errors.Is.
//
// rel must be a contained relative path under each candidate root. Symlink
// components in either candidate's path chain are rejected.
func Resolve(projectRoot, kind, packName, rel string) (string, bool, error) {
	if projectRoot == "" {
		return "", false, errors.New("packroot: projectRoot is required")
	}
	if kind == "" {
		return "", false, errors.New("packroot: kind is required")
	}
	if packName == "" {
		return "", false, errors.New("packroot: packName is required")
	}
	if rel == "" {
		return "", false, errors.New("packroot: rel is required")
	}

	templatesRoot := filepath.Join(projectRoot, "workspace", "templates", kind)
	overrideRoot := filepath.Join(templatesRoot, packName+".local")
	canonicalRoot := filepath.Join(templatesRoot, packName)

	path, hit, err := tryCandidate(overrideRoot, rel, "override pack "+packName+".local")
	if err != nil {
		return "", false, err
	}
	if hit {
		return path, true, nil
	}

	path, hit, err = tryCandidate(canonicalRoot, rel, "pack "+packName)
	if err != nil {
		return "", false, err
	}
	if hit {
		return path, false, nil
	}

	return "", false, fmt.Errorf("template %q not found in %s or %s: %w",
		rel, overrideRoot, canonicalRoot, os.ErrNotExist)
}

// tryCandidate enforces ContainedRel + CheckNoSymlinks discipline on the
// candidate root, then Lstats the resulting path. Returns hit=true with the
// resolved absolute path when the path exists as a regular file. Returns
// hit=false with no error when the file does not exist. Any other state
// (directory, symlink, device, ...) is a hard error.
func tryCandidate(root, rel, label string) (string, bool, error) {
	// Reject a symlinked pack root: if root itself is a symlink, following it
	// would allow template sources to be read from outside the project tree,
	// bypassing the per-file pathsafe checks that only walk components of rel.
	if fi, err := os.Lstat(root); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("%s: pack root %s is a symlink; symlinked pack roots are not supported", label, root)
	}
	abs := filepath.Join(root, rel)
	if _, err := pathsafe.ContainedRel(root, abs); err != nil {
		return "", false, fmt.Errorf("%s: %w", label, err)
	}
	if err := pathsafe.CheckNoSymlinks(root, abs, label); err != nil {
		return "", false, err
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("%s: stat %s: %w", label, abs, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("%s: %s is a symlink; symlinked template sources are not supported", label, abs)
	}
	if !fi.Mode().IsRegular() {
		return "", false, fmt.Errorf("%s: %s is not a regular file (mode %s)", label, abs, fi.Mode())
	}
	return abs, true, nil
}
