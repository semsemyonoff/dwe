package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFile_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.yml")

	written, err := writeFile(path, []byte("name: myproj\n"), false)
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if !written {
		t.Fatal("expected written=true for a new file")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "name: myproj\n" {
		t.Fatalf("content mismatch: %q", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != filePerm {
		t.Fatalf("perm = %o, want %o", perm, filePerm)
	}
}

func TestWriteFile_SkipsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.yml")
	original := []byte("name: original\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	written, err := writeFile(path, []byte("name: replaced\n"), false)
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if written {
		t.Fatal("expected written=false when the file exists and force is unset")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("existing file was modified: %q", got)
	}
}

func TestWriteFile_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(path, []byte("name: original\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	written, err := writeFile(path, []byte("name: replaced\n"), true)
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if !written {
		t.Fatal("expected written=true when force is set")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "name: replaced\n" {
		t.Fatalf("force did not overwrite: %q", got)
	}
}

func TestWriteFile_CreatesNestedDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace", "services", "app", "service.yml")

	written, err := writeFile(path, []byte("type: php\n"), false)
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if !written {
		t.Fatal("expected written=true")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back nested file: %v", err)
	}
	if string(got) != "type: php\n" {
		t.Fatalf("content mismatch: %q", got)
	}

	// Intermediate directory perms.
	info, err := os.Stat(filepath.Join(dir, "workspace", "services"))
	if err != nil {
		t.Fatalf("stat intermediate dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected intermediate path to be a directory")
	}
}

func TestWriteFile_NoPartialFileOnError(t *testing.T) {
	dir := t.TempDir()
	// A regular file occupies a path component, so MkdirAll for the parent of
	// the destination must fail — the write aborts before producing anything at
	// the destination path.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	path := filepath.Join(blocker, "nested", "file.yml")

	written, err := writeFile(path, []byte("data"), false)
	if err == nil {
		t.Fatal("expected an error when a parent path component is a file")
	}
	if written {
		t.Fatal("expected written=false on error")
	}
	// The destination must not hold a readable file. Because a parent path
	// component is a regular file, Stat returns ENOTDIR rather than ENOENT — both
	// mean "no file here"; only a nil error (a real file) is a failure.
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("expected no file at destination, but one exists")
	}
}

func TestWriteFile_StatErrorSurfaces(t *testing.T) {
	dir := t.TempDir()
	// Force the non-NotExist stat branch: a file used as a directory component
	// makes Stat on the destination return a non-IsNotExist error (ENOTDIR).
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	path := filepath.Join(blocker, "file.yml")

	if _, err := writeFile(path, []byte("data"), false); err == nil {
		t.Fatal("expected stat error to surface, got nil")
	}
}
