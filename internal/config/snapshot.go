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

// ServicesMismatchValue is the typed policy controlling restore behavior when
// the manifest's captured service list diverges from the current config.
type ServicesMismatchValue int

const (
	// ServicesMismatchWarn (default) emits a warning and asks for confirmation.
	ServicesMismatchWarn ServicesMismatchValue = iota
	// ServicesMismatchBlock aborts the restore with a typed error before any
	// side effect on devbox/local.yml.
	ServicesMismatchBlock
	// ServicesMismatchIgnore proceeds silently regardless of any diff.
	ServicesMismatchIgnore
)

// ServicesMismatchPolicy is the YAML shape under snapshot.yml `services_mismatch`.
type ServicesMismatchPolicy struct {
	// Policy is the raw policy string: "warn" (default), "block", or "ignore".
	Policy string `yaml:"policy"`
}

// Effective resolves the raw Policy string into a typed ServicesMismatchValue.
// An empty string resolves to ServicesMismatchWarn. The loader rejects unknown
// values before reaching this point, so Effective never returns an error.
func (p ServicesMismatchPolicy) Effective() ServicesMismatchValue {
	switch p.Policy {
	case "", "warn":
		return ServicesMismatchWarn
	case "block":
		return ServicesMismatchBlock
	case "ignore":
		return ServicesMismatchIgnore
	default:
		return ServicesMismatchWarn
	}
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

// LocalYMLPolicy is the YAML shape under snapshot.yml `local_yml`. PreserveKeys
// holds dot-paths into devbox/local.yml that should survive a restore — the
// captured snapshot strips them out at create time and the restore step splices
// the current working-copy values back in.
type LocalYMLPolicy struct {
	// PreserveKeys is the list of dot-paths to preserve across restore.
	PreserveKeys []string `yaml:"preserve_keys"`
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
	// ServicesMismatch controls restore behavior when the manifest's captured
	// service list diverges from the current config (default: warn).
	ServicesMismatch ServicesMismatchPolicy `yaml:"services_mismatch"`
	// LocalYML controls how devbox/local.yml is handled across snapshot
	// create/restore (e.g. preserve_keys for machine-specific overrides).
	LocalYML LocalYMLPolicy `yaml:"local_yml"`

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

	if err := validateServicesMismatchPolicy(cfg.ServicesMismatch.Policy); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
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

// validateServicesMismatchPolicy rejects unknown policy values with a clear
// error message naming the offending value and the allowed set. An empty
// string is accepted and resolves to the default at use time.
func validateServicesMismatchPolicy(policy string) error {
	switch policy {
	case "", "warn", "block", "ignore":
		return nil
	default:
		return fmt.Errorf("services_mismatch.policy: unknown policy %q (allowed: warn, block, ignore)", policy)
	}
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
