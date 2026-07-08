package envtest

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/lock"
)

// stubCleanSeams overrides cleanTeardownFn and listComposeProjectsFn for the
// duration of the test, restoring the real implementations on cleanup — every
// Clean test must avoid touching real Docker/subprocesses.
func stubCleanSeams(
	t *testing.T,
	teardown func(ctx context.Context, manifestPath string, m *Manifest, warn func(string)) error,
	listProjects func(ctx context.Context, dockerBin string) ([]string, error),
) {
	t.Helper()
	origTeardown := cleanTeardownFn
	origList := listComposeProjectsFn
	cleanTeardownFn = teardown
	listComposeProjectsFn = listProjects
	t.Cleanup(func() {
		cleanTeardownFn = origTeardown
		listComposeProjectsFn = origList
	})
}

// writeCleanManifest writes a manifest for scenario/runID with the given
// compose project name, returning it.
func writeCleanManifest(t *testing.T, dir, scenario, runID, composeProject string) *Manifest {
	t.Helper()
	m := &Manifest{
		Scenario:       scenario,
		RunID:          runID,
		ComposeProject: composeProject,
		CopyPath:       filepath.Join(dir, "copy-"+scenario+"-"+runID),
	}
	if err := WriteManifest(ManifestPath(dir, scenario, runID), m); err != nil {
		t.Fatalf("writing manifest for %s: %v", scenario, err)
	}
	return m
}

func noOrphans(context.Context, string) ([]string, error) { return nil, nil }

func fatalListProjects(t *testing.T) func(context.Context, string) ([]string, error) {
	t.Helper()
	return func(context.Context, string) ([]string, error) {
		t.Fatal("listComposeProjectsFn must not be called")
		return nil, nil
	}
}

func fatalTeardown(t *testing.T) func(context.Context, string, *Manifest, func(string)) error {
	t.Helper()
	return func(context.Context, string, *Manifest, func(string)) error {
		t.Fatal("cleanTeardownFn must not be called")
		return nil
	}
}

func TestClean_SweepsFreeManifestsInOrder(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "x", "")
	writeCleanManifest(t, dir, "alpha", "aaaaaa", "dwe-t-alpha-aaaaaa")
	writeCleanManifest(t, dir, "beta", "bbbbbb", "dwe-t-beta-bbbbbb")

	var calls []string
	stubCleanSeams(t, func(_ context.Context, manifestPath string, m *Manifest, _ func(string)) error {
		calls = append(calls, m.Scenario)
		_ = manifestPath
		return nil
	}, noOrphans)

	result, err := Clean(context.Background(), CleanRequest{BaseDir: dir})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(result.Swept) != 2 || result.Swept[0].Scenario != "alpha" || result.Swept[1].Scenario != "beta" {
		t.Fatalf("unexpected Swept: %+v", result.Swept)
	}
	if len(result.Skipped) != 0 || len(result.Failed) != 0 || len(result.Orphans) != 0 {
		t.Fatalf("unexpected non-swept entries: %+v", result)
	}
	if want := []string{"alpha", "beta"}; !equalStrings(calls, want) {
		t.Fatalf("teardown call order = %v, want %v", calls, want)
	}
}

func TestClean_LiveScenario_SkippedNotTornDown(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "x", "")
	writeCleanManifest(t, dir, "smoke", "aaaaaa", "dwe-t-smoke-aaaaaa")

	held, err := lock.Acquire(LockPath(dir, "smoke"))
	if err != nil {
		t.Fatalf("pre-acquiring lock: %v", err)
	}
	defer func() { _ = held.Release() }()

	stubCleanSeams(t, fatalTeardown(t), noOrphans)

	result, err := Clean(context.Background(), CleanRequest{BaseDir: dir})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(result.Swept) != 0 {
		t.Fatalf("expected nothing swept, got %+v", result.Swept)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Scenario != "smoke" || result.Skipped[0].Reason != "live" {
		t.Fatalf("unexpected Skipped: %+v", result.Skipped)
	}
}

func TestClean_DryRun_NoTeardown_LockReleased(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "x", "")
	writeCleanManifest(t, dir, "smoke", "aaaaaa", "dwe-t-smoke-aaaaaa")

	stubCleanSeams(t, fatalTeardown(t), noOrphans)

	result, err := Clean(context.Background(), CleanRequest{BaseDir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if !result.DryRun {
		t.Fatalf("expected DryRun result flag set")
	}
	if len(result.Swept) != 1 || result.Swept[0].Scenario != "smoke" {
		t.Fatalf("unexpected Swept (would-sweep): %+v", result.Swept)
	}

	// The flock must have been released after classification, else a real
	// `dwe test run smoke` afterward would incorrectly see it as live.
	lk, err := lock.Acquire(LockPath(dir, "smoke"))
	if err != nil {
		t.Fatalf("expected lock to be free after dry-run Clean, got: %v", err)
	}
	_ = lk.Release()
}

func TestClean_ScenarioFilter(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "x", "")
	writeCleanManifest(t, dir, "alpha", "aaaaaa", "dwe-t-alpha-aaaaaa")
	writeCleanManifest(t, dir, "beta", "bbbbbb", "dwe-t-beta-bbbbbb")

	var calls []string
	stubCleanSeams(t, func(_ context.Context, _ string, m *Manifest, _ func(string)) error {
		calls = append(calls, m.Scenario)
		return nil
	}, func(context.Context, string) ([]string, error) {
		// beta's compose project is still reported by docker even though beta
		// is filtered out of this sweep — it must not surface as an orphan,
		// since its manifest still "explains" it.
		return []string{"dwe-t-beta-bbbbbb"}, nil
	})

	result, err := Clean(context.Background(), CleanRequest{BaseDir: dir, Scenarios: []string{"alpha"}})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(result.Swept) != 1 || result.Swept[0].Scenario != "alpha" {
		t.Fatalf("unexpected Swept: %+v", result.Swept)
	}
	if !equalStrings(calls, []string{"alpha"}) {
		t.Fatalf("expected only alpha's teardown to run, got %v", calls)
	}
	if len(result.Orphans) != 0 {
		t.Fatalf("filtered-out beta must not surface as an orphan, got %+v", result.Orphans)
	}
}

func TestClean_OrphanScan_PrefixMinusKnownMinusUnrelated(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "x", "")
	writeCleanManifest(t, dir, "alpha", "aaaaaa", "dwe-t-alpha-aaaaaa")

	stubCleanSeams(t, func(_ context.Context, _ string, _ *Manifest, _ func(string)) error {
		return nil
	}, func(context.Context, string) ([]string, error) {
		return []string{
			"dwe-t-alpha-aaaaaa", // known — excluded
			"dwe-t-ghost-zzzzzz", // orphan — same prefix, no manifest
			"other-t-x-111111",   // unrelated prefix — excluded
			"dwe-t-ghost-zzzzzz", // duplicate — deduped
			"",                   // empty label — ignored
		}, nil
	})

	result, err := Clean(context.Background(), CleanRequest{BaseDir: dir})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(result.Orphans) != 1 || result.Orphans[0].ComposeProject != "dwe-t-ghost-zzzzzz" {
		t.Fatalf("unexpected Orphans: %+v", result.Orphans)
	}
}

func TestClean_AbsentManifestsDir_EmptyResult(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "x", "")

	stubCleanSeams(t, fatalTeardown(t), noOrphans)

	result, err := Clean(context.Background(), CleanRequest{BaseDir: dir})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(result.Swept) != 0 || len(result.Skipped) != 0 || len(result.Failed) != 0 || len(result.Orphans) != 0 {
		t.Fatalf("expected an entirely empty result, got %+v", result)
	}
}

func TestClean_OrigCfgLoadFailure_StillSweeps(t *testing.T) {
	// No workspace.yml at all — origCfg load must fail, but the sweep itself
	// must still proceed from the manifests alone.
	dir := t.TempDir()
	writeCleanManifest(t, dir, "smoke", "aaaaaa", "someproj-t-smoke-aaaaaa")

	var swept bool
	var warned []string
	stubCleanSeams(t, func(_ context.Context, _ string, _ *Manifest, _ func(string)) error {
		swept = true
		return nil
	}, fatalListProjects(t))

	result, err := Clean(context.Background(), CleanRequest{
		BaseDir: dir,
		Warn:    func(msg string) { warned = append(warned, msg) },
	})
	if err != nil {
		t.Fatalf("Clean must not hard-fail on a broken root config: %v", err)
	}
	if !swept || len(result.Swept) != 1 {
		t.Fatalf("expected the manifest to still be swept, got %+v (swept=%v)", result, swept)
	}
	if len(result.Orphans) != 0 {
		t.Fatalf("expected no orphan scan without a loadable config, got %+v", result.Orphans)
	}
	if len(warned) == 0 {
		t.Fatalf("expected a warning about the unloadable config")
	}
}

func TestClean_ListComposeProjectsError_OrphansEmptySweepIntact(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "x", "")
	writeCleanManifest(t, dir, "smoke", "aaaaaa", "dwe-t-smoke-aaaaaa")

	var warned []string
	stubCleanSeams(t, func(_ context.Context, _ string, _ *Manifest, _ func(string)) error {
		return nil
	}, func(context.Context, string) ([]string, error) {
		return nil, errors.New("docker unreachable")
	})

	result, err := Clean(context.Background(), CleanRequest{
		BaseDir: dir,
		Warn:    func(msg string) { warned = append(warned, msg) },
	})
	if err != nil {
		t.Fatalf("Clean must not hard-fail when the orphan scan can't reach docker: %v", err)
	}
	if len(result.Swept) != 1 {
		t.Fatalf("expected the sweep to still succeed, got %+v", result.Swept)
	}
	if len(result.Orphans) != 0 {
		t.Fatalf("expected no orphans on a scan error, got %+v", result.Orphans)
	}
	if len(warned) == 0 {
		t.Fatalf("expected a warning about the failed orphan scan")
	}
}

func TestClean_TeardownError_RecordsFailedNotSwept(t *testing.T) {
	dir := writeRunnerFixtureProject(t, "x", "")
	writeCleanManifest(t, dir, "smoke", "aaaaaa", "dwe-t-smoke-aaaaaa")

	var warned []string
	stubCleanSeams(t, func(_ context.Context, _ string, _ *Manifest, _ func(string)) error {
		return errors.New("boom")
	}, noOrphans)

	result, err := Clean(context.Background(), CleanRequest{
		BaseDir: dir,
		Warn:    func(msg string) { warned = append(warned, msg) },
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(result.Swept) != 0 {
		t.Fatalf("a failed teardown must NOT be counted as swept, got %+v", result.Swept)
	}
	if len(result.Failed) != 1 || result.Failed[0].Scenario != "smoke" || !strings.Contains(result.Failed[0].Error, "boom") {
		t.Fatalf("unexpected Failed: %+v", result.Failed)
	}
	if len(warned) == 0 {
		t.Fatalf("expected a warning about the failed teardown")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
