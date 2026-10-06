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
	for _, name := range []string{"ws-check", "ws-status", "ws-log", "ws-diff", "ws-wip", "ws-prepare", "ws-unknown"} {
		f.failure(name)
	}
	f.success("rev-parse", "HEAD")
	if got := f.current("."); got != "task" {
		t.Fatalf("ws-prepare without run state changed the root branch: %s", got)
	}
}

func TestExampleWSGit_InvalidState(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"missing repos", "missing base", "missing branch", "empty run directory", "empty repos", "empty base", "empty branch",
		"multiline base", "multiline branch", "invalid branch", "reserved branch", "checkout shorthand", "absolute repo", "parent repo", "symlink repo",
		"symlink parent", "symlink git", "git file", "symlink state file", "symlink run", "run file",
		"missing repo", "nested base missing", "root base missing", "blank repo line", "base equals branch",
		"base aliases branch", "case variant base",
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
			case "base equals branch":
				writeFile(t, filepath.Join(run, "base-ref"), "task\n")
			case "base aliases branch":
				writeFile(t, filepath.Join(run, "base-ref"), "heads/task\n")
			case "case variant base":
				writeFile(t, filepath.Join(run, "base-ref"), "Refs/Heads/TASK\n")
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
					f.success("ws-status")
				}
				// The root may wait on ralphex's default branch (main without config);
				// every other scoped repository must already be on the task branch.
				if problem == "wrong branch" && repo == "." {
					f.success("ws-check")
				} else {
					f.failure("ws-check")
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
		{"ws-diff", "--name-only"}, {"ws-wip", "--stat", "extra"}, {"ws-prepare", "extra"}, {"ws-unknown"}} {
		f.failure(args...)
	}
	// State files also accept the final line without a newline.
	writeFile(t, filepath.Join(f.root, ".ralphex/run/repos"), "services/one/src")
	writeFile(t, filepath.Join(f.root, ".ralphex/run/base-ref"), "base")
	writeFile(t, filepath.Join(f.root, ".ralphex/run/task-branch"), "task")
	f.success("ws-check")
}

func TestExampleWSGit_SilentWithAmbiguousBase(t *testing.T) {
	t.Parallel()
	f := newWSGitFixture(t)
	// A branch named like the base tag makes "base" ambiguous; Git warns on
	// every lookup, which must not reach ralphex through intercepted calls.
	f.git("services/one/src", "branch", "base", "refs/tags/base")
	probe := f.command("git", "-C", filepath.Join(f.root, "services/one/src"), "rev-parse", "--verify", "base^{commit}")
	if raw, err := probe.CombinedOutput(); err != nil || !strings.Contains(string(raw), "ambiguous") {
		t.Fatalf("fixture must produce an ambiguous-ref warning: %v, %s", err, raw)
	}
	writeFile(t, filepath.Join(f.root, "services/one/src/tracked.txt"), "dirty\n")
	for _, args := range [][]string{
		{"rev-parse", "HEAD"}, {"diff", "HEAD"}, {"ls-files", "-z", "--others", "--exclude-standard"},
		{"ws-check"}, {"ws-status"},
	} {
		f.success(args...)
	}
}

func TestExampleWSGit_StrictCheck(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		config string
		setup  func(*wsGitFixture)
		passes bool
	}{
		{name: "all on task branch", passes: true},
		{name: "nested off task branch", setup: func(f *wsGitFixture) { f.git("services/two/src", "checkout", "main") }},
		{name: "nested on default branch", config: "default_branch = trunk\n",
			setup: func(f *wsGitFixture) { f.git("services/one/src", "checkout", "-b", "trunk") }},
		{name: "root on configured default with task branch", config: "default_branch = trunk\n", passes: true,
			setup: func(f *wsGitFixture) { f.git(".", "checkout", "-b", "trunk", "main") }},
		{name: "root on default without task branch", passes: true, setup: func(f *wsGitFixture) {
			f.git(".", "checkout", "main")
			f.git(".", "branch", "-D", "task")
		}},
		{name: "root on master without config", passes: true,
			setup: func(f *wsGitFixture) { f.git(".", "checkout", "-b", "master", "main") }},
		{name: "root on main with other configured default", config: "default_branch = trunk\n",
			setup: func(f *wsGitFixture) { f.git(".", "checkout", "main") }},
		{name: "root on unrelated branch", setup: func(f *wsGitFixture) { f.git(".", "checkout", "-b", "feature", "main") }},
		{name: "root default task branch lacks base", setup: func(f *wsGitFixture) {
			f.git(".", "checkout", "main")
			f.commit(".", "new base")
			f.git(".", "tag", "-f", "base")
		}},
		{name: "root default HEAD lacks base", setup: func(f *wsGitFixture) {
			f.commit(".", "new base")
			f.git(".", "tag", "-f", "base")
			f.git(".", "checkout", "main")
			f.git(".", "branch", "-D", "task")
		}},
		{name: "nested base not ancestor", setup: func(f *wsGitFixture) {
			f.commit("services/one/src", "future base")
			f.git("services/one/src", "tag", "-f", "base")
			f.git("services/one/src", "reset", "--hard", "HEAD~1")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newWSGitFixture(t)
			if tt.config != "" {
				writeFile(t, filepath.Join(f.root, ".ralphex/config"), "vcs_command = .ralphex/scripts/ws-git\n"+tt.config)
			}
			if tt.setup != nil {
				tt.setup(f)
			}
			if !tt.passes {
				f.failure("ws-check")
				return
			}
			out := f.success("ws-check")
			if !strings.Contains(out, "workspace: branch=") || !strings.Contains(out, "services/two/src: branch=task base=base task=task\n") {
				t.Fatalf("unexpected ws-check output: %s", out)
			}
		})
	}
}

func TestExampleWSGit_Prepare(t *testing.T) {
	t.Parallel()
	scoped := []string{".", "services/one/src", "services/two/src"}
	t.Run("creates base and branches", func(t *testing.T) {
		t.Parallel()
		f := newWSGitFixture(t)
		f.unprepare(append(scoped, "services/unscoped/src")...)
		// HEAD ahead of an existing base: the new branch starts at the base.
		f.git("services/two/src", "tag", "base")
		f.commit("services/two/src", "ahead of base")
		heads := map[string]string{}
		for _, repo := range scoped {
			heads[repo] = f.rev(repo, "HEAD")
		}
		unscoped := f.refState("services/unscoped/src")
		out := f.success("ws-prepare")
		for _, want := range []string{
			"workspace: created tag base at HEAD\n", "workspace: left on main; ralphex switches to task at launch\n",
			"services/one/src: created task from base\n", "services/two/src: created task from base\n",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("prepare output missing %q: %s", want, out)
			}
		}
		if strings.Contains(out, "services/two/src: created tag") {
			t.Errorf("existing base was replaced: %s", out)
		}
		if got := f.current("."); got != "main" {
			t.Errorf("root on default branch moved to %s", got)
		}
		if f.hasRef(".", "refs/heads/task") {
			t.Error("prepare created the task branch in a root on its default branch")
		}
		for _, repo := range scoped[1:] {
			if got := f.current(repo); got != "task" {
				t.Errorf("%s on %s, want task", repo, got)
			}
		}
		for repo, head := range map[string]string{".": heads["."], "services/one/src": heads["services/one/src"]} {
			if got := f.rev(repo, "base^{commit}"); got != head {
				t.Errorf("%s base tag = %s, want HEAD %s", repo, got, head)
			}
			if typ := strings.TrimSpace(f.git(repo, "cat-file", "-t", "refs/tags/base")); typ != "commit" {
				t.Errorf("%s base tag must be lightweight, got %s", repo, typ)
			}
		}
		if got, base := f.rev("services/two/src", "task"), f.rev("services/two/src", "base"); got != base || got == heads["services/two/src"] {
			t.Errorf("task branch tip %s, want base %s (HEAD was %s)", got, base, heads["services/two/src"])
		}
		if upstream := f.gitStatus("services/one/src", "config", "branch.task.merge"); upstream == nil {
			t.Error("task branch must not track its base")
		}
		if got := f.refState("services/unscoped/src"); got != unscoped {
			t.Errorf("unscoped repository changed: %s -> %s", unscoped, got)
		}
		f.success("ws-check")
		before := f.refState(scoped...)
		f.success("ws-prepare")
		if got := f.refState(scoped...); got != before {
			t.Errorf("second prepare changed refs:\n%s\n%s", before, got)
		}
	})
	t.Run("switches to existing branch", func(t *testing.T) {
		t.Parallel()
		f := newWSGitFixture(t)
		f.commit("services/one/src", "task work")
		tip := f.rev("services/one/src", "task")
		f.git("services/one/src", "checkout", "main")
		f.git(".", "checkout", "-b", "feature", "main")
		f.git(".", "branch", "-D", "task")
		out := f.success("ws-prepare")
		if !strings.Contains(out, "services/one/src: switched to task\n") || !strings.Contains(out, "services/two/src: on task\n") ||
			!strings.Contains(out, "workspace: created task from base\n") {
			t.Errorf("unexpected prepare output: %s", out)
		}
		if f.current("services/one/src") != "task" || f.rev("services/one/src", "HEAD") != tip {
			t.Error("existing task branch not checked out unchanged")
		}
		if f.current(".") != "task" || f.rev(".", "task") != f.rev(".", "base") {
			t.Error("root on a non-default branch must get the task branch from base")
		}
		f.success("ws-check")
	})
	t.Run("root on non-default branch with configured default", func(t *testing.T) {
		t.Parallel()
		f := newWSGitFixture(t)
		writeFile(t, filepath.Join(f.root, ".ralphex/config"), "default_branch = trunk\n")
		f.git(".", "checkout", "main")
		f.success("ws-prepare")
		if got := f.current("."); got != "task" {
			t.Errorf("root on main with default_branch=trunk stayed on %s", got)
		}
		f.git(".", "checkout", "-b", "trunk")
		f.success("ws-prepare")
		if got := f.current("."); got != "trunk" {
			t.Errorf("root on configured default switched to %s", got)
		}
	})
	for _, tt := range []struct {
		name  string
		setup func(*wsGitFixture)
	}{
		{name: "existing branch lacks base", setup: func(f *wsGitFixture) {
			f.git("services/two/src", "checkout", "main")
			f.commit("services/two/src", "newer base")
			f.git("services/two/src", "tag", "-f", "base")
		}},
		{name: "existing branch lacks new base at HEAD", setup: func(f *wsGitFixture) {
			f.git("services/two/src", "checkout", "main")
			f.commit("services/two/src", "ahead")
			f.git("services/two/src", "tag", "-d", "base")
		}},
		{name: "invalid tag name", setup: func(f *wsGitFixture) {
			writeFile(f.t, filepath.Join(f.root, ".ralphex/run/base-ref"), "bad..base\n")
		}},
		{name: "tag is not a commit", setup: func(f *wsGitFixture) {
			f.git("services/two/src", "tag", "-d", "base")
			f.git("services/two/src", "tag", "base", "HEAD^{tree}")
		}},
		{name: "root default HEAD lacks existing base", setup: func(f *wsGitFixture) {
			f.git(".", "checkout", "-b", "side")
			f.commit(".", "side commit")
			f.git(".", "tag", "base")
			f.git(".", "checkout", "main")
		}},
		{name: "base equals branch", setup: func(f *wsGitFixture) {
			writeFile(f.t, filepath.Join(f.root, ".ralphex/run/base-ref"), "task\n")
		}},
		{name: "base aliases branch via heads", setup: func(f *wsGitFixture) {
			writeFile(f.t, filepath.Join(f.root, ".ralphex/run/base-ref"), "heads/task\n")
		}},
		{name: "base aliases branch via refs/heads", setup: func(f *wsGitFixture) {
			writeFile(f.t, filepath.Join(f.root, ".ralphex/run/base-ref"), "refs/heads/task\n")
		}},
		{name: "base relative to task branch", setup: func(f *wsGitFixture) {
			writeFile(f.t, filepath.Join(f.root, ".ralphex/run/base-ref"), "task~0\n")
		}},
		{name: "base relative to HEAD", setup: func(f *wsGitFixture) {
			writeFile(f.t, filepath.Join(f.root, ".ralphex/run/base-ref"), "HEAD~0\n")
		}},
		{name: "missing base named like a ref path", setup: func(f *wsGitFixture) {
			writeFile(f.t, filepath.Join(f.root, ".ralphex/run/base-ref"), "tags/new-base\n")
		}},
		// Spelling-based, so these hold on case-sensitive filesystems too.
		{name: "case variant of task branch", setup: setWSGitBase("TASK")},
		{name: "case variant via heads", setup: setWSGitBase("Heads/task")},
		{name: "case variant via refs/heads", setup: setWSGitBase("REFS/HEADS/task")},
		{name: "case variant of HEAD", setup: setWSGitBase("HeAd~0")},
		{name: "case variant ref path tag", setup: setWSGitBase("Tags/new-base")},
		{name: "unborn repository", setup: func(f *wsGitFixture) {
			if err := os.RemoveAll(filepath.Join(f.root, "services/two/src/.git")); err != nil {
				f.t.Fatal(err)
			}
			f.git("services/two/src", "init", "-b", "main")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newWSGitFixture(t)
			// Earlier repositories in scope order need changes, so a late
			// rejection would show up as a partial mutation.
			f.unprepare(".", "services/one/src")
			tt.setup(f)
			before := f.refState(scoped...)
			f.failure("ws-prepare")
			if got := f.refState(scoped...); got != before {
				t.Fatalf("rejected prepare mutated repositories:\n%s\n%s", before, got)
			}
		})
	}
	t.Run("base differing from branch by more than case", func(t *testing.T) {
		t.Parallel()
		f := newWSGitFixture(t)
		f.unprepare(".", "services/one/src")
		setWSGitBase("Task2")(f)
		f.success("ws-prepare")
		f.success("ws-check")
		if got := f.current("services/one/src"); got != "task" || f.rev("services/one/src", "task") != f.rev("services/one/src", "Task2") {
			t.Fatalf("prepare with base Task2 left %s at the wrong commit", got)
		}
	})
	t.Run("switch conflict fails", func(t *testing.T) {
		t.Parallel()
		f := newWSGitFixture(t)
		f.git("services/one/src", "checkout", "main")
		f.git("services/one/src", "branch", "-D", "task")
		f.commit("services/one/src", "ahead of base")
		writeFile(t, filepath.Join(f.root, "services/one/src/tracked.txt"), "dirty\n")
		cmd := f.command(f.script, "ws-prepare")
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "cannot create task from base") {
			t.Fatalf("conflicting switch must fail: %v, %s", err, out)
		}
		if got := f.current("services/one/src"); got != "main" {
			t.Errorf("failed switch left %s checked out", got)
		}
	})
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

func setWSGitBase(base string) func(*wsGitFixture) {
	return func(f *wsGitFixture) {
		writeFile(f.t, filepath.Join(f.root, ".ralphex/run/base-ref"), base+"\n")
	}
}

// unprepare puts repositories on main without the task branch or base tag.
func (f *wsGitFixture) unprepare(repos ...string) {
	f.t.Helper()
	for _, repo := range repos {
		f.git(repo, "checkout", "main")
		f.git(repo, "branch", "-D", "task")
		f.git(repo, "tag", "-d", "base")
	}
}

func (f *wsGitFixture) current(repo string) string {
	f.t.Helper()
	return strings.TrimSpace(f.git(repo, "symbolic-ref", "--short", "HEAD"))
}

func (f *wsGitFixture) rev(repo, ref string) string {
	f.t.Helper()
	return strings.TrimSpace(f.git(repo, "rev-parse", "--verify", ref))
}

func (f *wsGitFixture) hasRef(repo, ref string) bool {
	f.t.Helper()
	return f.gitStatus(repo, "rev-parse", "--verify", "--quiet", ref) == nil
}

func (f *wsGitFixture) gitStatus(repo string, args ...string) error {
	f.t.Helper()
	return f.command("git", append([]string{"-C", filepath.Join(f.root, repo)}, args...)...).Run()
}

// refState captures every ref and the checked-out branch of each repository.
func (f *wsGitFixture) refState(repos ...string) string {
	f.t.Helper()
	var state strings.Builder
	for _, repo := range repos {
		state.WriteString("### " + repo + "\n")
		state.WriteString(f.git(repo, "for-each-ref", "--format=%(refname) %(objectname)"))
		state.Write(readExampleFile(f.t, filepath.Join(f.root, repo, ".git/HEAD")))
	}
	return state.String()
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
