package snapshot

import (
	"context"
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
// On workflow failure: the snapshot directory is kept; the manifest is
// written with last_create.status set to "failed" or "interrupted"; the
// current pointer is NOT touched. Callers convert ctx.Err()==Canceled into
// the interrupted exit code.
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
		if err := os.RemoveAll(snapDir); err != nil {
			return nil, fmt.Errorf("snapshot: remove existing dir: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("snapshot: stat existing dir: %w", statErr)
	}

	if err := os.MkdirAll(filepath.Join(snapDir, DevboxSubdir), 0o755); err != nil {
		return nil, fmt.Errorf("snapshot: create snapshot dir: %w", err)
	}

	devboxFiles, err := captureDevboxFiles(p.BaseDir, snapDir)
	if err != nil {
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
		Cfg:            p.Cfg,
		Registry:       p.Registry,
		BaseDir:        p.BaseDir,
		Workflow:       wf,
		Vars:           vars,
		Scope:          tpl.SnapshotScopeCreate,
		Stdout:         p.Stdout,
		Stderr:         p.Stderr,
		SkipConfirm:    p.SkipConfirm,
		NonInteractive: p.NonInteractive,
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
		// Return the original workflow or scan error so the CLI sees the
		// real failure cause; the manifest is already on disk.
		if runErr != nil {
			return res, runErr
		}
		if scanErr != nil {
			return res, scanErr
		}
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

// captureDevboxFiles copies devbox/local.yml and the deploy state file from
// baseDir into <snapDir>/devbox/. Missing source files are skipped silently
// — neither file is mandatory at create time.
func captureDevboxFiles(baseDir, snapDir string) (DevboxFiles, error) {
	var df DevboxFiles
	targets := []struct {
		src    string
		dstRel string
		field  *string
	}{
		{filepath.Join(baseDir, "devbox", "local.yml"), filepath.Join(DevboxSubdir, "local.yml"), &df.LocalYML},
		{filepath.Join(baseDir, journal.DefaultRelPath), filepath.Join(DevboxSubdir, "deploy-state.yml"), &df.DeployState},
	}
	for _, t := range targets {
		ok, err := copyFileIfExists(t.src, filepath.Join(snapDir, t.dstRel))
		if err != nil {
			return df, fmt.Errorf("snapshot: capture %s: %w", t.src, err)
		}
		if ok {
			*t.field = filepath.ToSlash(t.dstRel)
		}
	}
	return df, nil
}

// copyFileIfExists copies src to dst when src exists; returns (false, nil)
// when src is missing.
func copyFileIfExists(src, dst string) (bool, error) {
	in, err := os.Open(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return false, err
	}
	if err := out.Close(); err != nil {
		return false, err
	}
	return true, nil
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
