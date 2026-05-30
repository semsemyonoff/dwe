package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"devbox-cli/internal/core/usercommands/model"
)

// ComputeFilePathsProbe probes a subset of files (given by `only`) to check for existence.
// Missing files produce Resolved: false with no error (soft absence).
// Configuration errors (bad template, bad glob, bad regex) produce Err != nil.
// Empty or nil `only` is an error (contract: caller expands require spec, then passes the IDs).
func ComputeFilePathsProbe(ctx RunContext, only []string) (map[string]FileProbeResult, error) {
	if len(only) == 0 {
		return nil, fmt.Errorf("ComputeFilePathsProbe: only must be non-empty")
	}

	if len(ctx.Cmd.Files) == 0 {
		return nil, fmt.Errorf("ComputeFilePathsProbe: command has no files")
	}

	result := make(map[string]FileProbeResult)

	for _, fid := range only {
		fspec, ok := ctx.Cmd.Files[fid]
		if !ok {
			return nil, fmt.Errorf("ComputeFilePathsProbe: unknown file ID %q", fid)
		}

		res := probeFileSpec(ctx, fid, fspec)
		result[fid] = res
		// If probe returned a configuration error, return immediately (fail-fast).
		if res.Err != nil {
			return nil, res.Err
		}
	}

	return result, nil
}

// probeFileSpec returns a FileProbeResult for a single file spec.
// For read and read_write files, missing files return Resolved: false with no error.
// write-only files are not probed by the gate API; this function rejects them.
func probeFileSpec(ctx RunContext, fid string, fspec model.FileSpec) FileProbeResult {
	switch fspec.Access {
	case model.FileAccessRead, model.FileAccessReadWrite:
		return probeAccessibleFile(ctx, fid, fspec)
	case model.FileAccessWrite:
		return FileProbeResult{
			Err: fmt.Errorf("files.%s: cannot probe write-only file (access: write)", fid),
		}
	default:
		return FileProbeResult{
			Err: fmt.Errorf("files.%s: unknown access mode %q", fid, fspec.Access),
		}
	}
}

// probeAccessibleFile probes a read or read_write file for existence.
// Missing files return Resolved: false with no error.
func probeAccessibleFile(ctx RunContext, fid string, fspec model.FileSpec) FileProbeResult {
	if fspec.Path != "" {
		path, _, err := probePathCandidate(ctx, fid, fspec.Path)
		if err != nil {
			return FileProbeResult{Err: err}
		}
		return FileProbeResult{Resolved: path != "", Path: path}
	}

	for i, cand := range fspec.Candidates {
		path, err := probeCandidate(ctx, fid, i, cand)
		if err != nil {
			return FileProbeResult{Err: err}
		}
		if path != "" {
			return FileProbeResult{Resolved: true, Path: path}
		}
	}

	return FileProbeResult{Resolved: false}
}

// probePathCandidate probes a single path candidate without requiring it to exist.
func probePathCandidate(ctx RunContext, fid string, pathTemplate string) (found string, resolved string, err error) {
	path, err := renderPath(ctx, pathTemplate)
	if err != nil {
		return "", "", fmt.Errorf("files.%s: render path: %w", fid, err)
	}

	abs, err := resolveRelative(ctx.ProjectRoot, path)
	if err != nil {
		return "", "", fmt.Errorf("files.%s: resolve path: %w", fid, err)
	}

	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return "", abs, nil
		}
		return "", "", fmt.Errorf("files.%s: stat %s: %w", fid, abs, err)
	}

	return abs, abs, nil
}

// probeCandidate probes a single candidate (path or glob) for existence.
func probeCandidate(ctx RunContext, fid string, candIdx int, cand model.FileCandidate) (string, error) {
	if cand.Path != "" {
		path, _, err := probePathCandidate(ctx, fid, cand.Path)
		if err != nil {
			return "", fmt.Errorf("candidates[%d]: %w", candIdx, err)
		}
		return path, nil
	}

	if cand.Glob != "" {
		path, err := probeGlobCandidate(ctx, cand)
		if err != nil {
			return "", fmt.Errorf("candidates[%d]: %w", candIdx, err)
		}
		return path, nil
	}

	return "", nil
}

// probeGlobCandidate probes a glob pattern for existence, filtering by match regex.
func probeGlobCandidate(ctx RunContext, cand model.FileCandidate) (string, error) {
	globPattern, err := renderPath(ctx, cand.Glob)
	if err != nil {
		return "", fmt.Errorf("render glob: %w", err)
	}

	absGlobPattern, err := resolveRelative(ctx.ProjectRoot, globPattern)
	if err != nil {
		return "", fmt.Errorf("resolve glob: %w", err)
	}

	matches, err := filepath.Glob(absGlobPattern)
	if err != nil {
		return "", fmt.Errorf("glob %s: %w", absGlobPattern, err)
	}

	if len(matches) == 0 {
		return "", nil
	}

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
		return "", nil
	}

	sortMatches(matches, cand.Sort)

	return matches[0], nil
}
