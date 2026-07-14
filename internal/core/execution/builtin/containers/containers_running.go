package containers

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// probeRetries is the number of extra attempts Run makes after a transient
// compose-probe failure. containers_running is designed to run as an assertion
// immediately after `docker up --wait`, and at that exact boundary a
// `docker compose ps` probe can transiently fail (exit 1, empty stderr) even
// though every container is already up and healthy per the daemon — the
// compose CLI / daemon is momentarily busy right as `up --wait` returns.
//
// This does NOT poll for readiness: a probe that succeeds but reports a service
// as not-running still fails on the first attempt (that is a real assertion
// failure, not a transient one). Only a probe that could not run at all is
// retried, so the "does not poll" contract holds.
const probeRetries = 2

// probeRetryBackoff is the pause between transient-probe retries. A var so
// tests can shrink it.
var probeRetryBackoff = 300 * time.Millisecond

// ContainersRunning is a fast "is running" check for compose services.
// Unlike docker_wait_healthy it does not poll for readiness, does not honour a
// timeout, and does not require services to have a healthcheck — it asks compose
// for the set of currently-running services and returns as soon as it gets an
// answer. A transient probe failure (the compose CLI erroring right at the
// `up --wait` boundary) is retried a bounded number of times; a service simply
// not running is not. Intended as a `check:` partner for `docker up` steps and
// as a precondition for service-touching pipeline steps.
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
	compose := docker.NewCompose(ectx.Config, dockerCfg, ectx.ProjectRoot)

	running, err := runningServicesWithRetry(ctx, compose, services)
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

// runningServicesWithRetry probes the running services, retrying only a
// transient command failure (the probe itself erroring) — never a successful
// probe. It is ctx-aware: a cancelled context short-circuits the backoff and
// returns immediately instead of retrying.
func runningServicesWithRetry(ctx context.Context, compose *docker.Compose, services []string) ([]string, error) {
	var lastErr error
	for attempt := 0; attempt <= probeRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(probeRetryBackoff):
			}
		}
		running, err := compose.RunningServices(ctx, services)
		if err == nil {
			return running, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
