package deploy

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"devbox-cli/internal/config"
)

var (
	// ErrDeployCycle is returned by TopoSortByAfter when the after: graph contains a cycle.
	ErrDeployCycle = errors.New("deploy: ordering cycle detected")
	// ErrDeploySelfReference is returned when a service lists itself in after:.
	ErrDeploySelfReference = errors.New("deploy: service references itself in after")
	// ErrDeployUnknownAfterRef is returned when after: references a service that does not exist.
	ErrDeployUnknownAfterRef = errors.New("deploy: after references unknown service")
)

// TopoSortByAfter returns service names in deploy-order (dependencies-first) based
// on the after: field in each DeployConfig. The services parameter is the full
// services map (including services that have no deploy.yml) used to distinguish
// "unknown service" from "service without deploy.yml".
//
// Rules:
//   - Self-reference → ErrDeploySelfReference
//   - Reference to a service not in services map → ErrDeployUnknownAfterRef
//   - Reference to a service that exists but has no deploy.yml → edge silently dropped
//   - Cycle → ErrDeployCycle with the cycle path in the message
//   - Alphabetical tie-break for nodes at the same topo level
func TopoSortByAfter(deploys map[string]*config.DeployConfig, services map[string]config.ServiceConfig) ([]string, error) {
	// Validate all after: references before attempting the sort.
	for name, dc := range deploys {
		for _, dep := range dc.After {
			if dep == name {
				return nil, fmt.Errorf("%w: service %q", ErrDeploySelfReference, name)
			}
			if _, ok := services[dep]; !ok {
				return nil, fmt.Errorf("%w: service %q after: references %q", ErrDeployUnknownAfterRef, name, dep)
			}
		}
	}

	// Build adjacency: only include edges where the dependency also has a deploy.yml.
	adj := make(map[string][]string, len(deploys))
	for name, dc := range deploys {
		var deps []string
		for _, dep := range dc.After {
			if deploys[dep] != nil {
				deps = append(deps, dep)
			}
		}
		sort.Strings(deps)
		adj[name] = deps
	}

	// Collect and sort all deploy node names for deterministic iteration.
	allNames := make([]string, 0, len(deploys))
	for name := range deploys {
		allNames = append(allNames, name)
	}
	sort.Strings(allNames)

	// DFS topo sort. States: 0=unvisited, 1=in-progress, 2=done.
	state := make(map[string]int, len(deploys))
	path := make([]string, 0)
	var order []string

	var visit func(name string) error
	visit = func(name string) error {
		if state[name] == 2 {
			return nil
		}
		if state[name] == 1 {
			// Find cycle in path and report it.
			start := -1
			for i, n := range path {
				if n == name {
					start = i
					break
				}
			}
			cycle := make([]string, len(path[start:]), len(path[start:])+1)
			copy(cycle, path[start:])
			cycle = append(cycle, name)
			return fmt.Errorf("%w: %s", ErrDeployCycle, strings.Join(cycle, " → "))
		}
		state[name] = 1
		path = append(path, name)
		for _, dep := range adj[name] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[name] = 2
		order = append(order, name)
		return nil
	}

	for _, name := range allNames {
		if err := visit(name); err != nil {
			return nil, err
		}
	}

	return order, nil
}
