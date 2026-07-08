package envtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available on PATH")
	}
}

func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v in %s: %v\n%s", name, args, dir, err, out)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	mustRun(t, dir, "git", "init", "-b", "main")
	mustRun(t, dir, "git", "config", "user.email", "test@example.com")
	mustRun(t, dir, "git", "config", "user.name", "test")
	mustRun(t, dir, "git", "config", "commit.gpgsign", "false")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCopyTree_GitRepo_TrackedAndUntrackedCopied(t *testing.T) {
	requireGit(t)
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")

	gitInit(t, src)
	writeFile(t, filepath.Join(src, "tracked.txt"), "tracked")
	mustRun(t, src, "git", "add", "tracked.txt")
	mustRun(t, src, "git", "commit", "-m", "initial")
	writeFile(t, filepath.Join(src, "untracked.txt"), "untracked")

	if err := CopyTree(src, dst, "git", nil); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	assertFileContent(t, filepath.Join(dst, "tracked.txt"), "tracked")
	assertFileContent(t, filepath.Join(dst, "untracked.txt"), "untracked")
}

func TestCopyTree_GitRepo_GitignoredFileNotCopied(t *testing.T) {
	requireGit(t)
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")

	gitInit(t, src)
	writeFile(t, filepath.Join(src, ".gitignore"), "ignored.txt\n")
	writeFile(t, filepath.Join(src, "ignored.txt"), "should not be copied")
	mustRun(t, src, "git", "add", ".gitignore")
	mustRun(t, src, "git", "commit", "-m", "add gitignore")

	if err := CopyTree(src, dst, "git", nil); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	assertNotExist(t, filepath.Join(dst, "ignored.txt"))
}

func TestCopyTree_GitRepo_ExcludesTopLevelDweEnvGit(t *testing.T) {
	requireGit(t)
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")

	gitInit(t, src)
	writeFile(t, filepath.Join(src, "workspace.yml"), "services: {}\n")
	writeFile(t, filepath.Join(src, ".env"), "SECRET=1\n")
	writeFile(t, filepath.Join(src, ".dwe", "state.yml"), "x: 1\n")
	// .gitignore not used here — these files are untracked/gitignore-agnostic;
	// the exclusion must hold even if git itself would happily list them.
	mustRun(t, src, "git", "add", "-A", "--", "workspace.yml")
	mustRun(t, src, "git", "commit", "-m", "initial")

	if err := CopyTree(src, dst, "git", nil); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	assertFileContent(t, filepath.Join(dst, "workspace.yml"), "services: {}\n")
	assertNotExist(t, filepath.Join(dst, ".env"))
	assertNotExist(t, filepath.Join(dst, ".dwe"))
	assertNotExist(t, filepath.Join(dst, ".git"))
}

func TestCopyTree_GitRepo_TrackedButDeletedFileSkipped(t *testing.T) {
	requireGit(t)
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")

	gitInit(t, src)
	gone := filepath.Join(src, "gone.txt")
	writeFile(t, gone, "will be removed from disk, not from the index")
	mustRun(t, src, "git", "add", "gone.txt")
	mustRun(t, src, "git", "commit", "-m", "add gone.txt")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	if err := CopyTree(src, dst, "git", nil); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	assertNotExist(t, filepath.Join(dst, "gone.txt"))
}

func TestCopyTree_GitRepo_SymlinkRecreated(t *testing.T) {
	requireGit(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require elevated privileges on windows")
	}
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")

	gitInit(t, src)
	writeFile(t, filepath.Join(src, "target.txt"), "target")
	if err := os.Symlink("target.txt", filepath.Join(src, "link.txt")); err != nil {
		t.Fatal(err)
	}
	mustRun(t, src, "git", "add", "target.txt", "link.txt")
	mustRun(t, src, "git", "commit", "-m", "add symlink")

	if err := CopyTree(src, dst, "git", nil); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	dstLink := filepath.Join(dst, "link.txt")
	info, err := os.Lstat(dstLink)
	if err != nil {
		t.Fatalf("Lstat(%s): %v", dstLink, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink", dstLink)
	}
	target, err := os.Readlink(dstLink)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if target != "target.txt" {
		t.Fatalf("symlink target = %q, want %q", target, "target.txt")
	}
}

func TestCopyTree_GitRepo_NestedDirsAndPermissions(t *testing.T) {
	requireGit(t)
	if runtime.GOOS == "windows" {
		t.Skip("exact permission bits aren't meaningful on windows")
	}
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")

	gitInit(t, src)
	nested := filepath.Join(src, "a", "b", "c", "deep.txt")
	writeFile(t, nested, "deep")
	if err := os.Chmod(nested, 0o640); err != nil {
		t.Fatal(err)
	}
	mustRun(t, src, "git", "add", "a/b/c/deep.txt")
	mustRun(t, src, "git", "commit", "-m", "nested")

	if err := CopyTree(src, dst, "git", nil); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	dstPath := filepath.Join(dst, "a", "b", "c", "deep.txt")
	assertFileContent(t, dstPath, "deep")
	info, err := os.Stat(dstPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o640 {
		t.Fatalf("perm = %o, want %o", perm, 0o640)
	}
}

func TestCopyTree_RemovesStaleDestinationFiles(t *testing.T) {
	requireGit(t)
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")

	gitInit(t, src)
	writeFile(t, filepath.Join(src, "tracked.txt"), "tracked")
	mustRun(t, src, "git", "add", "tracked.txt")
	mustRun(t, src, "git", "commit", "-m", "initial")

	writeFile(t, filepath.Join(dst, "stale.txt"), "leftover from a prior run")

	if err := CopyTree(src, dst, "git", nil); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	assertNotExist(t, filepath.Join(dst, "stale.txt"))
	assertFileContent(t, filepath.Join(dst, "tracked.txt"), "tracked")
}

func TestCopyTree_Fallback_NonGitDirCopiedFullyMinusExclusions(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")

	writeFile(t, filepath.Join(src, "regular.txt"), "regular")
	writeFile(t, filepath.Join(src, "sub", "nested.txt"), "nested")
	writeFile(t, filepath.Join(src, ".env"), "SECRET=1\n")
	writeFile(t, filepath.Join(src, ".dwe", "state.yml"), "x: 1\n")

	var warnings []string
	warn := func(msg string) { warnings = append(warnings, msg) }

	if err := CopyTree(src, dst, "git", warn); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	assertFileContent(t, filepath.Join(dst, "regular.txt"), "regular")
	assertFileContent(t, filepath.Join(dst, "sub", "nested.txt"), "nested")
	assertNotExist(t, filepath.Join(dst, ".env"))
	assertNotExist(t, filepath.Join(dst, ".dwe"))

	if len(warnings) != 1 {
		t.Fatalf("want exactly 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "gitignored") {
		t.Fatalf("warning %q does not mention gitignored files", warnings[0])
	}
}

func TestCopyTree_Fallback_UnreadableSourceErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based unreadable dirs aren't meaningful on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")

	blocked := filepath.Join(src, "blocked")
	writeFile(t, filepath.Join(blocked, "secret.txt"), "secret")
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	if err := CopyTree(src, dst, "git", nil); err == nil {
		t.Fatal("expected error for unreadable source directory, got nil")
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s content = %q, want %q", path, got, want)
	}
}

func assertNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s: want not-exist, got err=%v", path, err)
	}
}
