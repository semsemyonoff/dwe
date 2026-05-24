package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/usercommands/registry"
)

// RemoveParams describes one `devbox snapshot remove` invocation. The caller
// is responsible for project-lock acquisition before calling Remove.
type RemoveParams struct {
	Cfg            *config.DevboxConfig
	SnapCfg        *config.SnapshotConfig
	Registry       *registry.Registry
	BaseDir        string
	Name           string
	SkipConfirm    bool
	NonInteractive bool
	Stdout         io.Writer
	Stderr         io.Writer
	// ConfirmRemove is invoked when SkipConfirm is false. A nil callback is
	// treated as a refusal so non-interactive callers cannot accidentally
	// destroy a snapshot.
	ConfirmRemove func(*Manifest) (bool, error)
	// StepObserverFactory builds a per-workflow live-UI observer; see
	// snapshot.StepObserverFactory for the contract. Nil disables.
	StepObserverFactory StepObserverFactory
}

// RemoveCancelledError is returned when the user declines the remove
// confirmation. CLI treats it as a non-error exit.
type RemoveCancelledError struct{}

func (e *RemoveCancelledError) Error() string { return "snapshot remove cancelled" }

// ExitCode returns 0 so fang suppresses an error banner.
func (e *RemoveCancelledError) ExitCode() int { return 0 }

// RemoveResult summarises the outcome of a successful Remove.
type RemoveResult struct {
	SnapshotDir    string
	ClearedCurrent bool
}

// Remove deletes a snapshot directory. If the snapshot config defines a
// `remove:` workflow, it runs first (under SnapshotScopeRestoreOrRemove) so
// user-defined cleanup (e.g. drop a snapshot database) can run before the
// directory is gone. The current pointer is cleared atomically when it
// pointed at the removed snapshot.
func Remove(ctx context.Context, p RemoveParams) (*RemoveResult, error) {
	if err := ValidateName(p.Name); err != nil {
		return nil, err
	}
	if p.SnapCfg == nil {
		return nil, errors.New("snapshot: snapshot config not loaded (missing devbox/snapshot.yml)")
	}
	if p.Cfg == nil {
		return nil, errors.New("snapshot: devbox config is required")
	}

	snapDir := SnapshotDir(p.BaseDir, p.SnapCfg, p.Name)
	st, statErr := os.Stat(snapDir)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("snapshot %q: not found at %s", p.Name, snapDir)
		}
		return nil, fmt.Errorf("snapshot %q: stat %s: %w", p.Name, snapDir, statErr)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("snapshot %q: %s is not a directory", p.Name, snapDir)
	}

	// Load manifest opportunistically — missing/corrupt manifest does not block removal.
	manifestPath := ManifestPath(p.BaseDir, p.SnapCfg, p.Name)
	m, _ := LoadManifest(manifestPath)

	if !p.SkipConfirm {
		ok, cErr := confirmRemove(p.ConfirmRemove, m)
		if cErr != nil {
			return nil, cErr
		}
		if !ok {
			return nil, &RemoveCancelledError{}
		}
	}

	// Run remove workflow if defined and has steps.
	if p.SnapCfg.Remove != nil && p.Registry != nil {
		wf, err := SelectWorkflow(p.SnapCfg, "remove", manifestVariant(m))
		if err != nil {
			return nil, fmt.Errorf("snapshot %q: %w", p.Name, err)
		}
		if len(wf.Steps) > 0 {
			absSnapDir, absErr := filepath.Abs(snapDir)
			if absErr != nil {
				absSnapDir = snapDir
			}
			var (
				name      = p.Name
				desc      string
				variant   string
				createdAt time.Time
			)
			if m != nil {
				name = m.Name
				desc = m.Description
				variant = m.Variant
				createdAt = m.CreatedAt
			}
			vars := BuildSnapshotVars(name, absSnapDir, desc, variant, createdAt)
			if err := RunWorkflow(ctx, ExecParams{
				Cfg:                 p.Cfg,
				Registry:            p.Registry,
				BaseDir:             p.BaseDir,
				Workflow:            wf,
				Vars:                vars,
				Scope:               tpl.SnapshotScopeRestoreOrRemove,
				Stdout:              p.Stdout,
				Stderr:              p.Stderr,
				SkipConfirm:         p.SkipConfirm,
				NonInteractive:      p.NonInteractive,
				StepObserverFactory: p.StepObserverFactory,
			}); err != nil {
				return nil, fmt.Errorf("snapshot %q: remove workflow: %w", p.Name, err)
			}
		}
	}

	if err := os.RemoveAll(snapDir); err != nil {
		return nil, fmt.Errorf("snapshot %q: remove dir: %w", p.Name, err)
	}

	cleared := false
	current, _ := ReadCurrent(p.BaseDir)
	if current == p.Name {
		if err := ClearCurrent(p.BaseDir); err != nil {
			return nil, fmt.Errorf("snapshot %q: clear current pointer: %w", p.Name, err)
		}
		cleared = true
	}

	return &RemoveResult{SnapshotDir: snapDir, ClearedCurrent: cleared}, nil
}

func confirmRemove(fn func(*Manifest) (bool, error), m *Manifest) (bool, error) {
	if fn == nil {
		return false, nil
	}
	return fn(m)
}

func manifestVariant(m *Manifest) string {
	if m == nil {
		return ""
	}
	return m.Variant
}
