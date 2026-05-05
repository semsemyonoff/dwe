package reset_test

import (
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/reset"
)

// makeResetCfg returns a DevboxConfig with __configPath pointing to a
// temp reset.yml so FindStep can load it.
func makeResetCfgWithPath(cfgPath string) *config.DevboxConfig {
	return &config.DevboxConfig{
		Raw: map[string]any{"__configPath": cfgPath},
	}
}

// --- FindStep invalid address tests (no filesystem needed) ---

func TestFindStep_InvalidAddress(t *testing.T) {
	cfg := &config.DevboxConfig{}
	_, _, err := reset.FindStep(cfg, "no-slash")
	if err == nil {
		t.Fatal("expected error for address without slash")
	}
	if !strings.Contains(err.Error(), "invalid step address") {
		t.Errorf("expected 'invalid step address' error, got: %v", err)
	}
}

func TestFindStep_MissingConfigPath(t *testing.T) {
	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	_, _, err := reset.FindStep(cfg, "phase/step")
	if err == nil {
		t.Fatal("expected error when __configPath missing")
	}
}

// --- ResolvePlan tests using filesystem ---

func TestResolvePlan_emptyPhasesReturnsNil(t *testing.T) {
	dir := t.TempDir()
	writeResetYML(t, dir, `phases: []`)
	cfg := makeResetCfgWithPath(dir + "/devbox.yml")
	steps, err := reset.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 0 {
		t.Errorf("want 0 steps, got %d", len(steps))
	}
}

func TestResolvePlan_singlePhase(t *testing.T) {
	dir := t.TempDir()
	writeResetYML(t, dir, `
phases:
  - name: cleanup
    steps:
      - name: remove-dirs
        type: shell
        cmd: rm -rf services/main/src
`)
	cfg := makeResetCfgWithPath(dir + "/devbox.yml")
	steps, err := reset.ResolvePlan(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("want 1 step, got %d", len(steps))
	}
	if steps[0].Step.Name != "remove-dirs" {
		t.Errorf("step name = %q, want remove-dirs", steps[0].Step.Name)
	}
}

// --- FindStep filesystem tests ---

func TestFindStep_findsExistingStep(t *testing.T) {
	dir := t.TempDir()
	writeResetYML(t, dir, `
phases:
  - name: cleanup
    steps:
      - name: remove-dirs
        type: shell
        cmd: rm -rf services/main/src
      - name: reset-db
        type: command
        cmd: services.main.db.create
`)
	cfg := makeResetCfgWithPath(dir + "/devbox.yml")
	phase, step, err := reset.FindStep(cfg, "cleanup/reset-db")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if phase.Name != "cleanup" {
		t.Errorf("phase.Name = %q, want cleanup", phase.Name)
	}
	if step.Name != "reset-db" {
		t.Errorf("step.Name = %q, want reset-db", step.Name)
	}
}

func TestFindStep_stepNotFound(t *testing.T) {
	dir := t.TempDir()
	writeResetYML(t, dir, `
phases:
  - name: cleanup
    steps:
      - name: remove-dirs
        run: rm -rf services/main/src
`)
	cfg := makeResetCfgWithPath(dir + "/devbox.yml")
	_, _, err := reset.FindStep(cfg, "cleanup/nonexistent")
	if err == nil {
		t.Fatal("expected error for missing step, got nil")
	}
}

func TestFindStep_phaseNotFound(t *testing.T) {
	dir := t.TempDir()
	writeResetYML(t, dir, `
phases:
  - name: cleanup
    steps:
      - name: remove-dirs
        run: rm -rf services/main/src
`)
	cfg := makeResetCfgWithPath(dir + "/devbox.yml")
	_, _, err := reset.FindStep(cfg, "nonexistent/remove-dirs")
	if err == nil {
		t.Fatal("expected error for missing phase, got nil")
	}
}
