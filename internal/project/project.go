// Package project provides project discovery and schema validation for devbox.
// It locates devbox.yml by walking up from cwd and validates the schema_version gate.
package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SchemaField is the YAML key for the schema version gate in devbox.yml.
// SupportedSchema is the only accepted schema_version value.
// ConfigFilename is the standard project config filename.
//
// SupportedSchema = "2" is frozen per CLAUDE.md "no schema_version bumps" policy.
// Devbox is pre-release with no external users, so breaking changes are made directly
// without migration paths or version gates.
const (
	SchemaField     = "schema_version"
	SupportedSchema = "2"
	ConfigFilename  = "devbox.yml"
)

// ErrNotFound is returned by Resolve when no devbox.yml is found during upward discovery
// (discovery mode only — explicit -c paths produce a wrapped os.ErrNotExist instead).
var ErrNotFound = errors.New("no devbox.yml found")

// Resolved holds the result of a successful project location.
type Resolved struct {
	ConfigPath string // absolute path to devbox.yml
	Root       string // directory containing devbox.yml
}

// Locate performs pure discovery without schema validation.
//
// Explicit mode (flag != ""): the user named a specific file.
// Missing/unreadable is a hard error. On ErrNotExist returns (zero, false, wrappedErr)
// so callers can test errors.Is(err, os.ErrNotExist). Other stat errors propagate as-is.
//
// Discovery mode (flag == ""): walks up from os.Getwd().
// First devbox.yml found wins. No file found → (zero, false, nil) — not an error.
// Non-ErrNotExist stat failures during the walk return (zero, false, err).
func Locate(flag string) (Resolved, bool, error) {
	if flag != "" {
		return locateExplicit(flag)
	}
	return locateDiscover()
}

func locateExplicit(flag string) (Resolved, bool, error) {
	abs, err := filepath.Abs(flag)
	if err != nil {
		return Resolved{}, false, fmt.Errorf("config file %s: %w", flag, err)
	}
	_, err = os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Resolved{}, false, fmt.Errorf("config file %s: %w", flag, os.ErrNotExist)
		}
		return Resolved{}, false, err
	}
	// Resolve symlinks so Root is canonical (important on macOS where /tmp → /private/tmp).
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		real = abs
	}
	return Resolved{ConfigPath: real, Root: filepath.Dir(real)}, true, nil
}

func locateDiscover() (Resolved, bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return Resolved{}, false, fmt.Errorf("getwd: %w", err)
	}

	dir := cwd
	for {
		candidate := filepath.Join(dir, ConfigFilename)
		_, err := os.Stat(candidate)
		if err == nil {
			// Resolve symlinks so Root is canonical (important on macOS where /tmp → /private/tmp).
			real, rerr := filepath.EvalSymlinks(candidate)
			if rerr != nil {
				real = candidate
			}
			return Resolved{ConfigPath: real, Root: filepath.Dir(real)}, true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return Resolved{}, false, err
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// reached filesystem root
			return Resolved{}, false, nil
		}
		dir = parent
	}
}

// ValidateSchema reads only the schema_version field from the file at path.
// Returns nil if schema_version == "2". Returns a clear error for v1 or missing version.
func ValidateSchema(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var view struct {
		SchemaVersion string `yaml:"schema_version"`
	}
	if err := yaml.Unmarshal(data, &view); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	switch view.SchemaVersion {
	case SupportedSchema:
		return nil
	case "":
		return fmt.Errorf("missing %s in %s; this Devbox supports %s: %q only", SchemaField, path, SchemaField, SupportedSchema)
	default:
		return fmt.Errorf("legacy devbox project detected at %s; this Devbox supports %s: %q only (found %q)", path, SchemaField, SupportedSchema, view.SchemaVersion)
	}
}

// Resolve is a convenience wrapper that composes Locate and ValidateSchema.
//
// Explicit path: returns a wrapped os.ErrNotExist if the file is missing
// (errors.Is(err, project.ErrNotFound) is false — this is not a discovery miss).
//
// Discovery mode: returns ErrNotFound (wrapped with cwd context) when no file is found
// (errors.Is(err, project.ErrNotFound) is true).
//
// In both modes, a located file with a wrong schema_version returns a schema error
// (not ErrNotFound).
func Resolve(flag string) (Resolved, error) {
	resolved, found, err := Locate(flag)
	if err != nil {
		return Resolved{}, err
	}
	if !found {
		// discovery mode only — flag was empty
		cwd, _ := os.Getwd()
		return Resolved{}, fmt.Errorf("%w in %s or any parent directory", ErrNotFound, cwd)
	}
	if err := ValidateSchema(resolved.ConfigPath); err != nil {
		return Resolved{}, err
	}
	return resolved, nil
}
