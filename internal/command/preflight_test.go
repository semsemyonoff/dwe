package command

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/lifecycle"
	"devbox-cli/internal/preflight"
	"devbox-cli/internal/usercommands"

	"github.com/spf13/cobra"
)

// init replaces lifecycle.PreflightFunc with a no-op for the test binary so
// command-layer stop / run / restart tests don't pick up the host's docker /
// compose / git binaries and fail preflight. Tests that exercise preflight
// behavior explicitly install their own stub.
func init() {
	noop := func(_ context.Context, _ *config.DevboxConfig, _ *usercommands.Registry, _, _ string, _ bool, _ io.Writer) error {
		return nil
	}
	lifecycle.PreflightFunc = noop
	preflightRun = noop
}

// TestAddSkipPreflightFlag verifies the helper registers a bool flag.
func TestAddSkipPreflightFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	var skip bool
	addSkipPreflightFlag(cmd, &skip)
	f := cmd.Flag("skip-preflight")
	if f == nil {
		t.Fatal("--skip-preflight flag not registered")
	}
	if f.Value.Type() != "bool" {
		t.Errorf("flag type = %q, want bool", f.Value.Type())
	}
}

// TestPreflightError_ExitCode verifies the sentinel returns 1.
func TestPreflightError_ExitCode(t *testing.T) {
	e := &preflight.Error{}
	if got := e.ExitCode(); got != 1 {
		t.Errorf("ExitCode = %d, want 1", got)
	}
}

// TestDeployRunCmd_PreflightBlocksBeforeLock asserts that a preflight-blocking
// error aborts deploy WITHOUT creating the deploy.lock file. The plan calls
// this out as the load-bearing ordering constraint: preflight runs before
// lock.Acquire.
func TestDeployRunCmd_PreflightBlocksBeforeLock(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(cfgPath, []byte("project:\n  name: test\n  prefix: devbox\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prev := preflightRun
	preflightRun = func(_ context.Context, _ *config.DevboxConfig, _ *usercommands.Registry, _, _ string, _ bool, _ io.Writer) error {
		return &preflight.Error{}
	}
	t.Cleanup(func() { preflightRun = prev })

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	flags := &rootFlags{configPath: cfgPath}
	err := deployRunCmd(cmd, flags, "", false, false, true, false, false)
	if err == nil {
		t.Fatal("expected preflight failure to abort deploy")
	}
	if _, ok := err.(*preflight.Error); !ok {
		t.Errorf("err = %T, want *preflight.Error", err)
	}
	lockPath := filepath.Join(dir, ".devbox", "deploy", "deploy.lock")
	if _, statErr := os.Stat(lockPath); !os.IsNotExist(statErr) {
		t.Errorf("expected no lock file at %s (preflight ran before lock), stat err = %v", lockPath, statErr)
	}
}

// TestDeployRunCmd_SkipPreflightIsThreaded asserts the --skip-preflight bool
// is threaded into the preflight call. We short-circuit after preflight by
// returning a sentinel error from the stub so the test doesn't run real deploy.
func TestDeployRunCmd_SkipPreflightIsThreaded(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(cfgPath, []byte("project:\n  name: test\n  prefix: devbox\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prev := preflightRun
	var sawSkip bool
	preflightRun = func(_ context.Context, _ *config.DevboxConfig, _ *usercommands.Registry, _, _ string, skip bool, _ io.Writer) error {
		sawSkip = skip
		return &preflight.Error{} // short-circuit
	}
	t.Cleanup(func() { preflightRun = prev })

	cmd2 := &cobra.Command{}
	cmd2.SetContext(context.Background())
	flags := &rootFlags{configPath: cfgPath}
	_ = deployRunCmd(cmd2, flags, "", false, false, true, true, false)
	if !sawSkip {
		t.Error("preflight should have been invoked with skip=true")
	}
}

// TestPreflightSkipBypass verifies --skip-preflight prints the bypass notice
// and runs no validator even when validate.yml would have produced an error.
func TestPreflightSkipBypass(t *testing.T) {
	dir := t.TempDir()
	// Write a validate.yml that would fail (unknown builtin) — if preflight
	// were to run, it would emit a config.validate error diagnostic.
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := "checks:\n  - id: x\n    description: y\n    stages: [deploy]\n    type: builtin\n    cmd: not_a_builtin\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "validate.yml"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := preflight.Run(context.Background(), nil, nil, dir, "deploy", true, &buf)
	if err != nil {
		t.Fatalf("skip should return nil, got: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "preflight skipped") {
		t.Errorf("expected skip notice in output, got %q", got)
	}
}
