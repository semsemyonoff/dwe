// Package stack — git workspace collector.
//
// Per-service git workspace metadata for the status view. The collector
// shells out to `<git-bin> -C <dir>/src status -b --porcelain=v2` once per
// service (capped by an errgroup with limit 8). Each goroutine writes into a
// pre-allocated, fixed-index slot — no mutex needed. Goroutines always
// return nil so errgroup never cancels siblings on first failure;
// per-row errors are captured on row.Err.
//
// Probe target: by project convention the actual source tree lives under
// `<svc.Dir>/src/` (mirrors `internal/core/execution/templates/git`, which writes hooks to
// `<svc.Dir>/src/.git/hooks/`). When `<svc.Dir>/src` is missing the service
// is omitted from the output entirely; when it exists but lacks its own
// `.git`, blank cells are emitted (no row error).
//
// Boundary check: a service src dir without its own `.git` short-circuits
// before any shellout — `git -C` walks up to the nearest enclosing repo,
// so an enclosing project-level repo would otherwise be reported as the
// service's status. This is intentional per the spec.
//
// Extends dedup: services that extend a parent share the parent's `dir`
// (and therefore probe the same src tree). The collector groups rows by
// probe directory and keeps the canonical root of the extends chain
// (shallowest depth) — duplicates from sidecar services like main-debug
// are dropped silently.
package stack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/statusview"
)

// gitShellOutFn is the seam used by CollectGitWorkspace to invoke git. Tests
// override it to count invocations / inject fake output. Production code uses
// runGitStatus.
var gitShellOutFn = runGitStatus

// runGitStatus runs `<bin> -C dir status -b --porcelain=v2` and returns the
// captured stdout. Stderr is intentionally discarded — git's exit code is the
// only signal we consume; the caller surfaces a generic "git status failed"
// error rather than leaking stderr into row.Err.
func runGitStatus(ctx context.Context, bin, dir string) ([]byte, error) {
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, "-C", dir, "status", "-b", "--porcelain=v2") //nolint:gosec
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// extendsDepth returns the depth of a service's extends chain (0 = root).
// Cycles are bounded by a 32-hop guard mirroring templates/git.ExtendsDepth.
func extendsDepth(services map[string]config.ServiceConfig, name string) int {
	const maxDepth = 32
	for d := range maxDepth {
		svc, ok := services[name]
		if !ok || svc.Extends == "" {
			return d
		}
		name = svc.Extends
	}
	return maxDepth
}

// CollectGitWorkspace shells out to git per service in parallel and returns
// one row per service that has a `<dir>/src` directory on disk. The slice is
// alphabetically sorted by service name. Cancellation propagates via ctx.
//
// projectRoot is the resolved project root directory. Relative service dirs
// are joined against it so the collector works correctly when dwe is
// invoked from a subdirectory of the project.
//
// Services without a `dir`, or whose `<dir>/src` is missing on disk, are
// silently omitted from the output. Sidecar services that extend a parent
// and share the parent's `dir` are deduplicated — the extends-chain root
// wins.
//
// Per-row Err discriminates "service has no own repo" (Err == nil, blank
// cells) from "configuration smells" (Err != nil — shellout or parse
// failure).
func CollectGitWorkspace(ctx context.Context, cfg *config.DweConfig, projectRoot string) []statusview.GitWorkspaceRow {
	if cfg == nil {
		return nil
	}
	bin := config.GitBin(cfg)

	// Group candidate services by probe directory (<dir>/src). Skip services
	// without dir, or whose src subdir is missing/not-a-directory — by spec.
	type candidate struct {
		name     string
		probeDir string
		depth    int
	}
	byProbe := make(map[string][]candidate)
	for name, svc := range cfg.Services {
		if svc.Dir == "" {
			continue
		}
		hubDir := svc.Dir
		if !filepath.IsAbs(hubDir) {
			hubDir = filepath.Join(projectRoot, hubDir)
		}
		probeDir := filepath.Join(hubDir, "src")
		info, err := os.Stat(probeDir)
		if err != nil || !info.IsDir() {
			continue
		}
		c := candidate{
			name:     name,
			probeDir: probeDir,
			depth:    extendsDepth(cfg.Services, name),
		}
		byProbe[probeDir] = append(byProbe[probeDir], c)
	}

	// Dedup: per probe dir, keep the extends-chain root (shallowest depth);
	// break ties alphabetically for determinism.
	selected := make([]candidate, 0, len(byProbe))
	for _, group := range byProbe {
		winner := group[0]
		for _, c := range group[1:] {
			if c.depth < winner.depth || (c.depth == winner.depth && c.name < winner.name) {
				winner = c
			}
		}
		selected = append(selected, winner)
	}
	slices.SortFunc(selected, func(a, b candidate) int { return strings.Compare(a.name, b.name) })

	rows := make([]statusview.GitWorkspaceRow, len(selected))
	for i, c := range selected {
		// Probe with the absolute path; the display form is substituted below
		// once shellouts have finished so log/error messages keep the real path.
		rows[i] = statusview.GitWorkspaceRow{Service: c.name, Dir: c.probeDir}
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for i := range rows {
		g.Go(func() error {
			fillGitRow(gctx, bin, &rows[i])
			return nil
		})
	}
	_ = g.Wait()

	// Substitute display form: strip projectRoot prefix and prepend an
	// ellipsis marker so users see e.g. `…/services/main/src` instead of the
	// noisy absolute path. Falls back to the absolute path when the row is
	// not under projectRoot.
	for i := range rows {
		rows[i].Dir = displayDir(projectRoot, rows[i].Dir)
	}
	return rows
}

// displayDir converts an absolute probe directory to a short, project-relative
// form prefixed with an ellipsis marker (`…/`). Returns the input unchanged
// when projectRoot is empty, the path is already relative, or filepath.Rel
// cannot express the path relative to projectRoot (e.g. different volume on
// Windows). Paths that resolve to "." (probe == projectRoot) or that escape
// the root via ".." also fall back to the absolute form so users notice the
// oddity.
func displayDir(projectRoot, absDir string) string {
	if projectRoot == "" || !filepath.IsAbs(absDir) {
		return absDir
	}
	rel, err := filepath.Rel(projectRoot, absDir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return absDir
	}
	return "…/" + rel
}

// fillGitRow probes one service src directory and populates the row in place.
// The caller has already verified that `row.Dir` exists and is a directory.
// The boundary check (no <dir>/.git → blank cells, nil Err) runs before any
// shellout.
func fillGitRow(ctx context.Context, bin string, row *statusview.GitWorkspaceRow) {
	if !hasOwnGitDir(row.Dir) {
		// Not a repo of its own — blank cells, no error.
		return
	}

	out, err := gitShellOutFn(ctx, bin, row.Dir)
	if err != nil {
		row.Err = fmt.Errorf("git status: %w", err)
		return
	}
	branch, oid, ahead, behind, hasAB, dirty, perr := parsePorcelainV2(out)
	if perr != nil {
		row.Err = perr
		return
	}
	row.Branch = branch
	row.SHA = shortenOID(oid)
	row.Dirty = dirty
	if hasAB {
		row.AheadBehind = fmt.Sprintf("+%d/-%d", ahead, behind)
	}
}

// hasOwnGitDir reports whether dir contains its own `.git` entry. The entry
// may be a directory (normal repo) or a regular file (worktree gitdir pointer).
// Anything else (missing, symlink to outside…) counts as "not its own repo".
func hasOwnGitDir(dir string) bool {
	gitPath := filepath.Join(dir, ".git")
	st, err := os.Lstat(gitPath)
	if err != nil {
		return false
	}
	mode := st.Mode()
	return mode.IsDir() || mode.IsRegular()
}

func shortenOID(oid string) string {
	if len(oid) > 8 {
		return oid[:8]
	}
	return oid
}

// parsePorcelainV2 extracts branch, oid, ahead/behind, and dirty state from a
// `git status -b --porcelain=v2` output. Headers we consume:
//
//	# branch.head <name>       — branch name (or "(detached)")
//	# branch.oid <oid>         — full commit SHA
//	# branch.ab +N -N          — ahead/behind counts
//
// Any non-comment record means the working tree is dirty. Detached HEAD is
// surfaced as branch = "detached" (parens stripped).
func parsePorcelainV2(out []byte) (branch, oid string, ahead, behind int, hasAB, dirty bool, err error) {
	for line := range bytes.Lines(out) {
		line = bytes.TrimRight(line, "\n")
		if len(line) == 0 {
			continue
		}
		if line[0] != '#' {
			dirty = true
			continue
		}
		fields := strings.Fields(string(line))
		if len(fields) < 2 {
			continue
		}
		switch fields[1] {
		case "branch.head":
			if len(fields) >= 3 {
				name := fields[2]
				if name == "(detached)" {
					branch = "detached"
				} else {
					branch = name
				}
			}
		case "branch.oid":
			if len(fields) >= 3 && fields[2] != "(initial)" {
				oid = fields[2]
			}
		case "branch.ab":
			if len(fields) >= 4 {
				a, aerr := strconv.Atoi(strings.TrimPrefix(fields[2], "+"))
				b, berr := strconv.Atoi(strings.TrimPrefix(fields[3], "-"))
				if aerr != nil || berr != nil {
					err = errors.New("malformed branch.ab header")
					return
				}
				ahead, behind = a, b
				hasAB = true
			}
		}
	}
	return
}
