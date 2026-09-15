package deploy

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/execution/preflight"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"

	"github.com/spf13/cobra"
)

func svc(enabled bool, deps ...string) config.ServiceConfig {
	return config.ServiceConfig{Enabled: enabled, DependsOn: deps}
}

// TestPreflightScope pins the closure a per-service deploy hands to
// preflight.WithServices: the requested names as given, plus every enabled
// service reachable through service.yml depends_on.
func TestPreflightScope(t *testing.T) {
	tests := []struct {
		name     string
		services map[string]config.ServiceConfig
		request  []string
		want     []string
	}{
		{
			name:     "no deps",
			services: map[string]config.ServiceConfig{"app": svc(true), "db": svc(true)},
			request:  []string{"app"},
			want:     []string{"app"},
		},
		{
			name: "transitive chain",
			services: map[string]config.ServiceConfig{
				"app":   svc(true, "db"),
				"db":    svc(true, "cache"),
				"cache": svc(true),
				"other": svc(true),
			},
			request: []string{"app"},
			want:    []string{"app", "cache", "db"},
		},
		{
			name: "diamond visits each node once",
			services: map[string]config.ServiceConfig{
				"app":   svc(true, "db", "queue"),
				"db":    svc(true, "store"),
				"queue": svc(true, "store"),
				"store": svc(true),
			},
			request: []string{"app"},
			want:    []string{"app", "db", "queue", "store"},
		},
		{
			name: "cycle terminates",
			services: map[string]config.ServiceConfig{
				"a": svc(true, "b"),
				"b": svc(true, "a"),
			},
			request: []string{"a"},
			want:    []string{"a", "b"},
		},
		{
			name: "disabled dependency skipped",
			services: map[string]config.ServiceConfig{
				"app": svc(true, "db"),
				"db":  svc(false, "cache"),
				// Only reachable through the disabled db, so it must not join
				// the scope either.
				"cache": svc(true),
			},
			request: []string{"app"},
			want:    []string{"app"},
		},
		{
			name: "unknown dependency name ignored",
			services: map[string]config.ServiceConfig{
				"app": svc(true, "ghost"),
			},
			request: []string{"app"},
			want:    []string{"app"},
		},
		{
			name: "requested disabled service passes through",
			services: map[string]config.ServiceConfig{
				"db": svc(false),
			},
			request: []string{"db"},
			want:    []string{"db"},
		},
		{
			name: "requested name unknown to the config passes through",
			// RunHelper rejects this before preflight; the helper itself must
			// not silently drop it.
			services: map[string]config.ServiceConfig{"app": svc(true)},
			request:  []string{"ghost"},
			want:     []string{"ghost"},
		},
		{
			name: "multiple requests deduplicated and sorted",
			services: map[string]config.ServiceConfig{
				"web": svc(true, "db"),
				"api": svc(true, "db"),
				"db":  svc(true),
			},
			request: []string{"web", "api", "web"},
			want:    []string{"api", "db", "web"},
		},
		{
			name:     "no request means no scope",
			services: map[string]config.ServiceConfig{"app": svc(true)},
			request:  nil,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.DweConfig{Services: tt.services}
			got := preflightScope(cfg, tt.request)
			if !slices.Equal(got, tt.want) {
				t.Errorf("preflightScope(%v) = %v, want %v", tt.request, got, tt.want)
			}
		})
	}

	t.Run("nil config", func(t *testing.T) {
		if got := preflightScope(nil, []string{"app"}); got != nil {
			t.Errorf("preflightScope(nil, …) = %v, want nil", got)
		}
	})
}

// writeScopeWorkspace lays out a project with two services so RunHelper can
// resolve --service names.
func writeScopeWorkspace(t *testing.T) (dir, cfgPath string) {
	t.Helper()
	dir = t.TempDir()
	cfgPath = filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\nproject:\n  name: test\n  prefix: dwe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"app": "type: app\ncontainer: app\nrequired: true\ndir: ./services/app\ndepends_on: [db]\n",
		"db":  "type: infra\ncontainer: db\nrequired: true\n",
	} {
		svcDir := filepath.Join(dir, "workspace", "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, cfgPath
}

// recordingPreflight captures what RunHelper passes to the preflight seam and
// stops the run there with stop. The options are folded back through
// preflight.ServicesFor, so the recorded scope is the exact list the real
// preflight.Run would narrow env.ports_free to.
func recordingPreflight(stop error, scope *[]string, optCount *int, calls *int) preflight.RunFn {
	return func(_ context.Context, _ *config.DweConfig, _ *usercommands.Registry, _, _ string, _ bool, _ io.Writer, opts ...preflight.Option) error {
		*calls++
		*optCount = len(opts)
		*scope = preflight.ServicesFor(opts...)
		return stop
	}
}

// TestRunHelper_PreflightScopeOption pins the wiring shared by
// `dwe deploy run --service` and the `dwe services enable|disable --apply`
// executor (both drive RunHelper with Opts.Services): a scoped run carries the
// WithServices option holding preflightScope's closure — not the raw request —
// and a whole-project run carries none. The fixture's app depends_on db, so
// dropping the closure is observable here.
func TestRunHelper_PreflightScopeOption(t *testing.T) {
	stop := errors.New("stop after preflight")

	tests := []struct {
		name      string
		services  []string
		wantOpts  int
		wantScope []string
	}{
		{
			name:      "per-service run scopes preflight to the depends_on closure",
			services:  []string{"app"},
			wantOpts:  1,
			wantScope: []string{"app", "db"},
		},
		{name: "whole-project run stays unscoped", services: nil, wantOpts: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cfgPath := writeScopeWorkspace(t)
			var optCount, calls int
			var scope []string
			flags := &cmdctx.RootFlags{ConfigPath: cfgPath}
			err := RunHelper(context.Background(), &cobra.Command{}, flags, Opts{
				Services:       tt.services,
				NonInteractive: true,
				Silent:         true,
				PreflightFn:    recordingPreflight(stop, &scope, &optCount, &calls),
			})
			if !errors.Is(err, stop) {
				t.Fatalf("err = %v, want %v", err, stop)
			}
			if calls != 1 {
				t.Fatalf("preflight called %d times, want 1", calls)
			}
			if optCount != tt.wantOpts {
				t.Errorf("preflight options = %d, want %d", optCount, tt.wantOpts)
			}
			if !slices.Equal(scope, tt.wantScope) {
				t.Errorf("preflight scope = %v, want %v", scope, tt.wantScope)
			}
		})
	}
}

// TestRunHelper_UnknownServiceRejectedBeforePreflight pins the ordering fix: a
// typo in --service must fail fast, not narrow the preflight scope to a name
// no config knows (which would silently skip every ports_free check).
func TestRunHelper_UnknownServiceRejectedBeforePreflight(t *testing.T) {
	_, cfgPath := writeScopeWorkspace(t)
	var optCount, calls int
	var scope []string
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}
	err := RunHelper(context.Background(), &cobra.Command{}, flags, Opts{
		Services:       []string{"ghost"},
		NonInteractive: true,
		Silent:         true,
		PreflightFn:    recordingPreflight(nil, &scope, &optCount, &calls),
	})
	if err == nil || err.Error() != `service "ghost" not found in config` {
		t.Fatalf("err = %v, want service-not-found", err)
	}
	if calls != 0 {
		t.Errorf("preflight called %d times for an unknown service, want 0", calls)
	}
}
