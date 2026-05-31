package lifecycle

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/core/usercommands"
	"github.com/semsemyonoff/devbox/internal/shared/i18n"
)

// init replaces PreflightFunc with a no-op for the test binary so lifecycle
// tests don't pick up the host's docker / compose / git binaries and fail
// preflight. Tests that exercise preflight behavior explicitly swap it back.
func init() {
	PreflightFunc = func(_ context.Context, _ *config.DevboxConfig, _ *usercommands.Registry, _, _ string, _ bool, _ io.Writer) error {
		return nil
	}
}

// stubRunPhases replaces RunPhasesFunc with a no-op for the duration of a test.
// Used by tests that exercise the default-config path to avoid the recursive
// test-binary execution that occurs when type:devbox steps call os.Executable().
func stubRunPhases(t *testing.T) {
	t.Helper()
	prev := RunPhasesFunc
	t.Cleanup(func() { RunPhasesFunc = prev })
	RunPhasesFunc = func(_ *config.DevboxConfig, _ *usercommands.Registry, _ string, _ []config.DeployPhase, _, _ string, _ bool, _ bool, _ i18n.Translator, _ string) error {
		return nil
	}
}

// makeMinimalDevboxYML writes the minimum devbox.yml needed for config.LoadConfig to succeed.
func makeMinimalDevboxYML(t *testing.T, dir string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "devbox.yml")
	content := "project:\n  name: test\n  prefix: devbox\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing devbox.yml: %v", err)
	}
	return cfgPath
}

// writeLifecycleYML writes lifecycle.yml with a single noop phase and the given FinalMessage.
func writeLifecycleYML(t *testing.T, devboxDir string, finalMessage string) {
	t.Helper()
	yaml := "run:\n  final_message: " + finalMessage + "\n  phases:\n    - name: start\n      steps:\n        - name: noop\n          type: shell\n          cmd: \"true\"\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}
}
