package containers

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// dualScopeStopCtx builds an ExecContext whose canonical (docker.yml
// project_name) and legacy (FullName) scopes differ: canonical "dwe_tbm",
// legacy "dwe-tbm" → candidate container names "dwe_tbm-<t>" and "dwe-tbm-<t>".
func dualScopeStopCtx(buf *bytes.Buffer) spec.ExecContext {
	cfg := &config.DweConfig{}
	cfg.Project.Name = "tbm"
	cfg.Project.Prefix = "dwe"
	return spec.ExecContext{
		Config:       cfg,
		DockerConfig: &config.DockerConfig{ProjectName: "dwe_tbm"},
		Output:       render.NewWriter(buf),
	}
}

// TestDaemonStop_Run_DrainsBothScopes verifies that when both the canonical and
// the legacy container exist (the duplicate-spawn recovery case), stop drains
// BOTH rather than returning after the first — otherwise .restart would leave
// the legacy worker alive.
func TestDaemonStop_Run_DrainsBothScopes(t *testing.T) {
	orig := daemonStopFn
	t.Cleanup(func() { daemonStopFn = orig })
	var stopped []string
	daemonStopFn = func(_ context.Context, _ *docker.Compose, name string, _ int) error {
		stopped = append(stopped, name)
		return nil
	}

	buf := &bytes.Buffer{}
	err := (DaemonStop{}).Run(context.Background(), map[string]any{"container_template": "queue"}, dualScopeStopCtx(buf))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	want := []string{"dwe_tbm-queue", "dwe-tbm-queue"}
	if !equalStrings(stopped, want) {
		t.Errorf("stopped = %v, want %v (must drain both scopes)", stopped, want)
	}
}

// TestDaemonStop_Run_OnlyLegacyExists verifies the canonical scope being absent
// does not abort: the legacy container is still stopped.
func TestDaemonStop_Run_OnlyLegacyExists(t *testing.T) {
	orig := daemonStopFn
	t.Cleanup(func() { daemonStopFn = orig })
	var stopped []string
	daemonStopFn = func(_ context.Context, _ *docker.Compose, name string, _ int) error {
		if name == "dwe_tbm-queue" {
			return errDaemonNoSuchContainer
		}
		stopped = append(stopped, name)
		return nil
	}

	buf := &bytes.Buffer{}
	if err := (DaemonStop{}).Run(context.Background(), map[string]any{"container_template": "queue"}, dualScopeStopCtx(buf)); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if want := []string{"dwe-tbm-queue"}; !equalStrings(stopped, want) {
		t.Errorf("stopped = %v, want %v", stopped, want)
	}
	if !strings.Contains(buf.String(), "✓ daemon stopped: dwe-tbm-queue") {
		t.Errorf("missing stopped line; got %q", buf.String())
	}
}

// TestDaemonStop_Run_NoneExist reports "no daemon to stop" only when no scope
// had a container.
func TestDaemonStop_Run_NoneExist(t *testing.T) {
	orig := daemonStopFn
	t.Cleanup(func() { daemonStopFn = orig })
	daemonStopFn = func(_ context.Context, _ *docker.Compose, _ string, _ int) error {
		return errDaemonNoSuchContainer
	}

	buf := &bytes.Buffer{}
	if err := (DaemonStop{}).Run(context.Background(), map[string]any{"container_template": "queue"}, dualScopeStopCtx(buf)); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(buf.String(), "no daemon to stop: dwe_tbm-queue") {
		t.Errorf("expected no-daemon line with canonical name; got %q", buf.String())
	}
}

// TestDaemonStop_Run_HardErrorPropagates verifies a non-missing docker error is
// fatal (not swallowed by the drain loop).
func TestDaemonStop_Run_HardErrorPropagates(t *testing.T) {
	orig := daemonStopFn
	t.Cleanup(func() { daemonStopFn = orig })
	hardErr := errors.New("docker stop: permission denied")
	daemonStopFn = func(_ context.Context, _ *docker.Compose, _ string, _ int) error {
		return hardErr
	}

	buf := &bytes.Buffer{}
	err := (DaemonStop{}).Run(context.Background(), map[string]any{"container_template": "queue"}, dualScopeStopCtx(buf))
	if !errors.Is(err, hardErr) {
		t.Errorf("expected hard error to propagate, got %v", err)
	}
}
