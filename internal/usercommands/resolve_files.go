package usercommands

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"devbox-cli/internal/tpl"
)

// ComputeFilePaths resolves file paths for all file specs in a command definition.
// It is non-mutating — no filesystem changes occur during this phase.
//
// For each file spec:
// - read/read_write: render path/candidates templates, normalize relative paths, discover via stat/glob
// - write: render path, normalize, no filesystem checks
// - presence validation: read requires file if Required=true; read_write always requires; write never requires
//
// Rendered paths are normalized to absolute paths via resolveRelative.
// For read/read_write modes, candidates are iterated in order until a hit is found;
// a missing path/glob is not an error (proceed to next candidate).
// Only after all candidates miss does a required-file error occur.
//
// Returns a map of file id → ResolvedFile; entries for optional-unresolved files
// are omitted from the result.
func ComputeFilePaths(ctx RunContext) (map[string]tpl.ResolvedFile, error) {
	if len(ctx.Cmd.Files) == 0 {
		return make(map[string]tpl.ResolvedFile), nil
	}

	result := make(map[string]tpl.ResolvedFile)

	for fid, fspec := range ctx.Cmd.Files {
		path, err := resolveFileSpec(ctx, fid, fspec)
		if err != nil {
			return nil, err
		}
		// path is empty only when optional read mode found no candidates
		if path != "" {
			result[fid] = tpl.ResolvedFile{Path: path}
		}
	}

	return result, nil
}

// resolveFileSpec returns the resolved absolute path for a single file spec.
// Returns (path, nil) on success.
// Returns ("", nil) when optional read and no candidates found (omit from result).
// Returns ("", err) when required and missing or any non-candidate error occurs.
func resolveFileSpec(ctx RunContext, fid string, fspec FileSpec) (string, error) {
	switch fspec.Access {
	case FileAccessRead:
		return resolveReadFile(ctx, fid, fspec)
	case FileAccessWrite:
		return resolveWriteFile(ctx, fid, fspec)
	case FileAccessReadWrite:
		return resolveReadWriteFile(ctx, fid, fspec)
	default:
		return "", fmt.Errorf("files.%s: unknown access mode %q", fid, fspec.Access)
	}
}

// resolveReadFile resolves a read-access file.
// Presence is required only if Required=true.
func resolveReadFile(ctx RunContext, fid string, fspec FileSpec) (string, error) {
	if fspec.Path != "" {
		path, abs, err := resolvePathCandidate(ctx, fid, fspec.Path)
		if err != nil {
			return "", err
		}
		if path != "" {
			return path, nil
		}
		// Path was rendered but file does not exist
		if fspec.Required {
			return "", fmt.Errorf("files.%s: required file not found at %s", fid, abs)
		}
		return "", nil // Optional file, not found — omit from result
	}

	// Try candidates in order
	for i, cand := range fspec.Candidates {
		path, err := resolveCandidate(ctx, fid, i, cand)
		if err != nil {
			return "", err
		}
		if path != "" {
			return path, nil
		}
		// Candidate miss (missing path or empty glob) — try next
	}

	// All candidates missed
	if fspec.Required {
		return "", fmt.Errorf("files.%s: required file not found (no candidates matched)", fid)
	}
	return "", nil // Optional, not found — omit from result
}

// resolveWriteFile resolves a write-access file (no filesystem checks).
// Write mode always returns the rendered path without checking existence.
func resolveWriteFile(ctx RunContext, fid string, fspec FileSpec) (string, error) {
	path, err := renderPath(ctx, fspec.Path)
	if err != nil {
		return "", fmt.Errorf("files.%s: render path: %w", fid, err)
	}

	abs, err := resolveRelative(ctx.ProjectRoot, path)
	if err != nil {
		return "", fmt.Errorf("files.%s: resolve path: %w", fid, err)
	}

	return abs, nil
}

// resolveReadWriteFile resolves a read_write-access file.
// read_write mode ALWAYS requires the file to exist, regardless of the Required field.
func resolveReadWriteFile(ctx RunContext, fid string, fspec FileSpec) (string, error) {
	if fspec.Path != "" {
		path, abs, err := resolvePathCandidate(ctx, fid, fspec.Path)
		if err != nil {
			return "", err
		}
		if path == "" {
			// read_write requires presence; no file found
			return "", fmt.Errorf("files.%s: read_write access requires file to exist; not found at %s", fid, abs)
		}
		return path, nil
	}

	// Try candidates in order
	for i, cand := range fspec.Candidates {
		path, err := resolveCandidate(ctx, fid, i, cand)
		if err != nil {
			return "", err
		}
		if path != "" {
			return path, nil
		}
		// Candidate miss — try next
	}

	// All candidates missed; read_write always requires presence
	return "", fmt.Errorf("files.%s: read_write access requires file to exist; not found (no candidates matched)", fid)
}

// resolvePathCandidate attempts to resolve a single path candidate.
// Returns (found, resolved, nil) where found is the absolute path if the file exists ("" if not),
// and resolved is the rendered absolute path (available even when the file is absent, for error messages).
// Returns ("", "", err) on render/stat error.
func resolvePathCandidate(ctx RunContext, fid string, pathTemplate string) (found string, resolved string, err error) {
	path, err := renderPath(ctx, pathTemplate)
	if err != nil {
		return "", "", fmt.Errorf("files.%s: render path: %w", fid, err)
	}

	abs, err := resolveRelative(ctx.ProjectRoot, path)
	if err != nil {
		return "", "", fmt.Errorf("files.%s: resolve path: %w", fid, err)
	}

	// Check if file exists
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return "", abs, nil // File does not exist; return resolved path for error messages
		}
		// Other errors (permission denied, etc.) are real errors
		return "", "", fmt.Errorf("files.%s: stat %s: %w", fid, abs, err)
	}

	return abs, abs, nil
}

// resolveCandidate attempts to resolve a single candidate (path or glob).
// Returns (path, nil) on success, ("", nil) on miss, or ("", err) on error.
func resolveCandidate(ctx RunContext, fid string, candIdx int, cand FileCandidate) (string, error) {
	if cand.Path != "" {
		path, _, err := resolvePathCandidate(ctx, fid, cand.Path)
		if err != nil {
			return "", fmt.Errorf("candidates[%d]: %w", candIdx, err)
		}
		return path, nil
	}

	if cand.Glob != "" {
		path, err := resolveGlobCandidate(ctx, cand)
		if err != nil {
			return "", fmt.Errorf("candidates[%d]: %w", candIdx, err)
		}
		return path, nil
	}

	return "", nil // Should not happen (validated at load time)
}

// resolveGlobCandidate expands a glob pattern, filters by match regex, sorts, and returns first.
// Returns (path, nil) on success, ("", nil) on no matches, or ("", err) on error.
func resolveGlobCandidate(ctx RunContext, cand FileCandidate) (string, error) {
	globPattern, err := renderPath(ctx, cand.Glob)
	if err != nil {
		return "", fmt.Errorf("render glob: %w", err)
	}

	// Normalize glob pattern to absolute path
	absGlobPattern, err := resolveRelative(ctx.ProjectRoot, globPattern)
	if err != nil {
		return "", fmt.Errorf("resolve glob: %w", err)
	}

	// Expand glob
	matches, err := filepath.Glob(absGlobPattern)
	if err != nil {
		return "", fmt.Errorf("glob %s: %w", absGlobPattern, err)
	}

	if len(matches) == 0 {
		return "", nil // No matches; not an error for candidates
	}

	// Filter by match regex (applied to basename)
	if cand.Match != "" {
		renderedMatch, err := renderPath(ctx, cand.Match)
		if err != nil {
			return "", fmt.Errorf("render match: %w", err)
		}
		matchRegex, err := regexp.Compile(renderedMatch)
		if err != nil {
			return "", fmt.Errorf("match regex %q: %w", renderedMatch, err)
		}

		filtered := []string{}
		for _, m := range matches {
			base := filepath.Base(m)
			if matchRegex.MatchString(base) {
				filtered = append(filtered, m)
			}
		}
		matches = filtered
	}

	if len(matches) == 0 {
		return "", nil // All matches filtered out
	}

	// Sort according to directive
	sortMatches(matches, cand.Sort)

	// Return first match
	return matches[0], nil
}

// sortMatches sorts matches in-place according to the sort directive.
func sortMatches(matches []string, sortMode FileSort) {
	switch sortMode {
	case FileSortNameAsc:
		sort.Slice(matches, func(i, j int) bool {
			return filepath.Base(matches[i]) < filepath.Base(matches[j])
		})
	case FileSortNameDesc:
		sort.Slice(matches, func(i, j int) bool {
			return filepath.Base(matches[i]) > filepath.Base(matches[j])
		})
	case FileSortModtimeAsc, FileSortModtimeDesc:
		type fileWithModtime struct {
			path    string
			modtime int64 // UnixNano; 0 if stat failed
		}
		infos := make([]fileWithModtime, len(matches))
		for i, m := range matches {
			infos[i].path = m
			if fi, err := os.Stat(m); err == nil {
				infos[i].modtime = fi.ModTime().UnixNano()
			}
		}
		if sortMode == FileSortModtimeAsc {
			sort.SliceStable(infos, func(i, j int) bool {
				return infos[i].modtime < infos[j].modtime
			})
		} else {
			sort.SliceStable(infos, func(i, j int) bool {
				return infos[i].modtime > infos[j].modtime
			})
		}
		for i, info := range infos {
			matches[i] = info.path
		}
	default:
		// No sort directive — preserve filesystem/glob order.
	}
}

// renderPath renders a path template string using the render context.
// Returns the rendered string (may be relative or absolute).
func renderPath(ctx RunContext, path string) (string, error) {
	if ctx.Render == nil {
		return path, nil
	}

	rendered, err := tpl.RenderCommand(path, ctx.Render)
	if err != nil {
		return "", err
	}

	return rendered, nil
}

// resolveRelative resolves a path to an absolute path.
//
// If the path is already absolute, it is returned as-is (after filepath.Clean).
// If the path is relative and projectRoot is non-empty, the path is joined with projectRoot.
// If the path is relative and projectRoot is empty, it is resolved against os.Getwd().
// This matches the contract in runner_script.go:79-85 (fallback for programmatic callers).
//
// Returns the cleaned absolute path, or an error if os.Getwd() fails.
func resolveRelative(projectRoot, p string) (string, error) {
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}

	root := projectRoot
	if root == "" {
		// Fallback to current working directory
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getwd: %w", err)
		}
	} else if !filepath.IsAbs(root) {
		var err error
		root, err = filepath.Abs(root)
		if err != nil {
			return "", fmt.Errorf("abs project root: %w", err)
		}
	}

	return filepath.Clean(filepath.Join(root, p)), nil
}

// PrepareFileEffects performs post-confirmation mutations for file specs:
// - For write entries with mkdir=true: create parent directories
// - For write entries: check overwrite constraint (fail if exists and overwrite=false)
// - Track whether each file existed before (used for safe on_error cleanup)
// - Return cleanup callbacks only for files created by this invocation (existedBefore=false)
//
// Cleanup callbacks are registered only when:
// - The file entry has on_error: remove
// - The file did not exist before this invocation (existedBefore=false)
// For read_write mode, existedBefore is always true (file must pre-exist), so
// cleanup is never registered, protecting pre-existing files from deletion.
//
// Returns (cleanups, nil) on success, where cleanups is a slice of callbacks
// to invoke on runner failure (in LIFO order). Returns (nil, err) if any
// overwrite/mkdir check fails.
func PrepareFileEffects(ctx RunContext, paths map[string]tpl.ResolvedFile) ([]func(), error) {
	if len(ctx.Cmd.Files) == 0 {
		return []func(){}, nil
	}

	cleanups := []func(){}
	stderr := ctx.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	for fid, fspec := range ctx.Cmd.Files {
		// Only process write and read_write modes
		if fspec.Access != FileAccessWrite && fspec.Access != FileAccessReadWrite {
			continue
		}

		path, ok := paths[fid]
		if !ok {
			// File was not resolved (should not happen for write/read_write)
			continue
		}

		absPath := path.Path

		// For write mode: handle mkdir and overwrite checks
		if fspec.Access == FileAccessWrite {
			// Create parent directories if mkdir=true
			if fspec.Mkdir {
				parentDir := filepath.Dir(absPath)
				if err := os.MkdirAll(parentDir, 0o755); err != nil {
					return nil, fmt.Errorf("files.%s: mkdir %s: %w", fid, parentDir, err)
				}
			}

			// Check if file exists
			_, err := os.Stat(absPath)
			existedBefore := err == nil
			if err != nil && !os.IsNotExist(err) {
				// Real error (permission denied, etc.)
				return nil, fmt.Errorf("files.%s: stat %s: %w", fid, absPath, err)
			}

			// Check overwrite constraint
			if existedBefore && !fspec.Overwrite {
				return nil, fmt.Errorf("files.%s: file already exists at %s and overwrite=false", fid, absPath)
			}

			// Register cleanup callback only if file didn't exist before
			// and on_error is set to remove
			if !existedBefore && fspec.OnError == FileOnErrorRemove {
				// Capture absPath in closure
				fileToClean := absPath
				cleanups = append(cleanups, func() {
					if err := os.Remove(fileToClean); err != nil {
						if os.IsNotExist(err) {
							return
						}
						_, _ = fmt.Fprintf(stderr, "cleanup: failed to remove %s: %v\n", fileToClean, err)
					}
				})
			}
		}

		// For read_write mode: existence already guaranteed by ComputeFilePaths,
		// so we just track that it always existed before.
		// Cleanup is never registered for read_write (existedBefore=true always).
		// No mutations needed: file must pre-exist, no mkdir, no overwrite check,
		// and on_error=remove is a no-op since existedBefore=true always.
	}

	return cleanups, nil
}
