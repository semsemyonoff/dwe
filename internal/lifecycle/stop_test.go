package lifecycle

import (
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/config"
)

func TestRunStop_MissingLifecycleYML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	ctx := StopContext{ConfigPath: cfgPath}
	if err := RunStop(ctx); err != nil {
		t.Fatalf("RunStop with missing lifecycle.yml should succeed (auto-reap only), got: %v", err)
	}
}

func TestRunStop_MissingStopSection(t *testing.T) {
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

	ctx := StopContext{ConfigPath: cfgPath}
	if err := RunStop(ctx); err != nil {
		t.Fatalf("RunStop with no stop: section should succeed (auto-reap only), got: %v", err)
	}
}

func TestRunStop_HappyPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := "stop:\n  final_message: \"Goodbye!\"\n  phases:\n    - name: down\n      steps:\n        - name: noop\n          type: shell\n          cmd: \"true\"\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	ctx := StopContext{ConfigPath: cfgPath}
	if err := RunStop(ctx); err != nil {
		t.Errorf("unexpected error on happy path: %v", err)
	}
}

func TestEnsureStopConfig_NilConfig(t *testing.T) {
	out := EnsureStopConfig(nil)
	if out == nil {
		t.Fatal("EnsureStopConfig(nil) returned nil")
	}
	if len(out.Phases) != 1 {
		t.Fatalf("expected 1 synthetic phase, got %d", len(out.Phases))
	}
	if out.Phases[0].Name != AutoReapPhaseName {
		t.Errorf("expected phase name %q, got %q", AutoReapPhaseName, out.Phases[0].Name)
	}
	if out.FinalMessage == "" {
		t.Error("expected default final message")
	}
}

func TestEnsureStopConfig_NilStop(t *testing.T) {
	out := EnsureStopConfig(&config.LifecycleConfig{Stop: nil})
	if len(out.Phases) != 1 || out.Phases[0].Name != AutoReapPhaseName {
		t.Fatalf("expected reap-only phases, got %+v", out.Phases)
	}
}

func TestEnsureStopConfig_PrependsToUserPhases(t *testing.T) {
	user := config.DeployPhase{Name: "user-down"}
	in := &config.LifecycleConfig{
		Stop: &config.LifecycleStopConfig{
			FinalMessage: "Bye",
			Phases:       []config.DeployPhase{user},
		},
	}
	out := EnsureStopConfig(in)
	if len(out.Phases) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(out.Phases))
	}
	if out.Phases[0].Name != AutoReapPhaseName {
		t.Errorf("reap phase must be first, got %q", out.Phases[0].Name)
	}
	if out.Phases[1].Name != "user-down" {
		t.Errorf("user phase must follow, got %q", out.Phases[1].Name)
	}
	if out.FinalMessage != "Bye" {
		t.Errorf("expected final message preserved, got %q", out.FinalMessage)
	}
	// Ensure the caller's slice was not aliased / mutated.
	if len(in.Stop.Phases) != 1 {
		t.Errorf("EnsureStopConfig must not mutate the input slice; got len=%d", len(in.Stop.Phases))
	}
}

func TestEnsureStopConfig_DefaultsFinalMessage(t *testing.T) {
	in := &config.LifecycleConfig{
		Stop: &config.LifecycleStopConfig{FinalMessage: "", Phases: nil},
	}
	out := EnsureStopConfig(in)
	if out.FinalMessage == "" {
		t.Error("expected fallback final message when empty")
	}
}
