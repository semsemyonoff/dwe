package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/shared/pathsafe"
)

// Empty reports whether all three verification groups are empty.
func (r ArtifactVerifyReport) Empty() bool {
	return len(r.Missing) == 0 && len(r.HashMismatch) == 0 && len(r.Extra) == 0
}

// VerifyExtractedArtifacts re-hashes every artifact recorded in the manifest
// against the on-disk staging directory and compares the result. It also
// walks the staging directory for files that are absent from the manifest
// (the "Extra" group).
//
// Manifest-declared paths are validated for containment before being opened:
// an attacker-crafted manifest cannot make the verifier read files outside
// the staging directory.
func VerifyExtractedArtifacts(stagingDir string, m *Manifest) (ArtifactVerifyReport, error) {
	if m == nil {
		return ArtifactVerifyReport{}, errors.New("verify: nil manifest")
	}
	absStaging, err := filepath.Abs(stagingDir)
	if err != nil {
		return ArtifactVerifyReport{}, fmt.Errorf("verify: abs staging: %w", err)
	}

	declared := make(map[string]ArtifactInfo, len(m.Artifacts))
	var report ArtifactVerifyReport

	for _, a := range m.Artifacts {
		// Path safety gate: archive-controlled paths must be local and
		// contained before any open. Reject before opening (not as "Missing").
		if a.Path == "" || filepath.IsAbs(a.Path) || strings.HasPrefix(a.Path, "/") {
			return ArtifactVerifyReport{}, fmt.Errorf("verify: manifest artifact path %q is absolute or empty", a.Path)
		}
		if !filepath.IsLocal(a.Path) {
			return ArtifactVerifyReport{}, fmt.Errorf("verify: manifest artifact path %q escapes staging", a.Path)
		}
		absChild := filepath.Join(absStaging, filepath.FromSlash(a.Path))
		if _, err := pathsafe.ContainedRel(absStaging, absChild); err != nil {
			return ArtifactVerifyReport{}, fmt.Errorf("verify: manifest artifact path %q escapes staging: %w", a.Path, err)
		}
		declared[a.Path] = a

		f, err := os.Open(absChild)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				report.Missing = append(report.Missing, a.Path)
				continue
			}
			return ArtifactVerifyReport{}, fmt.Errorf("verify: open %q: %w", a.Path, err)
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return ArtifactVerifyReport{}, fmt.Errorf("verify: read %q: %w", a.Path, copyErr)
		}
		if closeErr != nil {
			return ArtifactVerifyReport{}, fmt.Errorf("verify: close %q: %w", a.Path, closeErr)
		}
		got := hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(got, a.Sha256) {
			report.HashMismatch = append(report.HashMismatch, ArtifactHashMismatch{
				Path:           a.Path,
				ExpectedSha256: a.Sha256,
				ActualSha256:   got,
			})
		}
	}

	// Extras: anything on disk under stagingDir that is not the manifest and
	// not under DevboxSubdir, and not declared.
	scanned, err := ScanArtifacts(stagingDir)
	if err != nil {
		return ArtifactVerifyReport{}, fmt.Errorf("verify: scan staging: %w", err)
	}
	for _, s := range scanned {
		if _, ok := declared[s.Path]; !ok {
			report.Extra = append(report.Extra, s.Path)
		}
	}
	return report, nil
}

// printVerifyReport writes the per-line warnings to w in the documented
// wording. Order: Missing → HashMismatch → Extra (Missing first because it
// is the most likely to surface partial-archive corruption).
func printVerifyReport(w io.Writer, r ArtifactVerifyReport) {
	for _, p := range r.Missing {
		_, _ = fmt.Fprintf(w, "warning: artifact %q listed in manifest is missing from archive\n", p)
	}
	for _, m := range r.HashMismatch {
		_, _ = fmt.Fprintf(w, "warning: artifact %q sha256 mismatch (manifest=%s, actual=%s)\n",
			m.Path, m.ExpectedSha256, m.ActualSha256)
	}
	for _, p := range r.Extra {
		_, _ = fmt.Fprintf(w, "info: archive contains %q not listed in manifest\n", p)
	}
}
