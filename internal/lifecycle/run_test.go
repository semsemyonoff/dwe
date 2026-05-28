package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/shared/git"
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

func TestRunRun_MissingLifecycleYML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	ctx := RunContext{ConfigPath: cfgPath}
	err := RunRun(ctx)
	if err == nil {
		t.Fatal("expected error for missing lifecycle.yml, got nil")
	}
	if !strings.Contains(err.Error(), "no lifecycle.yml") {
		t.Errorf("error should mention 'no lifecycle.yml', got: %v", err)
	}
}

func TestRunRun_MissingRunSection(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := "stop:\n  final_message: bye\n  phases: []\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	ctx := RunContext{ConfigPath: cfgPath}
	err := RunRun(ctx)
	if err == nil {
		t.Fatal("expected error for missing run: section, got nil")
	}
	if !strings.Contains(err.Error(), "run:") && !strings.Contains(err.Error(), "run` section") {
		t.Errorf("error should mention missing run section, got: %v", err)
	}
}

func TestRunRun_ReloadsConfigAfterPull(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}

	writeLifecycleYML(t, devboxDir, "before-reload")

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
		writeLifecycleYML(t, devboxDir, "after-reload")
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
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	writeLifecycleYML(t, devboxDir, "done")

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
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	yaml := "run:\n  update:\n    mode: on\n  phases:\n    - name: s\n      steps:\n        - name: n\n          type: shell\n          cmd: \"true\"\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(yaml), 0644); err != nil {
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
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	yaml := "run:\n  phases:\n    - name: s\n      steps:\n        - name: n\n          type: shell\n          cmd: \"true\"\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(yaml), 0644); err != nil {
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
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	writeLifecycleYML(t, devboxDir, "done")

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
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	writeLifecycleYML(t, devboxDir, "done")

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
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	writeLifecycleYML(t, devboxDir, "done")

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
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	writeLifecycleYML(t, devboxDir, "done")

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

// TestRunRun_RendersDotEnvBeforePhases is a regression guard: `devbox run`
// must regenerate devbox/.env from the current config BEFORE preflight, lock
// acquisition, git probe, and lifecycle phases, mirroring the implicit
// render-env step at the head of the deploy pipeline. Lifecycle phases (and
// preflight type: command checks) read this file via deploy.SourceDotEnv —
// if it isn't materialized first, those steps observe stale or missing vars.
func TestRunRun_RendersDotEnvBeforePhases(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	writeLifecycleYML(t, devboxDir, "done")

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
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	writeLifecycleYML(t, devboxDir, "before-reload")

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
		writeLifecycleYML(t, devboxDir, "after-reload")
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
	cfgPath := makeMinimalDevboxYML(t, dir)
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	writeLifecycleYML(t, devboxDir, "done")

	ctx := RunContext{ConfigPath: cfgPath}
	err := RunRun(ctx)
	if err != nil {
		t.Errorf("expected no error when no tracked services; got: %v", err)
	}
}
