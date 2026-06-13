package docker

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Standard labels Docker Compose stamps on every container it creates. Match on
// these to identify a service's container by identity rather than by a guessed
// "<project>-<container>" name — container_name is user-controlled (custom
// names, compose's default "<project>-<service>-<index>" with a numeric index),
// so name-guessing is fragile. Centralised here so every probe
// (status.ServiceRunning, shell.serviceContainerState, env port-conflict
// classification) shares one definition.
const (
	ComposeProjectLabel = "com.docker.compose.project"
	ComposeServiceLabel = "com.docker.compose.service"
	// ComposeOneoffLabel marks one-off `docker compose run` containers ("True")
	// vs long-lived service containers from `up`/`run` services ("False"). We
	// exclude one-off containers from service-identity probes so an ephemeral
	// `dwe shell --mode run` / service-run container is never mistaken for the
	// real service container.
	ComposeOneoffLabel = "com.docker.compose.oneoff"
)

// psNamesRunner runs `docker <args>` and returns the first non-empty container
// name in stdout ("" when none matched). It is a package var so tests can drive
// ServiceContainerName's prefer/fallback logic without spawning a real process.
var psNamesRunner = runPSNames

func runPSNames(dockerBin string, processEnv, args []string) (string, error) {
	cmd := exec.Command(dockerBin, args...) //nolint:gosec
	cmd.Env = processEnv
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line, nil
		}
	}
	return "", nil
}

// serviceContainerPSArgs builds the `ps … --format '{{.Names}}'` argv that
// selects containers for compose service `service` in project `projectName`.
// runningOnly restricts to running containers (else `--all` includes every
// state). excludeOneoff adds the com.docker.compose.oneoff=False filter to skip
// one-off `compose run` containers. `{{.Names}}` is the single portable field —
// `.State` templates and `--format json` shapes differ between docker and podman.
func serviceContainerPSArgs(projectName, service string, runningOnly, excludeOneoff bool) []string {
	args := []string{
		"ps",
		"--filter", "label=" + ComposeProjectLabel + "=" + projectName,
		"--filter", "label=" + ComposeServiceLabel + "=" + service,
	}
	if runningOnly {
		args = append(args, "--filter", "status=running")
	} else {
		args = append(args, "--all")
	}
	if excludeOneoff {
		args = append(args, "--filter", "label="+ComposeOneoffLabel+"=False")
	}
	return append(args, "--format", "{{.Names}}")
}

// LookupServiceContainer resolves the real Docker container name for a dwe
// service (whose compose service name is `service`) in project `projectName`,
// querying by the compose project + service labels. It is correct regardless of
// any `container_name:` override and of compose's default
// "<project>-<service>-<index>" naming — unlike guessing "<project>-<service>",
// which only matches when the user pins container_name to exactly that string.
//
// It searches across ALL container states (running or exited) so logs/stop/reset
// can act on a stopped container too. Returns "" when no container exists for
// the service.
//
// processEnv is the environment for the `docker ps` probe; callers MUST pass the
// same env they use for the follow-up `docker logs/stop/restart/rm` action so
// the probe and the action target the same daemon (DOCKER_HOST/DOCKER_CONTEXT
// from docker.yml process_env). Pass nil to use the inherited environment.
//
// This is the per-service-targeting counterpart to the label-based probes used
// by status (ServiceRunning) and shell (serviceContainerState); centralising it
// keeps logs/stop/restart/reset from drifting back to fragile name-guessing.
func LookupServiceContainer(dockerBin string, processEnv []string, projectName, service string) (string, error) {
	return ServiceContainerName(dockerBin, processEnv, projectName, service, false)
}

// ServiceContainerName returns the name of a container for compose service
// `service` in project `projectName`, preferring a long-lived service container
// over an ephemeral one-off `docker compose run` container. runningOnly limits
// the search to running containers. Returns "" when none match, or a real Docker
// error (daemon unreachable, etc.) distinct from the empty no-match case.
//
// It queries first with com.docker.compose.oneoff=False (excluding one-off
// containers) and only falls back to the unfiltered query when that finds
// nothing — so on Docker a one-off is matched solely when no service container
// exists, while compose backends that omit the one-off label (some
// podman-compose versions) still resolve their container via the fallback rather
// than being wrongly filtered out.
func ServiceContainerName(dockerBin string, processEnv []string, projectName, service string, runningOnly bool) (string, error) {
	if projectName == "" || service == "" {
		return "", nil
	}
	name, err := psNamesRunner(dockerBin, processEnv, serviceContainerPSArgs(projectName, service, runningOnly, true))
	if err != nil || name != "" {
		return name, err
	}
	return psNamesRunner(dockerBin, processEnv, serviceContainerPSArgs(projectName, service, runningOnly, false))
}
