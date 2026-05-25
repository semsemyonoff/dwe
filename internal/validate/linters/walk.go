package linters

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/pathsafe"
)

// skipDirs are directory basenames the walker always skips regardless of
// adapter. Limited to .git because it is universal enough to hardcode;
// adapter-specific noise (node_modules, vendor) is left to the user to
// express via narrower paths:.
var skipDirs = map[string]struct{}{
	".git": {},
}

// collectFiles walks the project under baseDir for each entry in paths,
// returning the files whose extension is in exts OR whose basename is in
// filenames. Entries that resolve to a regular file bypass extension/filename
// filters (they were named explicitly).
//
// Symlinks (both file and directory) are skipped — they can point outside
// baseDir and following them risks running a linter over unintended trees.
//
// Missing-path semantics depend on pathsAreDefaults: when true (paths came
// from adapter defaults), absent entries are silently dropped. When false
// (paths came from the user's explicit config), absent entries are appended
// to the returned missing slice so the runtime can emit a Warning.
//
// The returned files slice is deduplicated and sorted for deterministic
// downstream behavior.
func collectFiles(baseDir string, paths, exts, filenames []string, pathsAreDefaults bool) (files []string, missing []string, err error) {
	extSet := make(map[string]struct{}, len(exts))
	for _, e := range exts {
		extSet[e] = struct{}{}
	}
	fileSet := make(map[string]struct{}, len(filenames))
	for _, f := range filenames {
		fileSet[f] = struct{}{}
	}

	seen := make(map[string]struct{})
	for _, entry := range paths {
		cleanEntry := filepath.Clean(entry)
		// The walk loop skips .git when it appears during descent (p != target),
		// but an explicit path like ".git" or ".git/hooks" bypasses that guard.
		// Apply the same skip here so .git is never linted regardless of whether
		// it was named explicitly or discovered via traversal.
		if cleanEntry == ".git" || strings.HasPrefix(cleanEntry, ".git"+string(filepath.Separator)) {
			continue
		}
		var target string
		if cleanEntry == "." {
			// Root-equality case: pathsafe.ContainedRel rejects "." by design,
			// so we short-circuit. Used by hadolint's default paths.
			target = baseDir
		} else {
			candidate := filepath.Join(baseDir, cleanEntry)
			if _, cerr := pathsafe.ContainedRel(baseDir, candidate); cerr != nil {
				return nil, nil, fmt.Errorf("path %q: %w", entry, cerr)
			}
			target = candidate
		}

		info, statErr := os.Lstat(target)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				if !pathsAreDefaults {
					missing = append(missing, entry)
				}
				continue
			}
			return nil, nil, fmt.Errorf("stat %s: %w", target, statErr)
		}

		// Top-level symlinks are skipped entirely.
		if info.Mode()&fs.ModeSymlink != 0 {
			continue
		}

		if !info.IsDir() {
			// Explicit file path bypasses filters.
			if _, dup := seen[target]; !dup {
				seen[target] = struct{}{}
				files = append(files, target)
			}
			continue
		}

		walkErr := filepath.WalkDir(target, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			name := d.Name()
			if d.IsDir() {
				if p != target {
					if _, skip := skipDirs[name]; skip {
						return filepath.SkipDir
					}
				}
				return nil
			}
			// Skip symlinks (file or, redundantly, dir — WalkDir already
			// won't recurse symlinked dirs but the entry is still reported).
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			ext := filepath.Ext(name)
			_, extMatch := extSet[ext]
			_, nameMatch := fileSet[name]
			if !extMatch && !nameMatch {
				return nil
			}
			if _, dup := seen[p]; dup {
				return nil
			}
			seen[p] = struct{}{}
			files = append(files, p)
			return nil
		})
		if walkErr != nil {
			return nil, nil, fmt.Errorf("walk %s: %w", target, walkErr)
		}
	}

	sort.Strings(files)
	return files, missing, nil
}
