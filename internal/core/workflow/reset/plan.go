package reset

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/devbox/internal/core/execution/pipeline"
	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/core/usercommands/registry"
)

// ResolvePlan builds the ordered step list from the reset pipeline config.
// Loads devbox/reset.yml and resolves all phases/steps. When the file is
// absent, the built-in default pipeline is used.
// reg (registry) is used to validate files_gate directives and must be non-nil.
func ResolvePlan(cfg *config.DevboxConfig, reg *registry.Registry) ([]pipeline.ResolvedStep, error) {
	_, steps, _, err := LoadAndResolvePlan(cfg, reg)
	return steps, err
}

// LoadAndResolvePlan loads devbox/reset.yml and resolves its phases.
// Returns the loaded reset config (for inspecting fields like Log), the
// resolved step list, and whether the built-in default pipeline was used.
// When the file is absent, the built-in default pipeline is used.
// reg (registry) is used to validate files_gate directives and must be non-nil.
func LoadAndResolvePlan(cfg *config.DevboxConfig, reg *registry.Registry) (*config.ProjectDeployConfig, []pipeline.ResolvedStep, bool, error) {
	cfgPath, ok := cfg.Raw["__configPath"].(string)
	if !ok {
		return nil, nil, false, fmt.Errorf("internal: __configPath missing from config")
	}
	baseDir := filepath.Dir(cfgPath)
	resetPath := filepath.Join(baseDir, "devbox", "reset.yml")

	loaded, err := config.LoadResetConfig(resetPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		loaded = nil
	case err != nil:
		return nil, nil, false, fmt.Errorf("load reset config: %w", err)
	}

	resetCfg, defaulted := EnsureResetConfig(loaded)

	var result []pipeline.ResolvedStep
	for _, phase := range resetCfg.Phases {
		resolved, err := pipeline.ResolvePhaseSteps(cfg, reg, phase, "")
		if err != nil {
			return nil, nil, false, err
		}
		result = append(result, resolved...)
	}
	return resetCfg, result, defaulted, nil
}

// FindStep looks up a step by <phase>/<step> address in the reset config.
// When the reset.yml file is absent, the built-in default pipeline is searched.
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

	loaded, err := config.LoadResetConfig(filepath.Join(baseDir, "devbox", "reset.yml"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		loaded = nil
	case err != nil:
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("loading reset config: %w", err)
	}

	resetCfg, _ := EnsureResetConfig(loaded)

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
