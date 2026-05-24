package snapshot

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/usercommands/registry"
)

// CreateParams describes one `devbox snapshot create` invocation. The caller
// is responsible for project-lock acquisition before calling Create.
type CreateParams struct {
	// Cfg is the loaded devbox config.
	Cfg *config.DevboxConfig
	// SnapCfg is the parsed devbox/snapshot.yml. Must be non-nil and define a
	// Create block, else Create returns an error before any filesystem
	// mutation.
	SnapCfg *config.SnapshotConfig
	// Registry is the user-command registry; workflows resolve commands here.
	Registry *registry.Registry
	// BaseDir is the project root (the directory that holds devbox/).
	BaseDir string
	// Name is the snapshot name (must pass ValidateName).
	Name string
	// Description is the human-readable description recorded in manifest.yml.
	Description string
	// Variant selects a Variants[Variant] body from the Create block; empty
	// selects the default block.
	Variant string
	// DevboxVersion is recorded in the manifest for diagnostic purposes.
	DevboxVersion string
	// SkipConfirm short-circuits the overwrite prompt (the `-y` flag).
	SkipConfirm bool
	// NonInteractive forces non-interactive code paths even on a TTY (used by
	// tests; the CLI sets this when stdin is not a TTY).
	NonInteractive bool
	// Now is the clock used for CreatedAt / LastCreate.At; nil defaults to
	// time.Now.
	Now func() time.Time
	// Stdout / Stderr receive workflow + progress output.
	Stdout io.Writer
	Stderr io.Writer
	// ConfirmOverwrite is called when the snapshot directory already exists
	// and the caller did not pass SkipConfirm. The default returns false
	// (refuse) when nil — the CLI layer supplies a real prompt.
	ConfirmOverwrite func() (bool, error)
	// StepObserverFactory builds a per-workflow live-UI observer; see
	// snapshot.StepObserverFactory for the contract. Nil disables.
	StepObserverFactory StepObserverFactory
}

// CreateCancelledError is returned when the user declines the overwrite
// confirmation. It is not a workflow failure; CLI surface treats it as a
// non-error exit (the snapshot directory is left untouched).
type CreateCancelledError struct{}

func (e *CreateCancelledError) Error() string { return "snapshot create cancelled" }

// ExitCode returns 0 so fang suppresses an error banner — cancellation is
// intentional.
func (e *CreateCancelledError) ExitCode() int { return 0 }

// CreateResult summarises the outcome of a successful or interrupted Create.
type CreateResult struct {
	// SnapshotDir is the absolute path of the created snapshot directory.
	SnapshotDir string
	// ManifestPath is the absolute path of the written manifest.
	ManifestPath string
	// Manifest is the manifest that was written to disk.
	Manifest *Manifest
	// Status mirrors Manifest.LastCreate.Status — "ok" / "failed" /
	// "interrupted".
	Status string
	// BackupRestored is true when an existing snapshot was backed up before
	// the create attempt, the workflow failed, and the backup was successfully
	// renamed back. The snapshot directory therefore contains the previous
	// (pre-attempt) state, not a partial failed one.
	BackupRestored bool
}

// Create runs the create workflow under p and writes the snapshot manifest.
//
// Flow:
//
//  1. Validate name, snapshot config block, and variant.
//  2. If the snapshot dir already exists, confirm overwrite (or fail).
//  3. Create <snap>/ + <snap>/devbox/; copy devbox/local.yml and
//     .devbox/deploy/state.yml into <snap>/devbox/ (each is optional —
//     missing source files are skipped silently).
//  4. Run the selected create workflow under SnapshotScopeCreate.
//  5. Scan artifacts, write the manifest atomically, update the current
//     pointer atomically.
//
// On workflow failure: if no previous snapshot existed, the partial directory
// is kept; if an existing snapshot was backed up before the attempt, the
// backup is restored so the user retains a working state. In both cases the
// manifest is written with last_create.status set to "failed" or
// "interrupted" and the current pointer is NOT touched. Callers convert
// ctx.Err()==Canceled into the interrupted exit code.
func Create(ctx context.Context, p CreateParams) (*CreateResult, error) {
	if err := ValidateName(p.Name); err != nil {
		return nil, err
	}
	if p.SnapCfg == nil {
		return nil, errors.New("snapshot: snapshot config not loaded (missing devbox/snapshot.yml)")
	}
	if p.Cfg == nil {
		return nil, errors.New("snapshot: devbox config is required")
	}
	if p.Registry == nil {
		return nil, errors.New("snapshot: user-command registry is required")
	}

	wf, err := SelectWorkflow(p.SnapCfg, "create", p.Variant)
	if err != nil {
		return nil, err
	}

	now := p.Now
	if now == nil {
		now = time.Now
	}

	snapDir := SnapshotDir(p.BaseDir, p.SnapCfg, p.Name)
	var backupDir string
	if _, statErr := os.Stat(snapDir); statErr == nil {
		// Directory already exists — confirm overwrite or refuse.
		if !p.SkipConfirm {
			ok, cErr := confirmOverwrite(p.ConfirmOverwrite)
			if cErr != nil {
				return nil, cErr
			}
			if !ok {
				return nil, &CreateCancelledError{}
			}
		}
		// Rename rather than delete: if the workflow fails we can restore the
		// previous snapshot so the user is not left with a failed state and no
		// fallback.
		var randB [8]byte
		if _, rErr := rand.Read(randB[:]); rErr != nil {
			return nil, fmt.Errorf("snapshot: generate backup name: %w", rErr)
		}
		backupDir = snapDir + fmt.Sprintf(".create-backup-%x", randB)
		if err := os.Rename(snapDir, backupDir); err != nil {
			return nil, fmt.Errorf("snapshot: backup existing snapshot: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("snapshot: stat existing dir: %w", statErr)
	}

	if err := os.MkdirAll(filepath.Join(snapDir, DevboxSubdir), 0o755); err != nil {
		if backupDir != "" {
			_ = os.RemoveAll(snapDir)
			if rErr := os.Rename(backupDir, snapDir); rErr != nil && p.Stderr != nil {
				_, _ = fmt.Fprintf(p.Stderr, "warning: could not restore snapshot backup %q: %v\n", backupDir, rErr)
			}
		}
		return nil, fmt.Errorf("snapshot: create snapshot dir: %w", err)
	}

	devboxFiles, err := captureDevboxFiles(p.BaseDir, snapDir, p.SnapCfg.LocalYML.PreserveKeys)
	if err != nil {
		if backupDir != "" {
			_ = os.RemoveAll(snapDir)
			if rErr := os.Rename(backupDir, snapDir); rErr != nil && p.Stderr != nil {
				_, _ = fmt.Fprintf(p.Stderr, "warning: could not restore snapshot backup %q: %v\n", backupDir, rErr)
			}
		}
		return nil, err
	}

	createdAt := now().UTC()
	absSnapDir, absErr := filepath.Abs(snapDir)
	if absErr != nil {
		absSnapDir = snapDir
	}
	vars := BuildSnapshotVars(p.Name, absSnapDir, p.Description, p.Variant, createdAt)

	// Run the workflow. On success or failure we still write a manifest.
	runErr := RunWorkflow(ctx, ExecParams{
		Cfg:                 p.Cfg,
		Registry:            p.Registry,
		BaseDir:             p.BaseDir,
		Workflow:            wf,
		Vars:                vars,
		Scope:               tpl.SnapshotScopeCreate,
		Stdout:              p.Stdout,
		Stderr:              p.Stderr,
		SkipConfirm:         p.SkipConfirm,
		NonInteractive:      p.NonInteractive,
		StepObserverFactory: p.StepObserverFactory,
	})

	status := StatusOk
	failedStep := ""
	switch {
	case runErr == nil:
		// keep StatusOk
	case errors.Is(runErr, context.Canceled), errors.Is(runErr, context.DeadlineExceeded):
		status = StatusInterrupted
		failedStep = runErr.Error()
	default:
		status = StatusFailed
		failedStep = runErr.Error()
	}

	artifacts, scanErr := ScanArtifacts(snapDir)
	if scanErr != nil {
		// Scan failure on a non-failed workflow demotes to "failed" so the
		// manifest does not advertise an inconsistent OK state. Keep the
		// stricter scan error as the failure cause.
		if status == StatusOk {
			status = StatusFailed
			failedStep = "artifact scan: " + scanErr.Error()
		}
		artifacts = nil
	}

	m := NewManifest(p.Name, now)
	m.CreatedAt = createdAt
	m.Description = p.Description
	m.Project = ProjectInfo{
		Name:       p.Cfg.Project.Name,
		ConfigHash: ProjectConfigHash(p.BaseDir),
		Services:   captureServiceSnapshots(p.Cfg.Services),
	}
	m.DevboxVersion = p.DevboxVersion
	m.Variant = p.Variant
	m.Artifacts = artifacts
	m.DevboxFiles = devboxFiles
	m.LastCreate = &LastCreate{
		At:         createdAt,
		Status:     status,
		FailedStep: failedStep,
	}

	manifestPath := ManifestPath(p.BaseDir, p.SnapCfg, p.Name)
	if err := SaveManifest(manifestPath, m); err != nil {
		// Preserve the original workflow / scan error if any; otherwise
		// surface the manifest write failure.
		if runErr == nil && scanErr == nil {
			return nil, fmt.Errorf("snapshot: write manifest: %w", err)
		}
		if p.Stderr != nil {
			_, _ = fmt.Fprintf(p.Stderr, "warning: could not write snapshot manifest: %v\n", err)
		}
	}

	res := &CreateResult{
		SnapshotDir:  snapDir,
		ManifestPath: manifestPath,
		Manifest:     m,
		Status:       status,
	}

	if status != StatusOk {
		// Restore the previous snapshot if we backed it up: remove the failed
		// attempt and rename the backup back so the user retains a working state.
		if backupDir != "" {
			if rmErr := os.RemoveAll(snapDir); rmErr != nil {
				if p.Stderr != nil {
					_, _ = fmt.Fprintf(p.Stderr, "warning: could not remove failed snapshot dir %s: %v; backup preserved at %s\n", snapDir, rmErr, backupDir)
				}
			} else if renameErr := os.Rename(backupDir, snapDir); renameErr != nil {
				if p.Stderr != nil {
					_, _ = fmt.Fprintf(p.Stderr, "warning: could not restore previous snapshot backup %s: %v\n", backupDir, renameErr)
				}
			} else {
				res.BackupRestored = true
			}
		}
		// Return the original workflow or scan error so the CLI sees the real failure cause.
		if runErr != nil {
			return res, runErr
		}
		if scanErr != nil {
			return res, scanErr
		}
	}

	// Success: remove the backup if we made one.
	if backupDir != "" {
		_ = os.RemoveAll(backupDir)
	}

	// Success: update the current pointer atomically.
	if err := WriteCurrent(p.BaseDir, p.Name); err != nil {
		return res, fmt.Errorf("snapshot: update current pointer: %w", err)
	}
	return res, nil
}

// confirmOverwrite invokes the caller-supplied prompt. A nil callback is
// treated as a refusal so non-interactive callers cannot accidentally
// overwrite an existing snapshot.
func confirmOverwrite(fn func() (bool, error)) (bool, error) {
	if fn == nil {
		return false, nil
	}
	return fn()
}

// captureServiceSnapshots renders the effective service map into a manifest
// slice sorted by name for deterministic output. A nil/empty map yields nil.
func captureServiceSnapshots(services map[string]config.ServiceConfig) []ServiceSnapshot {
	if len(services) == 0 {
		return nil
	}
	out := make([]ServiceSnapshot, 0, len(services))
	for name, svc := range services {
		out = append(out, ServiceSnapshot{Name: name, Enabled: svc.Enabled})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ProjectConfigHash reads the deploy state file and returns the project-level
// config_hash, or empty string if state is absent / unreadable.
func ProjectConfigHash(baseDir string) string {
	st, err := journal.Load(filepath.Join(baseDir, journal.DefaultRelPath))
	if err != nil || st == nil || st.Project == nil {
		return ""
	}
	return st.Project.ConfigHash
}
