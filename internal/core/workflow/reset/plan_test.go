package reset_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/workflow/reset"
)

// makeResetCfg returns a DweConfig with __configPath pointing to a
// temp reset.yml so FindStep can load it.
func makeResetCfgWithPath(cfgPath string) *config.DweConfig {
	return &config.DweConfig{
		Raw: map[string]any{"__configPath": cfgPath},
	}
}

// --- FindStep invalid address tests (no filesystem needed) ---

func TestFindStep_InvalidAddress(t *testing.T) {
	cfg := &config.DweConfig{}
	_, _, err := reset.FindStep(cfg, "no-slash")
	if err == nil {
		t.Fatal("expected error for address without slash")
	}
	if !strings.Contains(err.Error(), "invalid step address") {
		t.Errorf("expected 'invalid step address' error, got: %v", err)
	}
}

func TestFindStep_MissingConfigPath(t *testing.T) {
	cfg := &config.DweConfig{Raw: map[string]any{}}
	_, _, err := reset.FindStep(cfg, "phase/step")
	if err == nil {
		t.Fatal("expected error when __configPath missing")
	}
}

// --- ResolvePlan tests using filesystem ---

// TestResolvePlan_emptyPhasesUsesDefault verifies that a reset.yml with an
// empty phases list is treated as "no user pipeline" and falls back to the
// built-in default (which has steps).
func TestResolvePlan_emptyPhasesUsesDefault(t *testing.T) {
	dir := t.TempDir()
	writeResetYML(t, dir, `phases: []`)
	cfg := makeResetCfgWithPath(dir + "/devbox.yml")
	steps, err := reset.ResolvePlan(cfg, usercommands.NewEmptyRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) == 0 {
		t.Error("empty phases should return default steps, got 0")
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
	steps, err := reset.ResolvePlan(cfg, usercommands.NewEmptyRegistry())
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

// --- Tests for default pipeline (no reset.yml) ---

func TestResolvePlan_noFileUsesDefault(t *testing.T) {
	dir := t.TempDir()
	// Write devbox dir without a reset.yml so the default fires.
	devboxDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := makeResetCfgWithPath(dir + "/devbox.yml")
	steps, err := reset.ResolvePlan(cfg, usercommands.NewEmptyRegistry())
	if err != nil {
		t.Fatalf("unexpected error with no reset.yml: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("expected default steps but got none")
	}
	// Default includes docker down step.
	found := false
	for _, s := range steps {
		if s.Step.Cmd == "docker down" {
			found = true
			break
		}
	}
	if !found {
		t.Error("default pipeline should include a 'docker down' step")
	}
}

func TestLoadAndResolvePlan_noFileReturnsDefaulted(t *testing.T) {
	dir := t.TempDir()
	devboxDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := makeResetCfgWithPath(dir + "/devbox.yml")
	_, steps, defaulted, err := reset.LoadAndResolvePlan(cfg, usercommands.NewEmptyRegistry())
	if err != nil {
		t.Fatalf("unexpected error with no reset.yml: %v", err)
	}
	if !defaulted {
		t.Error("defaulted = false, want true when reset.yml is absent")
	}
	if len(steps) == 0 {
		t.Fatal("expected default steps but got none")
	}
}

func TestLoadAndResolvePlan_userFileReturnsFalse(t *testing.T) {
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
	_, steps, defaulted, err := reset.LoadAndResolvePlan(cfg, usercommands.NewEmptyRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if defaulted {
		t.Error("defaulted = true, want false when user reset.yml is present")
	}
	if len(steps) != 1 {
		t.Errorf("want 1 step, got %d", len(steps))
	}
}

func TestFindStep_noFileSearchesDefault(t *testing.T) {
	dir := t.TempDir()
	devboxDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := makeResetCfgWithPath(dir + "/devbox.yml")
	// cleanup/remove-volumes is a step in the default pipeline.
	phase, step, err := reset.FindStep(cfg, "cleanup/remove-volumes")
	if err != nil {
		t.Fatalf("FindStep against default: %v", err)
	}
	if phase.Name != "cleanup" {
		t.Errorf("phase.Name = %q, want cleanup", phase.Name)
	}
	if step.Name != "remove-volumes" {
		t.Errorf("step.Name = %q, want remove-volumes", step.Name)
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
        type: shell
        cmd: rm -rf services/main/src
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
        type: shell
        cmd: rm -rf services/main/src
`)
	cfg := makeResetCfgWithPath(dir + "/devbox.yml")
	_, _, err := reset.FindStep(cfg, "nonexistent/remove-dirs")
	if err == nil {
		t.Fatal("expected error for missing phase, got nil")
	}
}
