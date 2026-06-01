package deploy

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
)

// ErrServiceNoDeployFile is returned by ResolveServicePlan when the named
// service exists but has no deploy.yml in workspace/services/<name>/deploy.yml.
var ErrServiceNoDeployFile = errors.New("deploy: service has no deploy pipeline")

// ResolveServicePlan builds the step list for a single named service.
// Used by --service flag to deploy only one service.
// reg (registry) is used to validate files_gate directives.
func ResolveServicePlan(cfg *config.DweConfig, reg *registry.Registry, serviceName string) ([]pipeline.ResolvedStep, error) {
	implicit := pipeline.ResolvedStep{
		Phase: config.DeployPhase{Name: "env", Description: "Environment"},
		Step:  ImplicitEnvStep,
	}
	result := []pipeline.ResolvedStep{implicit}

	cfgPath, ok := cfg.Raw["__configPath"].(string)
	if !ok {
		return nil, fmt.Errorf("internal: __configPath missing from config")
	}
	baseDir := filepath.Dir(cfgPath)
	svc, declared := cfg.Services[serviceName]
	if !declared {
		return nil, fmt.Errorf("service %q is not declared in workspace/services/<name>/service.yml", serviceName)
	}
	svcDeploys, err := config.LoadServiceDeployConfigs(baseDir, map[string]config.ServiceConfig{
		serviceName: svc,
	})
	if err != nil {
		return nil, err
	}
	svcDeploy, ok := svcDeploys[serviceName]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrServiceNoDeployFile, serviceName)
	}

	for _, phase := range svcDeploy.Phases {
		resolved, err := pipeline.ResolvePhaseSteps(cfg, reg, phase, serviceName)
		if err != nil {
			return nil, err
		}
		result = append(result, resolved...)
	}
	return result, nil
}

// ResolveServicesPlan loads all per-service deploy pipelines, sorts them
// by deploy-ordering (after: field), and returns their steps inlined.
// reg (registry) is used to validate files_gate directives.
func ResolveServicesPlan(cfg *config.DweConfig, reg *registry.Registry) ([]pipeline.ResolvedStep, error) {
	cfgPath, ok := cfg.Raw["__configPath"].(string)
	if !ok {
		return nil, fmt.Errorf("internal: __configPath missing from config")
	}
	baseDir := filepath.Dir(cfgPath)

	enabled := make(map[string]config.ServiceConfig)
	for name, svc := range cfg.Services {
		if svc.Enabled {
			enabled[name] = svc
		}
	}

	svcDeploys, err := config.LoadServiceDeployConfigs(baseDir, enabled)
	if err != nil {
		return nil, err
	}
	if len(svcDeploys) == 0 {
		return nil, nil
	}

	// Use after:-based deploy ordering (not runtime depends_on: ordering).
	sorted, err := topoSortServiceDeploys(svcDeploys, cfg.Services)
	if err != nil {
		return nil, err
	}

	var result []pipeline.ResolvedStep
	for _, name := range sorted {
		svcDeploy := svcDeploys[name]
		for _, phase := range svcDeploy.Phases {
			resolved, err := pipeline.ResolvePhaseSteps(cfg, reg, phase, name)
			if err != nil {
				return nil, err
			}
			result = append(result, resolved...)
		}
	}
	return result, nil
}

// ResolveServicesPlanSubset resolves deploy steps for a named subset of services.
// deploys must contain deploy configs for exactly the named services (keyed by name).
// The result has exactly one ImplicitEnvStep at position 0, followed by
// per-service phase steps in after:-dependency order within the subset.
// References to services outside the subset are dropped from the sort graph
// (no transitive closure — subset deploys are explicit-intent only).
func ResolveServicesPlanSubset(
	cfg *config.DweConfig,
	reg *registry.Registry,
	deploys map[string]*config.ServiceDeployConfig,
	names []string,
) ([]pipeline.ResolvedStep, error) {
	// Validate all names have deploy.yml.
	for _, name := range names {
		if deploys[name] == nil {
			return nil, fmt.Errorf("%w: %s", ErrServiceNoDeployFile, name)
		}
	}

	// Build a filtered deploy map containing only the requested names,
	// with after: references to services outside the subset dropped.
	// This implements the "no transitive closure" rule for subset deploys.
	inSubset := make(map[string]bool, len(names))
	for _, name := range names {
		inSubset[name] = true
	}
	filtered := make(map[string]*config.ServiceDeployConfig, len(names))
	for _, name := range names {
		orig := deploys[name]
		if len(orig.After) == 0 {
			filtered[name] = orig
			continue
		}
		var subsetAfter []string
		for _, dep := range orig.After {
			if inSubset[dep] {
				subsetAfter = append(subsetAfter, dep)
			}
		}
		if len(subsetAfter) == len(orig.After) {
			// No references dropped; reuse the original.
			filtered[name] = orig
		} else {
			// Copy with filtered After list.
			cp := *orig
			cp.After = subsetAfter
			filtered[name] = &cp
		}
	}

	// Topo-sort the subset.
	sorted, err := topoSortServiceDeploys(filtered, cfg.Services)
	if err != nil {
		return nil, err
	}

	// Prepend exactly one ImplicitEnvStep.
	implicit := pipeline.ResolvedStep{
		Phase: config.DeployPhase{Name: "env", Description: "Environment"},
		Step:  ImplicitEnvStep,
	}
	result := []pipeline.ResolvedStep{implicit}

	// Resolve each service's phases in sorted order.
	for _, name := range sorted {
		svcDeploy := deploys[name]
		for _, phase := range svcDeploy.Phases {
			resolved, err := pipeline.ResolvePhaseSteps(cfg, reg, phase, name)
			if err != nil {
				return nil, err
			}
			result = append(result, resolved...)
		}
	}
	return result, nil
}

// topoSortServiceDeploys wraps TopoSortByAfter to work with ServiceDeployConfig maps.
// Converts the service-specific type to the generic DeployConfig type for sorting.
func topoSortServiceDeploys(deploys map[string]*config.ServiceDeployConfig, services map[string]config.ServiceConfig) ([]string, error) {
	// Build a generic DeployConfig map with the same After values.
	genericDeploys := make(map[string]*config.DeployConfig, len(deploys))
	for name, sdc := range deploys {
		dc := &config.DeployConfig{
			After:  sdc.After,
			Log:    sdc.Log,
			Phases: sdc.Phases,
		}
		genericDeploys[name] = dc
	}
	return TopoSortByAfter(genericDeploys, services)
}
