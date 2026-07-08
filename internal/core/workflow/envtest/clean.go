package envtest

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
)

// CleanRequest configures a Clean sweep.
type CleanRequest struct {
	// BaseDir is the project root (workspace.yml's directory).
	BaseDir string
	// Scenarios restricts the sweep to these scenario names; empty means
	// every scenario with a manifest.
	Scenarios []string
	// DryRun classifies every free manifest as would-sweep, without actually
	// running teardown.
	DryRun bool
	// Warn receives best-effort diagnostics (a teardown failure, a broken
	// root config, an unreachable Docker daemon during the orphan scan).
	// Optional; nil is a no-op.
	Warn func(string)
}

// CleanEntry identifies a single scenario run's manifest.
type CleanEntry struct {
	Scenario       string
	ComposeProject string
	CopyPath       string
}

// SkippedEntry is a manifest Clean did not sweep because its scenario's flock
// is held by a live run, or the lock could not be classified — never destroy
// on uncertainty.
type SkippedEntry struct {
	CleanEntry
	Reason string
}

// FailedEntry is a manifest whose teardown did not complete cleanly. It is
// deliberately NOT counted as swept: Teardown is best-effort and may have
// deleted the manifest — the recovery anchor — after an earlier container/
// volume/copy-removal step failed, so reporting it swept would mask a
// leftover the next `dwe test run` would collide with.
type FailedEntry struct {
	CleanEntry
	Error string
}

// OrphanEntry is a compose project discovered by the test-prefix label scan
// with no matching manifest. Report-only: Clean never destroys by name
// pattern, only by exact manifest-recorded identity.
type OrphanEntry struct {
	ComposeProject string
	Note           string
}

// CleanResult is the outcome of a Clean sweep.
type CleanResult struct {
	DryRun  bool
	Swept   []CleanEntry
	Skipped []SkippedEntry
	Failed  []FailedEntry
	Orphans []OrphanEntry
}

// cleanTeardownFn is a test seam over Teardown for the manifest at
// manifestPath: production reuses Teardown verbatim (byte-identical
// behaviour to a scenario's own teardown); tests inject a recording stub.
var cleanTeardownFn = func(ctx context.Context, manifestPath string, m *Manifest, warn func(string)) error {
	return Teardown(ctx, m, NewTeardownDeps(manifestPath, nil), warn)
}

// listComposeProjectsReal lists every distinct, non-empty compose-project
// label value docker currently knows about (running or stopped containers),
// for the report-only orphan scan.
func listComposeProjectsReal(ctx context.Context, dockerBin string) ([]string, error) {
	out, err := exec.CommandContext(ctx, dockerBin, "ps", "-a", "--format", //nolint:gosec
		`{{.Label "`+docker.ComposeProjectLabel+`"}}`).Output()
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var projects []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		p := strings.TrimSpace(line)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		projects = append(projects, p)
	}
	return projects, nil
}

// listComposeProjectsFn is a test seam over listComposeProjectsReal: tests
// inject a scripted stub to assert on the orphan-scan math without spawning
// a real docker process.
var listComposeProjectsFn = listComposeProjectsReal

// Clean sweeps orphaned/kept test environments, driven strictly by the
// on-disk manifests (spec §7): reuse Teardown for destruction, flock-guard
// every live run so a concurrently running scenario is skipped rather than
// torn down, and report-only surface possible docker leftovers that have no
// manifest at all — never destroy those by name pattern.
func Clean(ctx context.Context, req CleanRequest) (*CleanResult, error) {
	warn := req.Warn
	if warn == nil {
		warn = func(string) {}
	}

	paths, err := ListManifests(req.BaseDir)
	if err != nil {
		return nil, err
	}

	type candidate struct {
		path string
		m    *Manifest
	}

	// Load the full, unfiltered manifest set first: even a manifest later
	// excluded by req.Scenarios or skipped as live still "explains" its
	// compose project, so it must not be reported as an orphan.
	var all []candidate
	known := map[string]bool{}
	for _, p := range paths {
		m, loadErr := LoadManifest(p)
		if loadErr != nil {
			warn(fmt.Sprintf("clean: loading manifest %s: %v", p, loadErr))
			continue
		}
		all = append(all, candidate{path: p, m: m})
		known[m.ComposeProject] = true
	}

	var scenarioFilter map[string]bool
	if len(req.Scenarios) > 0 {
		scenarioFilter = make(map[string]bool, len(req.Scenarios))
		for _, s := range req.Scenarios {
			scenarioFilter[s] = true
		}
	}

	result := &CleanResult{DryRun: req.DryRun}
	for _, c := range all {
		if scenarioFilter != nil && !scenarioFilter[c.m.Scenario] {
			continue
		}
		sweepManifest(req, c.path, c.m, warn, result)
	}

	scanOrphans(ctx, req.BaseDir, known, warn, result)

	return result, nil
}

// sweepManifest classifies and (unless DryRun) tears down a single manifest,
// appending the outcome to result. The scenario's flock is always released
// before returning.
func sweepManifest(req CleanRequest, path string, m *Manifest, warn func(string), result *CleanResult) {
	entry := CleanEntry{Scenario: m.Scenario, ComposeProject: m.ComposeProject, CopyPath: m.CopyPath}

	lk, err := lock.Acquire(LockPath(req.BaseDir, m.Scenario))
	if err != nil {
		if _, ok := errors.AsType[*lock.HeldError](err); ok {
			result.Skipped = append(result.Skipped, SkippedEntry{CleanEntry: entry, Reason: "live"})
		} else {
			warn(fmt.Sprintf("clean: %s: acquiring lock: %v", m.Scenario, err))
			result.Skipped = append(result.Skipped, SkippedEntry{CleanEntry: entry, Reason: "lock error"})
		}
		return
	}
	defer func() { _ = lk.Release() }()

	if req.DryRun {
		result.Swept = append(result.Swept, entry)
		return
	}

	tctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()

	if err := cleanTeardownFn(tctx, path, m, warn); err != nil {
		warn(fmt.Sprintf("clean: %s: teardown: %v", m.Scenario, err))
		result.Failed = append(result.Failed, FailedEntry{CleanEntry: entry, Error: err.Error()})
		return
	}
	result.Swept = append(result.Swept, entry)
}

// scanOrphans performs the report-only orphan scan: it is entirely
// best-effort and never turns Clean itself into a failure — a broken root
// config or an unreachable Docker daemon only means the advisory scan is
// skipped, not that the sweep above is undone.
func scanOrphans(ctx context.Context, baseDir string, known map[string]bool, warn func(string), result *CleanResult) {
	origCfg, err := config.LoadConfigOrWrap(filepath.Join(baseDir, "workspace.yml"))
	if err != nil {
		warn(fmt.Sprintf("clean: loading project config, skipping orphan scan: %v", err))
		return
	}

	projects, err := listComposeProjectsFn(ctx, config.DockerBin(origCfg))
	if err != nil {
		warn(fmt.Sprintf("clean: listing compose projects, skipping orphan scan: %v", err))
		return
	}

	prefix := normalizeComposeName(projectBaseName(origCfg) + "-t-")
	seen := map[string]bool{}
	for _, p := range projects {
		if !strings.HasPrefix(p, prefix) || known[p] || seen[p] {
			continue
		}
		seen[p] = true
		result.Orphans = append(result.Orphans, OrphanEntry{ComposeProject: p, Note: "no manifest — remove manually"})
	}
}
