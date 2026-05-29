package builtin

import (
	"context"
	"fmt"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/shared/daemon"
	"devbox-cli/internal/shared/docker"
)

// stopContainerFn / removeContainerFn are test seams: production calls
// docker.StopContainer / docker.RemoveContainer; tests inject stubs to capture
// invocations without spawning real subprocesses.
var (
	stopContainerFn   = docker.StopContainer
	removeContainerFn = docker.RemoveContainer
)

// dockerStopRemoveContainerBuiltin implements docker_stop_remove_container.
//
// Issues `docker stop -t <secs> <full>` followed by `docker rm -f <full>`,
// both bypassing docker compose. Used by the synthetic baseline phase of
// per-service reset so that a service container is stopped AND removed even
// when the service has no reset.yml.
//
// Idempotent: missing-container errors on either step are swallowed by the
// underlying docker helpers and surface as success.
//
// Stop-failure contract: if StopContainer returns a non-nil error (other than
// missing-container, which it absorbs), the builtin propagates and does NOT
// attempt rm. Stop failure means the container is in an unexpected state;
// force-removing on top would hide the diagnostic.
type dockerStopRemoveContainerBuiltin struct{}

func (dockerStopRemoveContainerBuiltin) Validate(with map[string]any) error {
	if getStringParam(with, "container_template", "") == "" {
		return fmt.Errorf("docker_stop_remove_container: container_template required")
	}
	return nil
}

func (dockerStopRemoveContainerBuiltin) Describe(with map[string]any) string {
	return "stop+rm container: " + getStringParam(with, "container_template", "?")
}

func (dockerStopRemoveContainerBuiltin) Run(ctx context.Context, with map[string]any, ectx ExecContext) error {
	if ectx.Config == nil {
		return fmt.Errorf("docker_stop_remove_container: config not available")
	}

	projectFull := ectx.Config.Project.FullName()
	fullName, err := daemon.ResolveContainerName(projectFull, getStringParam(with, "container_template", ""))
	if err != nil {
		return err
	}

	secs := stopTimeoutSeconds(getStringParam(with, "stop_timeout", ""))
	dockerBin := config.DockerBin(ectx.Config)

	if err := stopContainerFn(ctx, dockerBin, fullName, secs); err != nil {
		return fmt.Errorf("stop container %q: %w", fullName, err)
	}
	_, _ = fmt.Fprintf(ectx.Output.Writer(), "✓ container stopped: %s\n", fullName)

	if err := removeContainerFn(ctx, dockerBin, fullName); err != nil {
		return fmt.Errorf("remove container %q: %w", fullName, err)
	}
	_, _ = fmt.Fprintf(ectx.Output.Writer(), "✓ container removed: %s\n", fullName)
	return nil
}
