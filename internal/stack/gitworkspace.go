// Package stack — git workspace collector.
//
// Per-service git workspace metadata for the status view. The collector
// shells out to `git -C <dir> status -b --porcelain=v2` once per service
// (capped by an errgroup with limit 8). Each goroutine writes into a
// pre-allocated, fixed-index slot — no mutex needed. Goroutines always
// return nil so errgroup never cancels siblings on first failure;
// per-row errors are captured on row.Err.
//
// Boundary check: a service dir without its own `.git` short-circuits
// before any shellout — `git -C` walks up to the nearest enclosing repo,
// so an enclosing project-level repo would otherwise be reported as the
// service's status. This is intentional per the spec.
package stack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"devbox-cli/internal/command/statusview"
	"devbox-cli/internal/config"
)

// gitShellOutFn is the seam used by CollectGitWorkspace to invoke git. Tests
// override it to count invocations / inject fake output. Production code uses
// runGitStatus.
var gitShellOutFn = runGitStatus

// runGitStatus runs `git -C dir status -b --porcelain=v2` and returns the
// captured stdout. Stderr is intentionally discarded — git's exit code is the
// only signal we consume; the caller surfaces a generic "git status failed"
// error rather than leaking stderr into row.Err.
func runGitStatus(ctx context.Context, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "status", "-b", "--porcelain=v2") //nolint:gosec
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// CollectGitWorkspace shells out to git per service in parallel and returns
// one row per service that has services.<name>.dir configured. The slice is
// alphabetically sorted by service name. Cancellation propagates via ctx.
//
// Per-row Err discriminates "service has no own repo" (Err == nil, blank
// cells) from "configuration smells" (Err != nil — dir missing, shellout
// failure, parse failure).
func CollectGitWorkspace(ctx context.Context, cfg *config.DevboxConfig) []statusview.GitWorkspaceRow {
	if cfg == nil {
		return nil
	}
	names := slices.Sorted(maps.Keys(cfg.Services))
	rows := make([]statusview.GitWorkspaceRow, 0, len(names))
	indices := make([]int, 0, len(names))
	for _, name := range names {
		svc := cfg.Services[name]
		if svc.Dir == "" {
			continue
		}
		indices = append(indices, len(rows))
		rows = append(rows, statusview.GitWorkspaceRow{
			Service: name,
			Dir:     svc.Dir,
		})
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, idx := range indices {
		i := idx
		g.Go(func() error {
			fillGitRow(gctx, &rows[i])
			return nil
		})
	}
	_ = g.Wait()
	return rows
}

// fillGitRow probes one service directory and populates the row in place.
// The boundary check (no <dir>/.git → blank cells, nil Err) runs before any
// shellout. Service-dir missing on disk is reported as an error; a non-repo
// service directory is not.
func fillGitRow(ctx context.Context, row *statusview.GitWorkspaceRow) {
	info, err := os.Stat(row.Dir)
	if err != nil {
		row.Err = fmt.Errorf("stat %s: %w", row.Dir, err)
		return
	}
	if !info.IsDir() {
		row.Err = fmt.Errorf("%s: not a directory", row.Dir)
		return
	}
	if !hasOwnGitDir(row.Dir) {
		// Not a repo of its own — blank cells, no error.
		return
	}

	out, err := gitShellOutFn(ctx, row.Dir)
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
