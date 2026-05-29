package meta

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentPointerLifecycle(t *testing.T) {
	base := t.TempDir()

	got, err := ReadCurrent(base)
	if err != nil {
		t.Fatalf("read empty: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	if err := WriteCurrent(base, "feature-x"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err = ReadCurrent(base)
	if err != nil {
		t.Fatalf("read after write: %v", err)
	}
	if got != "feature-x" {
		t.Fatalf("got %q want feature-x", got)
	}

	if err := WriteCurrent(base, "hotfix"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got, _ = ReadCurrent(base)
	if got != "hotfix" {
		t.Fatalf("overwrite got %q", got)
	}

	if err := ClearCurrent(base); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err = ReadCurrent(base)
	if err != nil {
		t.Fatalf("read after clear: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty after clear, got %q", got)
	}

	if err := ClearCurrent(base); err != nil {
		t.Fatalf("clear twice: %v", err)
	}
}

func TestWriteCurrent_createsStateDir(t *testing.T) {
	base := t.TempDir()
	if err := WriteCurrent(base, "x"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, ".devbox/snapshots")); err != nil {
		t.Fatalf("state dir not created: %v", err)
	}
}
