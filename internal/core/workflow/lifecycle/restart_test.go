package lifecycle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/git"
)

func TestRunRestart_MissingLifecycleYML_BothLegsUseDefault(t *testing.T) {
	// Stub RunPhasesFunc to avoid recursive test-binary execution from
	// type:dwe steps calling os.Executable() in the default run config.
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
	// Both stop and run legs use defaults when lifecycle.yml is absent.
	// stop fires first, run fires second.
	if len(called) != 2 || called[0] != DefaultedStop || called[1] != DefaultedRun {
		t.Errorf("OnDefaultUsed calls = %v, want [%q %q]", called, DefaultedStop, DefaultedRun)
	}
}

func TestRunRestart_MissingStopSection(t *testing.T) {
	// Missing stop: uses the built-in default stop (which includes a type:dwe
	// step); stub RunPhasesFunc to avoid recursive test binary execution.
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	lifecycleYAML := "run:\n  final_message: ready\n  phases: []\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	ctx := RunContext{ConfigPath: cfgPath}
	if err := RunRestart(ctx); err != nil {
		t.Fatalf("RunRestart with missing stop: section should succeed, got: %v", err)
	}
}

func TestRunRestart_MissingRunSection_UsesDefault(t *testing.T) {
	// Stub RunPhasesFunc to avoid recursive test-binary execution from
	// type:dwe steps calling os.Executable() in the default run config.
	stubRunPhases(t)

	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

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

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	lifecycleYAML := `stop:
  final_message: "Stopped."
  phases: []
run:
  final_message: "Ready."
  phases: []
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "lifecycle.yml"), []byte(lifecycleYAML), 0o644); err != nil {
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

func TestRunRestart_OnlyRunSectionPresent_StopUsesDefault(t *testing.T) {
	// Symmetric counterpart to TestRunRestart_MissingRunSection_UsesDefault:
	// lifecycle.yml has only run: (no stop:) → stop leg uses default, run leg uses user config.
	stubRunPhases(t)

	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	lifecycleYAML := "run:\n  final_message: ready\n  phases: []\n"
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
	if err := RunRestart(ctx); err != nil {
		t.Fatalf("RunRestart with only run: section should succeed, got: %v", err)
	}
	// stop: absent → DefaultedStop fires; run: present → no default.
	if len(called) != 1 || called[0] != DefaultedStop {
		t.Errorf("OnDefaultUsed calls = %v, want [%q]", called, DefaultedStop)
	}
}

func TestRunRestart_NoUpdatePropagated(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
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
	if err := os.WriteFile(filepath.Join(workspaceDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
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
