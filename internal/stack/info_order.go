package stack

import (
	"fmt"
	"os"
	"slices"

	"devbox-cli/internal/config"
)

// DeployOrder returns services ordered by deployment dependencies, grouped by type.
// For each type in types (e.g., ["app", "tool", "infra"]):
//   - Filters enabled services of that type
//   - Sorts them by DependsOn relationships (topologically)
//   - Appends to result in order
//
// On dependency cycle for a type group, falls back silently to alphabetic order and
// logs the error to stderr (respecting the renderer-signature contract of silent fallback).
//
// Disabled services are skipped entirely. Service order within each type group is
// deterministic: services without dependencies appear alphabetically, those with
// dependencies appear in topological order relative to their dependents.
func DeployOrder(cfg *config.DevboxConfig, types []string) []string {
	if cfg == nil || cfg.Services == nil {
		return nil
	}

	var result []string

	for _, svcType := range types {
		// Collect enabled services of this type.
		var names []string
		for name, svc := range cfg.Services {
			if svc.Enabled && string(svc.Type) == svcType {
				names = append(names, name)
			}
		}

		// Alphabetically pre-sort for determinism before topological sort.
		slices.Sort(names)

		// Topologically sort by DependsOn.
		ordered, err := config.TopoSortServices(names, cfg.Services)
		if err != nil {
			// Cycle or missing dependency. Fall back to alphabetic silently
			// and log to stderr so debuggable but doesn't interrupt rendering.
			fmt.Fprintf(os.Stderr, "warning: service ordering for type %q: %v; using alphabetic fallback\n", svcType, err)
			ordered = names
		}

		result = append(result, ordered...)
	}

	return result
}
