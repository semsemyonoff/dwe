package workspace_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExampleWSGit_ScopedReads(t *testing.T) {
	t.Parallel()
	f := newWSGitFixture(t)
	f.setScope(".\nservices/one/src\n./services/two/src\nservices/one/src/\n")
	for _, name := range []string{"ws-check", "ws-status", "ws-log", "ws-diff", "ws-wip"} {
		f.success(name)
	}
	if got := f.success("ls-files", "-z", "--others", "--exclude-standard"); got != "" {
		t.Fatalf("clean scope has untracked files: %q", got)
	}
	initial := f.success("rev-parse", "HEAD")
	f.commit("services/one/src", "scoped one commit")
	afterScoped := f.success("rev-parse", "HEAD")
	if afterScoped == initial {
		t.Fatal("scoped commit did not change combined HEAD")
	}
	f.commit("services/unscoped/src", "outside scope commit")
	if got := f.success("rev-parse", "HEAD"); got != afterScoped {
		t.Fatalf("unscoped commit changed combined HEAD: %q != %q", got, afterScoped)
	}
	f.commit(".", "root commit")
	if got := f.success("rev-parse", "HEAD"); got == afterScoped {
		t.Fatal("root commit did not change combined HEAD")
	}
	f.commit("services/two/src", "scoped two commit")
	log := f.success("ws-log")
	for _, message := range []string{"root commit", "scoped one commit", "scoped two commit"} {
		if !strings.Contains(log, message) {
			t.Errorf("log missing %q: %s", message, log)
		}
	}
	if strings.Contains(log, "outside scope commit") || strings.Count(log, "### services/one/src") != 1 {
		t.Fatalf("wrong log scope or duplicate repo: %s", log)
	}
	committed := f.success("ws-diff")
	if !strings.Contains(committed, "a/services/one/src/tracked.txt") || !strings.Contains(committed, "a/tracked.txt") {
		t.Fatalf("committed diff missing prefixed or root paths: %s", committed)
	}
	if got := f.success("ws-diff", "--stat"); !strings.Contains(got, "### services/two/src") || !strings.Contains(got, "tracked.txt") {
		t.Fatalf("stat diff missing scoped files: %s", got)
	}

	for _, repo := range []string{".", "services/one/src", "services/two/src", "services/unscoped/src"} {
		writeFile(t, filepath.Join(f.root, repo, "tracked.txt"), "dirty in "+repo+"\n")
	}
	for _, name := range []string{"root untracked.txt", "services/one/src/space name.txt", "services/two/src/файл.txt"} {
		writeFile(t, filepath.Join(f.root, name), "untracked contents\n")
	}
	writeFile(t, filepath.Join(f.root, "services/unscoped/src/hidden.txt"), "out of scope\n")
	diff := f.success("diff", "HEAD")
	for _, path := range []string{"a/tracked.txt", "b/tracked.txt", "a/services/one/src/tracked.txt", "b/services/two/src/tracked.txt"} {
		if !strings.Contains(diff, path) {
			t.Errorf("diff missing %q: %s", path, diff)
		}
	}
	if strings.Contains(diff, "unscoped") {
		t.Fatalf("diff includes unscoped changes: %s", diff)
	}
	got := nulNames(t, f.success("ls-files", "-z", "--others", "--exclude-standard"))
	want := []string{"root untracked.txt", "services/one/src/space name.txt", "services/two/src/файл.txt"}
	slices.Sort(got)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("untracked names = %q, want %q", got, want)
	}
	for _, name := range want {
		if got := f.success("hash-object", "--", name); got != f.git(".", "hash-object", "--", name) {
			t.Errorf("hash-object for %q = %q", name, got)
		}
	}
	for _, args := range [][]string{{"ws-wip"}, {"ws-wip", "--stat"}, {"ws-status"}} {
		out := f.success(args...)
		if strings.Contains(out, "unscoped") || !strings.Contains(out, "### services/one/src") {
			t.Fatalf("wrong scope for %v: %s", args, out)
		}
		if args[0] == "ws-wip" && (!strings.Contains(out, "untracked: services/one/src/space name.txt") ||
			!strings.Contains(out, "untracked: services/two/src/файл.txt")) {
			t.Fatalf("wip lost untracked names: %s", out)
		}
	}
}

func TestExampleWSGit_PassThrough(t *testing.T) {
	t.Parallel()
	f := newWSGitFixture(t)
	f.setScope("services/one/src\nservices/two/src\n")
	f.commit(".", "root second commit")
	writeFile(t, filepath.Join(f.root, "tracked.txt"), "root dirty\n")
	writeFile(t, filepath.Join(f.root, "root untracked.txt"), "root untracked\n")
	writeFile(t, filepath.Join(f.root, "services/one/src/scoped untracked.txt"), "nested untracked\n")
	for _, args := range [][]string{
		{"rev-parse", "--show-toplevel"},
		{"rev-parse", "HEAD~1"},
		{"rev-parse", "HEAD", "--"},
		{"diff", "HEAD", "--stat"},
		{"diff", "--stat", "HEAD"},
		{"ls-files", "--others", "--exclude-standard"},
		{"ls-files", "--others", "--exclude-standard", "-z"},
		{"status", "--porcelain"},
	} {
		if got, want := f.success(args...), f.git(".", args...); got != want {
			t.Errorf("pass-through %v = %q, want %q", args, got, want)
		}
	}
	// The wrapper derives the root from its installed location, not the caller's cwd.
	cmd := f.command(f.script, "rev-parse", "--show-toplevel")
	cmd.Dir = filepath.Join(f.root, "services/one/src")
	got, err := cmd.Output()
	if err != nil || string(got) != f.git(".", "rev-parse", "--show-toplevel") {
		t.Fatalf("root resolution from nested cwd: %q, %v", got, err)
	}
}

func TestExampleWSGit_NoRunState(t *testing.T) {
	t.Parallel()
	f := newWSGitFixture(t)
	if err := os.RemoveAll(filepath.Join(f.root, ".ralphex/run")); err != nil {
		t.Fatal(err)
	}
	initial := f.success("rev-parse", "HEAD")
	f.commit("services/one/src", "ignored without run state")
	if got := f.success("rev-parse", "HEAD"); got != initial {
		t.Fatal("root-only fingerprint includes a nested repository")
	}
	writeFile(t, filepath.Join(f.root, "tracked.txt"), "root dirty\n")
	writeFile(t, filepath.Join(f.root, "root file.txt"), "untracked\n")
	writeFile(t, filepath.Join(f.root, "services/one/src/nested.txt"), "out of scope\n")
	for _, args := range [][]string{{"diff", "HEAD"}, {"ls-files", "-z", "--others", "--exclude-standard"}, {"status", "--porcelain"}} {
		if got, want := f.success(args...), f.git(".", args...); got != want {
			t.Errorf("root-only %v = %q, want %q", args, got, want)
		}
	}
	for _, name := range []string{"ws-check", "ws-status", "ws-log", "ws-diff", "ws-wip", "ws-unknown"} {
		f.failure(name)
	}
}

func TestExampleWSGit_InvalidState(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"missing repos", "missing base", "missing branch", "empty run directory", "empty repos", "empty base", "empty branch",
		"multiline base", "multiline branch", "invalid branch", "reserved branch", "checkout shorthand", "absolute repo", "parent repo", "symlink repo",
		"symlink parent", "symlink git", "git file", "symlink state file", "symlink run", "run file",
		"missing repo", "nested base missing", "root base missing", "blank repo line",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newWSGitFixture(t)
			f.setScope("services/one/src\nservices/two/src\n")
			run := filepath.Join(f.root, ".ralphex/run")
			remove := func(path string) {
				t.Helper()
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
			}
			link := func(target, path string) {
				t.Helper()
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "missing repos":
				remove(filepath.Join(run, "repos"))
			case "missing base":
				remove(filepath.Join(run, "base-ref"))
			case "missing branch":
				remove(filepath.Join(run, "task-branch"))
			case "empty run directory":
				for _, file := range []string{"repos", "base-ref", "task-branch"} {
					remove(filepath.Join(run, file))
				}
			case "empty repos":
				writeFile(t, filepath.Join(run, "repos"), "")
			case "empty base":
				writeFile(t, filepath.Join(run, "base-ref"), "")
			case "empty branch":
				writeFile(t, filepath.Join(run, "task-branch"), "")
			case "multiline base":
				writeFile(t, filepath.Join(run, "base-ref"), "base\n\n")
			case "multiline branch":
				writeFile(t, filepath.Join(run, "task-branch"), "task\nother")
			case "invalid branch":
				writeFile(t, filepath.Join(run, "task-branch"), "bad branch\n")
			case "reserved branch":
				writeFile(t, filepath.Join(run, "task-branch"), "HEAD\n")
			case "checkout shorthand":
				writeFile(t, filepath.Join(run, "task-branch"), "@{-1}\n")
			case "absolute repo":
				f.setScope(filepath.Join(f.root, "services/one/src") + "\n")
			case "parent repo":
				f.setScope("services/../services/one/src\n")
			case "symlink repo":
				link("one/src", filepath.Join(f.root, "services/alias"))
				f.setScope("services/alias\n")
			case "symlink parent":
				link("services", filepath.Join(f.root, "alias"))
				f.setScope("alias/one/src\n")
			case "symlink git":
				remove(filepath.Join(f.root, "services/one/src/.git"))
				link(filepath.Join(f.root, "services/two/src/.git"), filepath.Join(f.root, "services/one/src/.git"))
			case "git file":
				remove(filepath.Join(f.root, "services/one/src/.git"))
				writeFile(t, filepath.Join(f.root, "services/one/src/.git"), "gitdir: elsewhere\n")
			case "symlink state file":
				remove(filepath.Join(run, "repos"))
				link("base-ref", filepath.Join(run, "repos"))
			case "symlink run":
				if err := os.Rename(run, filepath.Join(f.root, ".ralphex/saved-run")); err != nil {
					t.Fatal(err)
				}
				link("saved-run", run)
			case "run file":
				remove(run)
				writeFile(t, run, "not a directory\n")
			case "missing repo":
				remove(filepath.Join(f.root, "services/two/src"))
			case "nested base missing":
				f.git("services/two/src", "tag", "-d", "base")
			case "root base missing":
				f.git(".", "tag", "-d", "base")
			case "blank repo line":
				f.setScope("services/one/src\n\n")
			}
			for _, args := range [][]string{
				{"rev-parse", "HEAD"}, {"diff", "HEAD"}, {"ls-files", "-z", "--others", "--exclude-standard"},
				{"status", "--porcelain"}, {"ws-check"},
			} {
				f.failure(args...)
			}
		})
	}
}

func TestExampleWSGit_BranchAndAncestor(t *testing.T) {
	t.Parallel()
	for _, repo := range []string{".", "services/one/src"} {
		for _, problem := range []string{"wrong branch", "detached", "nonancestor"} {
			t.Run(repo+"/"+problem, func(t *testing.T) {
				t.Parallel()
				f := newWSGitFixture(t)
				f.setScope("services/one/src\nservices/two/src\n")
				switch problem {
				case "wrong branch":
					f.git(repo, "checkout", "main")
				case "detached":
					f.git(repo, "checkout", "--detach", "HEAD")
				case "nonancestor":
					f.commit(repo, "future base")
					f.git(repo, "tag", "-f", "base")
					f.git(repo, "reset", "--hard", "HEAD~1")
				}
				if problem == "wrong branch" {
					f.success("ws-check")
					f.success("ws-status")
				}
				for _, args := range [][]string{{"ws-log"}, {"ws-diff"}, {"ws-diff", "--stat"}, {"ws-wip"}, {"ws-wip", "--stat"}} {
					f.failure(args...)
				}
			})
		}
	}
}

func TestExampleWSGit_ArgumentsAndRootOnlyScope(t *testing.T) {
	t.Parallel()
	f := newWSGitFixture(t)
	f.setScope(".\n.\n")
	if got := f.success("ws-check"); strings.Count(got, "workspace:") != 1 || strings.Contains(got, "services/") {
		t.Fatalf("root-only scope = %s", got)
	}
	for _, args := range [][]string{{"ws-check", "extra"}, {"ws-status", "--stat"}, {"ws-log", "extra"},
		{"ws-diff", "--name-only"}, {"ws-wip", "--stat", "extra"}, {"ws-unknown"}} {
		f.failure(args...)
	}
	// State files also accept the final line without a newline.
	writeFile(t, filepath.Join(f.root, ".ralphex/run/repos"), "services/one/src")
	writeFile(t, filepath.Join(f.root, ".ralphex/run/base-ref"), "base")
	writeFile(t, filepath.Join(f.root, ".ralphex/run/task-branch"), "task")
	f.success("ws-check")
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for cur := cwd; ; cur = filepath.Dir(cur) {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
		if filepath.Dir(cur) == cur {
			break
		}
	}
	t.Fatalf("could not find repo root from %s", cwd)
	return ""
}

type wsGitFixture struct {
	t      *testing.T
	root   string
	script string
	env    []string
}

func newWSGitFixture(t *testing.T) *wsGitFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available on PATH")
	}
	f := &wsGitFixture{t: t, root: t.TempDir()}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			f.env = append(f.env, value)
		}
	}
	f.env = append(f.env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	source := filepath.Join(findRepoRoot(t), "examples/workspace-packs/ralphex/workspace/templates/workspace/ralphex/scripts/ws-git")
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("example script mode = %o, want 755", info.Mode().Perm())
	}
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(content, []byte("#!/bin/sh\n")) {
		t.Fatal("example must use the POSIX sh shebang")
	}
	f.script = filepath.Join(f.root, ".ralphex/scripts/ws-git")
	writeFile(t, f.script, string(content))
	if err := os.Chmod(f.script, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, ".gitignore"), "/services/\n/.ralphex/run/\n")
	for _, repo := range []string{".", "services/one/src", "services/two/src", "services/unscoped/src"} {
		writeFile(t, filepath.Join(f.root, repo, "tracked.txt"), "initial\n")
		f.git(repo, "init", "-b", "main")
		f.git(repo, "config", "user.email", "test@example.com")
		f.git(repo, "config", "user.name", "test")
		f.git(repo, "config", "commit.gpgsign", "false")
		f.git(repo, "add", ".")
		f.git(repo, "commit", "-m", "initial")
		f.git(repo, "tag", "base")
		f.git(repo, "checkout", "-b", "task")
	}
	f.setScope("services/one/src\nservices/two/src\n")
	return f
}

func (f *wsGitFixture) setScope(repos string) {
	f.t.Helper()
	writeFile(f.t, filepath.Join(f.root, ".ralphex/run/repos"), repos)
	writeFile(f.t, filepath.Join(f.root, ".ralphex/run/base-ref"), "base\n")
	writeFile(f.t, filepath.Join(f.root, ".ralphex/run/task-branch"), "task\n")
}

func (f *wsGitFixture) command(name string, args ...string) *exec.Cmd {
	f.t.Helper()
	cmd := exec.CommandContext(f.t.Context(), name, args...)
	cmd.Dir = f.root
	cmd.Env = f.env
	return cmd
}

func (f *wsGitFixture) git(repo string, args ...string) string {
	f.t.Helper()
	cmd := f.command("git", append([]string{"-C", filepath.Join(f.root, repo)}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git -C %s %v: %v\n%s", repo, args, err, out)
	}
	return string(out)
}

func (f *wsGitFixture) commit(repo, message string) {
	f.t.Helper()
	writeFile(f.t, filepath.Join(f.root, repo, "tracked.txt"), message+"\n")
	f.git(repo, "add", "tracked.txt")
	f.git(repo, "commit", "-m", message)
}

func (f *wsGitFixture) success(args ...string) string {
	f.t.Helper()
	cmd := f.command(f.script, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || stderr.Len() != 0 {
		f.t.Fatalf("ws-git %v: %v, stderr=%q, stdout=%q", args, err, stderr.String(), out)
	}
	return string(out)
}

func (f *wsGitFixture) failure(args ...string) {
	f.t.Helper()
	cmd := f.command(f.script, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil || stderr.Len() == 0 || len(out) != 0 {
		f.t.Fatalf("ws-git %v must fail with stderr and no stdout: err=%v, stderr=%q, stdout=%q",
			args, err, stderr.String(), out)
	}
}

func nulNames(t *testing.T, out string) []string {
	t.Helper()
	if out == "" {
		return nil
	}
	if !strings.HasSuffix(out, "\x00") {
		t.Fatalf("untracked output missing final NUL: %q", out)
	}
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
}
