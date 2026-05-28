package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Status holds the result of a git probe for a working directory.
type Status struct {
	IsRepo      bool
	HasUpstream bool
	Branch      string
	Upstream    string
	Dirty       bool
	Behind      int
	Ahead       int
	// FetchOK is true when a fetch was attempted and succeeded.
	FetchOK bool
	// FetchErr holds the stderr from a failed fetch attempt; empty if fetch was
	// not attempted (FetchOK == false && FetchErr == "" means mode was "off").
	FetchErr string
}

// runner abstracts os/exec so unit tests can inject stubs.
type runner interface {
	Run(ctx context.Context, dir string, args ...string) (stdout, stderr string, err error)
}

// execRunner is the production runner backed by os/exec.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec
	cmd.Dir = dir
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	return strings.TrimSpace(outBuf.String()), strings.TrimSpace(errBuf.String()), err
}

// defaultRunner is the runner used by Probe and PullFFOnly.
var defaultRunner runner = execRunner{}

const fetchTimeout = 15 * time.Second
const pullTimeout = 2 * time.Minute

// Probe inspects the git repository at workDir and returns a Status.
//
// bin is the git executable name (typically from config.GitBin); empty falls
// back to "git". When fetch is true and the working tree has an upstream,
// Probe runs `git fetch` before counting behind/ahead. On fetch failure the
// probe still returns a populated Status with FetchErr set; the caller should
// warn and continue rather than treating it as a fatal error.
func Probe(bin, workDir string, fetch bool) (Status, error) {
	return probeWith(bin, workDir, fetch, defaultRunner)
}

func probeWith(bin, workDir string, fetch bool, r runner) (Status, error) {
	if bin == "" {
		bin = "git"
	}
	ctx := context.Background()
	var s Status

	// 1. Is this a git repo?
	_, _, err := r.Run(ctx, workDir, bin, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		// Not a git repo — return zero status without error.
		return s, nil
	}
	s.IsRepo = true

	// 2. Dirty check.
	out, _, err := r.Run(ctx, workDir, bin, "status", "--porcelain")
	if err != nil {
		return s, fmt.Errorf("git status: %w", err)
	}
	s.Dirty = out != ""

	// 3. Current branch.
	branch, _, err := r.Run(ctx, workDir, bin, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return s, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	s.Branch = branch

	// 4. Upstream.
	upstream, _, err := r.Run(ctx, workDir, bin, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err == nil {
		s.HasUpstream = true
		s.Upstream = upstream
	}
	// No upstream is non-fatal.

	// 5. Fetch (if requested and upstream exists).
	if fetch && s.HasUpstream {
		remote := strings.SplitN(s.Upstream, "/", 2)[0]
		fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
		defer cancel()
		_, stderr, ferr := r.Run(fetchCtx, workDir, bin, "fetch", "--quiet", remote)
		if ferr != nil {
			s.FetchOK = false
			s.FetchErr = stderr
			if s.FetchErr == "" {
				s.FetchErr = ferr.Error()
			}
			// Non-fatal: return populated status so caller can warn.
			return s, nil
		}
		s.FetchOK = true
	}

	// 6. Behind/ahead counts (only meaningful after a successful fetch).
	if s.HasUpstream {
		counts, _, err := r.Run(ctx, workDir, bin, "rev-list", "--left-right", "--count",
			s.Upstream+"...HEAD")
		if err != nil {
			// Non-fatal: leave Behind/Ahead as zero.
			return s, nil
		}
		parts := strings.Fields(counts)
		if len(parts) == 2 {
			_, _ = fmt.Sscanf(parts[0], "%d", &s.Behind)
			_, _ = fmt.Sscanf(parts[1], "%d", &s.Ahead)
		}
	}

	return s, nil
}

// PullFFOnly runs `<bin> pull --ff-only` in workDir and reports whether HEAD
// actually moved (i.e. new commits were integrated). bin is the git executable
// (typically from config.GitBin); empty falls back to "git".
func PullFFOnly(bin, workDir string) (moved bool, err error) {
	return pullFFOnlyWith(bin, workDir, defaultRunner)
}

func pullFFOnlyWith(bin, workDir string, r runner) (bool, error) {
	if bin == "" {
		bin = "git"
	}
	ctx := context.Background()

	before, _, err := r.Run(ctx, workDir, bin, "rev-parse", "HEAD")
	if err != nil {
		return false, fmt.Errorf("git rev-parse HEAD (before): %w", err)
	}

	pullCtx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()
	_, stderr, err := r.Run(pullCtx, workDir, bin, "pull", "--ff-only")
	if err != nil {
		if stderr != "" {
			return false, fmt.Errorf("git pull --ff-only: %w\n%s", err, stderr)
		}
		return false, fmt.Errorf("git pull --ff-only: %w", err)
	}

	after, _, err := r.Run(ctx, workDir, bin, "rev-parse", "HEAD")
	if err != nil {
		return false, fmt.Errorf("git rev-parse HEAD (after): %w", err)
	}

	return before != after, nil
}
