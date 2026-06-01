// Package project provides project discovery for dwe.
// It locates workspace.yml by walking up from cwd.
package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ConfigFilename is the standard project config filename.
const (
	ConfigFilename = "workspace.yml"
)

// ErrNotFound is returned by Resolve when no workspace.yml is found during upward discovery
// (discovery mode only — explicit -c paths produce a wrapped os.ErrNotExist instead).
var ErrNotFound = errors.New("no workspace.yml found")

// Resolved holds the result of a successful project location.
type Resolved struct {
	ConfigPath string // absolute path to workspace.yml
	Root       string // directory containing workspace.yml
}

// Locate performs pure discovery.
//
// Explicit mode (flag != ""): the user named a specific file.
// Missing/unreadable is a hard error. On ErrNotExist returns (zero, false, wrappedErr)
// so callers can test errors.Is(err, os.ErrNotExist). Other stat errors propagate as-is.
//
// Discovery mode (flag == ""): walks up from os.Getwd().
// First workspace.yml found wins. No file found → (zero, false, nil) — not an error.
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

// Resolve is a convenience wrapper around Locate.
//
// Explicit path: returns a wrapped os.ErrNotExist if the file is missing
// (errors.Is(err, project.ErrNotFound) is false — this is not a discovery miss).
//
// Discovery mode: returns ErrNotFound (wrapped with cwd context) when no file is found
// (errors.Is(err, project.ErrNotFound) is true).
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
	return resolved, nil
}
