// Package archive implements the tar I/O layer of the snapshot subsystem:
// Pack, Unpack, VerifyExtractedArtifacts, and ReadManifestFromTar. It is
// behaviour-free above the descriptor types defined in
// internal/core/workflow/snapshot/meta — everything in this package operates
// on bytes, paths, and meta.Manifest.
package archive

import (
	"fmt"
	"io"

	"devbox-cli/internal/core/workflow/snapshot/meta"
)

// Archive safety constants. Constants for now — promote to SnapshotConfig
// only if a real tuning need emerges.
const (
	// maxUnpackBytes caps the total decompressed payload a single Unpack will
	// extract before aborting (50 GiB).
	maxUnpackBytes int64 = 50 << 30
	// maxUnpackFiles caps the number of entries a single Unpack will accept
	// (file-count overflow defense, e.g. against tar bombs).
	maxUnpackFiles = 100_000
)

// rejectedTarEntryError describes a single rejected tar entry — used by the
// archive-safety tests to assert on the specific reason.
type rejectedTarEntryError struct {
	Name   string
	Reason string
}

func (e *rejectedTarEntryError) Error() string {
	return fmt.Sprintf("rejected tar entry %q: %s", e.Name, e.Reason)
}

// PackResult summarises a successful Pack.
type PackResult struct {
	// OutPath is the absolute path of the written .tar.gz.
	OutPath string
	// Sha256 is the lowercase hex sha256 of the tar.gz file.
	Sha256 string
	// SizeBytes is the size of the .tar.gz file.
	SizeBytes int64
	// SkippedEntries lists snapshot-relative paths excluded by globs.
	SkippedEntries []string
}

// UnpackResult summarises a successful Unpack.
type UnpackResult struct {
	// SnapshotDir is the absolute final directory the archive was extracted into.
	SnapshotDir string
	// Manifest is the manifest read from the unpacked snapshot.
	Manifest *meta.Manifest
	// Verification is the discriminator for the verification outcome.
	Verification VerificationOutcome
	// VerifyReport carries the per-group artifact verification result.
	// Zero value when Verification == VerificationSkipped.
	VerifyReport ArtifactVerifyReport
}

// VerificationOutcome discriminates the artifact verification result.
type VerificationOutcome int

const (
	// VerificationSkipped indicates UnpackOptions.NoVerify=true bypassed the check.
	VerificationSkipped VerificationOutcome = iota
	// VerificationClean indicates verification ran and all three groups were empty.
	VerificationClean
	// VerificationWarned indicates verification ran, at least one group was non-empty,
	// and the user (or AssumeYes) accepted continuing.
	VerificationWarned
)

// ArtifactVerifyReport groups verification findings between manifest.yml and
// the on-disk artifacts of an unpacked snapshot.
type ArtifactVerifyReport struct {
	// Missing lists snapshot-relative artifact paths declared in the manifest
	// but not present in the extracted staging directory.
	Missing []string
	// HashMismatch lists artifacts present on disk whose sha256 differs from
	// the manifest entry.
	HashMismatch []ArtifactHashMismatch
	// Extra lists snapshot-relative paths found on disk that have no
	// corresponding entry in the manifest.
	Extra []string
}

// ArtifactHashMismatch describes one artifact whose on-disk sha256 differs
// from the manifest record.
type ArtifactHashMismatch struct {
	Path           string
	ExpectedSha256 string
	ActualSha256   string
}

// UnpackOptions configures Unpack behavior. The two confirmation callbacks are
// intentionally separate: AssumeYes collapses both to "yes" without conflating
// them, and the CLI can theme each prompt independently.
type UnpackOptions struct {
	// NoVerify bypasses artifact verification entirely (still emits the
	// "skipping artifact verification" warning to Stderr).
	NoVerify bool
	// AssumeYes skips both the overwrite and verify confirmation prompts.
	AssumeYes bool
	// ConfirmOverwrite is invoked when the target directory already exists.
	// Nil → treated as a refusal.
	ConfirmOverwrite func() (bool, error)
	// ConfirmVerify is invoked when verification finds Missing or HashMismatch
	// groups non-empty. Nil → treated as a refusal.
	ConfirmVerify func(prompt string) (bool, error)
	// Stderr is the writer for warnings and verification notices. Required.
	Stderr io.Writer
}

// UnpackVerifyDeclinedError is returned when the user declines to accept the
// verification warnings. Carries the typed report so callers can re-render.
type UnpackVerifyDeclinedError struct {
	Report ArtifactVerifyReport
}

func (e *UnpackVerifyDeclinedError) Error() string {
	return "snapshot unpack declined after verification warnings"
}

// ExitCode returns 0 so fang suppresses an error banner.
func (e *UnpackVerifyDeclinedError) ExitCode() int { return 0 }

// UnpackCancelledError is returned when the user declines to overwrite an
// existing target directory.
type UnpackCancelledError struct{}

func (e *UnpackCancelledError) Error() string { return "snapshot unpack cancelled" }

// ExitCode returns 0 so fang suppresses an error banner.
func (e *UnpackCancelledError) ExitCode() int { return 0 }
