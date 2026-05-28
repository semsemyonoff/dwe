package usercommands_test

import (
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/core/usercommands"
)

// TestLoadRegistryFromConfigPath_NoCommandsDirReturnsEmpty guards the
// silent-on-missing-dir contract: if devbox/commands/ is absent next to
// configPath, return an empty (non-nil) registry, not an error.
func TestLoadRegistryFromConfigPath_NoCommandsDirReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := usercommands.LoadRegistryFromConfigPath(cfgPath)
	if err != nil {
		t.Fatalf("LoadRegistryFromConfigPath: %v", err)
	}
	if reg == nil {
		t.Fatal("registry is nil; expected empty non-nil registry")
	}
	if got := len(reg.List("")); got != 0 {
		t.Errorf("registry has %d defs, want 0", got)
	}
}

// TestLoadRegistryFromConfigPath_LoadsCommands verifies that with
// devbox/commands/ present, definitions are loaded relative to configPath.
func TestLoadRegistryFromConfigPath_LoadsCommands(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(dir, "devbox", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "commands:\n  up:\n    type: shell\n    cmd: echo up\n"
	if err := os.WriteFile(filepath.Join(cmdDir, "db.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := usercommands.LoadRegistryFromConfigPath(cfgPath)
	if err != nil {
		t.Fatalf("LoadRegistryFromConfigPath: %v", err)
	}
	if got := len(reg.List("")); got == 0 {
		t.Error("expected at least one command def, got 0")
	}
}
