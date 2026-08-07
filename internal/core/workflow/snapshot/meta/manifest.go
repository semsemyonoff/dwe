// Package meta defines snapshot descriptor types and coordinates: the
// canonical Manifest shape, path helpers, name validation, the snapshot
// template variables map, the current-pointer state, the artifact scanner,
// and atomic file writing. It is the leaf layer of the snapshot subsystem —
// imported by archive/ and the root snapshot/ package.
package meta

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Status values recorded in last_create / last_restore.
const (
	StatusOk          = "ok"
	StatusFailed      = "failed"
	StatusInterrupted = "interrupted"
)

// ArtifactInfo records a single user-produced artifact inside the snapshot.
type ArtifactInfo struct {
	// Path is the snapshot-relative POSIX path of the artifact.
	Path string `yaml:"path" json:"path"`
	// Size is the byte length of the artifact at the time of capture.
	Size int64 `yaml:"size" json:"size"`
	// Sha256 is the lowercase hex sha256 of the artifact contents.
	Sha256 string `yaml:"sha256" json:"sha256"`
}

// ServiceSnapshot records one service from the effective service set at
// snapshot-capture time. Used by restore to detect divergence between the
// captured project and the current project's services.
type ServiceSnapshot struct {
	// Name is the service key from cfg.Services.
	Name string `yaml:"name" json:"name"`
	// Enabled mirrors ServiceConfig.Enabled at capture time (mandatory ||
	// services.<name>.enabled).
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// ProjectInfo records the project identity and config hash at capture time.
// ConfigHash may be empty when no deploy has run yet — restore treats an
// empty hash as a match against any current hash.
type ProjectInfo struct {
	// Name is the project name (cfg.Project.Name).
	Name string `yaml:"name" json:"name"`
	// ConfigHash mirrors deploy/journal ProjectLevelState.ConfigHash; empty
	// when no deploy has populated state.yml yet.
	ConfigHash string `yaml:"config_hash" json:"config_hash"`
	// Services records the effective service set at capture time, sorted by
	// Name for deterministic manifest output. Used by restore to detect
	// service-set divergence between snapshot and current project.
	Services []ServiceSnapshot `yaml:"services,omitempty" json:"services,omitempty"`
}

// WorkspaceFiles records which workspace files were captured into <snap>/workspace/.
type WorkspaceFiles struct {
	// LocalYML is the relative path of the captured local.yml (relative to
	// the snapshot dir), e.g. "workspace/local.yml".
	LocalYML string `yaml:"local_yml,omitempty" json:"local_yml,omitempty"`
	// DeployState is the relative path of the captured deploy state file.
	DeployState string `yaml:"deploy_state,omitempty" json:"deploy_state,omitempty"`
}

// LastCreate records the outcome of the most recent create attempt.
type LastCreate struct {
	// At is the wall-clock timestamp the create attempt finished (or was
	// interrupted at).
	At time.Time `yaml:"at,omitempty" json:"at,omitzero"`
	// Status is one of StatusOk / StatusFailed / StatusInterrupted.
	Status string `yaml:"status,omitempty" json:"status,omitempty"`
	// FailedStep is the workflow-step identifier that failed (when known).
	FailedStep string `yaml:"failed_step,omitempty" json:"failed_step,omitempty"`
}

// LastRestore records the outcome of the most recent restore attempt.
type LastRestore struct {
	// At is the wall-clock timestamp the restore attempt finished.
	At time.Time `yaml:"at,omitempty" json:"at,omitzero"`
	// Status mirrors LastCreate.Status.
	Status string `yaml:"status,omitempty" json:"status,omitempty"`
	// DurationMs is the wall-clock duration of the restore in milliseconds.
	DurationMs int64 `yaml:"duration_ms,omitempty" json:"duration_ms,omitempty"`
	// FailedStep is the workflow-step identifier that failed (when known).
	FailedStep string `yaml:"failed_step,omitempty" json:"failed_step,omitempty"`
}

// Manifest is the canonical shape of a snapshot's manifest.yml.
//
// The manifest carries no version field; the loader is lenient on unknown
// fields so that future dwe versions can add metadata without breaking older
// readers.
type Manifest struct {
	// Name mirrors the snapshot directory name.
	Name string `yaml:"name" json:"name"`
	// CreatedAt is the wall-clock timestamp the create flow started.
	CreatedAt time.Time `yaml:"created_at" json:"created_at,omitzero"`
	// Description is the human-readable description provided via `-d`.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Project identifies the source project + config hash at capture time.
	Project ProjectInfo `yaml:"project" json:"project"`
	// DweVersion is the version string of the dwe CLI that created it.
	DweVersion string `yaml:"dwe_version,omitempty" json:"dwe_version,omitempty"`
	// Variant is the snapshot variant name (empty for the default block).
	Variant string `yaml:"variant,omitempty" json:"variant,omitempty"`
	// Artifacts lists every user-produced file in the snapshot with its
	// size and sha256. Workspace files under WorkspaceSubdir are not included.
	Artifacts []ArtifactInfo `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
	// WorkspaceFiles records which workspace files were captured alongside.
	WorkspaceFiles WorkspaceFiles `yaml:"workspace_files,omitempty" json:"workspace_files,omitzero"`
	// LastCreate / LastRestore record the outcome of the most recent
	// create / restore attempt for this snapshot.
	LastCreate  *LastCreate  `yaml:"last_create,omitempty" json:"last_create,omitempty"`
	LastRestore *LastRestore `yaml:"last_restore,omitempty" json:"last_restore,omitempty"`
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
// tolerated (lenient decode) — older dwe versions must be able to read
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
	return WriteFileAtomic(path, data, 0o644)
}
