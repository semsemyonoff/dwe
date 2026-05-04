package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunStop_MissingLifecycleYML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	ctx := StopContext{ConfigPath: cfgPath}
	err := RunStop(ctx)
	if err == nil {
		t.Fatal("expected error for missing lifecycle.yml, got nil")
	}
	if !strings.Contains(err.Error(), "no lifecycle.yml") {
		t.Errorf("error should mention 'no lifecycle.yml', got: %v", err)
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
	err := RunStop(ctx)
	if err == nil {
		t.Fatal("expected error for missing stop: section, got nil")
	}
	if !strings.Contains(err.Error(), "stop:") && !strings.Contains(err.Error(), "stop` section") {
		t.Errorf("error should mention missing stop section, got: %v", err)
	}
}

func TestRunStop_HappyPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := "stop:\n  final_message: \"Goodbye!\"\n  phases:\n    - name: down\n      steps:\n        - name: noop\n          run: \"true\"\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	ctx := StopContext{ConfigPath: cfgPath}
	if err := RunStop(ctx); err != nil {
		t.Errorf("unexpected error on happy path: %v", err)
	}
}
