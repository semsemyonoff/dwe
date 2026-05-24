package snapshot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// CreateStatus / RestoreStatus enum values recorded in last_create / last_restore.
const (
	StatusOk          = "ok"
	StatusFailed      = "failed"
	StatusInterrupted = "interrupted"
)

// ArtifactInfo records a single user-produced artifact inside the snapshot.
type ArtifactInfo struct {
	// Path is the snapshot-relative POSIX path of the artifact.
	Path string `yaml:"path"`
	// Size is the byte length of the artifact at the time of capture.
	Size int64 `yaml:"size"`
	// Sha256 is the lowercase hex sha256 of the artifact contents.
	Sha256 string `yaml:"sha256"`
}

// ProjectInfo records the project identity and config hash at capture time.
// ConfigHash may be empty when no deploy has run yet — restore treats an
// empty hash as a match against any current hash.
type ProjectInfo struct {
	// Name is the devbox project name (cfg.Project.Name).
	Name string `yaml:"name"`
	// ConfigHash mirrors deploy/journal ProjectLevelState.ConfigHash; empty
	// when no deploy has populated state.yml yet.
	ConfigHash string `yaml:"config_hash"`
}

// DevboxFiles records which devbox files were captured into <snap>/devbox/.
type DevboxFiles struct {
	// LocalYML is the relative path of the captured local.yml (relative to
	// the snapshot dir), e.g. "devbox/local.yml".
	LocalYML string `yaml:"local_yml,omitempty"`
	// DeployState is the relative path of the captured deploy state file.
	DeployState string `yaml:"deploy_state,omitempty"`
}

// LastCreate records the outcome of the most recent create attempt.
type LastCreate struct {
	// At is the wall-clock timestamp the create attempt finished (or was
	// interrupted at).
	At time.Time `yaml:"at,omitempty"`
	// Status is one of StatusOk / StatusFailed / StatusInterrupted.
	Status string `yaml:"status,omitempty"`
	// FailedStep is the workflow-step identifier that failed (when known).
	FailedStep string `yaml:"failed_step,omitempty"`
}

// LastRestore records the outcome of the most recent restore attempt.
type LastRestore struct {
	// At is the wall-clock timestamp the restore attempt finished.
	At time.Time `yaml:"at,omitempty"`
	// Status mirrors LastCreate.Status.
	Status string `yaml:"status,omitempty"`
	// DurationMs is the wall-clock duration of the restore in milliseconds.
	DurationMs int64 `yaml:"duration_ms,omitempty"`
	// FailedStep is the workflow-step identifier that failed (when known).
	FailedStep string `yaml:"failed_step,omitempty"`
}

// Manifest is the canonical shape of a snapshot's manifest.yml.
//
// Per CLAUDE.md "no schema_version" project policy, the manifest carries no
// version field; the loader is lenient on unknown fields so that future
// devbox versions can add metadata without breaking older readers.
type Manifest struct {
	// Name mirrors the snapshot directory name.
	Name string `yaml:"name"`
	// CreatedAt is the wall-clock timestamp the create flow started.
	CreatedAt time.Time `yaml:"created_at"`
	// Description is the human-readable description provided via `-d`.
	Description string `yaml:"description,omitempty"`
	// Project identifies the source project + config hash at capture time.
	Project ProjectInfo `yaml:"project"`
	// DevboxVersion is the version string of the devbox CLI that created it.
	DevboxVersion string `yaml:"devbox_version,omitempty"`
	// Variant is the snapshot variant name (empty for the default block).
	Variant string `yaml:"variant,omitempty"`
	// Artifacts lists every user-produced file in the snapshot with its
	// size and sha256. Devbox files under DevboxSubdir are not included.
	Artifacts []ArtifactInfo `yaml:"artifacts,omitempty"`
	// DevboxFiles records which devbox files were captured alongside.
	DevboxFiles DevboxFiles `yaml:"devbox_files,omitempty"`
	// LastCreate / LastRestore record the outcome of the most recent
	// create / restore attempt for this snapshot.
	LastCreate  *LastCreate  `yaml:"last_create,omitempty"`
	LastRestore *LastRestore `yaml:"last_restore,omitempty"`
}

// NewManifest returns a Manifest pre-populated with the given name and a
// clock-driven CreatedAt. Pass a nil now to use the real wall clock.
func NewManifest(name string, now func() time.Time) *Manifest {
	if now == nil {
		now = time.Now
	}
	return &Manifest{
		Name:      name,
		CreatedAt: now().UTC(),
	}
}

// LoadManifest reads and decodes a manifest.yml from path. Unknown fields are
// tolerated (lenient decode) — older devbox versions must be able to read
// manifests written by newer versions without breaking.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}

// SaveManifest writes m to path atomically via write-temp + rename in the
// same directory as the final file (cross-filesystem renames are not
// guaranteed atomic on POSIX, so the temp file must live in the target dir).
// The parent directory is created with 0o755 and the manifest with 0o644.
func SaveManifest(path string, m *Manifest) error {
	if m == nil {
		return errors.New("snapshot: cannot save nil manifest")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	return writeFileAtomic(path, data, 0o644)
}

// writeFileAtomic writes data to path atomically using a temp file in the
// same directory (required for POSIX atomic rename) plus chmod + rename.
// On any error, the temp file is removed.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}
