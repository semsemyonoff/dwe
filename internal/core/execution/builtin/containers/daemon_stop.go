package containers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/daemon"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// errDaemonNoSuchContainer signals that the target container does not exist
// (idempotent stop). The drain loop skips it and moves to the next scope.
var errDaemonNoSuchContainer = errors.New("no such container")

// daemonStopFn is a test seam: production stops one container via `docker stop`;
// tests inject a stub to drive the dual-scope drain loop without subprocesses.
var daemonStopFn = stopDaemonContainer

// stopDaemonContainer issues `docker stop -t <secs> <name>`. It returns
// errDaemonNoSuchContainer when the container is absent so the caller can treat
// the stop as idempotent; any other docker failure is returned wrapped.
func stopDaemonContainer(ctx context.Context, compose *docker.Compose, name string, secs int) error {
	args := []string{"stop", "-t", strconv.Itoa(secs), name}
	cmd := exec.CommandContext(ctx, compose.BinName(), args...) //nolint:gosec
	cmd.Env = compose.BuildEnv()
	cmd.Stdout = io.Discard
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errOut := strings.TrimSpace(stderr.String())
		if strings.Contains(errOut, "No such container") {
			return errDaemonNoSuchContainer
		}
		if errOut != "" {
			return fmt.Errorf("docker stop: %w: %s", err, errOut)
		}
		return fmt.Errorf("docker stop: %w", err)
	}
	return nil
}

// defaultStopTimeout mirrors docker.DefaultStopTimeoutSec as a duration.
// Converting here keeps stop_timeout parsing independent of the docker package.
const defaultStopTimeout = docker.DefaultStopTimeoutSec * time.Second

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

// DaemonStop implements docker_daemon_stop.
//
// Issues `docker stop -t <secs> <full>`. Missing-container exits 0
// (idempotent). stop_timeout is parsed defensively; sub-second values round
// up to 1 second since docker stop -t 0 sends SIGKILL immediately.
type DaemonStop struct{}

// Validate checks that the with parameters are valid before the pipeline runs.
func (DaemonStop) Validate(with map[string]any) error {
	if spec.GetStringParam(with, "container_template", "") == "" {
		return fmt.Errorf("docker_daemon_stop: container_template required")
	}
	return nil
}

// Describe returns a short human-readable description used in plan output.
func (DaemonStop) Describe(with map[string]any) string {
	return "stop daemon: " + spec.GetStringParam(with, "container_template", "?")
}

// Run executes the docker_daemon_stop builtin.
func (DaemonStop) Run(ctx context.Context, with map[string]any, ectx spec.ExecContext) error {
	if ectx.Config == nil {
		return fmt.Errorf("docker_daemon_stop: config not available")
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

	secs := stopTimeoutSeconds(spec.GetStringParam(with, "stop_timeout", ""))

	compose := docker.NewCompose(ectx.Config, dockerCfg, ectx.ProjectRoot)

	// Stop the daemon under the canonical name AND the legacy FullName-scoped name
	// (when they differ). We must drain EVERY scope, not stop-and-return on the
	// first hit: the prior duplicate-spawn bug can leave both a legacy and a
	// canonical container running, and `.restart` (= .stop then .start) would
	// otherwise leave the legacy one alive and then noop/error on start. Only
	// "No such container" is skipped; any other docker error is fatal.
	stoppedAny := false
	for _, p := range config.ComposeProjectNameCandidates(dockerCfg, ectx.Config) {
		name, nerr := daemon.ResolveContainerName(p, template)
		if nerr != nil {
			continue // primary was already validated above
		}
		if serr := daemonStopFn(ctx, compose, name, secs); serr != nil {
			if errors.Is(serr, errDaemonNoSuchContainer) {
				continue // this scope has no container; try the next
			}
			return serr
		}
		stoppedAny = true
		_, _ = fmt.Fprintf(ectx.Output.Writer(), "✓ daemon stopped: %s\n", name)
	}
	if !stoppedAny {
		_, _ = fmt.Fprintf(ectx.Output.Writer(), "no daemon to stop: %s\n", fullName)
	}
	return nil
}
