package builtin

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/daemon"
	"devbox-cli/internal/docker"
)

const defaultStopTimeout = 10 * time.Second

func parseDurationPositive(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("non-positive duration: %s", s)
	}
	return d, nil
}

// stopTimeoutSeconds converts a raw stop_timeout string into the integer
// seconds passed to `docker stop -t`. Empty / unparseable / non-positive
// inputs fall back to the 10-second default. Sub-second positive durations
// round up to 1 (docker stop -t 0 is SIGKILL immediately — not what
// stop_timeout: 500ms should mean).
func stopTimeoutSeconds(raw string) int {
	timeout := defaultStopTimeout
	if raw != "" {
		if d, err := parseDurationPositive(raw); err == nil {
			timeout = d
		}
	}
	return max(int(timeout.Round(time.Second).Seconds()), 1)
}

// daemonStopBuiltin implements docker_daemon_stop.
//
// Issues `docker stop -t <secs> <full>`. Missing-container exits 0
// (idempotent). stop_timeout is parsed defensively; sub-second values round
// up to 1 second since docker stop -t 0 sends SIGKILL immediately.
type daemonStopBuiltin struct{}

func (daemonStopBuiltin) Validate(with map[string]any) error {
	if getStringParam(with, "container_template", "") == "" {
		return fmt.Errorf("docker_daemon_stop: container_template required")
	}
	return nil
}

func (daemonStopBuiltin) Describe(with map[string]any) string {
	return "stop daemon: " + getStringParam(with, "container_template", "?")
}

func (daemonStopBuiltin) Run(ctx context.Context, with map[string]any, ectx ExecContext) error {
	if ectx.Config == nil {
		return fmt.Errorf("docker_daemon_stop: config not available")
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

	secs := stopTimeoutSeconds(getStringParam(with, "stop_timeout", ""))

	compose := docker.NewCompose(ectx.Config, dockerCfg)

	args := []string{"stop", "-t", strconv.Itoa(secs), fullName}
	cmd := exec.CommandContext(ctx, compose.BinName(), args...) //nolint:gosec
	cmd.Env = compose.BuildEnv()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errOut := stderr.String()
		if strings.Contains(errOut, "No such container") {
			_, _ = fmt.Fprintf(ectx.Output.Writer(), "no daemon to stop: %s\n", fullName)
			return nil
		}
		if errOut != "" {
			return fmt.Errorf("docker stop: %w: %s", err, strings.TrimSpace(errOut))
		}
		return fmt.Errorf("docker stop: %w", err)
	}
	_, _ = fmt.Fprintf(ectx.Output.Writer(), "✓ daemon stopped: %s\n", fullName)
	return nil
}
