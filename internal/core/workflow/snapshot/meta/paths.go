package meta

import (
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// Default subdirectory under the project root where unpacked snapshots live.
const defaultSnapshotsDir = "snapshots"

// Internal state directory under <baseDir>/.devbox/ for the snapshot subsystem
// (lock file, current pointer, pre-restore backup, transient unpack staging).
const stateSubdir = ".devbox/snapshots"

// File names inside the snapshot state directory.
const (
	currentPointerName   = "current"
	lockFileName         = "snapshot.lock"
	preRestoreBackupName = ".pre-restore-backup"
)

// ManifestFileName is the canonical filename for the per-snapshot manifest.
const ManifestFileName = "manifest.yml"

// DevboxSubdir is the per-snapshot subdirectory that holds the captured
// devbox/local.yml and deploy state files. Excluded from artifact scans.
const DevboxSubdir = "devbox"

// SnapshotsDir returns the directory that holds unpacked snapshots, honoring
// cfg.Dir (relative paths are joined to baseDir; absolute paths are returned
// as-is). A nil or empty config falls back to "<baseDir>/snapshots".
func SnapshotsDir(baseDir string, cfg *config.SnapshotConfig) string {
	dir := defaultSnapshotsDir
	if cfg != nil && cfg.Dir != "" {
		dir = cfg.Dir
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Join(baseDir, dir)
}

// SnapshotDir returns the directory for a single named snapshot.
// Callers must validate the name with ValidateName before joining.
//
//revive:disable-next-line:exported  canonical name per docs/plans/2026-05-24-snapshot-subsystem.md
func SnapshotDir(baseDir string, cfg *config.SnapshotConfig, name string) string {
	return filepath.Join(SnapshotsDir(baseDir, cfg), name)
}

// ManifestPath returns the manifest path for a single named snapshot.
func ManifestPath(baseDir string, cfg *config.SnapshotConfig, name string) string {
	return filepath.Join(SnapshotDir(baseDir, cfg, name), ManifestFileName)
}

// StateDir returns the per-project snapshot state directory under .devbox/.
func StateDir(baseDir string) string {
	return filepath.Join(baseDir, stateSubdir)
}

// CurrentPointer returns the path of the current-snapshot pointer file.
func CurrentPointer(baseDir string) string {
	return filepath.Join(StateDir(baseDir), currentPointerName)
}

// LockPath returns the path of the snapshot subsystem's lock file.
func LockPath(baseDir string) string {
	return filepath.Join(StateDir(baseDir), lockFileName)
}

// PreRestoreBackup returns the path of the pre-restore backup directory.
// `restore` copies devbox/local.yml and the deploy state file here before
// overwriting them so users have a manual-recovery path on failure.
func PreRestoreBackup(baseDir string) string {
	return filepath.Join(StateDir(baseDir), preRestoreBackupName)
}
