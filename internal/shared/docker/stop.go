package docker

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// DefaultStopTimeoutSec is the default timeout passed to `docker stop -t`.
// Both the per-service stop helper and the daemon_stop builtin use this value
// so they share a single source of truth.
const DefaultStopTimeoutSec = 10

// runDirect runs `docker <args...>` directly (bypassing compose), discarding
// stdout and capturing stderr. On success it returns nil. When stderr matches a
// "no such container" error it returns onNoSuchContainer (nil for idempotent
// commands, a sentinel for callers that need to surface guidance). Any other
// failure is wrapped with the given label.
func runDirect(ctx context.Context, dockerBin, label string, onNoSuchContainer error, args ...string) error {
	if dockerBin == "" {
		dockerBin = "docker"
	}
	cmd := exec.CommandContext(ctx, dockerBin, args...) //nolint:gosec
	cmd.Stdout = io.Discard
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errOut := stderr.String()
		if IsNoSuchContainerErr(errOut) {
			return onNoSuchContainer
		}
		if errOut != "" {
			return fmt.Errorf("%s: %w: %s", label, err, strings.TrimSpace(errOut))
		}
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// StopContainer issues `docker stop -t <timeoutSec> <containerName>` directly,
// bypassing docker compose. This allows stopping a container even after its
// service has been disabled and removed from the rendered compose project.
//
// Idempotent: if the container does not exist, StopContainer returns nil.
// Any other docker error is wrapped and returned.
func StopContainer(ctx context.Context, dockerBin, containerName string, timeoutSec int) error {
	return runDirect(ctx, dockerBin, "docker stop", nil,
		"stop", "-t", strconv.Itoa(timeoutSec), containerName)
}

// RestartContainer issues `docker restart -t <timeoutSec> <containerName>`
// directly, bypassing docker compose. This allows restarting a container
// even after its service has been disabled and removed from the rendered
// compose project.
//
// Unlike StopContainer, this is NOT idempotent: if the container does not
// exist, ErrNoSuchContainer is returned so callers can surface domain
// guidance (e.g. "run `dwe deploy run` first"). Any other docker error is
// wrapped and returned.
func RestartContainer(ctx context.Context, dockerBin, containerName string, timeoutSec int) error {
	return runDirect(ctx, dockerBin, "docker restart", fmt.Errorf("%w: %s", ErrNoSuchContainer, containerName),
		"restart", "-t", strconv.Itoa(timeoutSec), containerName)
}

// RemoveContainer issues `docker rm -f <containerName>` directly, bypassing
// docker compose. The `-f` flag ensures containers in any intermediate state
// (Created, Exited, Restarting, even Running) are removed reliably.
//
// Idempotent: if the container does not exist, RemoveContainer returns nil.
// Any other docker error is wrapped and returned.
func RemoveContainer(ctx context.Context, dockerBin, containerName string) error {
	return runDirect(ctx, dockerBin, "docker rm", nil, "rm", "-f", containerName)
}
