package deploy

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
)

// TrackedServices returns the canonical tracked-service list derived from a resolved plan.
// A service is tracked iff it appears in the plan with non-empty rs.Service.
// Returns sorted service names for deterministic ordering.
func TrackedServices(plan []pipeline.ResolvedStep) []string {
	seen := make(map[string]bool)
	for _, rs := range plan {
		if rs.Service != "" {
			seen[rs.Service] = true
		}
	}

	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// LoadTrackedServices is a convenience helper that:
//  1. Resolves the deploy plan
//  2. Extracts the tracked-service list
//  3. Loads service deploy configs for tracked services only
//
// Returns: tracked service names, service deploy configs (keyed by service name), error.
// reg (registry) is used to validate files_gate directives and must be non-nil.
func LoadTrackedServices(cfg *config.DweConfig, reg *registry.Registry, baseDir string) ([]string, map[string]*config.ServiceDeployConfig, error) {
	// Resolve the full deploy plan to find which services are tracked.
	// Substitute the built-in default pipeline when the project omits
	// workspace/deploy.yml — mirrors the deploy entrypoint's contract so
	// read-only callers (status, root summary, lifecycle gate) see the
	// same tracked set as a real `dwe deploy run`.
	ensuredDeploy, _ := EnsureDeployConfig(cfg.Deploy)
	origDeploy := cfg.Deploy
	cfg.Deploy = ensuredDeploy
	plan, err := ResolvePlan(cfg, reg)
	cfg.Deploy = origDeploy
	if err != nil {
		return nil, nil, fmt.Errorf("resolving deploy plan: %w", err)
	}

	tracked := TrackedServices(plan)

	// Load service deploy configs for tracked services only
	svcDeploys := make(map[string]*config.ServiceDeployConfig)
	for _, name := range tracked {
		svcPath := filepath.Join(baseDir, "workspace", "services", name, "deploy.yml")
		svcDeploy, err := config.LoadServiceDeployConfig(svcPath)
		if err != nil {
			// Tracked services always have deploy files (discovered by ResolvePlan),
			// so any error here is a real problem.
			return nil, nil, fmt.Errorf("loading deploy config for service %q: %w", name, err)
		}
		svcDeploys[name] = svcDeploy
	}

	return tracked, svcDeploys, nil
}
