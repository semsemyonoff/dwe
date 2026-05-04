package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/git"
)

// --- resolveUpdateMode tests (unexported, same package) ---

func TestResolveUpdateMode_UpdateBlockOmitted_NoFlag(t *testing.T) {
	cfg := &config.LifecycleRunConfig{Update: nil}
	if got := resolveUpdateMode(cfg, false, ""); got != "off" {
		t.Errorf("resolveUpdateMode = %q, want %q", got, "off")
	}
}

func TestResolveUpdateMode_UpdateBlockPresent_ModeOmitted_NoFlag(t *testing.T) {
	enabled := true
	cfg := &config.LifecycleRunConfig{
		Update: &config.LifecycleUpdate{Enabled: &enabled, Mode: ""},
	}
	if got := resolveUpdateMode(cfg, false, ""); got != "prompt" {
		t.Errorf("resolveUpdateMode = %q, want %q", got, "prompt")
	}
}

func TestResolveUpdateMode_EnabledFalse_NoFlag(t *testing.T) {
	disabled := false
	cfg := &config.LifecycleRunConfig{
		Update: &config.LifecycleUpdate{Enabled: &disabled, Mode: "auto"},
	}
	if got := resolveUpdateMode(cfg, false, ""); got != "off" {
		t.Errorf("resolveUpdateMode = %q, want %q", got, "off")
	}
}

func TestResolveUpdateMode_NoUpdateFlag_ForcesOff(t *testing.T) {
	enabled := true
	cfg := &config.LifecycleRunConfig{
		Update: &config.LifecycleUpdate{Enabled: &enabled, Mode: "auto"},
	}
	if got := resolveUpdateMode(cfg, true, ""); got != "off" {
		t.Errorf("resolveUpdateMode with NoUpdate = %q, want %q", got, "off")
	}
}

func TestResolveUpdateMode_UpdateFlag_OverridesYAML(t *testing.T) {
	enabled := true
	cfg := &config.LifecycleRunConfig{
		Update: &config.LifecycleUpdate{Enabled: &enabled, Mode: "prompt"},
	}
	if got := resolveUpdateMode(cfg, false, "auto"); got != "auto" {
		t.Errorf("resolveUpdateMode with UpdateMode=auto = %q, want %q", got, "auto")
	}
}

func TestResolveUpdateMode_UpdateFlag_OverridesEnabledFalse(t *testing.T) {
	disabled := false
	cfg := &config.LifecycleRunConfig{
		Update: &config.LifecycleUpdate{Enabled: &disabled, Mode: "prompt"},
	}
	if got := resolveUpdateMode(cfg, false, "auto"); got != "auto" {
		t.Errorf("resolveUpdateMode with UpdateMode=auto (enabled:false yaml) = %q, want %q", got, "auto")
	}
}

func TestResolveUpdateMode_UpdateFlag_OverridesOmittedBlock(t *testing.T) {
	cfg := &config.LifecycleRunConfig{Update: nil}
	if got := resolveUpdateMode(cfg, false, "check"); got != "check" {
		t.Errorf("resolveUpdateMode with UpdateMode=check (no block) = %q, want %q", got, "check")
	}
}

func TestResolveUpdateMode_NoUpdateTakesPrecedenceOverUpdateFlag(t *testing.T) {
	enabled := true
	cfg := &config.LifecycleRunConfig{
		Update: &config.LifecycleUpdate{Enabled: &enabled, Mode: "prompt"},
	}
	if got := resolveUpdateMode(cfg, true, "auto"); got != "off" {
		t.Errorf("resolveUpdateMode with NoUpdate + UpdateMode=auto = %q, want %q", got, "off")
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

	GitProbeFunc = func(workDir string, fetch bool) (git.Status, error) {
		return git.Status{
			IsRepo:      true,
			HasUpstream: true,
			Branch:      "main",
			Upstream:    "origin/main",
			FetchOK:     true,
			Behind:      1,
		}, nil
	}

	GitPullFFOnlyFunc = func(workDir string) (bool, error) {
		writeLifecycleYML(t, devboxDir, "after-reload")
		return true, nil
	}

	ctx := RunContext{ConfigPath: cfgPath, UpdateMode: "auto"}
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
	GitProbeFunc = func(workDir string, fetch bool) (git.Status, error) {
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
	enabled := "true"
	yaml := "run:\n  update:\n    enabled: " + enabled + "\n    mode: auto\n  phases:\n    - name: s\n      steps:\n        - name: n\n          run: \"true\"\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	origProbe := GitProbeFunc
	t.Cleanup(func() { GitProbeFunc = origProbe })

	fetchCalled := false
	GitProbeFunc = func(workDir string, fetch bool) (git.Status, error) {
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
	yaml := "run:\n  phases:\n    - name: s\n      steps:\n        - name: n\n          run: \"true\"\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	origProbe := GitProbeFunc
	t.Cleanup(func() { GitProbeFunc = origProbe })

	fetchCalled := false
	GitProbeFunc = func(workDir string, fetch bool) (git.Status, error) {
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

	GitProbeFunc = func(workDir string, fetch bool) (git.Status, error) {
		return git.Status{}, errors.New("probe failed")
	}

	ctx := RunContext{ConfigPath: cfgPath, UpdateMode: "auto"}
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

	GitProbeFunc = func(workDir string, fetch bool) (git.Status, error) {
		return git.Status{
			IsRepo:      true,
			HasUpstream: true,
			Branch:      "main",
			Upstream:    "origin/main",
			FetchOK:     false,
			FetchErr:    "connection refused",
		}, nil
	}

	ctx := RunContext{ConfigPath: cfgPath, UpdateMode: "auto"}
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

	GitProbeFunc = func(workDir string, fetch bool) (git.Status, error) {
		return git.Status{
			IsRepo: true, HasUpstream: true, FetchOK: true, Behind: 1,
			Branch: "main", Upstream: "origin/main",
		}, nil
	}
	GitPullFFOnlyFunc = func(workDir string) (bool, error) {
		return false, errors.New("network error")
	}

	ctx := RunContext{ConfigPath: cfgPath, UpdateMode: "auto"}
	err := RunRun(ctx)
	if err != nil {
		t.Errorf("expected command to continue after pull error (warn path), got: %v", err)
	}
}
