package containers

import (
	"context"
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/daemon"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// stopContainerFn / removeContainerFn are test seams: production calls
// docker.StopContainer / docker.RemoveContainer; tests inject stubs to capture
// invocations without spawning real subprocesses.
var (
	stopContainerFn   = docker.StopContainer
	removeContainerFn = docker.RemoveContainer
)

// StopRemoveContainer implements docker_stop_remove_container.
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
type StopRemoveContainer struct{}

// Validate checks that the with parameters are valid before the pipeline runs.
func (StopRemoveContainer) Validate(with map[string]any) error {
	if spec.GetStringParam(with, "container_template", "") == "" {
		return fmt.Errorf("docker_stop_remove_container: container_template required")
	}
	return nil
}

// Describe returns a short human-readable description used in plan output.
func (StopRemoveContainer) Describe(with map[string]any) string {
	return "stop+rm container: " + spec.GetStringParam(with, "container_template", "?")
}

// Run executes the docker_stop_remove_container builtin.
func (StopRemoveContainer) Run(ctx context.Context, with map[string]any, ectx spec.ExecContext) error {
	if ectx.Config == nil {
		return fmt.Errorf("docker_stop_remove_container: config not available")
	}

	// Prefer the compose project name from workspace/docker.yml (already
	// resolved into ectx.DockerConfig) so the derived container name matches
	// what docker compose used at create time. Fall back to FullName() when
	// docker.yml is absent or its project_name is empty.
	projectFull := ectx.Config.Project.FullName()
	if ectx.DockerConfig != nil && ectx.DockerConfig.ProjectName != "" {
		projectFull = ectx.DockerConfig.ProjectName
	}
	fullName, err := daemon.ResolveContainerName(projectFull, spec.GetStringParam(with, "container_template", ""))
	if err != nil {
		return err
	}

	secs := stopTimeoutSeconds(spec.GetStringParam(with, "stop_timeout", ""))
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
