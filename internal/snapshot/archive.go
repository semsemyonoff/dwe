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
	// ChecksumPath is the absolute path of the .sha256 sidecar.
	ChecksumPath string
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
	// VerifiedChecksum is true when a .sha256 sidecar was present and verified.
	VerifiedChecksum bool
}

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
				b.WriteString(".*")
				i++
				// consume optional trailing `/` so that `foo/**` matches `foo` too
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
				}
				continue
			}
			b.WriteString("[^/]*")
		case '/':
			// `foo/**` → make the `/` (and the `**` it heads) optional so the
			// bare `foo` segment matches the prefix too.
			if i+2 < len(glob) && glob[i+1] == '*' && glob[i+2] == '*' {
				b.WriteString("(?:/.*)?")
				i += 2
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
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

// pack ------------------------------------------------------------------------

// Pack writes a .tar.gz archive of the snapshot directory plus a .sha256
// sidecar. The caller is responsible for holding project locks across the
// pack run (the snapshot directory must not be mutating concurrently —
// otherwise the archive can be corrupt or truncated).
func Pack(baseDir string, snapshotsRoot, snapDir, name string, outPath string, excludes []string) (*PackResult, error) {
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
		_ = f.Close()
		if err != nil {
			return fmt.Errorf("pack: write %q: %w", relSlash, err)
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
	checksumPath := outPath + ".sha256"
	checksumLine := sum + "  " + filepath.Base(outPath) + "\n"
	if err := writeFileAtomic(checksumPath, []byte(checksumLine), 0o644); err != nil {
		return nil, fmt.Errorf("snapshot pack: write checksum sidecar: %w", err)
	}

	st, err := os.Stat(outPath)
	if err != nil {
		return nil, fmt.Errorf("snapshot pack: stat out: %w", err)
	}

	return &PackResult{
		OutPath:        outPath,
		ChecksumPath:   checksumPath,
		Sha256:         sum,
		SizeBytes:      st.Size(),
		SkippedEntries: skipped,
	}, nil
}

// unpack ----------------------------------------------------------------------

// VerifyChecksumSidecar reads a .sha256 sidecar next to tarPath (or returns
// (false, nil) when missing) and returns true on a match, false on mismatch.
// The sidecar format is "<hex>  <basename>\n" — only the first whitespace-
// separated token is used to compare.
func VerifyChecksumSidecar(tarPath string) (present bool, ok bool, err error) {
	sidecar := tarPath + ".sha256"
	data, err := os.ReadFile(sidecar)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("read sidecar %s: %w", sidecar, err)
	}
	want := strings.TrimSpace(string(data))
	if i := strings.IndexAny(want, " \t"); i >= 0 {
		want = want[:i]
	}
	want = strings.ToLower(want)
	if want == "" {
		return true, false, fmt.Errorf("sidecar %s: empty checksum", sidecar)
	}

	f, err := os.Open(tarPath)
	if err != nil {
		return true, false, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return true, false, err
	}
	got := hex.EncodeToString(h.Sum(nil))
	return true, got == want, nil
}

// Unpack extracts a .tar.gz archive into <snapshotsRoot>/<targetName>/ with
// strict safety contract. The caller is responsible for holding project
// locks. Extraction happens into a sibling temp dir; on any error the temp
// dir is removed and no partial directory survives at the final path.
//
// If a .sha256 sidecar exists next to tarPath, Unpack verifies it before
// touching the filesystem. Mismatch → error and no extraction. Missing
// sidecar → warning is the caller's responsibility (Unpack does not write
// to stderr); the result's VerifiedChecksum field reports presence.
//
// confirmOverwrite is invoked when the target directory already exists.
// A nil callback is treated as a refusal.
func Unpack(tarPath, snapshotsRoot, targetName string, skipConfirm bool, confirmOverwrite func() (bool, error)) (*UnpackResult, error) {
	if err := ValidateName(targetName); err != nil {
		return nil, err
	}
	if _, err := os.Stat(tarPath); err != nil {
		return nil, fmt.Errorf("unpack: %s: %w", tarPath, err)
	}

	present, ok, err := VerifyChecksumSidecar(tarPath)
	if err != nil {
		return nil, fmt.Errorf("unpack: checksum: %w", err)
	}
	if present && !ok {
		return nil, fmt.Errorf("unpack: %s: sha256 mismatch with sidecar; refusing to extract", tarPath)
	}

	finalDir := filepath.Join(snapshotsRoot, targetName)
	if _, err := os.Stat(finalDir); err == nil {
		if !skipConfirm {
			yes, cErr := confirmUnpackOverwrite(confirmOverwrite)
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

	// Atomically install: remove the previous final dir (already confirmed) and rename.
	if err := os.RemoveAll(finalDir); err != nil {
		cleanupStaging()
		return nil, fmt.Errorf("unpack: remove existing dir: %w", err)
	}
	if err := os.Rename(stagingDir, finalDir); err != nil {
		cleanupStaging()
		return nil, fmt.Errorf("unpack: install: %w", err)
	}

	return &UnpackResult{
		SnapshotDir:      finalDir,
		Manifest:         m,
		VerifiedChecksum: present && ok,
	}, nil
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
