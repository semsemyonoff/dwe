package archive

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"devbox-cli/internal/core/workflow/snapshot/meta"
)

// glob matching ---------------------------------------------------------------

// globToRegexp converts a doublestar-style glob (`**`, `*`, `?`) into an
// anchored regexp. The pattern matches against forward-slash separated paths.
//
//   - `**` matches zero or more path segments (including separators).
//   - `*`  matches any run of characters except `/`.
//   - `?`  matches a single character except `/`.
//   - All other characters are taken literally.
func globToRegexp(glob string) (*regexp.Regexp, error) {
	if strings.ContainsAny(glob, "{}") {
		return nil, fmt.Errorf("brace expansion is not supported; use separate glob entries")
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				i++
				if i+1 < len(glob) && glob[i+1] == '/' {
					// `**/foo` → optional path prefix; `(?:.*/)?` prevents false
					// matches like `barfoo` that `.*foo` would accept.
					b.WriteString("(?:.*/)?")
					i++ // consume the trailing '/'
				} else {
					// `**` at end or `**<literal>` — match anything including separators.
					b.WriteString(".*")
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
		case '.', '+', '(', ')', '|', '^', '$', '[', ']', '\\':
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
	if err := meta.ValidateName(name); err != nil {
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
