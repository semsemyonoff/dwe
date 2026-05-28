package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"devbox-cli/internal/core/usercommands/model"
	"devbox-cli/internal/shared/tpl"
)

// FileProbeResult tracks the outcome of a single file probe.
type FileProbeResult struct {
	Resolved bool   // true if the file (or a candidate chain) resolved
	Path     string // the resolved path, if Resolved is true
	Err      error  // configuration error, if any (e.g. bad template, bad glob, bad regex)
}

// ComputeFilePaths resolves file paths for all file specs in a command definition.
// It is non-mutating — no filesystem changes occur during this phase.
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
		if path != "" {
			result[fid] = tpl.ResolvedFile{Path: path}
		}
	}

	return result, nil
}

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

// resolveFileSpec returns the resolved absolute path for a single file spec.
func resolveFileSpec(ctx RunContext, fid string, fspec model.FileSpec) (string, error) {
	switch fspec.Access {
	case model.FileAccessRead:
		return resolveReadFile(ctx, fid, fspec)
	case model.FileAccessWrite:
		return resolveWriteFile(ctx, fid, fspec)
	case model.FileAccessReadWrite:
		return resolveReadWriteFile(ctx, fid, fspec)
	default:
		return "", fmt.Errorf("files.%s: unknown access mode %q", fid, fspec.Access)
	}
}

// resolveReadFile resolves a read-access file.
func resolveReadFile(ctx RunContext, fid string, fspec model.FileSpec) (string, error) {
	if fspec.Path != "" {
		path, abs, err := resolvePathCandidate(ctx, fid, fspec.Path)
		if err != nil {
			return "", err
		}
		if path != "" {
			return path, nil
		}
		if fspec.Required {
			return "", fmt.Errorf("files.%s: required file not found at %s", fid, abs)
		}
		return "", nil
	}

	for i, cand := range fspec.Candidates {
		path, err := resolveCandidate(ctx, fid, i, cand)
		if err != nil {
			return "", err
		}
		if path != "" {
			return path, nil
		}
	}

	if fspec.Required {
		return "", fmt.Errorf("files.%s: required file not found (no candidates matched)", fid)
	}
	return "", nil
}

// resolveWriteFile resolves a write-access file (no filesystem checks).
func resolveWriteFile(ctx RunContext, fid string, fspec model.FileSpec) (string, error) {
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
// read_write mode ALWAYS requires the file to exist.
func resolveReadWriteFile(ctx RunContext, fid string, fspec model.FileSpec) (string, error) {
	if fspec.Path != "" {
		path, abs, err := resolvePathCandidate(ctx, fid, fspec.Path)
		if err != nil {
			return "", err
		}
		if path == "" {
			return "", fmt.Errorf("files.%s: read_write access requires file to exist; not found at %s", fid, abs)
		}
		return path, nil
	}

	for i, cand := range fspec.Candidates {
		path, err := resolveCandidate(ctx, fid, i, cand)
		if err != nil {
			return "", err
		}
		if path != "" {
			return path, nil
		}
	}

	return "", fmt.Errorf("files.%s: read_write access requires file to exist; not found (no candidates matched)", fid)
}

// resolvePathCandidate attempts to resolve a single path candidate.
func resolvePathCandidate(ctx RunContext, fid string, pathTemplate string) (found string, resolved string, err error) {
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

// resolveCandidate attempts to resolve a single candidate (path or glob).
func resolveCandidate(ctx RunContext, fid string, candIdx int, cand model.FileCandidate) (string, error) {
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

	return "", nil
}

// resolveGlobCandidate expands a glob pattern, filters by match regex, sorts, and returns first.
func resolveGlobCandidate(ctx RunContext, cand model.FileCandidate) (string, error) {
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

// sortMatches sorts matches in-place according to the sort directive.
func sortMatches(matches []string, sortMode model.FileSort) {
	switch sortMode {
	case model.FileSortNameAsc:
		sort.Slice(matches, func(i, j int) bool {
			return filepath.Base(matches[i]) < filepath.Base(matches[j])
		})
	case model.FileSortNameDesc:
		sort.Slice(matches, func(i, j int) bool {
			return filepath.Base(matches[i]) > filepath.Base(matches[j])
		})
	case model.FileSortModtimeAsc, model.FileSortModtimeDesc:
		type fileWithModtime struct {
			path    string
			modtime int64
		}
		infos := make([]fileWithModtime, len(matches))
		for i, m := range matches {
			infos[i].path = m
			if fi, err := os.Stat(m); err == nil {
				infos[i].modtime = fi.ModTime().UnixNano()
			}
		}
		if sortMode == model.FileSortModtimeAsc {
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
func resolveRelative(projectRoot, p string) (string, error) {
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}

	root := projectRoot
	if root == "" {
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

// PrepareFileEffects performs post-confirmation mutations for file specs.
func PrepareFileEffects(ctx RunContext, paths map[string]tpl.ResolvedFile) ([]func(), error) {
	if len(ctx.Cmd.Files) == 0 {
		return []func(){}, nil
	}

	cleanups := []func(){}
	stderrW := ctx.Stderr
	if stderrW == nil {
		stderrW = os.Stderr
	}

	for fid, fspec := range ctx.Cmd.Files {
		if fspec.Access != model.FileAccessWrite && fspec.Access != model.FileAccessReadWrite {
			continue
		}

		path, ok := paths[fid]
		if !ok {
			continue
		}

		absPath := path.Path

		if fspec.Access == model.FileAccessWrite {
			if fspec.Mkdir {
				parentDir := filepath.Dir(absPath)
				if err := os.MkdirAll(parentDir, 0o755); err != nil {
					return nil, fmt.Errorf("files.%s: mkdir %s: %w", fid, parentDir, err)
				}
			}

			_, err := os.Stat(absPath)
			existedBefore := err == nil
			if err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("files.%s: stat %s: %w", fid, absPath, err)
			}

			if existedBefore && !fspec.Overwrite {
				return nil, fmt.Errorf("files.%s: file already exists at %s and overwrite=false", fid, absPath)
			}

			if !existedBefore && fspec.OnError == model.FileOnErrorRemove {
				fileToClean := absPath
				cleanups = append(cleanups, func() {
					if err := os.Remove(fileToClean); err != nil {
						if os.IsNotExist(err) {
							return
						}
						_, _ = fmt.Fprintf(stderrW, "cleanup: failed to remove %s: %v\n", fileToClean, err)
					}
				})
			}
		}
	}

	return cleanups, nil
}
