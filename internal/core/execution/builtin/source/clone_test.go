package source

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available on PATH")
	}
}

func newTestExecCtx(root string, out *bytes.Buffer) spec.ExecContext {
	return spec.ExecContext{
		Config:      &config.DweConfig{},
		ProjectRoot: root,
		Output:      render.NewWriter(out),
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

// makeSourceRepo creates a git repository with one commit on branch main and an
// extra branch `other`, and returns its absolute path (usable as a clone URL).
func makeSourceRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	mustRun(t, dir, "git", "init", "-b", "main")
	mustRun(t, dir, "git", "config", "user.email", "test@example.com")
	mustRun(t, dir, "git", "config", "user.name", "test")
	mustRun(t, dir, "git", "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "git", "add", ".")
	mustRun(t, dir, "git", "commit", "-m", "initial")
	mustRun(t, dir, "git", "branch", "other")
	return dir
}

// --- Validate ---

func TestClone_Validate(t *testing.T) {
	cases := []struct {
		name    string
		with    map[string]any
		wantErr string
	}{
		{"ok", map[string]any{"repo": "git@host:x.git", "dir": "services/backend/src"}, ""},
		{"ok with branch", map[string]any{"repo": "r", "dir": "src", "branch": "main"}, ""},
		{"nil params", nil, "'repo'"},
		{"missing repo", map[string]any{"dir": "src"}, "'repo'"},
		{"missing dir", map[string]any{"repo": "r"}, "'dir'"},
		{"empty dir", map[string]any{"repo": "r", "dir": ""}, "'dir'"},
		{"absolute dir", map[string]any{"repo": "r", "dir": "/etc/passwd"}, "must be relative"},
		{"root-equivalent dir", map[string]any{"repo": "r", "dir": "."}, "not allowed"},
		{"escaping dir", map[string]any{"repo": "r", "dir": "../outside"}, "not allowed"},
		{"escaping dir via nesting", map[string]any{"repo": "r", "dir": "a/../../outside"}, "not allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (Clone{}).Validate(tc.with)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestClone_Describe(t *testing.T) {
	got := (Clone{}).Describe(map[string]any{"repo": "git@host:x.git", "dir": "src"})
	if got != "builtin: source_clone(repo=git@host:x.git, dir=src)" {
		t.Errorf("describe = %q", got)
	}
	got = (Clone{}).Describe(map[string]any{"repo": "r", "dir": "src", "branch": "dev"})
	if !strings.Contains(got, "branch=dev") {
		t.Errorf("describe with branch = %q", got)
	}
}

// --- Run ---

func TestClone_FreshClone(t *testing.T) {
	repo := makeSourceRepo(t)
	root := t.TempDir()
	var out bytes.Buffer

	err := (Clone{}).Run(context.Background(),
		map[string]any{"repo": repo, "dir": "services/backend/src"},
		newTestExecCtx(root, &out))
	if err != nil {
		t.Fatalf("clone failed: %v", err)
	}
	dest := filepath.Join(root, "services", "backend", "src")
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Fatalf("expected a git checkout at %s: %v", dest, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); err != nil {
		t.Fatalf("expected worktree content: %v", err)
	}
	if !strings.Contains(out.String(), "cloned") {
		t.Errorf("expected a success message, got %q", out.String())
	}
}

func TestClone_RerunIsNoop(t *testing.T) {
	repo := makeSourceRepo(t)
	root := t.TempDir()
	var out bytes.Buffer
	ectx := newTestExecCtx(root, &out)
	with := map[string]any{"repo": repo, "dir": "src"}

	if err := (Clone{}).Run(context.Background(), with, ectx); err != nil {
		t.Fatalf("first clone: %v", err)
	}
	dest := filepath.Join(root, "src")
	// A local edit proves the second run does not re-clone over the worktree.
	marker := filepath.Join(dest, "local-edit.txt")
	if err := os.WriteFile(marker, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := (Clone{}).Run(context.Background(), with, ectx); err != nil {
		t.Fatalf("second clone: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("second run must leave the existing checkout untouched: %v", err)
	}
	if !strings.Contains(out.String(), "already cloned") {
		t.Errorf("expected a skip message, got %q", out.String())
	}
}

// A checkout sitting on a different branch than the one requested is still a
// no-op: source_clone materialises the source once and never re-points it.
func TestClone_DifferentBranchIsNoop(t *testing.T) {
	repo := makeSourceRepo(t)
	root := t.TempDir()
	var out bytes.Buffer
	ectx := newTestExecCtx(root, &out)

	if err := (Clone{}).Run(context.Background(),
		map[string]any{"repo": repo, "dir": "src", "branch": "other"}, ectx); err != nil {
		t.Fatalf("first clone: %v", err)
	}
	dest := filepath.Join(root, "src")
	branchOf := func() string {
		cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
		cmd.Dir = dest
		b, err := cmd.Output()
		if err != nil {
			t.Fatalf("rev-parse: %v", err)
		}
		return strings.TrimSpace(string(b))
	}
	if got := branchOf(); got != "other" {
		t.Fatalf("branch after clone = %q, want other", got)
	}

	out.Reset()
	if err := (Clone{}).Run(context.Background(),
		map[string]any{"repo": repo, "dir": "src", "branch": "main"}, ectx); err != nil {
		t.Fatalf("second clone: %v", err)
	}
	if got := branchOf(); got != "other" {
		t.Errorf("branch must stay %q, got %q", "other", got)
	}
	if !strings.Contains(out.String(), "already cloned") {
		t.Errorf("expected a skip message, got %q", out.String())
	}
}

func TestClone_EmptyDestinationIsCloned(t *testing.T) {
	repo := makeSourceRepo(t)
	root := t.TempDir()
	dest := filepath.Join(root, "src")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := (Clone{}).Run(context.Background(),
		map[string]any{"repo": repo, "dir": "src"}, newTestExecCtx(root, &out)); err != nil {
		t.Fatalf("clone into empty dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Fatalf("expected a git checkout: %v", err)
	}
}

func TestClone_NonEmptyNonGitDestinationErrors(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "src")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := (Clone{}).Run(context.Background(),
		map[string]any{"repo": "unused", "dir": "src"}, newTestExecCtx(root, &out))
	if err == nil {
		t.Fatal("expected an error for a non-empty non-git destination")
	}
	if !strings.Contains(err.Error(), "src") || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("error must name the path and the reason, got %v", err)
	}
}

func TestClone_DestinationIsFileErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "src"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := (Clone{}).Run(context.Background(),
		map[string]any{"repo": "unused", "dir": "src"}, newTestExecCtx(root, &out))
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected a not-a-directory error, got %v", err)
	}
}

func TestClone_EscapingDirRejected(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	for _, dir := range []string{"../outside", "/tmp/outside", "a/../../outside"} {
		err := (Clone{}).Run(context.Background(),
			map[string]any{"repo": "unused", "dir": dir}, newTestExecCtx(root, &out))
		if err == nil {
			t.Fatalf("dir %q must be rejected", dir)
		}
	}
}

// A symlinked path component must not become a write path out of the project.
func TestClone_SymlinkedDestinationRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	var out bytes.Buffer
	err := (Clone{}).Run(context.Background(),
		map[string]any{"repo": "unused", "dir": "link/src"}, newTestExecCtx(root, &out))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected a symlink rejection, got %v", err)
	}
}

func TestClone_MissingRequiredParamRejected(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	err := (Clone{}).Run(context.Background(), map[string]any{"dir": "src"}, newTestExecCtx(root, &out))
	if err == nil || !strings.Contains(err.Error(), "'repo'") {
		t.Fatalf("expected a missing-repo error, got %v", err)
	}
}

func TestClone_NoProjectRootRejected(t *testing.T) {
	var out bytes.Buffer
	err := (Clone{}).Run(context.Background(),
		map[string]any{"repo": "r", "dir": "src"}, newTestExecCtx("", &out))
	if err == nil || !strings.Contains(err.Error(), "project root") {
		t.Fatalf("expected a project-root error, got %v", err)
	}
}

// A failing git invocation must surface git's own stderr, not just an exit code.
func TestClone_GitFailureSurfacesStderr(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	var out bytes.Buffer
	err := (Clone{}).Run(context.Background(),
		map[string]any{"repo": filepath.Join(root, "no-such-repo"), "dir": "src"},
		newTestExecCtx(root, &out))
	if err == nil {
		t.Fatal("expected an error for a missing repository")
	}
	if !strings.Contains(err.Error(), "git clone") {
		t.Errorf("error should name the operation, got %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "repository") &&
		!strings.Contains(strings.ToLower(err.Error()), "does not exist") {
		t.Errorf("error should carry git's stderr, got %v", err)
	}
}

// --- non-interactive posture ---

func TestNonInteractiveGitEnv(t *testing.T) {
	env := nonInteractiveGitEnv([]string{"PATH=/bin", "GIT_ASKPASS=/usr/bin/gui-askpass"})
	joined := strings.Join(env, "\n")
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GIT_SSH_COMMAND=ssh -o BatchMode=yes"} {
		if !strings.Contains(joined, want) {
			t.Errorf("env missing %q:\n%s", want, joined)
		}
	}
	// os/exec keeps the last occurrence, so the emptying entry must come after
	// the inherited one.
	if strings.LastIndex(joined, "GIT_ASKPASS=\n") < strings.Index(joined, "GIT_ASKPASS=/usr/bin/gui-askpass") {
		t.Error("the empty GIT_ASKPASS must override the inherited value")
	}
}

func TestNonInteractiveGitEnv_RespectsExistingSSHCommand(t *testing.T) {
	env := nonInteractiveGitEnv([]string{"GIT_SSH_COMMAND=ssh -i /keys/id_ed25519"})
	for _, kv := range env {
		if kv == "GIT_SSH_COMMAND=ssh -o BatchMode=yes" {
			t.Fatal("an author-set GIT_SSH_COMMAND must not be overridden")
		}
	}
}

// An inherited GIT_SSH_COMMAND= carries no ssh command, and git would take the
// empty value literally rather than falling back to its default — so it counts
// as unset and gets the BatchMode default, unlike the askpass pair where empty
// is the meaningful state.
func TestNonInteractiveGitEnv_EmptySSHCommandTakesTheDefault(t *testing.T) {
	env := nonInteractiveGitEnv([]string{"GIT_SSH_COMMAND="})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GIT_SSH_COMMAND=ssh -o BatchMode=yes") {
		t.Errorf("an empty GIT_SSH_COMMAND must be defaulted:\n%s", joined)
	}
	// The default must come last — os/exec keeps the last occurrence.
	if strings.LastIndex(joined, "GIT_SSH_COMMAND=ssh -o BatchMode=yes") < strings.Index(joined, "GIT_SSH_COMMAND=\n") {
		t.Error("the default must override the inherited empty value")
	}
}
