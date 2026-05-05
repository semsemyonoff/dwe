package lifecycle

import (
	"os"
	"path/filepath"
	"testing"
)

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
