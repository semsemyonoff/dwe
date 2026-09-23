package containers

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// stubDocker puts a `docker` first on PATH that exits 0 and, for `ps`, prints
// a container id when running is true — enough for every daemon builtin to
// reach its real spawn without a daemon.
func stubDocker(t *testing.T, running bool) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nexit 0\n"
	if running {
		script = "#!/bin/sh\n[ \"$1\" = ps ] && echo cid0123\nexit 0\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing stub docker: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func traceExecCtx(t *testing.T) spec.ExecContext {
	t.Helper()
	cfg := &config.DweConfig{}
	cfg.Project.Name = "tbm"
	cfg.Project.Prefix = "dwe"
	return spec.ExecContext{
		Config:       cfg,
		DockerConfig: &config.DockerConfig{},
		ProjectRoot:  t.TempDir(),
		Output:       render.NewWriter(&bytes.Buffer{}),
	}
}

// TestBuiltinSpawns_TraceEcho pins the -v contract for the builtins a user
// command (daemon .start/.stop/.logs) or a lifecycle pipeline runs through:
// the spawn that changes state or streams to the user echoes at Verbose, the
// read-only probe that decides whether to run it echoes at Debug only, and
// nothing is echoed at the default level.
func TestBuiltinSpawns_TraceEcho(t *testing.T) {
	const (
		startLine = "$ docker compose -p dwe-tbm run"
		probeLine = "$ docker ps -q --filter"
	)
	tests := []struct {
		name    string
		running bool
		run     func(ctx context.Context, ectx spec.ExecContext) error
		verbose []string // lines expected at Verbose (and Debug)
		debug   []string // lines expected at Debug only
	}{
		{
			name: "daemon start",
			run: func(ctx context.Context, ectx spec.ExecContext) error {
				return DaemonStart{}.Run(ctx, map[string]any{
					"service": "app", "container_template": "queue", "argv": []any{"php", "artisan", "queue:work"},
				}, ectx)
			},
			verbose: []string{startLine},
			debug:   []string{probeLine},
		},
		{
			name: "daemon stop",
			run: func(ctx context.Context, ectx spec.ExecContext) error {
				return DaemonStop{}.Run(ctx, map[string]any{"container_template": "queue"}, ectx)
			},
			verbose: []string{"$ docker stop -t 10 dwe-tbm-queue"},
		},
		{
			name:    "daemon logs",
			running: true,
			run: func(ctx context.Context, ectx spec.ExecContext) error {
				return DaemonLogs{}.Run(ctx, map[string]any{"container_template": "queue"}, ectx)
			},
			verbose: []string{"$ docker logs -f '--tail=100' dwe-tbm-queue"},
			debug:   []string{probeLine},
		},
		{
			name: "volume remove",
			run: func(ctx context.Context, _ spec.ExecContext) error {
				return removeDockerVolume(ctx, "docker", "dwe-tbm_db")
			},
			verbose: []string{"$ docker volume rm dwe-tbm_db"},
		},
		{
			name: "volume list",
			run: func(ctx context.Context, _ spec.ExecContext) error {
				_, err := listDockerVolumes(ctx, "docker")
				return err
			},
			debug: []string{"$ docker volume ls -q"},
		},
		{
			name: "daemons list",
			run: func(ctx context.Context, ectx spec.ExecContext) error {
				compose := docker.NewCompose(ectx.Config, ectx.DockerConfig, ectx.ProjectRoot)
				_, err := listDaemons(ctx, compose, "dwe-tbm")
				return err
			},
			debug: []string{"$ docker ps '--format=json'"},
		},
	}
	for _, tt := range tests {
		for _, lvl := range []trace.Level{trace.LevelOff, trace.LevelVerbose, trace.LevelDebug} {
			t.Run(tt.name+"/"+levelName(lvl), func(t *testing.T) {
				stubDocker(t, tt.running)
				var buf bytes.Buffer
				trace.Configure(&buf, lvl)
				t.Cleanup(func() { trace.Configure(nil, trace.LevelOff) })

				if err := tt.run(context.Background(), traceExecCtx(t)); err != nil {
					t.Fatalf("run: %v", err)
				}
				got := buf.String()
				for _, want := range tt.verbose {
					if has := strings.Contains(got, want); has != (lvl >= trace.LevelVerbose) {
						t.Errorf("level %s: contains %q = %v\ntrace:\n%s", levelName(lvl), want, has, got)
					}
				}
				for _, want := range tt.debug {
					if has := strings.Contains(got, want); has != (lvl >= trace.LevelDebug) {
						t.Errorf("level %s: contains %q = %v\ntrace:\n%s", levelName(lvl), want, has, got)
					}
				}
			})
		}
	}
}

// TestDaemonsReap_TraceEchoesStop covers the reap loop's inline `docker stop`,
// which bypasses the daemonStopFn seam.
func TestDaemonsReap_TraceEchoesStop(t *testing.T) {
	stubDocker(t, false)
	orig := listDaemonsFn
	t.Cleanup(func() { listDaemonsFn = orig })
	listDaemonsFn = func(context.Context, *docker.Compose, string) ([]string, error) {
		return []string{"dwe-tbm-queue"}, nil
	}

	var buf bytes.Buffer
	trace.Configure(&buf, trace.LevelVerbose)
	t.Cleanup(func() { trace.Configure(nil, trace.LevelOff) })

	if err := (DaemonsReap{}).Run(context.Background(), nil, traceExecCtx(t)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := "$ docker stop -t 10 dwe-tbm-queue"; !strings.Contains(buf.String(), want) {
		t.Fatalf("trace %q lacks %q", buf.String(), want)
	}
}

func levelName(l trace.Level) string {
	switch l {
	case trace.LevelOff:
		return "off"
	case trace.LevelVerbose:
		return "verbose"
	default:
		return "debug"
	}
}
