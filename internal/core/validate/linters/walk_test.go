package linters

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func relNames(t *testing.T, root string, files []string) []string {
	t.Helper()
	out := make([]string, 0, len(files))
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			t.Fatalf("rel %s: %v", f, err)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

func TestCollectFilesExtensionFilter(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "scripts", "a.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(root, "scripts", "b.bash"), "#!/bin/bash\n")
	writeFile(t, filepath.Join(root, "scripts", "ignore.txt"), "x")

	files, missing, err := collectFiles(root, []string{"scripts"}, []string{".sh", ".bash"}, nil, false)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("missing: want empty, got %v", missing)
	}
	got := relNames(t, root, files)
	want := []string{"scripts/a.sh", "scripts/b.bash"}
	if !slicesEqual(got, want) {
		t.Errorf("files: want %v, got %v", want, got)
	}
}

func TestCollectFilesFilenameFilter(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Dockerfile"), "FROM scratch\n")
	writeFile(t, filepath.Join(root, "svc", "service.dockerfile"), "FROM scratch\n")
	writeFile(t, filepath.Join(root, "svc", "notes.txt"), "x")

	files, _, err := collectFiles(root, []string{"."}, []string{".dockerfile"}, []string{"Dockerfile"}, true)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	got := relNames(t, root, files)
	want := []string{"Dockerfile", "svc/service.dockerfile"}
	if !slicesEqual(got, want) {
		t.Errorf("files: want %v, got %v", want, got)
	}
}

func TestCollectFilesDotPathWalksBaseDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Dockerfile"), "FROM scratch\n")

	files, missing, err := collectFiles(root, []string{"."}, nil, []string{"Dockerfile"}, true)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("missing: want empty, got %v", missing)
	}
	if len(files) != 1 {
		t.Fatalf("files: want 1, got %v", files)
	}
}

func TestCollectFilesExplicitFileBypassesFilter(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "weird.notmatch"), "x")

	files, _, err := collectFiles(root, []string{"weird.notmatch"}, []string{".sh"}, nil, false)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files: want 1 (explicit path bypasses ext filter), got %v", files)
	}
}

func TestCollectFilesMissingDefaultSilent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files, missing, err := collectFiles(root, []string{"does-not-exist"}, []string{".sh"}, nil, true)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	if len(files) != 0 || len(missing) != 0 {
		t.Errorf("want silent skip, got files=%v missing=%v", files, missing)
	}
}

func TestCollectFilesMissingExplicitReturned(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files, missing, err := collectFiles(root, []string{"does-not-exist"}, []string{".sh"}, nil, false)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("files: want empty, got %v", files)
	}
	want := []string{"does-not-exist"}
	if !slicesEqual(missing, want) {
		t.Errorf("missing: want %v, got %v", want, missing)
	}
}

func TestCollectFilesPathTraversalRejected(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, _, err := collectFiles(root, []string{"../outside"}, []string{".sh"}, nil, false)
	if err == nil {
		t.Fatal("want error for path traversal, got nil")
	}
}

func TestCollectFilesSymlinkSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require admin on windows")
	}
	t.Parallel()
	root := t.TempDir()

	// real file outside the walked subtree
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "real.sh"), "#!/bin/sh\n")
	// symlink to file inside walked subtree
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "real.sh"), filepath.Join(root, "scripts", "linked.sh")); err != nil {
		t.Fatal(err)
	}
	// real file alongside
	writeFile(t, filepath.Join(root, "scripts", "real.sh"), "#!/bin/sh\n")

	files, _, err := collectFiles(root, []string{"scripts"}, []string{".sh"}, nil, false)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	got := relNames(t, root, files)
	want := []string{"scripts/real.sh"}
	if !slicesEqual(got, want) {
		t.Errorf("files: want %v (symlink skipped), got %v", want, got)
	}
}

func TestCollectFilesSymlinkedDirSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require admin on windows")
	}
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "x.sh"), "#!/bin/sh\n")
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "scripts", "linkedDir")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "scripts", "real.sh"), "#!/bin/sh\n")

	files, _, err := collectFiles(root, []string{"scripts"}, []string{".sh"}, nil, false)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	got := relNames(t, root, files)
	if len(got) != 1 || !strings.HasSuffix(got[0], "real.sh") {
		t.Errorf("want only real.sh, got %v", got)
	}
}

func TestCollectFilesGitDirSkipped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "hooks", "pre-commit.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(root, "scripts", "real.sh"), "#!/bin/sh\n")

	files, _, err := collectFiles(root, []string{"."}, []string{".sh"}, nil, true)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	got := relNames(t, root, files)
	want := []string{"scripts/real.sh"}
	if !slicesEqual(got, want) {
		t.Errorf("files: want %v (.git skipped), got %v", want, got)
	}
}

func TestCollectFilesExplicitGitPathSkipped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "hooks", "pre-commit.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(root, "scripts", "real.sh"), "#!/bin/sh\n")

	// Explicit "paths: [.git]" must be silently skipped — not walked, not reported missing.
	files, missing, err := collectFiles(root, []string{".git"}, []string{".sh"}, nil, false)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("explicit .git path must be skipped, got files=%v", files)
	}
	if len(missing) != 0 {
		t.Errorf("explicit .git path must not produce missing, got missing=%v", missing)
	}
}

func TestCollectFilesExplicitGitSubpathSkipped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "hooks", "pre-commit.sh"), "#!/bin/sh\n")

	// Explicit subpath ".git/hooks" must also be skipped.
	files, missing, err := collectFiles(root, []string{filepath.Join(".git", "hooks")}, []string{".sh"}, nil, false)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("explicit .git/hooks path must be skipped, got files=%v", files)
	}
	if len(missing) != 0 {
		t.Errorf("explicit .git subpath must not produce missing, got missing=%v", missing)
	}
}

func TestCollectFilesExplicitGitFileSkipped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "hooks", "pre-commit.sh"), "#!/bin/sh\n")

	// Explicit file path inside .git must also be skipped.
	gitFile := filepath.Join(".git", "hooks", "pre-commit.sh")
	files, missing, err := collectFiles(root, []string{gitFile}, []string{}, nil, false)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("explicit file inside .git must be skipped, got files=%v", files)
	}
	if len(missing) != 0 {
		t.Errorf("explicit .git file must not produce missing, got missing=%v", missing)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
