package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"devbox-cli/internal/pathsafe"
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
	Manifest *Manifest
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

// glob matching ---------------------------------------------------------------

// globToRegexp converts a doublestar-style glob (`**`, `*`, `?`) into an
// anchored regexp. The pattern matches against forward-slash separated paths.
//
//   - `**` matches zero or more path segments (including separators).
//   - `*`  matches any run of characters except `/`.
//   - `?`  matches a single character except `/`.
//   - All other characters are taken literally.
func globToRegexp(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				// `**/foo` → optional path prefix; `(?:.*/)?` prevents false
				// matches like `barfoo` that `.*foo` would accept.
				b.WriteString("(?:.*/)?")
				i++
				// consume optional trailing `/` so that `foo/**` matches `foo` too
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
				}
				continue
			}
			b.WriteString("[^/]*")
		case '/':
			// `foo/**` → optional `/...` so bare `foo` matches too.
			// `a/**/b` → required `/` then optional intermediates so `ab` (no separator) is rejected.
			if i+2 < len(glob) && glob[i+1] == '*' && glob[i+2] == '*' {
				i += 2 // skip both '*'
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++ // consume trailing '/'
				}
				if i+1 < len(glob) {
					// Middle position: a segment follows — the slash separator is mandatory.
					b.WriteString("/(?:.*/)?")
				} else {
					// Trailing position: nothing follows — the whole `/...` is optional.
					b.WriteString("(?:/.*)?")
				}
				continue
			}
			b.WriteByte('/')
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// excludesMatch reports whether any glob in excludes matches the given
// snapshot-relative POSIX path (slash-separated). Invalid globs cause an
// error so a malformed config surfaces at pack time, not silently.
func excludesMatch(excludes []string, relPath string) (bool, error) {
	for _, g := range excludes {
		re, err := globToRegexp(g)
		if err != nil {
			return false, fmt.Errorf("invalid exclude glob %q: %w", g, err)
		}
		if re.MatchString(relPath) {
			return true, nil
		}
	}
	return false, nil
}

// resolveExistingAncestor resolves symlinks on the deepest existing ancestor of
// dir and appends the non-existing tail so the result is a canonical physical
// path even when part of the hierarchy does not yet exist. This is needed for
// the containment check in Pack: if the user supplies --out /tmp/lnk/new/a.tar.gz
// where /tmp/lnk is a symlink into the snapshot directory and /tmp/lnk/new does
// not exist yet, EvalSymlinks on the full parent would fail and the check would
// be silently skipped — letting MkdirAll create the missing component inside the
// snapshot directory.
func resolveExistingAncestor(dir string) string {
	tail := ""
	p := dir
	for {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			if tail == "" {
				return resolved
			}
			return filepath.Join(resolved, tail)
		}
		parent := filepath.Dir(p)
		if parent == p {
			// Reached filesystem root without resolving anything.
			return dir
		}
		if tail == "" {
			tail = filepath.Base(p)
		} else {
			tail = filepath.Join(filepath.Base(p), tail)
		}
		p = parent
	}
}

// pack ------------------------------------------------------------------------

// Pack writes a .tar.gz archive of the snapshot directory. The caller is
// responsible for holding project locks across the pack run (the snapshot
// directory must not be mutating concurrently — otherwise the archive can
// be corrupt or truncated).
func Pack(snapshotsRoot, snapDir, name string, outPath string, excludes []string) (*PackResult, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if st, err := os.Stat(snapDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("snapshot %q: not found at %s", name, snapDir)
		}
		return nil, fmt.Errorf("snapshot %q: stat %s: %w", name, snapDir, err)
	} else if !st.IsDir() {
		return nil, fmt.Errorf("snapshot %q: %s is not a directory", name, snapDir)
	}

	if outPath == "" {
		outPath = filepath.Join(snapshotsRoot, name+".tar.gz")
	}

	// Reject output paths inside the snapshot directory: the temp file created
	// in filepath.Dir(outPath) would be discovered by the walk and either
	// produce a corrupt archive or trigger a disk-filling feedback loop.
	absSnap, err := filepath.Abs(snapDir)
	if err != nil {
		return nil, fmt.Errorf("snapshot pack: resolve snapshot dir: %w", err)
	}
	absOut, err := filepath.Abs(outPath)
	if err != nil {
		return nil, fmt.Errorf("snapshot pack: resolve output path: %w", err)
	}
	// Resolve symlinks so a symlinked output directory that physically
	// resolves inside snapDir is still caught by the containment check.
	if resolved, err := filepath.EvalSymlinks(absSnap); err == nil {
		absSnap = resolved
	}
	// Resolve the deepest existing ancestor of the output parent so that a
	// path like --out /tmp/link→snapDir/new/archive.tar.gz is caught even
	// when the "new/" component does not exist yet (EvalSymlinks would fail
	// on a non-existent path, silently skipping the resolution).
	resolvedParent := resolveExistingAncestor(filepath.Dir(absOut))
	absOut = filepath.Join(resolvedParent, filepath.Base(absOut))
	sep := string(filepath.Separator)
	if absOut == absSnap || strings.HasPrefix(absOut, absSnap+sep) {
		return nil, fmt.Errorf("snapshot pack: output path %s is inside the snapshot directory %s; use a path outside the snapshot", outPath, snapDir)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return nil, fmt.Errorf("snapshot pack: create out dir: %w", err)
	}

	// Write to a temp file in the same dir, hashing as we go; rename on success.
	tmp, err := os.CreateTemp(filepath.Dir(outPath), "."+filepath.Base(outPath)+".*.tmp")
	if err != nil {
		return nil, fmt.Errorf("snapshot pack: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	hasher := sha256.New()
	mw := io.MultiWriter(tmp, hasher)
	gw := gzip.NewWriter(mw)
	tw := tar.NewWriter(gw)

	skipped := []string{}
	walkErr := filepath.Walk(snapDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == snapDir {
			return nil
		}
		rel, err := filepath.Rel(snapDir, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)

		// Reject symlinks at pack time (they cannot survive the archive-safety
		// contract on unpack).
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("pack: snapshot contains symlink at %q; not allowed", relSlash)
		}

		if matched, err := excludesMatch(excludes, relSlash); err != nil {
			return err
		} else if matched {
			if info.IsDir() {
				skipped = append(skipped, relSlash+"/")
				return filepath.SkipDir
			}
			skipped = append(skipped, relSlash)
			return nil
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("pack: header for %q: %w", relSlash, err)
		}
		hdr.Name = relSlash
		if info.IsDir() {
			hdr.Name += "/"
		}
		// Strip uid/gid/uname/gname to keep archives portable across machines.
		hdr.Uid, hdr.Gid = 0, 0
		hdr.Uname, hdr.Gname = "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("pack: write header %q: %w", relSlash, err)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("pack: open %q: %w", relSlash, err)
		}
		_, err = io.Copy(tw, f)
		closeErr := f.Close()
		if err != nil {
			return fmt.Errorf("pack: write %q: %w", relSlash, err)
		}
		if closeErr != nil {
			return fmt.Errorf("pack: close %q: %w", relSlash, closeErr)
		}
		return nil
	})

	if walkErr != nil {
		_ = tw.Close()
		_ = gw.Close()
		_ = tmp.Close()
		cleanup()
		return nil, walkErr
	}
	if err := tw.Close(); err != nil {
		_ = gw.Close()
		_ = tmp.Close()
		cleanup()
		return nil, fmt.Errorf("snapshot pack: close tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		_ = tmp.Close()
		cleanup()
		return nil, fmt.Errorf("snapshot pack: close gzip: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return nil, fmt.Errorf("snapshot pack: close temp: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		cleanup()
		return nil, fmt.Errorf("snapshot pack: chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		cleanup()
		return nil, fmt.Errorf("snapshot pack: rename: %w", err)
	}

	sum := hex.EncodeToString(hasher.Sum(nil))

	st, err := os.Stat(outPath)
	if err != nil {
		return nil, fmt.Errorf("snapshot pack: stat out: %w", err)
	}

	return &PackResult{
		OutPath:        outPath,
		Sha256:         sum,
		SizeBytes:      st.Size(),
		SkippedEntries: skipped,
	}, nil
}

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
	if err := ValidateName(targetName); err != nil {
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

	m, err := LoadManifest(filepath.Join(stagingDir, ManifestFileName))
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
		if err := SaveManifest(filepath.Join(finalDir, ManifestFileName), m); err != nil {
			_ = os.RemoveAll(finalDir)
			if backupDir != "" {
				_ = os.Rename(backupDir, finalDir)
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

// UnpackCancelledError is returned when the user declines to overwrite an
// existing target directory.
type UnpackCancelledError struct{}

func (e *UnpackCancelledError) Error() string { return "snapshot unpack cancelled" }

// ExitCode returns 0 so fang suppresses an error banner.
func (e *UnpackCancelledError) ExitCode() int { return 0 }

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
		case tar.TypeReg, tar.TypeDir:
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
		if hdr.Size < 0 || totalBytes > maxUnpackBytes-hdr.Size {
			return fmt.Errorf("unpack: archive exceeds maximum size (%d bytes)", maxUnpackBytes)
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("unpack: mkdir for %q: %w", hdr.Name, err)
		}
		out, err := os.OpenFile(full, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
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
