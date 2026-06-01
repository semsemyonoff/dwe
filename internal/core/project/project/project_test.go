package project_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/project"
)

// makeProject creates a temp dir with a workspace.yml.
// Returns the canonical (symlink-resolved) path so comparisons work on macOS.
func makeProject(t *testing.T, _ string) string {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks for canonical comparison (macOS /var/folders → /private/var/folders).
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		real = dir
	}
	content := "project:\n  name: test\n"
	if err := os.WriteFile(filepath.Join(real, "workspace.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("makeProject: %v", err)
	}
	return real
}

// chdir changes the working directory for the duration of the test.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// --- Locate tests ---

func TestLocate_ExplicitExisting(t *testing.T) {
	root := makeProject(t, "")
	path := filepath.Join(root, "workspace.yml")

	resolved, found, err := project.Locate(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if resolved.ConfigPath != path {
		t.Errorf("ConfigPath = %q; want %q", resolved.ConfigPath, path)
	}
	if resolved.Root != root {
		t.Errorf("Root = %q; want %q", resolved.Root, root)
	}
}

func TestLocate_ExplicitNonexistent(t *testing.T) {
	_, found, err := project.Locate("/nonexistent/path/workspace.yml")
	if err == nil {
		t.Fatal("expected error for nonexistent explicit path")
	}
	if found {
		t.Fatal("expected found=false")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected errors.Is(err, os.ErrNotExist); got %v", err)
	}
	// message should name the bad path
	if !containsStr(err.Error(), "/nonexistent/path/workspace.yml") {
		t.Errorf("error message should name the path; got: %v", err)
	}
}

func TestLocate_DiscoveryAtCwd(t *testing.T) {
	root := makeProject(t, "")
	chdir(t, root)

	resolved, found, err := project.Locate("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if resolved.Root != root {
		t.Errorf("Root = %q; want %q", resolved.Root, root)
	}
}

func TestLocate_DiscoveryTwoLevelsUp(t *testing.T) {
	root := makeProject(t, "")
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, sub)

	resolved, found, err := project.Locate("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if resolved.Root != root {
		t.Errorf("Root = %q; want %q", resolved.Root, root)
	}
}

func TestLocate_DiscoveryNoProject(t *testing.T) {
	// Walk upward — we might find a workspace.yml in a parent of the temp dir on the
	// developer's machine. Use a deep subpath under /tmp that won't have one.
	isolated := filepath.Join(t.TempDir(), "deep", "nested", "path")
	if err := os.MkdirAll(isolated, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, isolated)

	_, found, err := project.Locate("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false when no workspace.yml exists in any parent")
	}
}

// --- Resolve tests ---

func TestResolve_ExplicitGood(t *testing.T) {
	root := makeProject(t, "")
	resolved, err := project.Resolve(filepath.Join(root, "workspace.yml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Root != root {
		t.Errorf("Root = %q; want %q", resolved.Root, root)
	}
}

func TestResolve_ExplicitBadPath(t *testing.T) {
	_, err := project.Resolve("/nonexistent/workspace.yml")
	if err == nil {
		t.Fatal("expected error for bad path")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected wrapped os.ErrNotExist; got: %v", err)
	}
	// must NOT be ErrNotFound (allowlist must not swallow explicit-bad-path errors)
	if errors.Is(err, project.ErrNotFound) {
		t.Error("explicit bad path must not be ErrNotFound")
	}
}

func TestResolve_DiscoveryNoProject(t *testing.T) {
	isolated := filepath.Join(t.TempDir(), "deep", "sub")
	if err := os.MkdirAll(isolated, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, isolated)

	_, err := project.Resolve("")
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
	if !errors.Is(err, project.ErrNotFound) {
		t.Errorf("expected errors.Is(err, project.ErrNotFound); got: %v", err)
	}
}

func TestResolve_DiscoveryFound(t *testing.T) {
	root := makeProject(t, "")
	chdir(t, root)

	resolved, err := project.Resolve("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Root != root {
		t.Errorf("Root = %q; want %q", resolved.Root, root)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
