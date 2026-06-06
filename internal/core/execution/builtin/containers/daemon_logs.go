package containers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/daemon"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// DaemonLogs implements docker_daemon_logs.
//
// Foreground tail of `docker logs -f --tail=100 <full>`. Ctrl-C / context
// cancellation sends SIGINT to docker (graceful detach); the container is
// never signalled by this builtin.
type DaemonLogs struct{}

// Validate checks that the with parameters are valid before the pipeline runs.
func (DaemonLogs) Validate(with map[string]any) error {
	if spec.GetStringParam(with, "container_template", "") == "" {
		return fmt.Errorf("docker_daemon_logs: container_template required")
	}
	return nil
}

// Describe returns a short human-readable description used in plan output.
func (DaemonLogs) Describe(with map[string]any) string {
	return "tail daemon logs: " + spec.GetStringParam(with, "container_template", "?")
}

// Run executes the docker_daemon_logs builtin.
func (DaemonLogs) Run(ctx context.Context, with map[string]any, ectx spec.ExecContext) error {
	if ectx.Config == nil {
		return fmt.Errorf("docker_daemon_logs: config not available")
	}
	dockerCfg := ectx.DockerConfig
	if dockerCfg == nil {
		dockerCfg = &config.DockerConfig{}
	}

	template := spec.GetStringParam(with, "container_template", "")
	projectFull := config.ComposeProjectName(dockerCfg, ectx.Config)
	fullName, err := daemon.ResolveContainerName(projectFull, template)
	if err != nil {
		return err
	}

	compose := docker.NewCompose(ectx.Config, dockerCfg, ectx.ProjectRoot)

	// Tail whichever scope is actually running: the canonical name first, then
	// the legacy FullName-scoped name (when they differ) for a daemon started
	// before docker.yml project_name was honored.
	targetName := ""
	for _, p := range config.ComposeProjectNameCandidates(dockerCfg, ectx.Config) {
		name, nerr := daemon.ResolveContainerName(p, template)
		if nerr != nil {
			continue
		}
		running, probeErr := isDaemonRunning(ctx, compose, name)
		if probeErr != nil {
			return fmt.Errorf("docker_daemon_logs: probe failed: %w", probeErr)
		}
		if running {
			targetName = name
			break
		}
	}
	if targetName == "" {
		return fmt.Errorf("%w: %s (start it with .start)", daemon.ErrDaemonNotRunning, fullName)
	}

	args := []string{"logs", "-f", "--tail=100", targetName}
	cmd := exec.CommandContext(ctx, compose.BinName(), args...) //nolint:gosec
	cmd.Env = compose.BuildEnv()
	cmd.Stdout = ectx.Output.Writer()
	cmd.Stderr = ectx.Output.Writer()

	// Graceful detach on context cancel: SIGINT to docker logs, not SIGKILL.
	// docker logs flushes pending output and exits; container is untouched.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 3 * time.Second

	if err := cmd.Run(); err != nil {
		// Treat SIGINT-induced exit as success (user-initiated detach).
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 130 {
			return nil
		}
		// SIGKILL after WaitDelay expiry: exit code is -1. Only treat as
		// success when the context was already cancelled — otherwise a
		// negative exit code means the docker process was killed externally.
		if errors.As(err, &exitErr) && exitErr.ExitCode() < 0 && ctx.Err() != nil {
			return nil
		}
		if strings.Contains(err.Error(), "signal: interrupt") {
			return nil
		}
		// WaitDelay fired after context cancel — I/O pipes closed while flushing.
		if errors.Is(err, exec.ErrWaitDelay) {
			return nil
		}
		return fmt.Errorf("docker logs: %w", err)
	}
	return nil
}
