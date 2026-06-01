package containers

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// WaitHealthy implements docker_wait_healthy.
type WaitHealthy struct{}

// Validate checks that the with parameters are valid before the pipeline runs.
func (WaitHealthy) Validate(with map[string]any) error {
	// Validate timeout.
	timeout, err := spec.GetDurationParam(with, "timeout", 60*time.Second)
	if err != nil {
		return err
	}
	if timeout <= 0 {
		return fmt.Errorf("builtin docker_wait_healthy: timeout must be positive, got %v", timeout)
	}

	// Validate interval.
	interval, err := spec.GetDurationParam(with, "interval", 2*time.Second)
	if err != nil {
		return err
	}
	if interval <= 0 {
		return fmt.Errorf("builtin docker_wait_healthy: interval must be positive, got %v", interval)
	}

	// Validate services if present. Check element types before spec.GetStringSlice
	// so that non-string entries (e.g. YAML integers) are rejected rather than
	// silently coerced to strings.
	if raw, ok := with["services"]; ok && raw != nil {
		if items, ok := raw.([]any); ok {
			for i, item := range items {
				if _, ok := item.(string); !ok {
					return fmt.Errorf("builtin docker_wait_healthy: services[%d]: expected string, got %T", i, item)
				}
			}
		}
	}
	services, err := spec.GetStringSlice(with, "services")
	if err != nil {
		return err
	}
	if slices.Contains(services, "") {
		return fmt.Errorf("builtin docker_wait_healthy: services list contains empty string")
	}

	// Validate no unknown keys.
	for key := range with {
		if !slices.Contains([]string{"timeout", "interval", "services"}, key) {
			return fmt.Errorf("builtin docker_wait_healthy: unknown key %q", key)
		}
	}

	return nil
}

// Describe returns a short human-readable description used in plan output.
func (WaitHealthy) Describe(with map[string]any) string {
	timeout, _ := spec.GetDurationParam(with, "timeout", 60*time.Second)
	interval, _ := spec.GetDurationParam(with, "interval", 2*time.Second)
	services, _ := spec.GetStringSlice(with, "services")

	if len(services) > 0 {
		if len(services) == 1 {
			return fmt.Sprintf("wait until 1 service is healthy (timeout: %s, interval: %s)",
				timeout, interval)
		}
		return fmt.Sprintf("wait until %d services are healthy (timeout: %s, interval: %s)",
			len(services), timeout, interval)
	}
	return fmt.Sprintf("wait until all containers are healthy (timeout: %s, interval: %s)",
		timeout, interval)
}

// Run executes the docker_wait_healthy builtin.
func (WaitHealthy) Run(ctx context.Context, with map[string]any, ectx spec.ExecContext) error {
	// Parse parameters.
	timeout, _ := spec.GetDurationParam(with, "timeout", 60*time.Second)
	interval, _ := spec.GetDurationParam(with, "interval", 2*time.Second)
	services, _ := spec.GetStringSlice(with, "services")

	// Use the pre-loaded docker config from spec.ExecContext; callers normalise
	// os.ErrNotExist to &config.DockerConfig{} so we never load it here.
	dockerCfg := ectx.DockerConfig
	if dockerCfg == nil {
		dockerCfg = &config.DockerConfig{}
	}

	var err error
	// Build compose.
	compose := docker.NewCompose(ectx.Config, dockerCfg, ectx.ProjectRoot)

	// Obtain container IDs.
	var ids []string
	if len(services) > 0 {
		ids, err = compose.ContainerIDsFor(services)
	} else {
		ids, err = compose.ContainerIDs()
	}
	if err != nil {
		return fmt.Errorf("docker_wait_healthy: getting container IDs: %w", err)
	}

	// If no containers, warn and return nil (idempotent: may run before up).
	if len(ids) == 0 {
		if ectx.Output != nil {
			ectx.Output.Warning("no containers found")
		}
		return nil
	}

	// Compute attempts using ceiling division so the full timeout duration is covered.
	attempts := max(int((timeout+interval-1)/interval), 1)

	// Wait for healthy.
	return docker.WaitContainersHealthyContext(ctx, ids, compose.HealthStatus, attempts, interval, ectx.Output)
}
