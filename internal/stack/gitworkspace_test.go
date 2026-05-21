package stack

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/goleak"

	"devbox-cli/internal/config"
)

func TestParsePorcelainV2_Clean(t *testing.T) {
	out := []byte("# branch.oid 1234567890abcdef\n# branch.head main\n# branch.ab +0 -0\n")
	branch, oid, ahead, behind, dirty, err := parsePorcelainV2(out)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if branch != "main" || oid != "1234567890abcdef" || ahead != 0 || behind != 0 || dirty {
		t.Fatalf("got branch=%q oid=%q ahead=%d behind=%d dirty=%v",
			branch, oid, ahead, behind, dirty)
	}
}

func TestParsePorcelainV2_Dirty(t *testing.T) {
	out := []byte("# branch.oid deadbeefcafebabe\n# branch.head feature/x\n# branch.ab +2 -1\n1 .M N... 100644 100644 100644 abc abc file.go\n")
	branch, oid, ahead, behind, dirty, err := parsePorcelainV2(out)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if branch != "feature/x" || oid != "deadbeefcafebabe" || ahead != 2 || behind != 1 || !dirty {
		t.Fatalf("got branch=%q oid=%q ahead=%d behind=%d dirty=%v",
			branch, oid, ahead, behind, dirty)
	}
}

func TestParsePorcelainV2_Detached(t *testing.T) {
	out := []byte("# branch.oid abcdef0123456789\n# branch.head (detached)\n")
	branch, _, _, _, _, err := parsePorcelainV2(out)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if branch != "detached" {
		t.Fatalf("got branch=%q", branch)
	}
}

func TestParsePorcelainV2_InitialOID(t *testing.T) {
	out := []byte("# branch.oid (initial)\n# branch.head main\n")
	_, oid, _, _, _, err := parsePorcelainV2(out)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if oid != "" {
		t.Fatalf("expected empty oid for initial commit, got %q", oid)
	}
}

func TestParsePorcelainV2_MalformedAB(t *testing.T) {
	out := []byte("# branch.head main\n# branch.ab plus minus\n")
	_, _, _, _, _, err := parsePorcelainV2(out)
	if err == nil {
		t.Fatal("expected parse error for malformed branch.ab")
	}
}

// gitInit runs `git init -b <branch> <dir>` and stages initial config so that
// subsequent commits don't depend on a global git identity.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	mustRun(t, dir, "git", "init", "-b", "main")
	mustRun(t, dir, "git", "config", "user.email", "test@example.com")
	mustRun(t, dir, "git", "config", "user.name", "test")
	mustRun(t, dir, "git", "config", "commit.gpgsign", "false")
}

func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v in %s: %v\n%s", name, args, dir, err, out)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available on PATH")
	}
}

func TestCollectGitWorkspace_BlankWhenNoOwnGit(t *testing.T) {
	defer goleak.VerifyNone(t)

	root := t.TempDir()
	svcDir := filepath.Join(root, "svc")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Outer parent IS a git repo — confirms our boundary check prevents
	// `git -C` from walking up to it.
	requireGit(t)
	gitInit(t, root)

	var calls atomic.Int32
	prev := gitShellOutFn
	gitShellOutFn = func(ctx context.Context, dir string) ([]byte, error) {
		calls.Add(1)
		return prev(ctx, dir)
	}
	t.Cleanup(func() { gitShellOutFn = prev })

	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"app": {Dir: svcDir},
		},
	}
	rows := CollectGitWorkspace(t.Context(), cfg)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Err != nil {
		t.Fatalf("expected nil Err, got %v", r.Err)
	}
	if r.Branch != "" || r.SHA != "" || r.AheadBehind != "" || r.Dirty {
		t.Fatalf("expected blank row, got %+v", r)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("expected no shellouts (boundary check), got %d", got)
	}
}

func TestCollectGitWorkspace_DirMissingErrors(t *testing.T) {
	defer goleak.VerifyNone(t)

	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"ghost": {Dir: "/definitely/does/not/exist/anywhere"},
		},
	}
	rows := CollectGitWorkspace(t.Context(), cfg)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].Err == nil {
		t.Fatal("expected Err for missing dir")
	}
}

func TestCollectGitWorkspace_SkipsServicesWithoutDir(t *testing.T) {
	defer goleak.VerifyNone(t)

	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"with":    {Dir: "/tmp"},
			"without": {Dir: ""},
		},
	}
	rows := CollectGitWorkspace(t.Context(), cfg)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].Service != "with" {
		t.Fatalf("got service %q", rows[0].Service)
	}
}

func TestCollectGitWorkspace_RealRepoClean(t *testing.T) {
	requireGit(t)
	defer goleak.VerifyNone(t)

	root := t.TempDir()
	svcDir := filepath.Join(root, "svc")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, svcDir)
	if err := os.WriteFile(filepath.Join(svcDir, "README"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, svcDir, "git", "add", ".")
	mustRun(t, svcDir, "git", "commit", "-m", "init")

	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"app": {Dir: svcDir},
		},
	}
	rows := CollectGitWorkspace(t.Context(), cfg)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Err != nil {
		t.Fatalf("unexpected Err: %v", r.Err)
	}
	if r.Branch != "main" {
		t.Fatalf("got branch=%q", r.Branch)
	}
	if len(r.SHA) != 8 {
		t.Fatalf("got sha=%q (want 8 chars)", r.SHA)
	}
	if r.Dirty {
		t.Fatal("expected clean")
	}
	if r.AheadBehind != "+0/-0" {
		t.Fatalf("got ab=%q", r.AheadBehind)
	}
}

func TestCollectGitWorkspace_RealRepoDirty(t *testing.T) {
	requireGit(t)
	defer goleak.VerifyNone(t)

	root := t.TempDir()
	svcDir := filepath.Join(root, "svc")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, svcDir)
	if err := os.WriteFile(filepath.Join(svcDir, "README"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, svcDir, "git", "add", ".")
	mustRun(t, svcDir, "git", "commit", "-m", "init")
	// Untracked file → working tree dirty.
	if err := os.WriteFile(filepath.Join(svcDir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"app": {Dir: svcDir},
		},
	}
	rows := CollectGitWorkspace(t.Context(), cfg)
	if !rows[0].Dirty {
		t.Fatal("expected dirty")
	}
}

func TestCollectGitWorkspace_ShelloutFailure(t *testing.T) {
	defer goleak.VerifyNone(t)

	root := t.TempDir()
	svcDir := filepath.Join(root, "svc")
	if err := os.MkdirAll(filepath.Join(svcDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	prev := gitShellOutFn
	gitShellOutFn = func(ctx context.Context, dir string) ([]byte, error) {
		return nil, errors.New("synthetic failure")
	}
	t.Cleanup(func() { gitShellOutFn = prev })

	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"app": {Dir: svcDir},
		},
	}
	rows := CollectGitWorkspace(t.Context(), cfg)
	if rows[0].Err == nil {
		t.Fatal("expected Err for shellout failure")
	}
}

func TestCollectGitWorkspace_NilCfg(t *testing.T) {
	defer goleak.VerifyNone(t)
	if rows := CollectGitWorkspace(t.Context(), nil); rows != nil {
		t.Fatalf("want nil, got %v", rows)
	}
}

func TestCollectGitWorkspace_OrderingAlphabetical(t *testing.T) {
	defer goleak.VerifyNone(t)

	root := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"c": {Dir: filepath.Join(root, "c")},
			"a": {Dir: filepath.Join(root, "a")},
			"b": {Dir: filepath.Join(root, "b")},
		},
	}
	rows := CollectGitWorkspace(t.Context(), cfg)
	want := []string{"a", "b", "c"}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	for i, r := range rows {
		if r.Service != want[i] {
			t.Fatalf("row %d: got %q want %q", i, r.Service, want[i])
		}
	}
}

// Mitigate Windows-specific path noise (collector currently runs on Unix-ish
// hosts only — kept for future-proofing).
func TestParsePorcelainV2_LineSeparators(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix line separators only")
	}
	out := []byte(strings.Join([]string{
		"# branch.oid 0123456789abcdef",
		"# branch.head main",
		"",
		"# branch.ab +1 -2",
		"",
	}, "\n"))
	branch, _, ahead, behind, _, err := parsePorcelainV2(out)
	if err != nil || branch != "main" || ahead != 1 || behind != 2 {
		t.Fatalf("got branch=%q ahead=%d behind=%d err=%v", branch, ahead, behind, err)
	}
}
