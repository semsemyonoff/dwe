package envtest

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// excludedTopLevel are top-level path prefixes (relative to srcRoot) never
// copied into a scenario's disposable tree, regardless of git tracking or
// .gitignore: .dwe/ is dwe's own runtime state (locks, manifests, this very
// copy), .env is developer-local secrets, and .git/ is the source
// repository's metadata (the copy is not itself a git checkout).
var excludedTopLevel = map[string]struct{}{
	".dwe": {},
	".env": {},
	".git": {},
}

// CopyTree materializes a disposable copy of the project at srcRoot into
// dstRoot. When srcRoot is a usable git working tree, only files git
// considers part of the tree (cached + untracked-but-not-ignored, via
// `git ls-files -co --exclude-standard`) are copied, so build artifacts and
// gitignored junk never leak into the test run; a path git still lists but
// that is absent from the worktree (a tracked-but-deleted file) is silently
// skipped — the worktree, not the index, wins. When srcRoot is not a git
// repo (or the git invocation otherwise fails), CopyTree falls back to a
// full directory-tree copy and reports the gitignored-inclusion caveat via
// warn.
//
// gitBin is the git executable (typically config.GitBin(cfg)); warn
// receives non-fatal diagnostics and may be nil.
//
// dstRoot is removed first: the copy path is fixed per-scenario, and a stale
// prior copy must never shadow files the fresh copy no longer has.
//
// CopyTree is all-or-nothing: if any error occurs after dstRoot is (re)created,
// the partial copy is best-effort removed before returning, so a failed copy
// never leaves an orphaned tree behind. This matters because the caller has no
// manifest yet at this point — a partial copy would otherwise be invisible to
// manifest-driven Teardown and `dwe test clean`.
func CopyTree(srcRoot, dstRoot, gitBin string, warn func(string)) (err error) {
	if warn == nil {
		warn = func(string) {}
	}
	if err := os.RemoveAll(dstRoot); err != nil {
		return fmt.Errorf("envtest: removing stale copy destination: %w", err)
	}

	defer func() {
		if err != nil {
			if rmErr := os.RemoveAll(dstRoot); rmErr != nil {
				warn(fmt.Sprintf("removing partial copy after failure: %v", rmErr))
			}
		}
	}()

	files, gitErr := gitLsFiles(gitBin, srcRoot)
	if gitErr != nil {
		warn(fmt.Sprintf(
			"%s is not a usable git working tree (%v); copying the full directory tree, including any gitignored files",
			srcRoot, gitErr))
		return copyTreeWalk(srcRoot, dstRoot)
	}

	for _, rel := range files {
		if isExcludedTopLevel(rel) {
			continue
		}
		if err := copyGitEntry(srcRoot, dstRoot, rel); err != nil {
			return err
		}
	}
	return nil
}

// gitLsFiles lists cached + untracked-but-not-ignored paths under srcRoot via
// `<gitBin> -C srcRoot ls-files -co --exclude-standard -z`, split on NUL. A
// non-nil error means srcRoot is not usable as a git working tree (not a
// repo, git binary missing, etc.) — the caller falls back to a full
// directory walk. Paths are "/"-separated, as git always emits them.
func gitLsFiles(gitBin, srcRoot string) ([]string, error) {
	if gitBin == "" {
		gitBin = "git"
	}
	cmd := exec.Command(gitBin, "-C", srcRoot, "ls-files", "-co", "--exclude-standard", "-z") //nolint:gosec
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}

	parts := bytes.Split(stdout.Bytes(), []byte{0})
	files := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		files = append(files, string(p))
	}
	return files, nil
}

// isExcludedTopLevel reports whether rel (a "/"-separated path relative to
// the tree root) falls under one of the always-excluded top-level prefixes
// (.dwe/, .env, .git/).
func isExcludedTopLevel(rel string) bool {
	first, _, _ := strings.Cut(rel, "/")
	_, excluded := excludedTopLevel[first]
	return excluded
}

// copyGitEntry copies a single git-listed path from srcRoot to dstRoot,
// creating parent directories as needed. A path absent from the worktree is
// silently skipped (see CopyTree doc).
func copyGitEntry(srcRoot, dstRoot, rel string) error {
	relOS := filepath.FromSlash(rel)
	srcPath := filepath.Join(srcRoot, relOS)

	info, err := os.Lstat(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("envtest: stat %s: %w", srcPath, err)
	}

	dstPath := filepath.Join(dstRoot, relOS)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("envtest: creating directory for %s: %w", rel, err)
	}

	if info.Mode()&fs.ModeSymlink != 0 {
		return copySymlink(srcPath, dstPath)
	}
	return copyRegularFile(srcPath, dstPath, info)
}

func copySymlink(srcPath, dstPath string) error {
	target, err := os.Readlink(srcPath)
	if err != nil {
		return fmt.Errorf("envtest: reading symlink %s: %w", srcPath, err)
	}
	if err := os.Symlink(target, dstPath); err != nil {
		return fmt.Errorf("envtest: creating symlink %s: %w", dstPath, err)
	}
	return nil
}

func copyRegularFile(srcPath, dstPath string, info fs.FileInfo) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("envtest: opening %s: %w", srcPath, err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("envtest: creating %s: %w", dstPath, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("envtest: copying %s: %w", srcPath, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("envtest: closing %s: %w", dstPath, err)
	}
	if err := os.Chmod(dstPath, info.Mode().Perm()); err != nil {
		return fmt.Errorf("envtest: chmod %s: %w", dstPath, err)
	}
	return nil
}

// copyTreeWalk is the non-git fallback: a full filepath.WalkDir copy of
// srcRoot into dstRoot, applying the same top-level exclusions as the git
// path. Directories are created lazily (as parents of copied files), which
// matches the git-listed path's behaviour of never materializing empty
// directories.
func copyTreeWalk(srcRoot, dstRoot string) error {
	return filepath.WalkDir(srcRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == srcRoot {
			return nil
		}

		rel, err := filepath.Rel(srcRoot, p)
		if err != nil {
			return fmt.Errorf("envtest: relativizing %s: %w", p, err)
		}
		relSlash := filepath.ToSlash(rel)
		if isExcludedTopLevel(relSlash) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		dstPath := filepath.Join(dstRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return fmt.Errorf("envtest: creating directory for %s: %w", rel, err)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return copySymlink(p, dstPath)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("envtest: stat %s: %w", p, err)
		}
		return copyRegularFile(p, dstPath, info)
	})
}
