package containers

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/semsemyonoff/devbox/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/shared/docker"
)

// ContainersRunning is a fast "is running" check for compose services.
// Unlike docker_wait_healthy it does not poll, does not honour a timeout, and
// does not require services to have a healthcheck — it asks compose once for
// the set of currently-running services and returns immediately. Intended as
// a `check:` partner for `docker up` steps and as a precondition for
// service-touching pipeline steps.
//
// Name mirrors the registered builtin name `containers_running`.
//
//nolint:revive // intentional: type name mirrors the registered builtin name.
type ContainersRunning struct{}

// Validate checks that the with parameters are valid before the pipeline runs.
func (ContainersRunning) Validate(with map[string]any) error {
	if raw, ok := with["services"]; ok && raw != nil {
		if items, ok := raw.([]any); ok {
			for i, item := range items {
				if _, ok := item.(string); !ok {
					return fmt.Errorf("builtin containers_running: services[%d]: expected string, got %T", i, item)
				}
			}
		}
	}
	services, err := spec.GetStringSlice(with, "services")
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return fmt.Errorf("builtin containers_running: services list is required and must be non-empty")
	}
	if slices.Contains(services, "") {
		return fmt.Errorf("builtin containers_running: services list contains empty string")
	}

	for key := range with {
		if key != "services" {
			return fmt.Errorf("builtin containers_running: unknown key %q", key)
		}
	}
	return nil
}

// Describe returns a short human-readable description used in plan output.
func (ContainersRunning) Describe(with map[string]any) string {
	services, _ := spec.GetStringSlice(with, "services")
	if len(services) == 1 {
		return fmt.Sprintf("check that service %q is running", services[0])
	}
	return fmt.Sprintf("check that %d services are running", len(services))
}

// Run executes the containers_running predicate.
func (ContainersRunning) Run(ctx context.Context, with map[string]any, ectx spec.ExecContext) error {
	services, _ := spec.GetStringSlice(with, "services")

	dockerCfg := ectx.DockerConfig
	if dockerCfg == nil {
		dockerCfg = &config.DockerConfig{}
	}
	compose := docker.NewCompose(ectx.Config, dockerCfg)

	running, err := compose.RunningServices(ctx, services)
	if err != nil {
		return fmt.Errorf("containers_running: %w", err)
	}

	runningSet := make(map[string]struct{}, len(running))
	for _, s := range running {
		runningSet[s] = struct{}{}
	}
	var missing []string
	for _, s := range services {
		if _, ok := runningSet[s]; !ok {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("services not running: %s", strings.Join(missing, ", "))
	}
	return nil
}
