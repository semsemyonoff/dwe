package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"

	"devbox-cli/internal/usercommands/model"
)

// SnapshotConfigFileName is the filename of the project-level snapshot.yml,
// relative to the devbox/ directory.
const SnapshotConfigFileName = "snapshot.yml"

// SnapshotConfigPath returns the canonical path to snapshot.yml given a project
// base directory (the directory that contains the devbox/ folder).
func SnapshotConfigPath(baseDir string) string {
	return filepath.Join(baseDir, "devbox", SnapshotConfigFileName)
}

// snapshotVariantNamePattern restricts variant identifiers to a safe subset
// (used in filesystem paths and CLI flags).
var snapshotVariantNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,30}$`)

// SnapshotPackConfig controls the optional pack subcommand's archive contents.
type SnapshotPackConfig struct {
	// Exclude is a list of glob patterns excluded from `devbox snapshot pack`.
	Exclude []string `yaml:"exclude"`
}

// SnapshotWorkflow is one of the create/restore/remove workflow blocks in
// devbox/snapshot.yml. The Steps shape reuses model.WorkflowStep directly so
// snapshot workflows are executed by the same runner as user `type: workflow`
// commands.
type SnapshotWorkflow struct {
	// Description is human-readable help text for the workflow.
	Description string `yaml:"description"`
	// Steps is the workflow body. Reuses model.WorkflowStep so the existing
	// runner handles when:/confirm/parallel/continue_on_error uniformly.
	Steps []model.WorkflowStep `yaml:"steps"`
	// Variants holds named alternative bodies selectable via --using=<variant>.
	// Variant names must match [a-z0-9][a-z0-9._-]{0,30}.
	Variants map[string]SnapshotWorkflow `yaml:"variants"`
}

// SnapshotConfig is the parsed shape of devbox/snapshot.yml.
type SnapshotConfig struct {
	// Dir is the directory where unpacked snapshots live, relative to baseDir.
	// Defaults to "./snapshots" when empty.
	Dir string `yaml:"dir"`
	// RollbackTarget is the snapshot name `devbox snapshot rollback` resolves to.
	RollbackTarget string `yaml:"rollback_target"`
	// RequireMatchingConfig blocks restore when manifest config_hash differs
	// from the current config_hash (empty manifest hash is treated as a match).
	RequireMatchingConfig bool `yaml:"require_matching_config"`
	// Pack holds optional pack subcommand settings.
	Pack SnapshotPackConfig `yaml:"pack"`

	// Create is the workflow run by `devbox snapshot create`.
	Create *SnapshotWorkflow `yaml:"create"`
	// Restore is the workflow run by `devbox snapshot restore` / `rollback`.
	Restore *SnapshotWorkflow `yaml:"restore"`
	// Remove is the optional workflow run by `devbox snapshot remove`.
	Remove *SnapshotWorkflow `yaml:"remove"`
}

// LoadSnapshotConfig reads and strictly decodes devbox/snapshot.yml at path.
//
// Returns (nil, nil) when the file does not exist — callers decide per
// subcommand whether absence is fatal. Other I/O errors are returned wrapped.
// Unknown YAML fields are rejected (KnownFields(true)).
func LoadSnapshotConfig(path string) (*SnapshotConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var cfg SnapshotConfig
	if len(bytes.TrimSpace(data)) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}

	if err := validateSnapshotWorkflow("create", cfg.Create); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validateSnapshotWorkflow("restore", cfg.Restore); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validateSnapshotWorkflow("remove", cfg.Remove); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return &cfg, nil
}

// validateSnapshotWorkflow checks step shapes and variant naming for a single
// top-level block. A nil workflow is allowed (the block is optional).
func validateSnapshotWorkflow(kind string, w *SnapshotWorkflow) error {
	if w == nil {
		return nil
	}
	for i, step := range w.Steps {
		if err := step.Validate(); err != nil {
			return fmt.Errorf("%s: steps[%d]: %w", kind, i, err)
		}
	}
	for name, variant := range w.Variants {
		if !snapshotVariantNamePattern.MatchString(name) {
			return fmt.Errorf("%s: variant name %q is invalid (allowed: [a-z0-9][a-z0-9._-]{0,30})", kind, name)
		}
		if len(variant.Variants) > 0 {
			return fmt.Errorf("%s: variant %q: nested variants are not allowed", kind, name)
		}
		for i, step := range variant.Steps {
			if err := step.Validate(); err != nil {
				return fmt.Errorf("%s: variant %q: steps[%d]: %w", kind, name, i, err)
			}
		}
	}
	return nil
}
