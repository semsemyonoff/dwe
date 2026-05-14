package builtin

import (
	"fmt"
	"slices"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
)

type dockerWaitHealthyBuiltin struct{}

func (dockerWaitHealthyBuiltin) Validate(with map[string]any) error {
	// Validate timeout.
	timeout, err := getDurationParam(with, "timeout", 60*time.Second)
	if err != nil {
		return err
	}
	if timeout <= 0 {
		return fmt.Errorf("builtin docker_wait_healthy: timeout must be positive, got %v", timeout)
	}

	// Validate interval.
	interval, err := getDurationParam(with, "interval", 2*time.Second)
	if err != nil {
		return err
	}
	if interval <= 0 {
		return fmt.Errorf("builtin docker_wait_healthy: interval must be positive, got %v", interval)
	}

	// Validate services if present. Check element types before getStringSlice
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
	services, err := getStringSlice(with, "services")
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

func (dockerWaitHealthyBuiltin) Describe(with map[string]any) string {
	timeout, _ := getDurationParam(with, "timeout", 60*time.Second)
	interval, _ := getDurationParam(with, "interval", 2*time.Second)
	services, _ := getStringSlice(with, "services")

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

func (dockerWaitHealthyBuiltin) Run(with map[string]any, ctx ExecContext) error {
	// Parse parameters.
	timeout, _ := getDurationParam(with, "timeout", 60*time.Second)
	interval, _ := getDurationParam(with, "interval", 2*time.Second)
	services, _ := getStringSlice(with, "services")

	// Load docker config.
	dockerCfg, err := config.LoadDockerConfig(ctx.ProjectRoot, ctx.Config)
	if err != nil {
		return fmt.Errorf("docker_wait_healthy: loading docker config: %w", err)
	}

	// Build compose.
	compose := docker.NewCompose(ctx.Config, dockerCfg)

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
		if ctx.Output != nil {
			ctx.Output.Warning("no containers found")
		}
		return nil
	}

	// Compute attempts using ceiling division so the full timeout duration is covered.
	attempts := max(int((timeout+interval-1)/interval), 1)

	// Wait for healthy.
	return docker.WaitContainersHealthy(ids, compose.HealthStatus, attempts, interval, ctx.Output)
}
