package config

import (
	"slices"
)

// DeployOrder returns services ordered by deployment dependencies, grouped by type.
// For each type in types (e.g., ["app", "tool", "infra"]):
//   - Filters enabled services of that type
//   - Sorts them by DependsOn relationships (topologically)
//   - Appends to result in order
//
// On dependency cycle for a type group, falls back silently to alphabetic order.
//
// Disabled services are skipped entirely. Service order within each type group is
// deterministic: services without dependencies appear alphabetically, those with
// dependencies appear in topological order relative to their dependents.
func DeployOrder(cfg *DweConfig, types []string) []string {
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
		ordered, err := TopoSortServices(names, cfg.Services)
		if err != nil {
			// Cycle or missing dependency — fall back to alphabetic silently.
			ordered = names
		}

		result = append(result, ordered...)
	}

	return result
}
