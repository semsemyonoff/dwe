package deploy

import (
	"fmt"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/pipeline"
	"devbox-cli/internal/usercommands/registry"
)

// ResolveServicePlan builds the step list for a single named service.
// Used by --service flag to deploy only one service.
// reg (registry) is used to validate files_gate directives.
func ResolveServicePlan(cfg *config.DevboxConfig, reg *registry.Registry, serviceName string) ([]pipeline.ResolvedStep, error) {
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
		return nil, fmt.Errorf("service %q is not declared in devbox/services/<name>/service.yml", serviceName)
	}
	if !svc.IsApp() {
		return nil, fmt.Errorf("%w: service %q has type %s", config.ErrDeployTargetNotApp, serviceName, svc.Type)
	}
	svcDeploys, err := config.LoadServiceDeployConfigs(baseDir, map[string]config.ServiceConfig{
		serviceName: svc,
	})
	if err != nil {
		return nil, err
	}
	svcDeploy, ok := svcDeploys[serviceName]
	if !ok {
		return nil, fmt.Errorf("no deploy pipeline found for service %q (expected devbox/services/%s/deploy.yml)", serviceName, serviceName)
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
// by dependency order, and returns their steps inlined.
// reg (registry) is used to validate files_gate directives.
func ResolveServicesPlan(cfg *config.DevboxConfig, reg *registry.Registry) ([]pipeline.ResolvedStep, error) {
	cfgPath, ok := cfg.Raw["__configPath"].(string)
	if !ok {
		return nil, fmt.Errorf("internal: __configPath missing from config")
	}
	baseDir := filepath.Dir(cfgPath)

	// Deploy enumeration is restricted to app-typed services: tool/infra
	// services may be Enabled (and show up in compose) but never deployable.
	enabled := make(map[string]config.ServiceConfig)
	for name, svc := range cfg.Services {
		if svc.Enabled && svc.IsApp() {
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

	var names []string
	for name := range svcDeploys {
		names = append(names, name)
	}
	sorted, err := config.TopoSortServices(names, cfg.Services)
	if err != nil {
		return nil, err
	}

	var result []pipeline.ResolvedStep
	for _, name := range sorted {
		deploy := svcDeploys[name]
		for _, phase := range deploy.Phases {
			resolved, err := pipeline.ResolvePhaseSteps(cfg, reg, phase, name)
			if err != nil {
				return nil, err
			}
			result = append(result, resolved...)
		}
	}
	return result, nil
}
