package test

import (
	"os"
	"path/filepath"
	"testing"
)

// writeScenarioFile writes workspace/tests/<name>.yml with the given body
// under baseDir, creating the directory as needed.
func writeScenarioFile(t *testing.T, baseDir, name, body string) {
	t.Helper()
	dir := filepath.Join(baseDir, "workspace", "tests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir tests dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write scenario %s: %v", name, err)
	}
}
