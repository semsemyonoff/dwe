package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/git"
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
    enabled: true
    mode: auto
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
