package containers

import (
	"context"
	"errors"
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

// Seams for the whole-project mode, so tests drive every branch without a
// daemon.
var (
	configProjectName     = (*docker.Compose).ConfigProjectName
	configServices        = (*docker.Compose).ConfigServices
	projectContainerNames = docker.ProjectContainerNames
)

// errNoProjectName is returned when neither dwe nor compose yields a project
// name: a label filter on "" would match nothing and pass vacuously.
var errNoProjectName = errors.New("containers_running: cannot determine the compose project name — set project.name or docker.yml project_name")

// ContainersRunning is a fast "is running" check for compose services.
// Unlike docker_wait_healthy it does not poll for readiness, does not honour a
// timeout, and does not require services to have a healthcheck — it asks compose
// for the set of currently-running services and returns as soon as it gets an
// answer. A transient probe failure (the compose CLI erroring right at the
// `up --wait` boundary) is retried a bounded number of times; a service simply
// not running is not. Intended as a `check:` partner for `docker up` steps and
// as a precondition for service-touching pipeline steps.
//
// With no services it checks the whole project instead (see runWholeProject).
//
// Name mirrors the registered builtin name `containers_running`.
//
//nolint:revive // intentional: type name mirrors the registered builtin name.
type ContainersRunning struct{}

// Validate checks that the with parameters are valid before the pipeline runs.
// An absent or empty services list selects the whole-project mode.
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
	switch len(services) {
	case 0:
		return "check that every compose container is running (or exited 0)"
	case 1:
		return fmt.Sprintf("check that service %q is running", services[0])
	default:
		return fmt.Sprintf("check that %d services are running", len(services))
	}
}

// Run executes the containers_running predicate.
func (ContainersRunning) Run(ctx context.Context, with map[string]any, ectx spec.ExecContext) error {
	services, _ := spec.GetStringSlice(with, "services")

	dockerCfg := ectx.DockerConfig
	if dockerCfg == nil {
		dockerCfg = &config.DockerConfig{}
	}
	compose := docker.NewCompose(ectx.Config, dockerCfg, ectx.ProjectRoot)

	if len(services) == 0 {
		return runWholeProject(ctx, compose)
	}

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

// runWholeProject passes when every non-one-off container of the compose
// project that belongs to a service active in the current config is running
// or exited 0 (a finished one-shot). The expected set is the containers that
// exist, not the config's service list, so `scale: 0` services never fail it
// and a project with no containers at all passes. The active-service filter
// matters because `up --remove-orphans` keeps the stopped containers of
// inactive-profile services.
func runWholeProject(ctx context.Context, compose *docker.Compose) error {
	project := compose.ProjectName
	if project == "" {
		name, err := withProbeRetry(ctx, func() (string, error) { return configProjectName(compose, ctx) })
		if err != nil {
			return fmt.Errorf("%w: %w", errNoProjectName, err)
		}
		if name == "" {
			return errNoProjectName
		}
		project = name
	}

	bin, env := compose.BinName(), compose.BuildEnv()
	query := func(q docker.ProjectContainerQuery) (map[string]bool, error) {
		q.Project = project
		names, err := withProbeRetry(ctx, func() ([]string, error) { return projectContainerNames(ctx, bin, env, q) })
		if err != nil {
			return nil, fmt.Errorf("containers_running: %w", err)
		}
		set := make(map[string]bool, len(names))
		for _, n := range names {
			set[n] = true
		}
		return set, nil
	}

	// A backend that never stamps the oneoff label (some podman-compose
	// versions) would match nothing under oneoff=False; query it unfiltered.
	labelled, err := query(docker.ProjectContainerQuery{Oneoff: docker.OneoffLabelled})
	if err != nil {
		return err
	}
	oneoff := docker.OneoffAny
	if len(labelled) > 0 {
		oneoff = docker.OneoffExcluded
	}

	// all → running → done: a container created between the queries is then
	// simply not evaluated, never misclassified as not running. A state change
	// of an existing container between them is not re-probed — this is a
	// best-effort snapshot taken right after `up --wait`, when the stack has
	// settled; a container restarting in a loop is a candidate either way.
	all, err := query(docker.ProjectContainerQuery{Oneoff: oneoff})
	if err != nil || len(all) == 0 {
		return err
	}
	running, err := query(docker.ProjectContainerQuery{Oneoff: oneoff, State: docker.StateRunning})
	if err != nil {
		return err
	}
	done, err := query(docker.ProjectContainerQuery{Oneoff: oneoff, State: docker.StateExitedZero})
	if err != nil {
		return err
	}
	var candidates []string
	for n := range all {
		if !running[n] && !done[n] {
			candidates = append(candidates, n)
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	active, err := withProbeRetry(ctx, func() ([]string, error) { return configServices(compose, ctx) })
	if err != nil {
		return fmt.Errorf("containers_running: %w", err)
	}
	activeContainers := make(map[string]bool)
	for _, s := range active {
		names, err := query(docker.ProjectContainerQuery{Service: s, Oneoff: oneoff})
		if err != nil {
			return err
		}
		for n := range names {
			activeContainers[n] = true
		}
	}
	var failed []string
	for _, n := range candidates {
		if activeContainers[n] {
			failed = append(failed, n)
		}
	}
	if len(failed) > 0 {
		slices.Sort(failed)
		return fmt.Errorf("containers not running: %s", strings.Join(failed, ", "))
	}
	return nil
}

// runningServicesWithRetry probes the running services with withProbeRetry.
func runningServicesWithRetry(ctx context.Context, compose *docker.Compose, services []string) ([]string, error) {
	return withProbeRetry(ctx, func() ([]string, error) { return compose.RunningServices(ctx, services) })
}

// withProbeRetry runs probe, retrying only a transient command failure (the
// probe itself erroring) — never a successful answer. It is ctx-aware: a
// cancelled context short-circuits the backoff and returns immediately instead
// of retrying.
func withProbeRetry[T any](ctx context.Context, probe func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt <= probeRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(probeRetryBackoff):
			}
		}
		v, err := probe()
		if err == nil {
			return v, nil
		}
		lastErr = err
	}
	return zero, lastErr
}
