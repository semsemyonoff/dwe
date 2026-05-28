package builtin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/shared/daemon"
	"devbox-cli/internal/shared/docker"
)

// daemonLogsBuiltin implements docker_daemon_logs.
//
// Foreground tail of `docker logs -f --tail=100 <full>`. Ctrl-C / context
// cancellation sends SIGINT to docker (graceful detach); the container is
// never signalled by this builtin.
type daemonLogsBuiltin struct{}

func (daemonLogsBuiltin) Validate(with map[string]any) error {
	if getStringParam(with, "container_template", "") == "" {
		return fmt.Errorf("docker_daemon_logs: container_template required")
	}
	return nil
}

func (daemonLogsBuiltin) Describe(with map[string]any) string {
	return "tail daemon logs: " + getStringParam(with, "container_template", "?")
}

func (daemonLogsBuiltin) Run(ctx context.Context, with map[string]any, ectx ExecContext) error {
	if ectx.Config == nil {
		return fmt.Errorf("docker_daemon_logs: config not available")
	}
	dockerCfg := ectx.DockerConfig
	if dockerCfg == nil {
		dockerCfg = &config.DockerConfig{}
	}

	projectFull := ectx.Config.Project.FullName()
	fullName, err := daemon.ResolveContainerName(projectFull, getStringParam(with, "container_template", ""))
	if err != nil {
		return err
	}

	compose := docker.NewCompose(ectx.Config, dockerCfg)

	running, probeErr := isDaemonRunning(ctx, compose, fullName)
	if probeErr != nil {
		return fmt.Errorf("docker_daemon_logs: probe failed: %w", probeErr)
	}
	if !running {
		return fmt.Errorf("%w: %s (start it with .start)", daemon.ErrDaemonNotRunning, fullName)
	}

	args := []string{"logs", "-f", "--tail=100", fullName}
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
