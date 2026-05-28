package docs

import (
	"io/fs"
	"testing"
)

func TestBuiltinFS(t *testing.T) {
	// Smoke test: BuiltinFS should contain reference/config/devbox.md
	// (or be empty if the sync hasn't run, which is tolerated in tests).
	if BuiltinFS == nil {
		t.Fatal("BuiltinFS is nil")
	}

	_, err := fs.Stat(BuiltinFS, "reference/config/devbox.md")
	if err != nil && err != fs.ErrNotExist {
		t.Fatalf("unexpected error checking for reference/config/devbox.md: %v", err)
	}
	// If file doesn't exist, that's fine for tests (sync may not have run yet).
	// This test just ensures BuiltinFS is initialized and accessible.
}
