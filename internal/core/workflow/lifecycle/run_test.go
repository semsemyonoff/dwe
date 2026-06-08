package lifecycle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/generatedstore"
	"github.com/semsemyonoff/dwe/internal/shared/git"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// --- resolveUpdateMode tests (unexported, same package) ---

func TestResolveUpdateMode_UpdateBlockOmitted_NoFlag(t *testing.T) {
	cfg := &config.LifecycleRunConfig{Update: nil}
	if got := resolveUpdateMode(cfg, false, ""); got != "off" {
		t.Errorf("resolveUpdateMode = %q, want %q", got, "off")
	}
}

func TestResolveUpdateMode_ModeOn_NoFlag(t *testing.T) {
	cfg := &config.LifecycleRunConfig{
		Update: &config.LifecycleUpdate{Mode: "on"},
	}
	if got := resolveUpdateMode(cfg, false, ""); got != "on" {
		t.Errorf("resolveUpdateMode = %q, want %q", got, "on")
	}
}

func TestResolveUpdateMode_ModeOff_NoFlag(t *testing.T) {
	cfg := &config.LifecycleRunConfig{
		Update: &config.LifecycleUpdate{Mode: "off"},
	}
	if got := resolveUpdateMode(cfg, false, ""); got != "off" {
		t.Errorf("resolveUpdateMode = %q, want %q", got, "off")
	}
}

func TestResolveUpdateMode_NoUpdateFlag_ForcesOff(t *testing.T) {
	cfg := &config.LifecycleRunConfig{
		Update: &config.LifecycleUpdate{Mode: "on"},
	}
	if got := resolveUpdateMode(cfg, true, ""); got != "off" {
		t.Errorf("resolveUpdateMode with NoUpdate = %q, want %q", got, "off")
	}
}

func TestResolveUpdateMode_UpdateFlag_OverridesYAML(t *testing.T) {
	cfg := &config.LifecycleRunConfig{
		Update: &config.LifecycleUpdate{Mode: "off"},
	}
	if got := resolveUpdateMode(cfg, false, "on"); got != "on" {
		t.Errorf("resolveUpdateMode with UpdateMode=on = %q, want %q", got, "on")
	}
}

func TestResolveUpdateMode_UpdateFlag_OverridesOmittedBlock(t *testing.T) {
	cfg := &config.LifecycleRunConfig{Update: nil}
	if got := resolveUpdateMode(cfg, false, "on"); got != "on" {
		t.Errorf("resolveUpdateMode with UpdateMode=on (no block) = %q, want %q", got, "on")
	}
}

func TestResolveUpdateMode_NoUpdateTakesPrecedenceOverUpdateFlag(t *testing.T) {
	cfg := &config.LifecycleRunConfig{
		Update: &config.LifecycleUpdate{Mode: "on"},
	}
	if got := resolveUpdateMode(cfg, true, "on"); got != "off" {
		t.Errorf("resolveUpdateMode with NoUpdate + UpdateMode=on = %q, want %q", got, "off")
	}
}

// --- RunRun tests ---

func TestRunRun_MissingLifecycleYML_UsesDefault(t *testing.T) {
	// Stub RunPhasesFunc to avoid recursive test-binary execution from
	// type:dwe steps calling os.Executable() in the default run config.
	stubRunPhases(t)

	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	var called []DefaultedPipeline
	ctx := RunContext{
		ConfigPath: cfgPath,
		OnDefaultUsed: func(p DefaultedPipeline) {
			called = append(called, p)
		},
	}
	if err := RunRun(ctx); err != nil {
		t.Fatalf("RunRun with missing lifecycle.yml should succeed (built-in default), got: %v", err)
	}
	if len(called) != 1 || called[0] != DefaultedRun {
		t.Errorf("OnDefaultUsed calls = %v, want [%q]", called, DefaultedRun)
	}
}

func TestRunRun_MissingRunSection_UsesDefault(t *testing.T) {
	// Stub RunPhasesFunc to avoid recursive test-binary execution from
	// type:dwe steps calling os.Executable() in the default run config.
	stubRunPhases(t)

	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	lifecycleYAML := "stop:\n  final_message: bye\n  phases: []\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	var called []DefaultedPipeline
	ctx := RunContext{
		ConfigPath: cfgPath,
		OnDefaultUsed: func(p DefaultedPipeline) {
			called = append(called, p)
		},
	}
	if err := RunRun(ctx); err != nil {
		t.Fatalf("RunRun with no run: section should succeed (built-in default), got: %v", err)
	}
	if len(called) != 1 || called[0] != DefaultedRun {
		t.Errorf("OnDefaultUsed calls = %v, want [%q]", called, DefaultedRun)
	}
}

// TestRunRun_MissingLifecycleYML_DefaultedCallbackFiresOnceAcrossPullReload
// guards against the OnDefaultUsed callback being invoked twice when
// lifecycle.yml is absent both before and after a pull. The CLI surfaces this
// callback as a stderr info line — two callbacks would print two duplicate
// notices.
func TestRunRun_MissingLifecycleYML_DefaultedCallbackFiresOnceAcrossPullReload(t *testing.T) {
	stubRunPhases(t)

	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	origProbe := GitProbeFunc
	origPull := GitPullFFOnlyFunc
	t.Cleanup(func() {
		GitProbeFunc = origProbe
		GitPullFFOnlyFunc = origPull
	})

	GitProbeFunc = func(_, _ string, _ bool) (git.Status, error) {
		return git.Status{
			IsRepo:      true,
			HasUpstream: true,
			Branch:      "main",
			Upstream:    "origin/main",
			FetchOK:     true,
			Behind:      1,
		}, nil
	}
	GitPullFFOnlyFunc = func(_, _ string) (bool, error) {
		// Pull "succeeds" but lifecycle.yml remains absent (e.g. a doc-only
		// upstream change). Both EnsureRunConfig calls return defaulted=true.
		return true, nil
	}

	var called []DefaultedPipeline
	ctx := RunContext{
		ConfigPath: cfgPath,
		UpdateMode: "on",
		OnDefaultUsed: func(p DefaultedPipeline) {
			called = append(called, p)
		},
	}
	if err := RunRun(ctx); err != nil {
		t.Fatalf("RunRun: %v", err)
	}
	if len(called) != 1 || called[0] != DefaultedRun {
		t.Errorf("OnDefaultUsed should fire exactly once across pre/post-pull reload, got %v", called)
	}
}

func TestRunRun_ReloadsConfigAfterPull(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}

	writeLifecycleYML(t, workspaceDir, "before-reload")

	origProbe := GitProbeFunc
	origPull := GitPullFFOnlyFunc
	t.Cleanup(func() {
		GitProbeFunc = origProbe
		GitPullFFOnlyFunc = origPull
	})

	GitProbeFunc = func(_, workDir string, fetch bool) (git.Status, error) {
		return git.Status{
			IsRepo:      true,
			HasUpstream: true,
			Branch:      "main",
			Upstream:    "origin/main",
			FetchOK:     true,
			Behind:      1,
		}, nil
	}

	GitPullFFOnlyFunc = func(_, workDir string) (bool, error) {
		writeLifecycleYML(t, workspaceDir, "after-reload")
		return true, nil
	}

	ctx := RunContext{ConfigPath: cfgPath, UpdateMode: "on"}
	err := RunRun(ctx)
	if err != nil {
		t.Errorf("unexpected error after simulated pull: %v", err)
	}
}

func TestRunRun_NoUpdateFlag_SkipsFetch(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	writeLifecycleYML(t, workspaceDir, "done")

	origProbe := GitProbeFunc
	t.Cleanup(func() { GitProbeFunc = origProbe })

	fetchCalled := false
	GitProbeFunc = func(_, workDir string, fetch bool) (git.Status, error) {
		if fetch {
			fetchCalled = true
		}
		return git.Status{IsRepo: false}, nil
	}

	ctx := RunContext{ConfigPath: cfgPath, NoUpdate: true}
	err := RunRun(ctx)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if fetchCalled {
		t.Error("git fetch should NOT be called when NoUpdate is set")
	}
}

func TestRunRun_UpdateFlagOff_SkipsFetch(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	yaml := "run:\n  update:\n    mode: on\n  phases:\n    - name: s\n      steps:\n        - name: n\n          type: shell\n          cmd: \"true\"\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "lifecycle.yml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	origProbe := GitProbeFunc
	t.Cleanup(func() { GitProbeFunc = origProbe })

	fetchCalled := false
	GitProbeFunc = func(_, workDir string, fetch bool) (git.Status, error) {
		if fetch {
			fetchCalled = true
		}
		return git.Status{IsRepo: false}, nil
	}

	ctx := RunContext{ConfigPath: cfgPath, UpdateMode: "off"}
	err := RunRun(ctx)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if fetchCalled {
		t.Error("git fetch should NOT be called when UpdateMode=off")
	}
}

func TestRunRun_UpdateBlockOmitted_DefaultsToOffNoFetch(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	yaml := "run:\n  phases:\n    - name: s\n      steps:\n        - name: n\n          type: shell\n          cmd: \"true\"\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "lifecycle.yml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	origProbe := GitProbeFunc
	t.Cleanup(func() { GitProbeFunc = origProbe })

	fetchCalled := false
	GitProbeFunc = func(_, workDir string, fetch bool) (git.Status, error) {
		if fetch {
			fetchCalled = true
		}
		return git.Status{IsRepo: false}, nil
	}

	ctx := RunContext{ConfigPath: cfgPath}
	if err := RunRun(ctx); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if fetchCalled {
		t.Error("git fetch should NOT be called when update: block is omitted (effective mode = off)")
	}
}

func TestRunRun_ProbeError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	writeLifecycleYML(t, workspaceDir, "done")

	origProbe := GitProbeFunc
	t.Cleanup(func() { GitProbeFunc = origProbe })

	GitProbeFunc = func(_, workDir string, fetch bool) (git.Status, error) {
		return git.Status{}, errors.New("probe failed")
	}

	ctx := RunContext{ConfigPath: cfgPath, UpdateMode: "on"}
	err := RunRun(ctx)
	if err == nil {
		t.Fatal("expected error from probe failure, got nil")
	}
	if !strings.Contains(err.Error(), "git probe") {
		t.Errorf("error should mention 'git probe', got: %v", err)
	}
}

func TestRunRun_InvalidUpdateFlag(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	writeLifecycleYML(t, workspaceDir, "done")

	ctx := RunContext{ConfigPath: cfgPath, UpdateMode: "bogus"}
	err := RunRun(ctx)
	if err == nil {
		t.Fatal("expected error for invalid update mode, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --update mode") {
		t.Errorf("error should mention 'invalid --update mode', got: %v", err)
	}
}

func TestRunRun_WarnOnFetchFailed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	writeLifecycleYML(t, workspaceDir, "done")

	origProbe := GitProbeFunc
	t.Cleanup(func() { GitProbeFunc = origProbe })

	GitProbeFunc = func(_, workDir string, fetch bool) (git.Status, error) {
		return git.Status{
			IsRepo:      true,
			HasUpstream: true,
			Branch:      "main",
			Upstream:    "origin/main",
			FetchOK:     false,
			FetchErr:    "connection refused",
		}, nil
	}

	ctx := RunContext{ConfigPath: cfgPath, UpdateMode: "on"}
	err := RunRun(ctx)
	if err != nil {
		t.Errorf("unexpected error on fetch failure (should warn and continue): %v", err)
	}
}

func TestRunRun_PullError_ContinuesWithWarning(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	writeLifecycleYML(t, workspaceDir, "done")

	origProbe := GitProbeFunc
	origPull := GitPullFFOnlyFunc
	t.Cleanup(func() {
		GitProbeFunc = origProbe
		GitPullFFOnlyFunc = origPull
	})

	GitProbeFunc = func(_, workDir string, fetch bool) (git.Status, error) {
		return git.Status{
			IsRepo: true, HasUpstream: true, FetchOK: true, Behind: 1,
			Branch: "main", Upstream: "origin/main",
		}, nil
	}
	GitPullFFOnlyFunc = func(_, workDir string) (bool, error) {
		return false, errors.New("network error")
	}

	ctx := RunContext{ConfigPath: cfgPath, UpdateMode: "on"}
	err := RunRun(ctx)
	if err != nil {
		t.Errorf("expected command to continue after pull error (warn path), got: %v", err)
	}
}

// --- .env render-and-source tests ---

// TestRunRun_RendersDotEnvBeforePhases is a regression guard: `dwe run`
// must regenerate workspace/.env from the current config BEFORE preflight, lock
// acquisition, git probe, and lifecycle phases, mirroring the implicit
// render-env step at the head of the deploy pipeline. Lifecycle phases (and
// preflight type: command checks) read this file via deploy.SourceDotEnv —
// if it isn't materialized first, those steps observe stale or missing vars.
func TestRunRun_RendersDotEnvBeforePhases(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	writeLifecycleYML(t, workspaceDir, "done")

	envPath := filepath.Join(dir, ".env")
	if _, err := os.Stat(envPath); err == nil {
		t.Fatalf("pre-condition: .env should not exist before RunRun")
	}

	ctx := RunContext{ConfigPath: cfgPath}
	if err := RunRun(ctx); err != nil {
		t.Fatalf("RunRun: %v", err)
	}

	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf(".env should be written by RunRun: %v", err)
	}
	if info.Mode().IsRegular() != true {
		t.Errorf(".env should be a regular file, got mode %v", info.Mode())
	}
}

// TestRunRun_ReRendersDotEnvAfterPull verifies that when the pre-phase git
// pull moves HEAD and the config is reloaded, .env is re-rendered against
// the post-pull config — otherwise phases below would see a stale .env from
// before the pull.
func TestRunRun_ReRendersDotEnvAfterPull(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	writeLifecycleYML(t, workspaceDir, "before-reload")

	origProbe := GitProbeFunc
	origPull := GitPullFFOnlyFunc
	t.Cleanup(func() {
		GitProbeFunc = origProbe
		GitPullFFOnlyFunc = origPull
	})

	GitProbeFunc = func(_, _ string, _ bool) (git.Status, error) {
		return git.Status{
			IsRepo:      true,
			HasUpstream: true,
			Branch:      "main",
			Upstream:    "origin/main",
			FetchOK:     true,
			Behind:      1,
		}, nil
	}

	var pullCalledAt time.Time
	GitPullFFOnlyFunc = func(_, _ string) (bool, error) {
		pullCalledAt = time.Now()
		// Simulate the pull bringing in an updated lifecycle.yml.
		writeLifecycleYML(t, workspaceDir, "after-reload")
		// Sleep a touch so the post-pull write of .env is observably newer
		// than the pre-pull one even on coarse-grained mtime filesystems.
		time.Sleep(20 * time.Millisecond)
		return true, nil
	}

	ctx := RunContext{ConfigPath: cfgPath, UpdateMode: "on"}
	if err := RunRun(ctx); err != nil {
		t.Fatalf("RunRun: %v", err)
	}

	envPath := filepath.Join(dir, ".env")
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf(".env should exist after RunRun: %v", err)
	}
	// The second render runs after the simulated pull, so .env's mtime must
	// be at-or-after the moment the pull stub fired.
	if info.ModTime().Before(pullCalledAt) {
		t.Errorf(".env mtime %v is before pull at %v — re-render did not run", info.ModTime(), pullCalledAt)
	}
}

// --- Deployment gate tests ---

func TestRunRun_DeploymentGate_NoTrackedServices_Passes(t *testing.T) {
	// When there are no tracked services (no deploy_services: true in plan),
	// the gate should pass through without checking state.
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	writeLifecycleYML(t, workspaceDir, "done")

	ctx := RunContext{ConfigPath: cfgPath}
	err := RunRun(ctx)
	if err != nil {
		t.Errorf("expected no error when no tracked services; got: %v", err)
	}
}

// TestRunRun_ClearsPendingRestart_KeepsPendingDeploy verifies that a successful
// `dwe run` clears any stale PendingRestart entry (a toggle recorded while the
// stack was stopped) but leaves PendingDeploy ops intact — deploy tracks
// artifact state separately and the run gate enforces it.
func TestRunRun_ClearsPendingRestart_KeepsPendingDeploy(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	writeLifecycleYML(t, workspaceDir, "done")

	statePath := filepath.Join(dir, journal.DefaultRelPath)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := journal.AddPendingOps(statePath, []journal.PendingOp{
		{Kind: journal.PendingRestart},
		{Kind: journal.PendingDeploy, Services: []string{"web"}},
	}, "stub"); err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	if err := RunRun(RunContext{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("RunRun: %v", err)
	}

	state, err := journal.Load(statePath)
	if err != nil {
		t.Fatalf("loading state after run: %v", err)
	}
	if state.Pending == nil {
		t.Fatal("pending must not be wiped; deploy op should survive run")
	}
	if state.Pending.Find(journal.PendingRestart) != nil {
		t.Errorf("pending restart must be cleared after a successful run, got %+v", state.Pending)
	}
	if state.Pending.Find(journal.PendingDeploy) == nil {
		t.Errorf("pending deploy must survive run (artifact state outlasts runtime), got %+v", state.Pending)
	}
}

// --- run-preamble config render (renderConfigsForRun) ---

// mustReadFile reads path or fails the test.
func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// writeConfigPackFixture writes a config template pack (manifest + sources)
// under workspace/templates/config/<name>/ within root.
func writeConfigPackFixture(t *testing.T, root, name, manifest string, sources map[string]string) {
	t.Helper()
	packDir := filepath.Join(root, "workspace", "templates", "config", name)
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("mkdir pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "manifest.yml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for rel, content := range sources {
		p := filepath.Join(packDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir source dir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write source: %v", err)
		}
	}
}

// appServiceCfg builds an in-memory config with a single enabled app service
// "main" rooted at services/main, plus the given Raw map and generated fields.
func appServiceCfg(raw map[string]any, generated map[string]config.GeneratedField) *config.DweConfig {
	return &config.DweConfig{
		Raw: raw,
		Services: map[string]config.ServiceConfig{
			"main": {
				Type:      config.ServiceTypeApp,
				Enabled:   true,
				Dir:       "services/main",
				Generated: generated,
			},
		},
	}
}

func TestRenderConfigsForRun_RendersAndReplays(t *testing.T) {
	root := t.TempDir()
	writeConfigPackFixture(t, root, "default",
		"render:\n  - from: env.tmpl\n    to: src/.env\n",
		map[string]string{"env.tmpl": "DB=${databases.magento}\nAPP_KEY=${generated.app_key}\n"})

	store := generatedstore.New()
	store.SetIfAbsent("main", "app_key", "base64:secret==")
	if err := generatedstore.Save(filepath.Join(root, generatedstore.DefaultRelPath), store); err != nil {
		t.Fatal(err)
	}

	cfg := appServiceCfg(
		map[string]any{"databases": map[string]any{"magento": "pgsql"}},
		map[string]config.GeneratedField{"app_key": {File: "src/.env", Pattern: `^APP_KEY=(.*)$`}},
	)
	buf := &bytes.Buffer{}
	if err := renderConfigsForRun(cfg, root, render.NewWriter(buf)); err != nil {
		t.Fatalf("renderConfigsForRun: %v", err)
	}

	got := mustReadFile(t, filepath.Join(root, "services", "main", "src", ".env"))
	if got != "DB=pgsql\nAPP_KEY=base64:secret==\n" {
		t.Errorf("rendered content = %q", got)
	}
}

// TestRenderConfigsForRun_MissingGeneratedKey_SkipsService verifies the run-only
// non-destructive guard: a service that declares a generated: key whose value is
// absent from the store is SKIPPED (no render) with a hint — never overwritten
// with a blanked secret.
func TestRenderConfigsForRun_MissingGeneratedKey_SkipsService(t *testing.T) {
	root := t.TempDir()
	writeConfigPackFixture(t, root, "default",
		"render:\n  - from: env.tmpl\n    to: src/.env\n",
		map[string]string{"env.tmpl": "APP_KEY=${generated.app_key}\n"})

	dest := filepath.Join(root, "services", "main", "src", ".env")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	const sentinel = "APP_KEY=base64:live-secret==\n"
	if err := os.WriteFile(dest, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}

	// Store is empty → the declared app_key is missing.
	cfg := appServiceCfg(
		map[string]any{},
		map[string]config.GeneratedField{"app_key": {File: "src/.env", Pattern: `^APP_KEY=(.*)$`}},
	)
	buf := &bytes.Buffer{}
	if err := renderConfigsForRun(cfg, root, render.NewWriter(buf)); err != nil {
		t.Fatalf("renderConfigsForRun: %v", err)
	}

	if got := mustReadFile(t, dest); got != sentinel {
		t.Errorf("config file was rewritten; want preserved %q, got %q", sentinel, got)
	}
	out := buf.String()
	if !strings.Contains(out, "skipping config render") || !strings.Contains(out, "app_key") {
		t.Errorf("expected skip hint mentioning app_key, got: %q", out)
	}
}

func TestRenderConfigsForRun_AbsentPack_NoError(t *testing.T) {
	root := t.TempDir()
	cfg := appServiceCfg(map[string]any{}, nil)
	buf := &bytes.Buffer{}
	if err := renderConfigsForRun(cfg, root, render.NewWriter(buf)); err != nil {
		t.Fatalf("renderConfigsForRun with no pack should succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "services", "main", "src", ".env")); !os.IsNotExist(err) {
		t.Errorf("no file should be written when no pack resolves")
	}
}

// TestRenderConfigsForRun_SkipsNonAppServices guards the app-only iteration:
// tool/infra services cannot declare a dir, so the implicit `default` pack would
// resolve for them and error at RenderConfigs. The run-render loop iterates only
// app services, so an enabled tool service alongside a default pack is a no-op,
// and the app service still renders.
func TestRenderConfigsForRun_SkipsNonAppServices(t *testing.T) {
	root := t.TempDir()
	writeConfigPackFixture(t, root, "default",
		"render:\n  - from: env.tmpl\n    to: src/.env\n",
		map[string]string{"env.tmpl": "DB=${databases.magento}\n"})

	cfg := &config.DweConfig{
		Raw: map[string]any{"databases": map[string]any{"magento": "pgsql"}},
		Services: map[string]config.ServiceConfig{
			"main": {Type: config.ServiceTypeApp, Enabled: true, Dir: "services/main"},
			// Tool/infra services have no Dir; iterating them would resolve the
			// default pack and error.
			"cli":   {Type: config.ServiceTypeTool, Enabled: true},
			"redis": {Type: config.ServiceTypeInfra, Enabled: true},
		},
	}
	buf := &bytes.Buffer{}
	if err := renderConfigsForRun(cfg, root, render.NewWriter(buf)); err != nil {
		t.Fatalf("renderConfigsForRun must skip non-app services, got: %v", err)
	}
	if got := mustReadFile(t, filepath.Join(root, "services", "main", "src", ".env")); got != "DB=pgsql\n" {
		t.Errorf("app service should still render; got %q", got)
	}
}

// writeConfigRenderProject builds a full on-disk project with a single app
// service "main" (service.yml), a config pack named "default", and a pre-written
// rendered file holding sentinel. When deployYML is non-empty it is written as
// the service's deploy.yml (making the service tracked by the deploy gate).
// Returns the workspace.yml path.
func writeConfigRenderProject(t *testing.T, dir, tmpl, sentinel, deployYML string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("project:\n  name: test\n  prefix: dwe\n"), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	svcDir := filepath.Join(dir, "workspace", "services", "main")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatalf("mkdir service: %v", err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "service.yml"),
		[]byte("type: app\ncontainer: app-main\nrequired: true\ndir: ./services/main\n"), 0o644); err != nil {
		t.Fatalf("write service.yml: %v", err)
	}
	if deployYML != "" {
		if err := os.WriteFile(filepath.Join(svcDir, "deploy.yml"), []byte(deployYML), 0o644); err != nil {
			t.Fatalf("write deploy.yml: %v", err)
		}
	}
	writeLifecycleYML(t, filepath.Join(dir, "workspace"), "done")
	writeConfigPackFixture(t, dir, "default",
		"render:\n  - from: env.tmpl\n    to: src/.env\n",
		map[string]string{"env.tmpl": tmpl})

	dest := filepath.Join(dir, "services", "main", "src", ".env")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// TestRunRun_GateFails_DoesNotBlankConfig is the central safety test: a tracked
// service that is NOT deployed makes `dwe run` fail the gate. Because config
// render runs only AFTER the gate, the on-disk config file must be left
// untouched (never blanked).
func TestRunRun_GateFails_DoesNotBlankConfig(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	const sentinel = "APP_KEY=base64:live==\n"
	deployYML := "phases:\n  - name: setup\n    steps:\n      - name: noop\n        type: shell\n        cmd: \"true\"\n"
	cfgPath := writeConfigRenderProject(t, dir, "APP_KEY=${generated.app_key}\n", sentinel, deployYML)

	// No journal state → main is not StatusDeployed → gate fails.
	err := RunRun(RunContext{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected deployment gate error, got nil")
	}
	var gate *deploymentGateError
	if !errors.As(err, &gate) {
		t.Fatalf("expected deploymentGateError, got %T: %v", err, err)
	}

	if got := mustReadFile(t, filepath.Join(dir, "services", "main", "src", ".env")); got != sentinel {
		t.Errorf("config blanked despite gate failure; want %q, got %q", sentinel, got)
	}
}

// TestRunRun_DeployedService_RendersAndReplays drives the full run path with a
// deployed tracked service and verifies the config is re-rendered after the gate
// with the stored generated value replayed.
func TestRunRun_DeployedService_RendersAndReplays(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	deployYML := "phases:\n  - name: setup\n    steps:\n      - name: noop\n        type: shell\n        cmd: \"true\"\n"
	cfgPath := writeConfigRenderProject(t, dir, "APP_KEY=${generated.app_key}\n", "stale\n", deployYML)

	statePath := filepath.Join(dir, journal.DefaultRelPath)
	if err := journal.Save(statePath, &journal.ProjectState{
		SchemaVersion: "1",
		Services: map[string]*journal.ServiceState{
			"main": {Status: journal.StatusDeployed},
		},
	}); err != nil {
		t.Fatalf("seed deployed state: %v", err)
	}

	store := generatedstore.New()
	store.SetIfAbsent("main", "app_key", "base64:replayed==")
	if err := generatedstore.Save(filepath.Join(dir, generatedstore.DefaultRelPath), store); err != nil {
		t.Fatal(err)
	}

	if err := RunRun(RunContext{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("RunRun: %v", err)
	}

	got := mustReadFile(t, filepath.Join(dir, "services", "main", "src", ".env"))
	if got != "APP_KEY=base64:replayed==\n" {
		t.Errorf("config not re-rendered with replayed value; got %q", got)
	}
}

// TestRunRun_RendersFreshTemplateAtRunTime verifies the run-preamble render
// reads the CURRENT on-disk template at run time rather than reusing a stale
// pre-rendered output. This is the substance of the post-pull guarantee: the
// render call is placed after the post-pull config reload (and before lifecycle
// phases), so a template that changed since the last deploy — whether by a pull
// or a manual edit — renders fresh. (The git-pull leg itself cannot be exercised
// in-process: lifecycle update mode "on" maps to ActionSkip in git.Decide, so
// the pull never fires under test; the on-disk template change models the
// post-reload state directly.)
func TestRunRun_RendersFreshTemplateAtRunTime(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	// Untracked service (no deploy.yml) → gate passes trivially; the pre-rendered
	// file holds a stale value from an earlier deploy.
	cfgPath := writeConfigRenderProject(t, dir, "V=before\n", "V=stale\n", "")

	// The template on disk changed since the last render (models a pull / edit).
	writeConfigPackFixture(t, dir, "default",
		"render:\n  - from: env.tmpl\n    to: src/.env\n",
		map[string]string{"env.tmpl": "V=after\n"})

	if err := RunRun(RunContext{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("RunRun: %v", err)
	}

	got := mustReadFile(t, filepath.Join(dir, "services", "main", "src", ".env"))
	if got != "V=after\n" {
		t.Errorf("config not re-rendered from current template; got %q", got)
	}
}
