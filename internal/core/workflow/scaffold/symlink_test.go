package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const agentsContent = "# AGENTS\n\nThis is the canonical agent doc.\n"

func seedAgents(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsContent), 0o644); err != nil {
		t.Fatalf("seed AGENTS.md: %v", err)
	}
}

func TestLinkClaudeMd_Symlink(t *testing.T) {
	dir := t.TempDir()
	seedAgents(t, dir)

	fallback, err := linkClaudeMd(dir, false)
	if err != nil {
		t.Fatalf("linkClaudeMd: %v", err)
	}
	if fallback {
		t.Fatal("expected fallback=false when the symlink succeeds")
	}

	claudePath := filepath.Join(dir, "CLAUDE.md")
	target, err := os.Readlink(claudePath)
	if err != nil {
		t.Fatalf("expected CLAUDE.md to be a symlink: %v", err)
	}
	if target != "AGENTS.md" {
		t.Fatalf("symlink target = %q, want %q", target, "AGENTS.md")
	}

	// The link must resolve to the canonical content.
	got, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("read through symlink: %v", err)
	}
	if string(got) != agentsContent {
		t.Fatalf("content through symlink mismatch: %q", got)
	}
}

func TestLinkClaudeMd_CopyFallback(t *testing.T) {
	dir := t.TempDir()
	seedAgents(t, dir)

	// Force the symlink to fail so the copy fallback runs.
	orig := symlinkFn
	t.Cleanup(func() { symlinkFn = orig })
	symlinkFn = func(string, string) error {
		return errors.New("symlinks not supported")
	}

	fallback, err := linkClaudeMd(dir, false)
	if err != nil {
		t.Fatalf("linkClaudeMd: %v", err)
	}
	if !fallback {
		t.Fatal("expected fallback=true when the symlink fails")
	}

	claudePath := filepath.Join(dir, "CLAUDE.md")
	// It must be a regular file, not a symlink.
	if _, err := os.Readlink(claudePath); err == nil {
		t.Fatal("expected CLAUDE.md to be a regular file, not a symlink")
	}
	got, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("read copy fallback: %v", err)
	}
	if string(got) != agentsContent {
		t.Fatalf("copy fallback content mismatch: %q", got)
	}
}

func TestLinkClaudeMd_PreexistingTargetLeftUntouched(t *testing.T) {
	dir := t.TempDir()
	seedAgents(t, dir)

	claudePath := filepath.Join(dir, "CLAUDE.md")
	existing := []byte("# pre-existing CLAUDE.md\n")
	if err := os.WriteFile(claudePath, existing, 0o644); err != nil {
		t.Fatalf("seed CLAUDE.md: %v", err)
	}

	// Symlink must not even be attempted when the target already exists.
	orig := symlinkFn
	t.Cleanup(func() { symlinkFn = orig })
	symlinkFn = func(string, string) error {
		t.Fatal("symlinkFn should not be called when CLAUDE.md already exists")
		return nil
	}

	fallback, err := linkClaudeMd(dir, false)
	if err != nil {
		t.Fatalf("linkClaudeMd: %v", err)
	}
	if fallback {
		t.Fatal("expected fallback=false for a pre-existing target")
	}

	got, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(existing) {
		t.Fatalf("pre-existing CLAUDE.md was modified: %q", got)
	}
}

func TestLinkClaudeMd_ForceOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	seedAgents(t, dir)

	claudePath := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte("# stale CLAUDE.md\n"), 0o644); err != nil {
		t.Fatalf("seed stale CLAUDE.md: %v", err)
	}

	fallback, err := linkClaudeMd(dir, true)
	if err != nil {
		t.Fatalf("linkClaudeMd(force): %v", err)
	}
	if fallback {
		t.Fatal("expected fallback=false when the symlink succeeds after force")
	}

	target, err := os.Readlink(claudePath)
	if err != nil {
		t.Fatalf("expected CLAUDE.md to be a symlink after force: %v", err)
	}
	if target != "AGENTS.md" {
		t.Fatalf("symlink target = %q, want AGENTS.md", target)
	}
}

func TestLinkClaudeMd_MissingAgentsCopyFallbackErrors(t *testing.T) {
	dir := t.TempDir()
	// No AGENTS.md seeded.

	orig := symlinkFn
	t.Cleanup(func() { symlinkFn = orig })
	symlinkFn = func(string, string) error {
		return errors.New("symlinks not supported")
	}

	if _, err := linkClaudeMd(dir, false); err == nil {
		t.Fatal("expected an error when AGENTS.md is missing on the copy-fallback path")
	}
}
