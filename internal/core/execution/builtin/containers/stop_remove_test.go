package containers

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/semsemyonoff/devbox/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/shared/render"
)

func newDockerStopRemoveCtx(cfg *config.DevboxConfig) (spec.ExecContext, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return spec.ExecContext{
		Config: cfg,
		Output: render.NewWriter(buf),
	}, buf
}

// swapDockerSeams replaces both stop/remove seams and registers cleanup.
func swapDockerSeams(t *testing.T, stop func(context.Context, string, string, int) error, removeFn func(context.Context, string, string) error) {
	t.Helper()
	prevStop := stopContainerFn
	prevRm := removeContainerFn
	t.Cleanup(func() {
		stopContainerFn = prevStop
		removeContainerFn = prevRm
	})
	if stop != nil {
		stopContainerFn = stop
	}
	if removeFn != nil {
		removeContainerFn = removeFn
	}
}

func TestDockerStopRemoveContainer_Validate(t *testing.T) {
	b := StopRemoveContainer{}

	if err := b.Validate(nil); err == nil {
		t.Fatal("expected error for nil with")
	}
	if err := b.Validate(map[string]any{}); err == nil {
		t.Fatal("expected error for missing container_template")
	}
	if err := b.Validate(map[string]any{"container_template": ""}); err == nil {
		t.Fatal("expected error for empty container_template")
	}
	if err := b.Validate(map[string]any{"container_template": "app-pg"}); err != nil {
		t.Fatalf("unexpected error for valid container_template: %v", err)
	}
}

func TestDockerStopRemoveContainer_Describe(t *testing.T) {
	b := StopRemoveContainer{}
	got := b.Describe(map[string]any{"container_template": "app-pg"})
	if !strings.Contains(got, "stop+rm container: app-pg") {
		t.Errorf("Describe = %q, want substring 'stop+rm container: app-pg'", got)
	}
	got = b.Describe(map[string]any{})
	if !strings.Contains(got, "stop+rm container: ?") {
		t.Errorf("Describe (empty) = %q, want substring 'stop+rm container: ?'", got)
	}
}

func TestDockerStopRemoveContainer_Run_HappyPath(t *testing.T) {
	var stopCalls, rmCalls []string
	var stopTimeouts []int
	swapDockerSeams(t,
		func(_ context.Context, _, name string, timeout int) error {
			stopCalls = append(stopCalls, name)
			stopTimeouts = append(stopTimeouts, timeout)
			return nil
		},
		nil,
	)
	prevRm := removeContainerFn
	t.Cleanup(func() { removeContainerFn = prevRm })
	removeContainerFn = func(_ context.Context, _, name string) error {
		rmCalls = append(rmCalls, name)
		return nil
	}

	cfg := &config.DevboxConfig{}
	cfg.Project.Name = "tbm"
	cfg.Project.Prefix = "devbox"

	ectx, buf := newDockerStopRemoveCtx(cfg)
	err := StopRemoveContainer{}.Run(
		context.Background(),
		map[string]any{"container_template": "app-postgres", "stop_timeout": "5s"},
		ectx,
	)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	want := []string{"devbox-tbm-app-postgres"}
	if !equalStrings(stopCalls, want) {
		t.Errorf("stop calls = %v, want %v", stopCalls, want)
	}
	if !equalStrings(rmCalls, want) {
		t.Errorf("rm calls = %v, want %v", rmCalls, want)
	}
	if len(stopTimeouts) != 1 || stopTimeouts[0] != 5 {
		t.Errorf("stop timeouts = %v, want [5]", stopTimeouts)
	}
	out := buf.String()
	if !strings.Contains(out, "✓ container stopped: devbox-tbm-app-postgres") {
		t.Errorf("missing stop success line; got %q", out)
	}
	if !strings.Contains(out, "✓ container removed: devbox-tbm-app-postgres") {
		t.Errorf("missing remove success line; got %q", out)
	}
}

func TestDockerStopRemoveContainer_Run_DefaultTimeout(t *testing.T) {
	var capturedTimeout int
	swapDockerSeams(t,
		func(_ context.Context, _, _ string, timeout int) error {
			capturedTimeout = timeout
			return nil
		},
		func(_ context.Context, _, _ string) error { return nil },
	)

	cfg := &config.DevboxConfig{}
	cfg.Project.Name = "demo"
	ectx, _ := newDockerStopRemoveCtx(cfg)
	err := StopRemoveContainer{}.Run(
		context.Background(),
		map[string]any{"container_template": "app"},
		ectx,
	)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if capturedTimeout != 10 {
		t.Errorf("default timeout = %d, want 10", capturedTimeout)
	}
}

func TestDockerStopRemoveContainer_Run_StopFailurePropagatesAndSkipsRm(t *testing.T) {
	stopErr := errors.New("docker stop: exit 1: connection refused")
	rmInvoked := false
	swapDockerSeams(t,
		func(_ context.Context, _, _ string, _ int) error { return stopErr },
		func(_ context.Context, _, _ string) error {
			rmInvoked = true
			return nil
		},
	)

	cfg := &config.DevboxConfig{}
	cfg.Project.Name = "tbm"
	ectx, buf := newDockerStopRemoveCtx(cfg)
	err := StopRemoveContainer{}.Run(
		context.Background(),
		map[string]any{"container_template": "app"},
		ectx,
	)
	if err == nil {
		t.Fatal("expected error from stop failure, got nil")
	}
	if !strings.Contains(err.Error(), `stop container "tbm-app":`) {
		t.Errorf("error = %q, want prefix `stop container \"tbm-app\":`", err.Error())
	}
	if !errors.Is(err, stopErr) {
		t.Errorf("error chain does not wrap stopErr: %v", err)
	}
	if rmInvoked {
		t.Error("removeContainerFn was invoked after stop failure; must be skipped")
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output on stop failure; got %q", buf.String())
	}
}

func TestDockerStopRemoveContainer_Run_MissingContainerIdempotent(t *testing.T) {
	// Both helpers swallow "No such container" and return nil, so the builtin
	// should produce both success lines exactly as in the happy path.
	swapDockerSeams(t,
		func(_ context.Context, _, _ string, _ int) error { return nil },
		func(_ context.Context, _, _ string) error { return nil },
	)
	cfg := &config.DevboxConfig{}
	cfg.Project.Name = "tbm"
	ectx, buf := newDockerStopRemoveCtx(cfg)
	err := StopRemoveContainer{}.Run(
		context.Background(),
		map[string]any{"container_template": "ghost"},
		ectx,
	)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "✓ container stopped: tbm-ghost") {
		t.Errorf("missing stop success line; got %q", out)
	}
	if !strings.Contains(out, "✓ container removed: tbm-ghost") {
		t.Errorf("missing remove success line; got %q", out)
	}
}

func TestDockerStopRemoveContainer_Run_RmFailurePropagates(t *testing.T) {
	rmErr := errors.New("docker rm: exit 1: in use")
	swapDockerSeams(t,
		func(_ context.Context, _, _ string, _ int) error { return nil },
		func(_ context.Context, _, _ string) error { return rmErr },
	)
	cfg := &config.DevboxConfig{}
	cfg.Project.Name = "tbm"
	ectx, _ := newDockerStopRemoveCtx(cfg)
	err := StopRemoveContainer{}.Run(
		context.Background(),
		map[string]any{"container_template": "app"},
		ectx,
	)
	if err == nil {
		t.Fatal("expected error from rm failure, got nil")
	}
	if !strings.Contains(err.Error(), `remove container "tbm-app":`) {
		t.Errorf("error = %q, want prefix `remove container \"tbm-app\":`", err.Error())
	}
	if !errors.Is(err, rmErr) {
		t.Errorf("error chain does not wrap rmErr: %v", err)
	}
}

func TestDockerStopRemoveContainer_Run_NoProjectPrefix(t *testing.T) {
	// Without project.prefix, FullName returns just project.Name.
	var stopName string
	swapDockerSeams(t,
		func(_ context.Context, _, name string, _ int) error {
			stopName = name
			return nil
		},
		func(_ context.Context, _, _ string) error { return nil },
	)
	cfg := &config.DevboxConfig{}
	cfg.Project.Name = "demo"
	ectx, _ := newDockerStopRemoveCtx(cfg)
	err := StopRemoveContainer{}.Run(
		context.Background(),
		map[string]any{"container_template": "app"},
		ectx,
	)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stopName != "demo-app" {
		t.Errorf("stop container name = %q, want demo-app", stopName)
	}
}

func TestDockerStopRemoveContainer_Run_NilConfig(t *testing.T) {
	ectx := spec.ExecContext{Output: render.NewWriter(&bytes.Buffer{})}
	err := StopRemoveContainer{}.Run(
		context.Background(),
		map[string]any{"container_template": "app"},
		ectx,
	)
	if err == nil {
		t.Fatal("expected error when Config is nil, got nil")
	}
	if !strings.Contains(err.Error(), "config not available") {
		t.Errorf("error = %q, want 'config not available'", err.Error())
	}
}

func TestDockerStopRemoveContainer_Run_InvalidContainerName(t *testing.T) {
	cfg := &config.DevboxConfig{}
	cfg.Project.Name = "demo"
	ectx, _ := newDockerStopRemoveCtx(cfg)
	err := StopRemoveContainer{}.Run(
		context.Background(),
		map[string]any{"container_template": "bad/name"},
		ectx,
	)
	if err == nil {
		t.Fatal("expected error for invalid container name, got nil")
	}
}

func equalStrings(a, b []string) bool {
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
