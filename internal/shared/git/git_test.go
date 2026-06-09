package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// --- stub runner ---

type stubCall struct {
	stdout string
	stderr string
	err    error
}

type stubRunner struct {
	calls   []stubCall
	callIdx int
}

func newStub(calls ...stubCall) *stubRunner {
	return &stubRunner{calls: calls}
}

func (s *stubRunner) Run(_ context.Context, _ string, args ...string) (string, string, error) {
	if s.callIdx >= len(s.calls) {
		panic("stubRunner: more calls than expected; args=" + strings.Join(args, " "))
	}
	c := s.calls[s.callIdx]
	s.callIdx++
	return c.stdout, c.stderr, c.err
}

// ok is a stub call that succeeds.
func ok(stdout string) stubCall { return stubCall{stdout: stdout} }

// fail is a stub call that returns an error.
func fail(stderr string) stubCall {
	return stubCall{stderr: stderr, err: errors.New("exit status 1")}
}

// --- Probe tests ---

func TestProbe_NotARepo(t *testing.T) {
	r := newStub(fail(""))
	s, err := probeWith("git", "/tmp/notarepo", false, r)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if s.IsRepo {
		t.Error("expected IsRepo=false for non-repo directory")
	}
}

func TestProbe_CleanUpToDate_NoFetch(t *testing.T) {
	r := newStub(
		ok("true"),        // rev-parse --is-inside-work-tree
		ok(""),            // git status --porcelain (clean)
		ok("main"),        // rev-parse HEAD abbrev-ref
		ok("origin/main"), // rev-parse @{u}
		ok("0\t0"),        // rev-list --left-right --count (fetch=false, still runs count)
	)
	s, err := probeWith("git", "/repo", false, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.IsRepo {
		t.Error("expected IsRepo=true")
	}
	if s.Dirty {
		t.Error("expected Dirty=false")
	}
	if s.Branch != "main" {
		t.Errorf("Branch=%q, want 'main'", s.Branch)
	}
	if !s.HasUpstream {
		t.Error("expected HasUpstream=true")
	}
	if s.FetchOK {
		t.Error("expected FetchOK=false (no fetch requested)")
	}
	if s.Behind != 0 || s.Ahead != 0 {
		t.Errorf("Behind=%d Ahead=%d, want 0,0", s.Behind, s.Ahead)
	}
}

func TestProbe_DirtyWorktree(t *testing.T) {
	r := newStub(
		ok("true"),        // is-inside-work-tree
		ok("M foo.go"),    // status --porcelain (dirty)
		ok("main"),        // rev-parse HEAD
		ok("origin/main"), // @{u}
		// no fetch (fetch=false), still runs rev-list
		ok("3\t1"), // rev-list counts
	)
	s, err := probeWith("git", "/repo", false, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.Dirty {
		t.Error("expected Dirty=true")
	}
}

func TestProbe_NoUpstream(t *testing.T) {
	r := newStub(
		ok("true"), // is-inside-work-tree
		ok(""),     // status (clean)
		ok("feat"), // branch
		fail(""),   // @{u} → no upstream configured
		// rev-list skipped because HasUpstream=false
	)
	s, err := probeWith("git", "/repo", false, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.HasUpstream {
		t.Error("expected HasUpstream=false")
	}
}

func TestProbe_FetchSuccess(t *testing.T) {
	r := newStub(
		ok("true"),        // is-inside-work-tree
		ok(""),            // clean
		ok("main"),        // branch
		ok("origin/main"), // upstream
		ok(""),            // git fetch --quiet origin → success
		ok("2\t0"),        // rev-list: behind 2, ahead 0
	)
	s, err := probeWith("git", "/repo", true, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.FetchOK {
		t.Error("expected FetchOK=true")
	}
	if s.Behind != 2 {
		t.Errorf("Behind=%d, want 2", s.Behind)
	}
}

func TestProbe_FetchFailure(t *testing.T) {
	r := newStub(
		ok("true"),        // is-inside-work-tree
		ok(""),            // clean
		ok("main"),        // branch
		ok("origin/main"), // upstream
		fail("fatal: could not read from remote repository"),
	)
	s, err := probeWith("git", "/repo", true, r)
	if err != nil {
		t.Fatalf("probe should not return error on fetch failure: %v", err)
	}
	if s.FetchOK {
		t.Error("expected FetchOK=false after fetch failure")
	}
	if s.FetchErr == "" {
		t.Error("expected FetchErr to be set")
	}
}

func TestProbe_FetchNotAttemptedWhenModeOff(t *testing.T) {
	// fetch=false: fetch call must NOT happen.
	r := newStub(
		ok("true"),        // is-inside-work-tree
		ok(""),            // clean
		ok("main"),        // branch
		ok("origin/main"), // upstream
		ok("0\t0"),        // rev-list (no fetch)
	)
	s, err := probeWith("git", "/repo", false, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.FetchOK {
		t.Error("expected FetchOK=false when fetch not requested")
	}
	if s.FetchErr != "" {
		t.Errorf("expected FetchErr empty, got %q", s.FetchErr)
	}
}

// --- PullFFOnly tests ---

func TestPullFFOnly_Moved(t *testing.T) {
	r := newStub(
		ok("abc123"), // rev-parse HEAD before
		ok(""),       // git pull --ff-only
		ok("def456"), // rev-parse HEAD after
	)
	moved, err := pullFFOnlyWith("git", "/repo", r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !moved {
		t.Error("expected moved=true when HEAD changed")
	}
}

func TestPullFFOnly_NotMoved(t *testing.T) {
	r := newStub(
		ok("abc123"), // before
		ok(""),       // pull
		ok("abc123"), // after (same)
	)
	moved, err := pullFFOnlyWith("git", "/repo", r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if moved {
		t.Error("expected moved=false when HEAD unchanged")
	}
}

func TestPullFFOnly_Error(t *testing.T) {
	r := newStub(
		ok("abc123"),
		fail("fatal: not possible to fast-forward, aborting"),
	)
	_, err := pullFFOnlyWith("git", "/repo", r)
	if err == nil {
		t.Error("expected error on pull failure")
	}
}

// --- trace echo tests (exercise the real execRunner, which holds the emit) ---

// captureTrace points the trace sink at a buffer at the given level for the
// duration of the test, restoring LevelOff afterwards.
func captureTrace(t *testing.T, lvl trace.Level) *strings.Builder {
	t.Helper()
	buf := &strings.Builder{}
	trace.Configure(buf, lvl)
	t.Cleanup(func() { trace.Configure(nil, trace.LevelOff) })
	return buf
}

func writeFakeGit(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("writing fake git: %v", err)
	}
	return path
}

func TestExecRunner_EchoesAtVerbose(t *testing.T) {
	fakeGit := writeFakeGit(t, "exit 0")
	buf := captureTrace(t, trace.LevelVerbose)

	_, _, err := (execRunner{}).Run(context.Background(), t.TempDir(), fakeGit, "status", "--porcelain")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := "$ " + trace.FormatCommand([]string{fakeGit, "status", "--porcelain"})
	if got := strings.TrimSpace(buf.String()); got != want {
		t.Fatalf("echo = %q, want %q", got, want)
	}
}

func TestExecRunner_EchoesEvenOnFailure(t *testing.T) {
	fakeGit := writeFakeGit(t, "exit 1")
	buf := captureTrace(t, trace.LevelVerbose)

	_, _, err := (execRunner{}).Run(context.Background(), "/repo", fakeGit, "rev-parse", "HEAD")
	if err == nil {
		t.Fatal("expected error from failing stub")
	}
	if !strings.Contains(buf.String(), "$ ") {
		t.Fatalf("expected command echo even on failure, got %q", buf.String())
	}
}

func TestExecRunner_SilentAtLevelOff(t *testing.T) {
	fakeGit := writeFakeGit(t, "exit 0")
	buf := captureTrace(t, trace.LevelOff)

	if _, _, err := (execRunner{}).Run(context.Background(), t.TempDir(), fakeGit, "status"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no output at LevelOff, got %q", buf.String())
	}
}
