package envtest

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
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

// orphanScanTimeout bounds the report-only orphan scan's `docker ps` probe. A
// wedged/unresponsive Docker daemon can make `docker ps` hang indefinitely, and
// scanOrphans runs synchronously after the sweep, so without a deadline it would
// stall the whole `dwe test clean` invocation — defeating the best-effort intent.
// 5s matches the bound the env validators use for their docker probes. A var so
// tests can shrink it.
var orphanScanTimeout = 5 * time.Second

// listComposeProjectsReal lists every distinct, non-empty compose-project
// label value docker currently knows about (running or stopped containers),
// for the report-only orphan scan.
func listComposeProjectsReal(ctx context.Context, dockerBin string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, orphanScanTimeout)
	defer cancel()

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
		// Warn on requested names that match no manifest. Clean sweeps by
		// on-disk manifest, not scenario definition, so an unmatched name is not
		// necessarily wrong (the scenario file may have been deleted after a kept
		// run) — hence a diagnostic, not an error — but it surfaces the common
		// typo case that would otherwise silently report "0 swept" and exit 0.
		present := make(map[string]bool, len(all))
		for _, c := range all {
			present[c.m.Scenario] = true
		}
		for _, s := range req.Scenarios {
			if !present[s] {
				warn(fmt.Sprintf("clean: no manifest for scenario %q — nothing to sweep (already clean, or a typo?)", s))
			}
		}
	}

	// Load the project's root config once, best-effort — needed ONLY by the
	// advisory orphan scan (base prefix + docker bin). Sweeping never consults
	// it: each manifest is validated and torn down from the manifest + its copy
	// alone (validateManifestIdentity), so a broken/mid-edit or renamed root
	// config must not block a recovery sweep. A load failure just skips the
	// orphan scan.
	origCfg, cfgErr := config.LoadConfigOrWrap(filepath.Join(req.BaseDir, "workspace.yml"))
	if cfgErr != nil {
		warn(fmt.Sprintf("clean: loading project config: %v", cfgErr))
		origCfg = nil
	}

	result := &CleanResult{DryRun: req.DryRun}
	for _, c := range all {
		if scenarioFilter != nil && !scenarioFilter[c.m.Scenario] {
			continue
		}
		sweepManifest(req, c.path, c.m, warn, result)
	}

	scanOrphans(ctx, origCfg, known, warn, result)

	return result, nil
}

// validateManifestIdentity guards Clean's destructive path against a tampered
// or corrupted manifest. `dwe test clean` is manifest-driven and ultimately
// runs os.RemoveAll(m.CopyPath) (teardown.go) plus a bridge-daemon stop against
// m.BridgeDir — both attacker-influenceable if the manifest is trusted blindly.
// The manifests live in the project's .dwe/tests/manifests/, which DWE mounts
// into containers, so a lower-trust container can plant a manifest with an
// arbitrary copy_path/bridge_dir; even though `dwe test clean` is itself
// container-blocked, a host developer running it would then delete a
// container-chosen path. It also guards against a corrupted/partial manifest.
//
// Every path a sweep touches is deterministically derived by the runner from
// (baseDir, scenario) — CopyPath == RunDir, BridgeDir == DefaultBridgeDir(copy),
// and the on-disk filename == "<scenario>-<runID>.yml". This re-derives those
// invariants and rejects any manifest that does not match, so only the
// canonical run directory under .dwe/tests/runs/ can ever be destroyed.
//
// compose_project is pinned to the copy's OWN stamped docker identity (the
// value the runner wrote into the copy at run time) rather than to the current
// root config — a manifest kept/failed before a `project.name`/`project.prefix`
// change is still recoverable, since the copy carries the identity teardown
// actually uses. See validateComposeProject.
func validateManifestIdentity(baseDir, manifestPath string, m *Manifest) error {
	if m.Scenario == "" {
		return fmt.Errorf("empty scenario")
	}
	if m.RunID == "" {
		return fmt.Errorf("empty run id")
	}
	// Tie the sweep target to the name on disk, so one scenario's manifest
	// cannot declare another scenario's identity.
	wantName := m.Scenario + "-" + m.RunID + ".yml"
	if got := filepath.Base(manifestPath); got != wantName {
		return fmt.Errorf("filename %q does not match declared identity %q", got, wantName)
	}
	// CopyPath is the sole path os.RemoveAll touches: require the canonical run
	// dir, and defensively confirm it stays contained under .dwe/tests/runs/
	// (a scenario carrying "../" would otherwise escape).
	expectedCopy := RunDir(baseDir, m.Scenario)
	if !samePath(m.CopyPath, expectedCopy) {
		return fmt.Errorf("copy_path %q is not the expected run dir %q", m.CopyPath, expectedCopy)
	}
	runsRoot := filepath.Join(testsRootDir(baseDir), "runs")
	if !isWithin(runsRoot, expectedCopy) {
		return fmt.Errorf("copy_path %q escapes %q", expectedCopy, runsRoot)
	}
	// isWithin is purely lexical, so it cannot see a symlinked path component:
	// a container that can plant a symlink at .dwe/tests/runs (or any ancestor)
	// pointing outside the project would make os.RemoveAll(copy_path) resolve
	// through it and delete host state outside the test root, even though the
	// literal string is the canonical run dir. Prove no existing component
	// between the project root and the deletion target is a symlink before any
	// teardown runs (a not-yet-created run dir stops the walk early and is a
	// safe no-op for RemoveAll).
	if err := pathsafe.CheckNoSymlinks(baseDir, expectedCopy, "copy_path"); err != nil {
		return err
	}
	if want := bridge.DefaultBridgeDir(m.CopyPath); !samePath(m.BridgeDir, want) {
		return fmt.Errorf("bridge_dir %q is not %q", m.BridgeDir, want)
	}
	// compose_project drives destructive Docker teardown (container reap by
	// label, volume removal by prefix). It is just as attacker-influenceable as
	// the paths above, so it must be pinned too — a canonical copy_path/bridge_dir
	// otherwise lets a crafted manifest aim teardown at another compose project.
	if err := validateComposeProject(m); err != nil {
		return err
	}
	return nil
}

// validateComposeProject pins m.ComposeProject to the runner-derived identity.
//
// It NEVER re-derives that identity from the CURRENT root config: a run kept
// (--keep) or left behind by a failure before the developer renamed the project
// (`project.name`/`project.prefix` changed) still records — and its copy is
// still stamped with — the exact compose project the run created. Matching the
// current config's derived name would then reject a legitimate manifest and
// strand its copy + Docker resources, contradicting `dwe test clean`'s
// manifest+copy recovery contract. Instead:
//
//   - The value must ALWAYS carry the normalised "-t-<scenario>-<runID>" test
//     infix the runner unconditionally produces (regardless of base) — a floor
//     that the real dev environment ("<base>") never satisfies, so teardown can
//     never be aimed at it even if the copy's own identity were tampered.
//   - When the disposable copy still loads, pin exactly to the identity it was
//     stamped with at run time (config-drift-proof; the same value the copy's
//     own `docker compose down` resolves), so a crafted manifest cannot point
//     teardown at a different in-range project. A gone/unloadable copy degrades
//     to the infix floor alone (nothing left to cross-check against).
func validateComposeProject(m *Manifest) error {
	suffix := normalizeComposeName("-t-" + m.Scenario + "-" + m.RunID)
	if !strings.HasSuffix(m.ComposeProject, suffix) {
		return fmt.Errorf("compose_project %q lacks the expected test suffix %q", m.ComposeProject, suffix)
	}
	if want, ok := copyComposeIdentity(m.CopyPath); ok && m.ComposeProject != want {
		return fmt.Errorf("compose_project %q is not the copy's stamped identity %q", m.ComposeProject, want)
	}
	return nil
}

// copyComposeIdentity returns the compose project name the disposable copy was
// stamped with at run time (WriteDockerIdentity → the copy's docker.yml /
// docker.local.yml project_name), resolved exactly as the copy's own `docker
// compose` invocations resolve it (config.ComposeProjectName). ok is false when
// the copy is gone or its config no longer loads — a degraded manifest whose
// copy was already removed — in which case the caller falls back to the infix
// shape check alone. This value reflects the project identity when the run was
// created, so it survives a later project rename.
func copyComposeIdentity(copyPath string) (string, bool) {
	cfg, dockerCfg, err := loadCopyConfig(copyPath)
	if err != nil {
		return "", false
	}
	name := config.ComposeProjectName(dockerCfg, cfg)
	if name == "" {
		return "", false
	}
	return name, true
}

// samePath reports whether a and b denote the same filesystem path, tolerating
// clean/abs differences between the base dir recorded at run time and the one
// passed to Clean (both are normally absolute, so this only matters at the
// margins — it must never reject a legitimate manifest).
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && aa == bb
}

// isWithin reports whether p is root itself or nested under it (no "../"
// escape), comparing lexically after Clean.
func isWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// sweepManifest classifies and (unless DryRun) tears down a single manifest,
// appending the outcome to result. The scenario's flock is always released
// before returning.
func sweepManifest(req CleanRequest, path string, m *Manifest, warn func(string), result *CleanResult) {
	entry := CleanEntry{Scenario: m.Scenario, ComposeProject: m.ComposeProject, CopyPath: m.CopyPath}

	// Never run a destructive teardown against a manifest whose declared paths
	// don't match what the runner would deterministically produce (tampered or
	// corrupted). Report it and move on — never os.RemoveAll a claimed path.
	if err := validateManifestIdentity(req.BaseDir, path, m); err != nil {
		warn(fmt.Sprintf("clean: %s: refusing untrusted manifest %s: %v", m.Scenario, path, err))
		result.Skipped = append(result.Skipped, SkippedEntry{CleanEntry: entry, Reason: "invalid manifest"})
		return
	}

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
func scanOrphans(ctx context.Context, origCfg *config.DweConfig, known map[string]bool, warn func(string), result *CleanResult) {
	if origCfg == nil {
		// Root config failed to load (already warned in Clean); the advisory
		// orphan scan needs it for the docker bin + prefix, so skip it.
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
