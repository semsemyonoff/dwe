package project_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/project"
)

// makeProject creates a temp dir with a devbox.yml containing the given schema_version.
// Pass an empty string to omit the schema_version field entirely.
// Returns the canonical (symlink-resolved) path so comparisons work on macOS.
func makeProject(t *testing.T, schemaVersion string) string {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks for canonical comparison (macOS /var/folders → /private/var/folders).
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		real = dir
	}
	var content string
	if schemaVersion == "" {
		content = "project:\n  name: test\n"
	} else {
		content = "schema_version: \"" + schemaVersion + "\"\nproject:\n  name: test\n"
	}
	if err := os.WriteFile(filepath.Join(real, "devbox.yml"), []byte(content), 0o644); err != nil {
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
	root := makeProject(t, "2")
	path := filepath.Join(root, "devbox.yml")

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
	_, found, err := project.Locate("/nonexistent/path/devbox.yml")
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
	if !containsStr(err.Error(), "/nonexistent/path/devbox.yml") {
		t.Errorf("error message should name the path; got: %v", err)
	}
}

func TestLocate_DiscoveryAtCwd(t *testing.T) {
	root := makeProject(t, "1") // Locate does NOT validate schema
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
	root := makeProject(t, "2")
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
	empty := t.TempDir()
	chdir(t, empty)

	// Walk upward — we might find a devbox.yml in a parent of the temp dir on the
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
		t.Fatal("expected found=false when no devbox.yml exists in any parent")
	}
}

func TestLocate_DiscoverySucceedsForLegacyV1(t *testing.T) {
	// Locate must succeed even for schema_version: "1" — validation is separate.
	root := makeProject(t, "1")
	chdir(t, root)

	resolved, found, err := project.Locate("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true for v1 project")
	}
	if resolved.Root != root {
		t.Errorf("Root = %q; want %q", resolved.Root, root)
	}
}

// --- ValidateSchema tests ---

func TestValidateSchema_V2(t *testing.T) {
	root := makeProject(t, "2")
	if err := project.ValidateSchema(filepath.Join(root, "devbox.yml")); err != nil {
		t.Errorf("unexpected error for v2: %v", err)
	}
}

func TestValidateSchema_V1(t *testing.T) {
	root := makeProject(t, "1")
	err := project.ValidateSchema(filepath.Join(root, "devbox.yml"))
	if err == nil {
		t.Fatal("expected error for v1 schema")
	}
	if !containsStr(err.Error(), "legacy devbox project") {
		t.Errorf("expected 'legacy devbox project' in error; got: %v", err)
	}
}

func TestValidateSchema_Missing(t *testing.T) {
	root := makeProject(t, "")
	err := project.ValidateSchema(filepath.Join(root, "devbox.yml"))
	if err == nil {
		t.Fatal("expected error for missing schema_version")
	}
	if !containsStr(err.Error(), "missing") {
		t.Errorf("expected 'missing' in error; got: %v", err)
	}
}

func TestValidateSchema_Unreadable(t *testing.T) {
	err := project.ValidateSchema("/nonexistent/devbox.yml")
	if err == nil {
		t.Fatal("expected error for unreadable file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected wrapped os.ErrNotExist; got: %v", err)
	}
}

// --- Resolve tests ---

func TestResolve_ExplicitGoodV2(t *testing.T) {
	root := makeProject(t, "2")
	resolved, err := project.Resolve(filepath.Join(root, "devbox.yml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Root != root {
		t.Errorf("Root = %q; want %q", resolved.Root, root)
	}
}

func TestResolve_ExplicitGoodV1(t *testing.T) {
	root := makeProject(t, "1")
	_, err := project.Resolve(filepath.Join(root, "devbox.yml"))
	if err == nil {
		t.Fatal("expected schema error for v1")
	}
	if errors.Is(err, project.ErrNotFound) {
		t.Error("schema error must not be ErrNotFound")
	}
	if !containsStr(err.Error(), "legacy devbox project") {
		t.Errorf("expected 'legacy devbox project'; got: %v", err)
	}
}

func TestResolve_ExplicitBadPath(t *testing.T) {
	_, err := project.Resolve("/nonexistent/devbox.yml")
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

func TestResolve_DiscoveryFoundLegacyV1(t *testing.T) {
	root := makeProject(t, "1")
	chdir(t, root)

	_, err := project.Resolve("")
	if err == nil {
		t.Fatal("expected schema error for v1")
	}
	if errors.Is(err, project.ErrNotFound) {
		t.Error("legacy v1 schema error must not be ErrNotFound")
	}
	if !containsStr(err.Error(), "legacy devbox project") {
		t.Errorf("expected 'legacy devbox project'; got: %v", err)
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
