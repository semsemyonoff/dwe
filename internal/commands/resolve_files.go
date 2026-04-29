package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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
		path, err := resolvePathCandidate(ctx, fid, fspec.Path)
		if err != nil {
			return "", err
		}
		if path != "" {
			return path, nil
		}
		// Path was rendered but file does not exist
		if fspec.Required {
			return "", fmt.Errorf("files.%s: required file not found at %s", fid, fspec.Path)
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
		path, err := resolvePathCandidate(ctx, fid, fspec.Path)
		if err != nil {
			return "", err
		}
		if path == "" {
			// read_write requires presence; no file found
			return "", fmt.Errorf("files.%s: read_write access requires file to exist; not found at %s", fid, fspec.Path)
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
// Returns (path, nil) if the file exists, ("", nil) if not found, or ("", err) on error.
func resolvePathCandidate(ctx RunContext, fid string, pathTemplate string) (string, error) {
	path, err := renderPath(ctx, pathTemplate)
	if err != nil {
		return "", fmt.Errorf("files.%s: render path: %w", fid, err)
	}

	abs, err := resolveRelative(ctx.ProjectRoot, path)
	if err != nil {
		return "", fmt.Errorf("files.%s: resolve path: %w", fid, err)
	}

	// Check if file exists
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return "", nil // File does not exist; not an error for candidates
		}
		// Other errors (permission denied, etc.) are real errors
		return "", fmt.Errorf("files.%s: stat %s: %w", fid, abs, err)
	}

	return abs, nil
}

// resolveCandidate attempts to resolve a single candidate (path or glob).
// Returns (path, nil) on success, ("", nil) on miss, or ("", err) on error.
func resolveCandidate(ctx RunContext, fid string, candIdx int, cand FileCandidate) (string, error) {
	if cand.Path != "" {
		path, err := resolvePathCandidate(ctx, fid, cand.Path)
		if err != nil {
			return "", fmt.Errorf("candidates[%d]: %w", candIdx, err)
		}
		return path, nil
	}

	if cand.Glob != "" {
		path, err := resolveGlobCandidate(ctx, fid, candIdx, cand)
		if err != nil {
			return "", fmt.Errorf("candidates[%d]: %w", candIdx, err)
		}
		return path, nil
	}

	return "", nil // Should not happen (validated at load time)
}

// resolveGlobCandidate expands a glob pattern, filters by match regex, sorts, and returns first.
// Returns (path, nil) on success, ("", nil) on no matches, or ("", err) on error.
func resolveGlobCandidate(ctx RunContext, fid string, candIdx int, cand FileCandidate) (string, error) {
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
		matchRegex, err := regexp.Compile(cand.Match)
		if err != nil {
			return "", fmt.Errorf("match regex %q: %w", cand.Match, err)
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
	case FileSortModtimeAsc:
		sort.Slice(matches, func(i, j int) bool {
			infoI, errI := os.Stat(matches[i])
			infoJ, errJ := os.Stat(matches[j])
			if errI != nil || errJ != nil {
				return false // Preserve order on stat error
			}
			return infoI.ModTime().Before(infoJ.ModTime())
		})
	case FileSortModtimeDesc:
		sort.Slice(matches, func(i, j int) bool {
			infoI, errI := os.Stat(matches[i])
			infoJ, errJ := os.Stat(matches[j])
			if errI != nil || errJ != nil {
				return false // Preserve order on stat error
			}
			return infoI.ModTime().After(infoJ.ModTime())
		})
	default:
		// No sorting (should not happen, validated at load time)
	}
}

// renderPath renders a path template string using the render context.
// Returns the rendered string (may be relative or absolute).
func renderPath(ctx RunContext, path string) (string, error) {
	if ctx.Render == nil || !strings.Contains(path, "${") {
		// No templates to render
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
	}

	return filepath.Clean(filepath.Join(root, p)), nil
}
