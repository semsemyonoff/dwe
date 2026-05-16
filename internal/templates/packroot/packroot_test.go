package packroot

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func canonicalPath(t *testing.T, root, kind, pack, rel string) string {
	t.Helper()
	return filepath.Join(root, "devbox", "templates", kind, pack, rel)
}

func overridePath(t *testing.T, root, kind, pack, rel string) string {
	t.Helper()
	return filepath.Join(root, "devbox", "templates", kind, pack+".local", rel)
}

func TestResolve_CanonicalHit(t *testing.T) {
	root := t.TempDir()
	want := canonicalPath(t, root, "ai", "default", "foo.tmpl")
	mustWrite(t, want, "canonical")

	got, fromOverride, err := Resolve(root, "ai", "default", "foo.tmpl")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if fromOverride {
		t.Error("fromOverride = true, want false")
	}
}

func TestResolve_OverrideHitWinsOverCanonical(t *testing.T) {
	root := t.TempDir()
	canonical := canonicalPath(t, root, "ai", "default", "foo.tmpl")
	override := overridePath(t, root, "ai", "default", "foo.tmpl")
	mustWrite(t, canonical, "canonical")
	mustWrite(t, override, "override")

	got, fromOverride, err := Resolve(root, "ai", "default", "foo.tmpl")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != override {
		t.Errorf("path = %q, want %q", got, override)
	}
	if !fromOverride {
		t.Error("fromOverride = false, want true")
	}
}

func TestResolve_OverrideOnlyHit(t *testing.T) {
	root := t.TempDir()
	override := overridePath(t, root, "ai", "default", "foo.tmpl")
	mustWrite(t, override, "override")

	got, fromOverride, err := Resolve(root, "ai", "default", "foo.tmpl")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != override {
		t.Errorf("path = %q, want %q", got, override)
	}
	if !fromOverride {
		t.Error("fromOverride = false, want true")
	}
}

func TestResolve_NeitherPresent(t *testing.T) {
	root := t.TempDir()

	_, _, err := Resolve(root, "ai", "default", "foo.tmpl")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error %v should wrap os.ErrNotExist", err)
	}
}

func TestResolve_RelEscape(t *testing.T) {
	root := t.TempDir()
	_, _, err := Resolve(root, "ai", "default", "../escape.tmpl")
	if err == nil {
		t.Fatal("expected error for escaping rel")
	}
}

func TestResolve_OverrideIsDirectoryHardError(t *testing.T) {
	root := t.TempDir()
	canonical := canonicalPath(t, root, "ai", "default", "foo.tmpl")
	mustWrite(t, canonical, "canonical")
	// Make the override path a directory.
	if err := os.MkdirAll(overridePath(t, root, "ai", "default", "foo.tmpl"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, err := Resolve(root, "ai", "default", "foo.tmpl")
	if err == nil {
		t.Fatal("expected hard error when override is a directory")
	}
}

func TestResolve_OverrideIsSymlinkHardError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := t.TempDir()
	canonical := canonicalPath(t, root, "ai", "default", "foo.tmpl")
	mustWrite(t, canonical, "canonical")

	other := filepath.Join(root, "target.tmpl")
	mustWrite(t, other, "elsewhere")
	override := overridePath(t, root, "ai", "default", "foo.tmpl")
	if err := os.MkdirAll(filepath.Dir(override), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, override); err != nil {
		t.Fatal(err)
	}

	_, _, err := Resolve(root, "ai", "default", "foo.tmpl")
	if err == nil {
		t.Fatal("expected hard error when override is a symlink")
	}
}

func TestResolve_CanonicalIsDirectoryHardError(t *testing.T) {
	root := t.TempDir()
	canonical := canonicalPath(t, root, "ai", "default", "foo.tmpl")
	if err := os.MkdirAll(canonical, 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, err := Resolve(root, "ai", "default", "foo.tmpl")
	if err == nil {
		t.Fatal("expected hard error when canonical is a directory")
	}
}

func TestResolve_CanonicalIsSymlinkHardError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target.tmpl")
	mustWrite(t, target, "elsewhere")
	canonical := canonicalPath(t, root, "ai", "default", "foo.tmpl")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, canonical); err != nil {
		t.Fatal(err)
	}

	_, _, err := Resolve(root, "ai", "default", "foo.tmpl")
	if err == nil {
		t.Fatal("expected hard error when canonical is a symlink")
	}
}

func TestResolve_SymlinkPackRootIsHardError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := t.TempDir()
	// Build a real directory elsewhere and symlink the override pack dir to it.
	realPack := filepath.Join(root, "stash", "default.local")
	mustWrite(t, filepath.Join(realPack, "foo.tmpl"), "x")
	overrideDir := filepath.Join(root, "devbox", "templates", "ai", "default.local")
	if err := os.MkdirAll(filepath.Dir(overrideDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPack, overrideDir); err != nil {
		t.Fatal(err)
	}
	// Also write a canonical file so we'd fall through if the symlink were allowed.
	mustWrite(t, canonicalPath(t, root, "ai", "default", "foo.tmpl"), "canonical")

	// A symlinked pack root must be a hard error: following it would allow
	// template sources to be read from outside the project tree, bypassing
	// per-file pathsafe checks. EnsureRealUnder only protects write destinations,
	// not template source reads, so it cannot serve as a safety net here.
	_, _, err := Resolve(root, "ai", "default", "foo.tmpl")
	if err == nil {
		t.Fatal("expected hard error when override pack root is a symlink, got nil")
	}
}

func TestResolve_SymlinkComponentInsidePack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := t.TempDir()
	canonicalDir := filepath.Join(root, "devbox", "templates", "ai", "default")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a symlinked subdir inside the canonical pack.
	target := filepath.Join(root, "outside")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(target, "foo.tmpl"), "outside")
	if err := os.Symlink(target, filepath.Join(canonicalDir, "sub")); err != nil {
		t.Fatal(err)
	}

	_, _, err := Resolve(root, "ai", "default", "sub/foo.tmpl")
	if err == nil {
		t.Fatal("expected error for symlink component inside pack")
	}
}

func TestResolve_RequiredArgs(t *testing.T) {
	cases := []struct {
		name                  string
		root, kind, pack, rel string
	}{
		{"no root", "", "ai", "default", "x"},
		{"no kind", "/tmp", "", "default", "x"},
		{"no pack", "/tmp", "ai", "", "x"},
		{"no rel", "/tmp", "ai", "default", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Resolve(tc.root, tc.kind, tc.pack, tc.rel); err == nil {
				t.Errorf("expected error for missing arg")
			}
		})
	}
}
