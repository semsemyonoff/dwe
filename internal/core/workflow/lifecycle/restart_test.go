package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/core/workflow/deploy/journal"
	"devbox-cli/internal/shared/git"
)

func TestRunRestart_MissingLifecycleYML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	ctx := RunContext{ConfigPath: cfgPath}
	err := RunRestart(ctx)
	if err == nil {
		t.Fatal("expected error for missing lifecycle.yml, got nil")
	}
	if !strings.Contains(err.Error(), "no lifecycle.yml") {
		t.Errorf("error should mention 'no lifecycle.yml', got: %v", err)
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

func TestRunRestart_MissingRunSection(t *testing.T) {
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
	err := RunRestart(ctx)
	if err == nil {
		t.Fatal("expected error for missing run: section, got nil")
	}
	if !strings.Contains(err.Error(), "run:") && !strings.Contains(err.Error(), "run` section") {
		t.Errorf("error should mention missing run section, got: %v", err)
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
