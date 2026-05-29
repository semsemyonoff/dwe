package archive

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/core/workflow/snapshot/meta"
	"devbox-cli/internal/shared/pathsafe"
)

// unpack ----------------------------------------------------------------------

// Unpack extracts a .tar.gz archive into <snapshotsRoot>/<targetName>/ with
// strict safety contract. The caller is responsible for holding project
// locks. Extraction happens into a sibling staging dir; on any error the
// staging dir is removed and no partial directory survives at the final path.
//
// After extraction (and before renaming staging into the final position),
// artifacts listed in manifest.yml are re-hashed and compared. Missing or
// hash-mismatched artifacts trigger a single grouped confirmation via
// opts.ConfirmVerify; declined → staging is removed and an
// UnpackVerifyDeclinedError is returned. opts.NoVerify bypasses verification
// entirely (warning is still printed).
//
// opts.ConfirmOverwrite is invoked when the target directory already exists
// and AssumeYes is false. A nil callback is treated as a refusal.
func Unpack(tarPath, snapshotsRoot, targetName string, opts UnpackOptions) (*UnpackResult, error) {
	if err := meta.ValidateName(targetName); err != nil {
		return nil, err
	}
	if _, err := os.Stat(tarPath); err != nil {
		return nil, fmt.Errorf("unpack: %s: %w", tarPath, err)
	}
	if opts.Stderr == nil {
		return nil, errors.New("unpack: UnpackOptions.Stderr is required")
	}

	finalDir := filepath.Join(snapshotsRoot, targetName)
	if _, err := os.Stat(finalDir); err == nil {
		if !opts.AssumeYes {
			yes, cErr := confirmUnpackOverwrite(opts.ConfirmOverwrite)
			if cErr != nil {
				return nil, cErr
			}
			if !yes {
				return nil, &UnpackCancelledError{}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("unpack: stat final dir: %w", err)
	}

	if err := os.MkdirAll(snapshotsRoot, 0o755); err != nil {
		return nil, fmt.Errorf("unpack: create snapshots root: %w", err)
	}
	stagingDir, err := mkdirRandom(snapshotsRoot, ".unpack-")
	if err != nil {
		return nil, fmt.Errorf("unpack: create staging dir: %w", err)
	}
	cleanupStaging := func() { _ = os.RemoveAll(stagingDir) }

	if err := extractTarGz(tarPath, stagingDir); err != nil {
		cleanupStaging()
		return nil, err
	}

	m, err := meta.LoadManifest(filepath.Join(stagingDir, meta.ManifestFileName))
	if err != nil {
		cleanupStaging()
		return nil, fmt.Errorf("unpack: read manifest: %w", err)
	}

	// Artifact verification. Runs before the install renames so a declined
	// verification leaves the previous finalDir untouched.
	var (
		outcome VerificationOutcome
		report  ArtifactVerifyReport
	)
	if opts.NoVerify {
		_, _ = fmt.Fprintln(opts.Stderr, "warning: skipping artifact verification at user request (--no-verify)")
		outcome = VerificationSkipped
	} else {
		report, err = VerifyExtractedArtifacts(stagingDir, m)
		if err != nil {
			cleanupStaging()
			return nil, err
		}
		if report.Empty() {
			outcome = VerificationClean
		} else {
			printVerifyReport(opts.Stderr, report)
			if len(report.Missing) > 0 || len(report.HashMismatch) > 0 {
				yes := opts.AssumeYes
				if !yes {
					if opts.ConfirmVerify == nil {
						cleanupStaging()
						return nil, &UnpackVerifyDeclinedError{Report: report}
					}
					ok, cErr := opts.ConfirmVerify("Continue despite verification warnings?")
					if cErr != nil {
						cleanupStaging()
						return nil, cErr
					}
					yes = ok
				}
				if !yes {
					cleanupStaging()
					return nil, &UnpackVerifyDeclinedError{Report: report}
				}
			}
			outcome = VerificationWarned
		}
	}

	// Install atomically: if the target already exists, rename it to a
	// temporary backup path first, then rename staging into place, then remove
	// the backup. If the second rename fails we can restore the backup, so the
	// previous snapshot is not lost. (RemoveAll-then-rename would lose the old
	// snapshot on an OS error between the two operations.)
	//
	// backupDir is kept alive past the install renames so that a subsequent
	// SaveManifest failure can still restore the previous snapshot.
	var backupDir string
	if _, statErr := os.Stat(finalDir); statErr == nil {
		var randB [8]byte
		if _, err := rand.Read(randB[:]); err != nil {
			cleanupStaging()
			return nil, fmt.Errorf("unpack: generate backup name: %w", err)
		}
		backupDir = filepath.Join(snapshotsRoot, ".unpack-old-"+hex.EncodeToString(randB[:]))
		if err := os.Rename(finalDir, backupDir); err != nil {
			cleanupStaging()
			return nil, fmt.Errorf("unpack: backup existing dir: %w", err)
		}
		if err := os.Rename(stagingDir, finalDir); err != nil {
			// Roll back: restore the backup so the previous snapshot is not lost.
			if rErr := os.Rename(backupDir, finalDir); rErr != nil {
				cleanupStaging()
				return nil, fmt.Errorf("unpack: install: %w; also failed to restore previous snapshot from %s: %v", err, backupDir, rErr)
			}
			cleanupStaging()
			return nil, fmt.Errorf("unpack: install: %w", err)
		}
	} else if errors.Is(statErr, os.ErrNotExist) {
		if err := os.Rename(stagingDir, finalDir); err != nil {
			cleanupStaging()
			return nil, fmt.Errorf("unpack: install: %w", err)
		}
	} else {
		cleanupStaging()
		return nil, fmt.Errorf("unpack: stat final dir before install: %w", statErr)
	}

	// Normalize the manifest name to match the installed directory name when
	// unpacking with --as <different-name>. All downstream reads (list, inspect,
	// restore vars) derive the name from the manifest, not the directory.
	if m.Name != targetName {
		m.Name = targetName
		if err := meta.SaveManifest(filepath.Join(finalDir, meta.ManifestFileName), m); err != nil {
			_ = os.RemoveAll(finalDir)
			if backupDir != "" {
				if renameErr := os.Rename(backupDir, finalDir); renameErr != nil {
					return nil, fmt.Errorf("unpack: normalize manifest name: %w (previous snapshot preserved at %s — manual rename required)", err, backupDir)
				}
			}
			return nil, fmt.Errorf("unpack: normalize manifest name: %w", err)
		}
	}

	// Normalization succeeded (or was not needed); the previous-snapshot backup
	// is no longer needed.
	if backupDir != "" {
		_ = os.RemoveAll(backupDir)
	}

	return &UnpackResult{
		SnapshotDir:  finalDir,
		Manifest:     m,
		Verification: outcome,
		VerifyReport: report,
	}, nil
}

func confirmUnpackOverwrite(fn func() (bool, error)) (bool, error) {
	if fn == nil {
		return false, nil
	}
	return fn()
}

// extractTarGz walks the gzipped tar at tarPath and writes every entry under
// targetRoot, enforcing the archive-safety contract documented in
// docs/plans/2026-05-24-snapshot-subsystem.md Task 9.
func extractTarGz(tarPath, targetRoot string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("unpack: open %s: %w", tarPath, err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("unpack: gzip %s: %w", tarPath, err)
	}
	defer func() { _ = gz.Close() }()

	// Cap the total bytes read post-decompression. io.LimitReader wraps the
	// decompressor; tar.Reader.Next still calls Read on it. We add 1 so we can
	// distinguish "exactly at cap" (ok) from "over cap" (the next Read fails).
	limited := io.LimitReader(gz, maxUnpackBytes+1)
	tr := tar.NewReader(limited)

	var (
		totalBytes int64
		fileCount  int
	)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("unpack: tar: %w", err)
		}

		fileCount++
		if fileCount > maxUnpackFiles {
			return fmt.Errorf("unpack: archive exceeds maximum file count (%d)", maxUnpackFiles)
		}

		// Path safety: layered checks. The header name must be safe BEFORE
		// any join with targetRoot — filepath.Join collapses `..` away so
		// pathsafe.ContainedRel alone is insufficient against `../escape`.
		if hdr.Name == "" || hdr.Name == "." || hdr.Name == ".." {
			return &rejectedTarEntryError{Name: hdr.Name, Reason: "empty or current/parent reference"}
		}
		if filepath.IsAbs(hdr.Name) || strings.HasPrefix(hdr.Name, "/") {
			return &rejectedTarEntryError{Name: hdr.Name, Reason: "absolute path"}
		}
		if !filepath.IsLocal(hdr.Name) {
			return &rejectedTarEntryError{Name: hdr.Name, Reason: "non-local path (escapes root)"}
		}

		// Type allowlist.
		switch hdr.Typeflag {
		case tar.TypeReg, '\x00', tar.TypeDir: // '\x00' is the deprecated TypeRegA emitted by old GNU tar
			// fall through
		case tar.TypeSymlink:
			return &rejectedTarEntryError{Name: hdr.Name, Reason: "symlink entry"}
		case tar.TypeLink:
			return &rejectedTarEntryError{Name: hdr.Name, Reason: "hardlink entry"}
		case tar.TypeChar, tar.TypeBlock:
			return &rejectedTarEntryError{Name: hdr.Name, Reason: "device entry"}
		case tar.TypeFifo:
			return &rejectedTarEntryError{Name: hdr.Name, Reason: "fifo entry"}
		default:
			return &rejectedTarEntryError{Name: hdr.Name, Reason: fmt.Sprintf("disallowed typeflag %q", hdr.Typeflag)}
		}

		full := filepath.Join(targetRoot, hdr.Name)
		if _, err := pathsafe.ContainedRel(targetRoot, full); err != nil {
			return &rejectedTarEntryError{Name: hdr.Name, Reason: "escapes target root"}
		}

		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(full, 0o755); err != nil {
				return fmt.Errorf("unpack: mkdir %q: %w", hdr.Name, err)
			}
			continue
		}

		// Regular file. Make parent dirs, then refuse to overwrite (O_EXCL).
		// Pre-check declared size so we don't write the file at all if it would exceed the cap.
		// Use subtraction to avoid int64 overflow when hdr.Size is crafted near MaxInt64.
		// Check hdr.Size > maxUnpackBytes first so the subtraction never underflows.
		if hdr.Size < 0 || hdr.Size > maxUnpackBytes || totalBytes > maxUnpackBytes-hdr.Size {
			return fmt.Errorf("unpack: archive exceeds maximum size (%d bytes)", maxUnpackBytes)
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("unpack: mkdir for %q: %w", hdr.Name, err)
		}
		out, err := os.OpenFile(full, os.O_CREATE|os.O_WRONLY|os.O_EXCL, hdr.FileInfo().Mode()&0o777)
		if err != nil {
			return fmt.Errorf("unpack: create %q: %w", hdr.Name, err)
		}
		n, copyErr := io.Copy(out, tr)
		closeErr := out.Close()
		if copyErr != nil {
			return fmt.Errorf("unpack: write %q: %w", hdr.Name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("unpack: close %q: %w", hdr.Name, closeErr)
		}
		totalBytes += n
		if totalBytes > maxUnpackBytes {
			return fmt.Errorf("unpack: archive exceeds maximum size (%d bytes)", maxUnpackBytes)
		}
	}

	return nil
}

// mkdirRandom creates a sibling staging directory under parent with the given
// prefix and a random suffix. Uses crypto/rand so concurrent unpacks cannot
// collide. Returns the absolute path of the created directory.
func mkdirRandom(parent, prefix string) (string, error) {
	for range 16 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		candidate := filepath.Join(parent, prefix+hex.EncodeToString(b[:]))
		if err := os.Mkdir(candidate, 0o755); err == nil {
			return candidate, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("mkdirRandom: too many collisions")
}
