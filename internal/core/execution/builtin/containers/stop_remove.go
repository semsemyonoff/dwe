package containers

import (
	"context"
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// stopContainerFn / removeContainerFn / lookupContainerFn are test seams:
// production calls docker.StopContainer / docker.RemoveContainer /
// docker.LookupServiceContainer; tests inject stubs to capture invocations
// without spawning real subprocesses.
var (
	stopContainerFn   = docker.StopContainer
	removeContainerFn = docker.RemoveContainer
	lookupContainerFn = docker.LookupServiceContainer
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

	// The label query must use the same name compose stamped onto the
	// containers, so it goes through the shared resolver (resolved docker.yml
	// project_name, else FullName(), lowercased) rather than re-deriving the
	// precedence here.
	projectFull := config.ComposeProjectName(ectx.DockerConfig, ectx.Config)
	dockerBin := config.DockerBin(ectx.Config)
	// container_template carries the compose service name (svc.Container).
	// Resolve the REAL container name via the compose project + service labels
	// so reset removes the actual container even when container_name is unset
	// (compose's default "<project>-<service>-<index>") or customised — guessing
	// "<project>-<service>" would silently no-op and leave the container behind.
	service := spec.GetStringParam(with, "container_template", "")
	// nil processEnv: stop+rm (StopContainer / RemoveContainer) run with the
	// inherited environment, so the label probe must too (same daemon).
	fullName, err := lookupContainerFn(dockerBin, nil, projectFull, service)
	if err != nil {
		return fmt.Errorf("resolving container for service %q: %w", service, err)
	}
	if fullName == "" {
		// No container exists for this service (never deployed or already
		// removed) — stop+remove is idempotent, nothing to do.
		_, _ = fmt.Fprintf(ectx.Output.Writer(), "• no container for service %q (nothing to stop)\n", service)
		return nil
	}

	secs := stopTimeoutSeconds(spec.GetStringParam(with, "stop_timeout", ""))

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
