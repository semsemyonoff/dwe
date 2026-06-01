package lifecycle

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/preflight"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/git"
)

// TestRunRun_PreflightBlocksBeforeGitProbe asserts a preflight error aborts
// RunRun BEFORE the git probe is called. With the test-binary default
// PreflightFunc set to a no-op (helpers_test.go), we install a failing stub
// and assert GitProbeFunc is never invoked.
func TestRunRun_PreflightBlocksBeforeGitProbe(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLifecycleYML(t, workspaceDir, "ready")

	prevPF := PreflightFunc
	prevGP := GitProbeFunc
	t.Cleanup(func() { PreflightFunc = prevPF; GitProbeFunc = prevGP })

	gitProbeCalls := 0
	GitProbeFunc = func(_, _ string, _ bool) (git.Status, error) {
		gitProbeCalls++
		return git.Status{}, nil
	}
	PreflightFunc = func(_ context.Context, _ *config.DweConfig, _ *usercommands.Registry, _, _ string, _ bool, _ io.Writer) error {
		return &preflight.Error{}
	}

	err := RunRun(RunContext{ConfigPath: cfgPath, SkipNotify: true})
	if err == nil {
		t.Fatal("expected preflight error to abort RunRun")
	}
	if !errors.As(err, new(*preflight.Error)) {
		t.Errorf("err = %v, want *preflight.Error", err)
	}
	if gitProbeCalls != 0 {
		t.Errorf("GitProbeFunc called %d times; should never run when preflight blocks", gitProbeCalls)
	}
}

// TestRunStop_PreflightBlocksBeforePhases asserts a preflight error aborts
// RunStop BEFORE any stop phase work happens. Since the test stub for the
// stop run phase is in helpers, we just assert the preflight error surfaces.
func TestRunStop_PreflightBlocksBeforePhases(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	prev := PreflightFunc
	t.Cleanup(func() { PreflightFunc = prev })
	PreflightFunc = func(_ context.Context, _ *config.DweConfig, _ *usercommands.Registry, _, _ string, _ bool, _ io.Writer) error {
		return &preflight.Error{}
	}

	err := RunStop(StopContext{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected preflight error to abort RunStop")
	}
	if !errors.As(err, new(*preflight.Error)) {
		t.Errorf("err = %v, want *preflight.Error", err)
	}
}

// TestRunRun_SkipPreflightThreaded confirms ctx.SkipPreflight propagates.
func TestRunRun_SkipPreflightThreaded(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLifecycleYML(t, workspaceDir, "ready")

	prev := PreflightFunc
	t.Cleanup(func() { PreflightFunc = prev })

	var sawSkip bool
	PreflightFunc = func(_ context.Context, _ *config.DweConfig, _ *usercommands.Registry, _, _ string, skip bool, _ io.Writer) error {
		sawSkip = skip
		return errors.New("short-circuit after preflight observation")
	}

	_ = RunRun(RunContext{ConfigPath: cfgPath, SkipNotify: true, SkipPreflight: true})
	if !sawSkip {
		t.Error("RunContext.SkipPreflight should reach PreflightFunc as skip=true")
	}
}

// TestRunRestart_PropagatesSkipPreflight ensures restart forwards
// SkipPreflight to both legs (stop then run).
func TestRunRestart_PropagatesSkipPreflight(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLifecycleYML(t, workspaceDir, "ready")

	prev := PreflightFunc
	t.Cleanup(func() { PreflightFunc = prev })

	var calls []bool
	PreflightFunc = func(_ context.Context, _ *config.DweConfig, _ *usercommands.Registry, _, _ string, skip bool, _ io.Writer) error {
		calls = append(calls, skip)
		return errors.New("short-circuit")
	}

	_ = RunRestart(RunContext{ConfigPath: cfgPath, SkipPreflight: true, SkipNotify: true})
	if len(calls) == 0 {
		t.Fatal("preflight was never invoked")
	}
	// At minimum the stop leg's preflight should have observed skip=true.
	if !calls[0] {
		t.Errorf("first preflight call (stop leg) saw skip=%v, want true", calls[0])
	}
}
