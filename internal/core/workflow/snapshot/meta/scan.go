package meta

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ScanArtifacts walks snapDir and returns ArtifactInfo entries for every
// regular file outside the manifest and the captured workspace/ subdir.
//
// Symlinks are rejected with a clear error — workflows may not produce
// symlinks in the snapshot dir (they would break pack/unpack safety).
// Sha256 is computed streaming via io.Copy so multi-GB dumps do not OOM.
// Returned entries are sorted by Path for deterministic manifests.
func ScanArtifacts(snapDir string) ([]ArtifactInfo, error) {
	var out []ArtifactInfo
	workspacePrefix := WorkspaceSubdir + string(filepath.Separator)
	err := filepath.WalkDir(snapDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == snapDir {
			return nil
		}
		rel, err := filepath.Rel(snapDir, path)
		if err != nil {
			return fmt.Errorf("relative path: %w", err)
		}
		// Skip the captured workspace/ subtree entirely (entries are not user artifacts).
		if rel == WorkspaceSubdir || strings.HasPrefix(rel, workspacePrefix) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// Use Lstat to surface symlinks instead of following them.
		fi, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("lstat %s: %w", rel, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("snapshot artifact %q is a symlink; symlinks are not allowed in snapshots", rel)
		}
		// Skip the manifest itself and any non-regular files (sockets, devices).
		if rel == ManifestFileName {
			return nil
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("snapshot artifact %q is not a regular file", rel)
		}
		sum, err := HashFile(path)
		if err != nil {
			return fmt.Errorf("hash %s: %w", rel, err)
		}
		out = append(out, ArtifactInfo{
			Path:   filepath.ToSlash(rel),
			Size:   fi.Size(),
			Sha256: sum,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// HashFile streams f through sha256 and returns the lowercase hex digest.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
