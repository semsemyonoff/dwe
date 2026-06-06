package containers

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// --- RemoveProjectVolumes ---

// swapVolumeSeams replaces the list/remove seams and registers cleanup.
func swapVolumeSeams(t *testing.T, list func(context.Context, string) ([]string, error), remove func(context.Context, string, string) error) {
	t.Helper()
	prevList := listVolumesFn
	prevRemove := removeVolumeFn
	t.Cleanup(func() {
		listVolumesFn = prevList
		removeVolumeFn = prevRemove
	})
	if list != nil {
		listVolumesFn = list
	}
	if remove != nil {
		removeVolumeFn = remove
	}
}

func TestDockerRemoveVolumes_Validate(t *testing.T) {
	b := RemoveProjectVolumes{}
	if err := b.Validate(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := b.Validate(map[string]any{"extra": "ignored"}); err != nil {
		t.Fatalf("unexpected error with extra params: %v", err)
	}
}

func TestDockerRemoveVolumes_Describe(t *testing.T) {
	b := RemoveProjectVolumes{}
	desc := b.Describe(nil)
	if !strings.Contains(desc, "docker_remove_project_volumes") {
		t.Errorf("expected builtin name in describe, got %q", desc)
	}
}

// TestDockerRemoveVolumes_Run_DefaultProjectName locks in the fix for `dwe
// reset` removing volumes on a project that has no workspace/docker.yml (or one
// without project_name): the prefix must fall back to the default
// "<prefix>-<name>" via ResolveComposeProjectName instead of aborting. The temp
// ProjectRoot has no docker.yml, so resolution hits the os.ErrNotExist →
// FullName() branch.
func TestDockerRemoveVolumes_Run_DefaultProjectName(t *testing.T) {
	var removed []string
	swapVolumeSeams(t,
		func(context.Context, string) ([]string, error) {
			return []string{
				"dwe-laravel_pgdata",   // match
				"dwe-laravel_redis",    // match
				"dwe-laravel-app",      // no underscore separator → no match
				"other-project_pgdata", // different project → no match
				"",                     // blank (defensive)
			}, nil
		},
		func(_ context.Context, _ string, vol string) error {
			removed = append(removed, vol)
			return nil
		},
	)

	cfg := &config.DweConfig{}
	cfg.Project.Prefix = "dwe"
	cfg.Project.Name = "laravel"

	buf := &bytes.Buffer{}
	ectx := spec.ExecContext{Config: cfg, ProjectRoot: t.TempDir(), Output: render.NewWriter(buf)}

	if err := (RemoveProjectVolumes{}).Run(context.Background(), nil, ectx); err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	want := []string{"dwe-laravel_pgdata", "dwe-laravel_redis"}
	if !equalStrings(removed, want) {
		t.Errorf("removed = %v, want %v", removed, want)
	}
	if !strings.Contains(buf.String(), `removing 2 volume(s) with prefix "dwe-laravel_"`) {
		t.Errorf("missing/incorrect summary line; got %q", buf.String())
	}
}

// TestDockerRemoveVolumes_Run_NoMatches verifies the idempotent no-op path:
// when no volume carries the project prefix, nothing is removed.
func TestDockerRemoveVolumes_Run_NoMatches(t *testing.T) {
	removeCalled := false
	swapVolumeSeams(t,
		func(context.Context, string) ([]string, error) {
			return []string{"unrelated_a", "unrelated_b"}, nil
		},
		func(context.Context, string, string) error {
			removeCalled = true
			return nil
		},
	)

	cfg := &config.DweConfig{}
	cfg.Project.Name = "demo"
	buf := &bytes.Buffer{}
	ectx := spec.ExecContext{Config: cfg, Output: render.NewWriter(buf)}

	if err := (RemoveProjectVolumes{}).Run(context.Background(), nil, ectx); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if removeCalled {
		t.Error("removeVolumeFn was invoked despite no matching volumes")
	}
	if !strings.Contains(buf.String(), `no volumes found with prefix "demo_"`) {
		t.Errorf("missing no-volumes line; got %q", buf.String())
	}
}

// TestDockerRemoveVolumes_Run_PerVolumeBestEffort verifies that a single volume
// that cannot be removed (e.g. still in use) is reported and skipped, while the
// remaining volumes are still removed and the step succeeds — so a stuck volume
// never aborts the surrounding reset. This is why the default reset pipeline
// does NOT need step-level continue_on_error.
func TestDockerRemoveVolumes_Run_PerVolumeBestEffort(t *testing.T) {
	rmErr := errors.New("volume is in use")
	var attempted []string
	swapVolumeSeams(t,
		func(context.Context, string) ([]string, error) {
			return []string{"demo_a", "demo_b", "demo_c"}, nil
		},
		func(_ context.Context, _ string, vol string) error {
			attempted = append(attempted, vol)
			if vol == "demo_b" {
				return rmErr
			}
			return nil
		},
	)

	cfg := &config.DweConfig{}
	cfg.Project.Name = "demo"
	buf := &bytes.Buffer{}
	ectx := spec.ExecContext{Config: cfg, Output: render.NewWriter(buf)}

	if err := (RemoveProjectVolumes{}).Run(context.Background(), nil, ectx); err != nil {
		t.Fatalf("per-volume rm failure must be best-effort (nil), got: %v", err)
	}
	if want := []string{"demo_a", "demo_b", "demo_c"}; !equalStrings(attempted, want) {
		t.Errorf("attempted = %v, want all %v (must continue past the failure)", attempted, want)
	}
	out := buf.String()
	if !strings.Contains(out, `could not remove volume "demo_b"`) {
		t.Errorf("missing failure warning for demo_b; got %q", out)
	}
	if !strings.Contains(out, "left in place") || !strings.Contains(out, "demo_b") {
		t.Errorf("missing left-in-place summary; got %q", out)
	}
	if !strings.Contains(out, "removed 2 volume(s)") {
		t.Errorf("missing removed-count summary; got %q", out)
	}
}

// TestDockerRemoveVolumes_Run_AbortWhenUnresolvable keeps the safety abort: when
// both docker.yml project_name AND FullName() are empty, removal must not
// proceed (a blank prefix would match — and delete — every volume on the host).
func TestDockerRemoveVolumes_Run_AbortWhenUnresolvable(t *testing.T) {
	swapVolumeSeams(t,
		func(context.Context, string) ([]string, error) {
			t.Fatal("listVolumesFn must not be called when project name is unresolvable")
			return nil, nil
		},
		nil,
	)

	cfg := &config.DweConfig{} // no Prefix, no Name → FullName() == ""
	ectx := spec.ExecContext{Config: cfg, Output: render.NewWriter(&bytes.Buffer{})}

	err := (RemoveProjectVolumes{}).Run(context.Background(), nil, ectx)
	if err == nil {
		t.Fatal("expected abort error when project name is unresolvable, got nil")
	}
	if !strings.Contains(err.Error(), "could not resolve project name") {
		t.Errorf("error = %q, want 'could not resolve project name'", err.Error())
	}
}

func TestDockerRemoveVolumes_Run_NilConfig(t *testing.T) {
	ectx := spec.ExecContext{Output: render.NewWriter(&bytes.Buffer{})}
	err := (RemoveProjectVolumes{}).Run(context.Background(), nil, ectx)
	if err == nil {
		t.Fatal("expected error when Config is nil, got nil")
	}
	if !strings.Contains(err.Error(), "config not available") {
		t.Errorf("error = %q, want 'config not available'", err.Error())
	}
}

// TestDockerRemoveVolumes_Run_ListError verifies that a `docker volume ls`
// failure is FATAL (returns an error) — so reset aborts rather than clearing the
// journal with volumes left behind.
func TestDockerRemoveVolumes_Run_ListError(t *testing.T) {
	listErr := errors.New("docker volume ls: cannot connect to daemon")
	swapVolumeSeams(t,
		func(context.Context, string) ([]string, error) { return nil, listErr },
		nil,
	)
	cfg := &config.DweConfig{}
	cfg.Project.Name = "demo"
	ectx := spec.ExecContext{Config: cfg, Output: render.NewWriter(&bytes.Buffer{})}

	err := (RemoveProjectVolumes{}).Run(context.Background(), nil, ectx)
	if err == nil {
		t.Fatal("expected error from list failure, got nil")
	}
	if !errors.Is(err, listErr) {
		t.Errorf("error chain does not wrap listErr: %v", err)
	}
}
