// Package shimassets embeds the cross-compiled bridge shim binaries (built
// into bin/ by scripts/build-shims.sh — gitignored, mirrors the embedded-docs
// tree) and materializes them into a project's `.dwe/bridge` directory so the
// compose overlay can bind-mount them into containers (design D8).
//
// bin/.gitkeep is committed so the `all:bin` embed pattern always matches on
// a fresh checkout — `//go:embed` against an empty directory is a hard
// compile error for the whole module (vet and lint compile too).
//
// The package is a leaf on purpose: it must not import its parent
// internal/core/bridge (composegen there will reference shim file names),
// so the bridge-dir path join is duplicated from bridge.DefaultBridgeDir.
package shimassets

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed all:bin
var embedded embed.FS

// shimPrefix selects the materializable entries in bin/; everything else
// (the committed .gitkeep placeholder) is skipped.
const shimPrefix = "shim-"

// FileName returns the shim file name for a linux GOARCH (e.g.
// "shim-linux-amd64"). Single naming source shared by the build script
// outputs, Materialize targets, and the compose overlay mount sources.
func FileName(arch string) string { return shimPrefix + "linux-" + arch }

// shimPerm makes materialized shims executable — they are bind-mounted as
// `/usr/local/bin/dwe` (or bridge.shim_path) inside containers.
const shimPerm = 0o755

// bridgeDirPerm mirrors the daemon's bridge-dir mode (design D3).
const bridgeDirPerm = 0o700

// Materialize writes the embedded shim binaries into `<baseDir>/.dwe/bridge`
// as `shim-linux-<arch>` files (0755, atomic, write-if-changed) and returns
// the target paths. Non-shim embed entries are skipped. A no-op when the
// embed tree holds only the placeholder (fresh checkout without `make shims`).
func Materialize(baseDir string) ([]string, error) {
	return materializeFS(embedded, baseDir)
}

// materializeFS is the fs-injectable core of Materialize for tests.
func materializeFS(fsys fs.FS, baseDir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, "bin")
	if err != nil {
		return nil, fmt.Errorf("bridge shims: reading embedded bin: %w", err)
	}
	bridgeDir := filepath.Join(baseDir, ".dwe", "bridge")
	if err := os.MkdirAll(bridgeDir, bridgeDirPerm); err != nil {
		return nil, fmt.Errorf("bridge shims: creating bridge dir: %w", err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), shimPrefix) {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join("bin", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("bridge shims: reading embedded %s: %w", e.Name(), err)
		}
		dst := filepath.Join(bridgeDir, e.Name())
		if err := writeIfChanged(dst, data); err != nil {
			return nil, err
		}
		paths = append(paths, dst)
	}
	return paths, nil
}

// Shim materialization states reported by Status.
const (
	// StateCurrent: the materialized file matches the embedded shim.
	StateCurrent = "current"
	// StateStale: the file exists but differs from the embedded shim (an
	// older dwe materialized it — the next prepare hook rewrites it).
	StateStale = "stale"
	// StateMissing: the shim has not been materialized yet.
	StateMissing = "missing"
)

// ShimState describes one embedded shim's materialization state in a project
// (consumed by `dwe bridge status`).
type ShimState struct {
	// Name is the shim file name (e.g. "shim-linux-amd64").
	Name string
	// Path is the materialization target under <baseDir>/.dwe/bridge.
	Path string
	// State is one of StateCurrent, StateStale, StateMissing.
	State string
}

// Status compares each embedded shim against its materialized copy under
// `<baseDir>/.dwe/bridge` without writing anything. On a fresh checkout
// (placeholder-only embed tree) it returns an empty slice.
func Status(baseDir string) ([]ShimState, error) {
	return statusFS(embedded, baseDir)
}

// statusFS is the fs-injectable core of Status for tests.
func statusFS(fsys fs.FS, baseDir string) ([]ShimState, error) {
	entries, err := fs.ReadDir(fsys, "bin")
	if err != nil {
		return nil, fmt.Errorf("bridge shims: reading embedded bin: %w", err)
	}
	bridgeDir := filepath.Join(baseDir, ".dwe", "bridge")
	states := []ShimState{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), shimPrefix) {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join("bin", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("bridge shims: reading embedded %s: %w", e.Name(), err)
		}
		dst := filepath.Join(bridgeDir, e.Name())
		state := StateCurrent
		existing, err := os.ReadFile(dst)
		switch {
		case errors.Is(err, os.ErrNotExist):
			state = StateMissing
		case err != nil:
			return nil, fmt.Errorf("bridge shims: reading %s: %w", dst, err)
		case !bytes.Equal(existing, data):
			state = StateStale
		}
		states = append(states, ShimState{Name: e.Name(), Path: dst, State: state})
	}
	return states, nil
}

// writeIfChanged replaces dst only when its content differs, via a same-dir
// temp file + rename — a half-written shim must never be observable at the
// bind-mount source path of a running container.
func writeIfChanged(dst string, data []byte) error {
	existing, err := os.ReadFile(dst)
	if err == nil && bytes.Equal(existing, data) {
		// Content current — repair the mode in case something stripped the
		// executable bit (chmod does not touch mtime, so this stays a no-op
		// for write-if-changed purposes).
		if err := os.Chmod(dst, shimPerm); err != nil {
			return fmt.Errorf("bridge shims: chmod %s: %w", dst, err)
		}
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("bridge shims: reading %s: %w", dst, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return fmt.Errorf("bridge shims: creating temp file: %w", err)
	}
	defer func() {
		_ = os.Remove(tmp.Name()) // no-op after successful rename
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("bridge shims: writing %s: %w", dst, err)
	}
	if err := tmp.Chmod(shimPerm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("bridge shims: chmod %s: %w", dst, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("bridge shims: closing temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return fmt.Errorf("bridge shims: replacing %s: %w", dst, err)
	}
	return nil
}
