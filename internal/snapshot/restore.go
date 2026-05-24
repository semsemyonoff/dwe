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
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/usercommands/registry"
)

// RestoreParams describes one `devbox snapshot restore` (or rollback)
// invocation. The caller is responsible for acquiring project locks before
// calling Restore.
type RestoreParams struct {
	Cfg            *config.DevboxConfig
	SnapCfg        *config.SnapshotConfig
	Registry       *registry.Registry
	BaseDir        string
	Name           string
	SkipConfirm    bool
	NonInteractive bool
	Now            func() time.Time
	Stdout         io.Writer
	Stderr         io.Writer
	// ConfirmRestore is invoked in interactive mode after manifest verification
	// passes. Returning (false, nil) is treated as a user cancellation. A nil
	// callback is treated as a refusal so non-interactive callers cannot
	// accidentally restore — pass SkipConfirm: true to bypass.
	ConfirmRestore func(*Manifest, bool) (bool, error)
	// Operation is the user-visible operation label ("restore" or "rollback")
	// used in error messages. Defaults to "restore".
	Operation string
}

// RestoreCancelledError is returned when the user declines the restore
// confirmation. Treated as a non-error exit by the CLI layer.
type RestoreCancelledError struct{}

func (e *RestoreCancelledError) Error() string { return "snapshot restore cancelled" }

// ExitCode returns 0 so fang suppresses an error banner — cancellation is
// intentional.
func (e *RestoreCancelledError) ExitCode() int { return 0 }

// RestoreBlockedError is returned when the manifest's config_hash diverges
// from the current project config_hash and require_matching_config is set.
type RestoreBlockedError struct {
	Name         string
	ManifestHash string
	CurrentHash  string
}

func (e *RestoreBlockedError) Error() string {
	return fmt.Sprintf("snapshot %q: config_hash mismatch (manifest=%s, current=%s); restore blocked by require_matching_config",
		e.Name, e.ManifestHash, e.CurrentHash)
}

// RestoreResult summarises the outcome of a Restore attempt.
type RestoreResult struct {
	SnapshotDir  string
	ManifestPath string
	Manifest     *Manifest
	Status       string
	DurationMs   int64
	// BackupDir is the path the pre-restore backup was written to (empty when
	// no devbox files were captured).
	BackupDir string
}

// Restore runs the restore workflow for p.Name and swaps the project's devbox
// files in place.
//
// Flow:
//
//  1. Validate name; load manifest from the snapshot dir.
//  2. Verify manifest.Project.Name == cfg.Project.Name (always blocks).
//  3. Compare manifest.Project.ConfigHash with the current project config_hash:
//     - empty manifest hash → treated as a match (never blocks).
//     - mismatch + require_matching_config → RestoreBlockedError.
//     - mismatch otherwise → warning on Stderr; restore proceeds.
//  4. Optionally confirm (skipped when SkipConfirm or callback returns true).
//  5. Back up the working-copy devbox files into .devbox/snapshots/.pre-restore-backup/.
//  6. Restore devbox files from <snap>/devbox/ over the working copies.
//  7. Run the restore workflow under SnapshotScopeRestoreOrRemove.
//  8. On success: update current pointer; record last_restore.status="ok".
//  9. On failure / SIGINT: leave current pointer untouched; record
//     last_restore.status in {"failed","interrupted"} with failed_step.
func Restore(ctx context.Context, p RestoreParams) (*RestoreResult, error) {
	if err := ValidateName(p.Name); err != nil {
		return nil, err
	}
	if p.Cfg == nil {
		return nil, errors.New("snapshot: devbox config is required")
	}
	if p.SnapCfg == nil {
		return nil, errors.New("snapshot: snapshot config not loaded (missing devbox/snapshot.yml)")
	}
	if p.Registry == nil {
		return nil, errors.New("snapshot: user-command registry is required")
	}

	now := p.Now
	if now == nil {
		now = time.Now
	}

	snapDir := SnapshotDir(p.BaseDir, p.SnapCfg, p.Name)
	if _, err := os.Stat(snapDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("snapshot %q: not found at %s", p.Name, snapDir)
		}
		return nil, fmt.Errorf("snapshot %q: stat %s: %w", p.Name, snapDir, err)
	}
	manifestPath := ManifestPath(p.BaseDir, p.SnapCfg, p.Name)
	m, err := LoadManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("snapshot %q: load manifest: %w", p.Name, err)
	}

	if m.Project.Name != "" && p.Cfg.Project.Name != "" && m.Project.Name != p.Cfg.Project.Name {
		return nil, fmt.Errorf("snapshot %q: project mismatch (manifest=%q, current=%q)",
			p.Name, m.Project.Name, p.Cfg.Project.Name)
	}

	currentHash := ProjectConfigHash(p.BaseDir)
	diverged := m.Project.ConfigHash != "" && currentHash != "" && m.Project.ConfigHash != currentHash
	if diverged {
		if p.SnapCfg.RequireMatchingConfig {
			return nil, &RestoreBlockedError{
				Name:         p.Name,
				ManifestHash: m.Project.ConfigHash,
				CurrentHash:  currentHash,
			}
		}
		if p.Stderr != nil {
			_, _ = fmt.Fprintf(p.Stderr, "warning: snapshot %q config_hash %s differs from current %s; restoring anyway\n",
				p.Name, m.Project.ConfigHash, currentHash)
		}
	}

	// Validate the workflow selection before touching the filesystem so that a
	// missing restore: block or empty steps list fails fast without overwriting
	// devbox files.
	wf, err := SelectWorkflow(p.SnapCfg, "restore", m.Variant)
	if err != nil {
		return nil, fmt.Errorf("snapshot %q: %w", p.Name, err)
	}
	if len(wf.Steps) == 0 {
		return nil, fmt.Errorf("snapshot %q: restore workflow has no steps; add steps to restore: in devbox/snapshot.yml", p.Name)
	}

	if !p.SkipConfirm {
		ok, cErr := confirmRestore(p.ConfirmRestore, m, diverged)
		if cErr != nil {
			return nil, cErr
		}
		if !ok {
			return nil, &RestoreCancelledError{}
		}
	}

	backupDir, err := writePreRestoreBackup(p.BaseDir)
	if err != nil {
		return nil, fmt.Errorf("snapshot %q: write pre-restore backup: %w", p.Name, err)
	}

	if err := restoreDevboxFiles(snapDir, p.BaseDir); err != nil {
		if p.Stderr != nil {
			_, _ = fmt.Fprintf(p.Stderr,
				"hint: pre-restore devbox files backed up under %s\n", backupDir)
		}
		return nil, fmt.Errorf("snapshot %q: restore devbox files: %w", p.Name, err)
	}

	absSnapDir, absErr := filepath.Abs(snapDir)
	if absErr != nil {
		absSnapDir = snapDir
	}
	vars := BuildSnapshotVars(m.Name, absSnapDir, m.Description, m.Variant, m.CreatedAt)

	start := now()
	runErr := RunWorkflow(ctx, ExecParams{
		Cfg:            p.Cfg,
		Registry:       p.Registry,
		BaseDir:        p.BaseDir,
		Workflow:       wf,
		Vars:           vars,
		Scope:          tpl.SnapshotScopeRestoreOrRemove,
		Stdout:         p.Stdout,
		Stderr:         p.Stderr,
		SkipConfirm:    p.SkipConfirm,
		NonInteractive: p.NonInteractive,
	})
	finishedAt := now()
	durationMs := finishedAt.Sub(start).Milliseconds()

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

	m.LastRestore = &LastRestore{
		At:         finishedAt.UTC(),
		Status:     status,
		DurationMs: durationMs,
		FailedStep: failedStep,
	}
	if err := SaveManifest(manifestPath, m); err != nil {
		// Preserve any workflow error; otherwise surface the manifest write
		// failure as the cause.
		if runErr == nil {
			return nil, fmt.Errorf("snapshot %q: write manifest: %w", p.Name, err)
		}
	}

	res := &RestoreResult{
		SnapshotDir:  snapDir,
		ManifestPath: manifestPath,
		Manifest:     m,
		Status:       status,
		DurationMs:   durationMs,
		BackupDir:    backupDir,
	}

	if status != StatusOk {
		if p.Stderr != nil {
			_, _ = fmt.Fprintf(p.Stderr,
				"hint: pre-restore devbox files preserved under %s for manual recovery\n",
				backupDir)
		}
		return res, runErr
	}

	if err := WriteCurrent(p.BaseDir, p.Name); err != nil {
		return res, fmt.Errorf("snapshot %q: update current pointer: %w", p.Name, err)
	}
	return res, nil
}

// Rollback resolves cfg.RollbackTarget and dispatches to Restore. The
// Operation label is set to "rollback" so notification / error surfaces can
// distinguish it from a direct restore.
func Rollback(ctx context.Context, p RestoreParams) (*RestoreResult, error) {
	if p.SnapCfg == nil {
		return nil, errors.New("snapshot: snapshot config not loaded (missing devbox/snapshot.yml)")
	}
	target := p.SnapCfg.RollbackTarget
	if target == "" {
		return nil, errors.New("snapshot: rollback_target is not set in devbox/snapshot.yml")
	}
	p.Name = target
	p.Operation = "rollback"
	return Restore(ctx, p)
}

func confirmRestore(fn func(*Manifest, bool) (bool, error), m *Manifest, diverged bool) (bool, error) {
	if fn == nil {
		return false, nil
	}
	return fn(m, diverged)
}

// writePreRestoreBackup snapshots the working-copy devbox/local.yml and
// .devbox/deploy/state.yml into <stateDir>/.pre-restore-backup/, replacing any
// previous backup atomically (write each file via writeFileAtomic). Missing
// source files are skipped silently.
func writePreRestoreBackup(baseDir string) (string, error) {
	backupDir := PreRestoreBackup(baseDir)
	// Remove the previous backup so stale files from an earlier restore can't
	// confuse manual recovery. We do not need atomicity here: each individual
	// file write below is atomic via writeFileAtomic.
	if err := os.RemoveAll(backupDir); err != nil {
		return "", fmt.Errorf("remove previous backup: %w", err)
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	candidates := []struct {
		src    string
		dstRel string
	}{
		{filepath.Join(baseDir, "devbox", "local.yml"), "local.yml"},
		{filepath.Join(baseDir, journal.DefaultRelPath), "deploy-state.yml"},
	}
	for _, c := range candidates {
		data, err := os.ReadFile(c.src)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("read %s: %w", c.src, err)
		}
		if err := writeFileAtomic(filepath.Join(backupDir, c.dstRel), data, 0o644); err != nil {
			return "", fmt.Errorf("write backup %s: %w", c.dstRel, err)
		}
	}
	return backupDir, nil
}

// restoreDevboxFiles copies <snapDir>/devbox/local.yml and
// <snapDir>/devbox/deploy-state.yml over the working-copy paths in baseDir.
// Each destination write is atomic. Source files missing from the snapshot
// are skipped — the snapshot is not required to carry both files.
//
// The manifest is consulted opportunistically (to pick the canonical relative
// paths) but defaults to the standard layout when manifest fields are empty.
func restoreDevboxFiles(snapDir, baseDir string) error {
	type pair struct{ src, dst string }
	pairs := []pair{
		{filepath.Join(snapDir, DevboxSubdir, "local.yml"), filepath.Join(baseDir, "devbox", "local.yml")},
		{filepath.Join(snapDir, DevboxSubdir, "deploy-state.yml"), filepath.Join(baseDir, journal.DefaultRelPath)},
	}
	for _, p := range pairs {
		data, err := os.ReadFile(p.src)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read %s: %w", p.src, err)
		}
		if err := os.MkdirAll(filepath.Dir(p.dst), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(p.dst), err)
		}
		if err := writeFileAtomic(p.dst, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", p.dst, err)
		}
	}
	return nil
}
