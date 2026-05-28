package reset

import (
	"fmt"
	"path/filepath"
	"strings"

	"devbox-cli/internal/core/execution/pipeline"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/usercommands/registry"
)

// ResolvePlan builds the ordered step list from the reset pipeline config.
// Loads devbox/reset.yml and resolves all phases/steps.
// reg (registry) is used to validate files_gate directives and must be non-nil.
func ResolvePlan(cfg *config.DevboxConfig, reg *registry.Registry) ([]pipeline.ResolvedStep, error) {
	_, steps, err := LoadAndResolvePlan(cfg, reg)
	return steps, err
}

// LoadAndResolvePlan loads devbox/reset.yml and resolves its phases.
// Returns the loaded reset config (for inspecting fields like Log) alongside
// the resolved step list.
// reg (registry) is used to validate files_gate directives and must be non-nil.
func LoadAndResolvePlan(cfg *config.DevboxConfig, reg *registry.Registry) (*config.ProjectDeployConfig, []pipeline.ResolvedStep, error) {
	cfgPath, ok := cfg.Raw["__configPath"].(string)
	if !ok {
		return nil, nil, fmt.Errorf("internal: __configPath missing from config")
	}
	baseDir := filepath.Dir(cfgPath)
	resetPath := filepath.Join(baseDir, "devbox", "reset.yml")

	resetCfg, err := config.LoadResetConfig(resetPath)
	if err != nil {
		return nil, nil, fmt.Errorf("loading reset config %s: %w", resetPath, err)
	}

	var result []pipeline.ResolvedStep
	for _, phase := range resetCfg.Phases {
		resolved, err := pipeline.ResolvePhaseSteps(cfg, reg, phase, "")
		if err != nil {
			return nil, nil, err
		}
		result = append(result, resolved...)
	}
	return resetCfg, result, nil
}

// FindStep looks up a step by <phase>/<step> address in the reset config.
func FindStep(cfg *config.DevboxConfig, address string) (config.DeployPhase, config.DeployStep, error) {
	parts := strings.Split(address, "/")
	if len(parts) != 2 {
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("invalid step address %q: expected <phase>/<step>", address)
	}
	phaseName, stepName := parts[0], parts[1]

	cfgPath, ok := cfg.Raw["__configPath"].(string)
	if !ok {
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("internal: __configPath missing from config")
	}
	baseDir := filepath.Dir(cfgPath)
	resetCfg, err := config.LoadResetConfig(filepath.Join(baseDir, "devbox", "reset.yml"))
	if err != nil {
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("loading reset config: %w", err)
	}

	for _, phase := range resetCfg.Phases {
		if phase.Name != phaseName {
			continue
		}
		for _, step := range phase.Steps {
			if step.Name == stepName {
				return phase, step, nil
			}
		}
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("step %q not found in phase %q", stepName, phaseName)
	}
	return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("phase %q not found in reset config", phaseName)
}
