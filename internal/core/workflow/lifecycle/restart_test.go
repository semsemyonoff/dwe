package lifecycle

import (
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/core/workflow/deploy/journal"
	"devbox-cli/internal/shared/git"
)

func TestRunRestart_MissingLifecycleYML_RunLegUsesDefault(t *testing.T) {
	// Stub RunPhasesFunc to avoid recursive test-binary execution from
	// type:devbox steps calling os.Executable() in the default run config.
	stubRunPhases(t)

	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	var called []DefaultedPipeline
	ctx := RunContext{
		ConfigPath: cfgPath,
		OnDefaultUsed: func(p DefaultedPipeline) {
			called = append(called, p)
		},
	}
	if err := RunRestart(ctx); err != nil {
		t.Fatalf("RunRestart with missing lifecycle.yml should succeed (built-in defaults), got: %v", err)
	}
	// Task 4 only adds DefaultedRun handling; DefaultedStop handling is Task 5.
	// The run leg fires the callback; stop leg uses EnsureStopConfig without callback.
	if len(called) != 1 || called[0] != DefaultedRun {
		t.Errorf("OnDefaultUsed calls = %v, want [%q]", called, DefaultedRun)
	}
}

func TestRunRestart_MissingStopSection(t *testing.T) {
	// With the auto-reap contract, missing stop: is fine — the stop leg
	// runs the synthetic reap phase only. Restart should therefore reach
	// the run leg and succeed.
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := "run:\n  final_message: ready\n  phases: []\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	ctx := RunContext{ConfigPath: cfgPath}
	if err := RunRestart(ctx); err != nil {
		t.Fatalf("RunRestart with missing stop: section should succeed, got: %v", err)
	}
}

func TestRunRestart_MissingRunSection_UsesDefault(t *testing.T) {
	// Stub RunPhasesFunc to avoid recursive test-binary execution from
	// type:devbox steps calling os.Executable() in the default run config.
	stubRunPhases(t)

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

	var called []DefaultedPipeline
	ctx := RunContext{
		ConfigPath: cfgPath,
		OnDefaultUsed: func(p DefaultedPipeline) {
			called = append(called, p)
		},
	}
	if err := RunRestart(ctx); err != nil {
		t.Fatalf("RunRestart with missing run: section should succeed (built-in default), got: %v", err)
	}
	// The stop leg uses user config (stop: present), run leg uses default.
	if len(called) != 1 || called[0] != DefaultedRun {
		t.Errorf("OnDefaultUsed calls = %v, want [%q]", called, DefaultedRun)
	}
}

// TestRunRestart_ClearsPendingRestartOnSuccess verifies that a successful restart
// clears the PendingRestart journal entry but leaves any PendingDeploy entry intact.
func TestRunRestart_ClearsPendingRestartOnSuccess(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := `stop:
  final_message: "Stopped."
  phases: []
run:
  final_message: "Ready."
  phases: []
`
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0o644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	// Pre-seed pending: both a restart op and a deploy op.
	statePath := filepath.Join(dir, journal.DefaultRelPath)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("creating state dir: %v", err)
	}
	state := &journal.ProjectState{
		Services: make(map[string]*journal.ServiceState),
		Pending: &journal.PendingApply{
			Operations: []journal.PendingOp{
				{Kind: journal.PendingRestart},
				{Kind: journal.PendingDeploy, Services: []string{"main"}},
			},
		},
	}
	if err := journal.Save(statePath, state); err != nil {
		t.Fatalf("seeding state: %v", err)
	}

	ctx := RunContext{ConfigPath: cfgPath}
	if err := RunRestart(ctx); err != nil {
		t.Fatalf("RunRestart: %v", err)
	}

	// Reload state from disk and verify restart op is gone, deploy op survives.
	after, err := journal.Load(statePath)
	if err != nil {
		t.Fatalf("loading state after restart: %v", err)
	}
	if after.Pending == nil {
		t.Fatal("pending should not be nil after restart — deploy op must survive")
	}
	if after.Pending.Find(journal.PendingRestart) != nil {
		t.Error("restart pending op should have been cleared after successful restart")
	}
	deployOp := after.Pending.Find(journal.PendingDeploy)
	if deployOp == nil {
		t.Fatal("deploy pending op must survive a restart (restart doesn't redeploy services)")
	}
	if len(deployOp.Services) != 1 || deployOp.Services[0] != "main" {
		t.Errorf("deploy pending op.Services = %v, want [main]", deployOp.Services)
	}
}

func TestRunRestart_NoUpdatePropagated(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := `stop:
  final_message: "Stopped."
  phases:
    - name: s
      steps:
        - name: noop
          type: shell
          cmd: "true"
run:
  update:
    mode: on
  final_message: "Ready."
  phases:
    - name: s
      steps:
        - name: noop
          type: shell
          cmd: "true"
`
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
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
	if err := RunRestart(ctx); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if fetchCalled {
		t.Error("git fetch should NOT be called during restart (run leg forces NoUpdate=true)")
	}
}
