package config

import (
	"fmt"
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate"
	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate/spec"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// validateFilesGatePhases walks the given phases and validates every step's
// files_gate (leaf and parallel sub-steps) against the command registry. Each
// diagnostic's Target is "<targetPrefix>.phases[i].steps[j].files-gate" (and the
// "...parallel.steps[k]..." variant for sub-steps). The diagnostics are
// byte-identical to the per-validator walkers this replaces.
func validateFilesGatePhases(cfg *config.DweConfig, reg *registry.Registry, phases []config.DeployPhase, file, targetPrefix string) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for phaseIdx, phase := range phases {
		for stepIdx, step := range phase.Steps {
			if step.FilesGate != nil {
				stepRef := filesgate.StepRef{
					Type: step.Type,
					Cmd:  step.Cmd,
					With: step.With,
				}
				issues := spec.Validate(cfg, reg, stepRef, step.FilesGate)
				for _, issue := range issues {
					diags = append(diags, validate.Diagnostic{
						Severity: validate.SeverityError,
						Domain:   "config",
						Target:   fmt.Sprintf("%s.phases[%d].steps[%d].files-gate", targetPrefix, phaseIdx, stepIdx),
						File:     file,
						Message:  issue.Message,
					})
				}
			}
			if step.Parallel != nil {
				for subIdx, sub := range step.Parallel.Steps {
					if sub.FilesGate == nil {
						continue
					}
					stepRef := filesgate.StepRef{
						Type: sub.Type,
						Cmd:  sub.Cmd,
						With: sub.With,
					}
					issues := spec.Validate(cfg, reg, stepRef, sub.FilesGate)
					for _, issue := range issues {
						diags = append(diags, validate.Diagnostic{
							Severity: validate.SeverityError,
							Domain:   "config",
							Target:   fmt.Sprintf("%s.phases[%d].steps[%d].parallel.steps[%d].files-gate", targetPrefix, phaseIdx, stepIdx, subIdx),
							File:     file,
							Message:  issue.Message,
						})
					}
				}
			}
		}
	}
	return diags
}

// deployHasFilesGateSteps reports whether any step in the deploy config (project or service) uses files_gate.
func deployHasFilesGateSteps(ctx validate.Context) bool {
	for _, phase := range ctx.Cfg.Deploy.Phases {
		for _, step := range phase.Steps {
			if step.FilesGate != nil {
				return true
			}
			if step.Parallel != nil {
				for _, sub := range step.Parallel.Steps {
					if sub.FilesGate != nil {
						return true
					}
				}
			}
		}
	}
	if len(ctx.Cfg.Services) > 0 {
		svcDeploys, err := config.LoadServiceDeployConfigs(ctx.ProjectRoot, ctx.Cfg.Services)
		if err == nil {
			for _, svcDeploy := range svcDeploys {
				for _, phase := range svcDeploy.Phases {
					for _, step := range phase.Steps {
						if step.FilesGate != nil {
							return true
						}
						if step.Parallel != nil {
							for _, sub := range step.Parallel.Steps {
								if sub.FilesGate != nil {
									return true
								}
							}
						}
					}
				}
			}
		}
	}
	return false
}

type deployFilesGateValidator struct{}

func (v *deployFilesGateValidator) ID() string {
	return "deploy"
}

func (v *deployFilesGateValidator) Domain() string {
	return "config"
}

func (v *deployFilesGateValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	if ctx.Cfg == nil {
		return diags
	}

	reg := registryFrom(ctx)
	if reg == nil {
		// Only emit a self-skip diagnostic when the project actually uses files_gate.
		if deployHasFilesGateSteps(ctx) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.deploy.files-gate",
				File:     relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "workspace", "deploy.yml")),
				Message:  "deploy.yml files_gate validation skipped: command registry not available",
			})
		}
		return diags
	}

	deployPath := filepath.Join(ctx.ProjectRoot, "workspace", "deploy.yml")

	// Iterate through all phases and steps in the deploy config
	diags = append(diags, validateFilesGatePhases(ctx.Cfg, reg, ctx.Cfg.Deploy.Phases,
		relPath(ctx.ProjectRoot, deployPath), "config.deploy")...)

	// Also validate per-service deploy files.
	if ctx.Cfg != nil && len(ctx.Cfg.Services) > 0 {
		svcDeploys, err := config.LoadServiceDeployConfigs(ctx.ProjectRoot, ctx.Cfg.Services)
		if err == nil {
			for svcName, svcDeploy := range svcDeploys {
				svcDeployPath := filepath.Join(ctx.ProjectRoot, "workspace", "services", svcName, "deploy.yml")
				diags = append(diags, validateFilesGatePhases(ctx.Cfg, reg, svcDeploy.Phases,
					relPath(ctx.ProjectRoot, svcDeployPath), fmt.Sprintf("config.service-deploy[%s]", svcName))...)
			}
		}
	}

	return diags
}

// phasesHaveFilesGateSteps reports whether any step in the given phases uses files_gate.
func phasesHaveFilesGateSteps(phases []config.DeployPhase) bool {
	for _, phase := range phases {
		for _, step := range phase.Steps {
			if step.FilesGate != nil {
				return true
			}
			if step.Parallel != nil {
				for _, sub := range step.Parallel.Steps {
					if sub.FilesGate != nil {
						return true
					}
				}
			}
		}
	}
	return false
}

// lifecycleHasFilesGateSteps reports whether any step in the lifecycle config uses files_gate.
func lifecycleHasFilesGateSteps(lifecycleCfg *config.LifecycleConfig) bool {
	if lifecycleCfg == nil {
		return false
	}
	if lifecycleCfg.Run != nil && phasesHaveFilesGateSteps(lifecycleCfg.Run.Phases) {
		return true
	}
	if lifecycleCfg.Stop != nil && phasesHaveFilesGateSteps(lifecycleCfg.Stop.Phases) {
		return true
	}
	return false
}

type lifecycleFilesGateValidator struct{}

func (v *lifecycleFilesGateValidator) ID() string {
	return "lifecycle"
}

func (v *lifecycleFilesGateValidator) Domain() string {
	return "config"
}

func (v *lifecycleFilesGateValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	if ctx.Cfg == nil {
		return diags
	}

	lifecyclePath := filepath.Join(ctx.ProjectRoot, "workspace", "lifecycle.yml")

	lifecycleCfg, err := config.LoadLifecycleConfig(lifecyclePath)
	if err != nil {
		return diags // Silently return if lifecycle config doesn't load
	}

	reg := registryFrom(ctx)
	if reg == nil {
		// Only emit a self-skip diagnostic when the project actually uses files_gate.
		if lifecycleHasFilesGateSteps(lifecycleCfg) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.lifecycle.files-gate",
				File:     relPath(ctx.ProjectRoot, lifecyclePath),
				Message:  "lifecycle.yml files_gate validation skipped: command registry not available",
			})
		}
		return diags
	}

	file := relPath(ctx.ProjectRoot, lifecyclePath)
	if lifecycleCfg.Run != nil {
		diags = append(diags, validateFilesGatePhases(ctx.Cfg, reg, lifecycleCfg.Run.Phases, file, "config.lifecycle.run")...)
	}
	if lifecycleCfg.Stop != nil {
		diags = append(diags, validateFilesGatePhases(ctx.Cfg, reg, lifecycleCfg.Stop.Phases, file, "config.lifecycle.stop")...)
	}

	return diags
}

type resetFilesGateValidator struct{}

func (v *resetFilesGateValidator) ID() string {
	return "reset"
}

func (v *resetFilesGateValidator) Domain() string {
	return "config"
}

func (v *resetFilesGateValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	if ctx.Cfg == nil {
		return diags
	}

	resetPath := filepath.Join(ctx.ProjectRoot, "workspace", "reset.yml")

	resetCfg, err := config.LoadResetConfig(resetPath)
	if err != nil {
		return diags // Silently return if reset config doesn't load
	}

	reg := registryFrom(ctx)
	if reg == nil {
		// Only emit a self-skip diagnostic when the project actually uses files_gate.
		if phasesHaveFilesGateSteps(resetCfg.Phases) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.reset.files-gate",
				File:     relPath(ctx.ProjectRoot, resetPath),
				Message:  "reset.yml files_gate validation skipped: command registry not available",
			})
		}
		return diags
	}

	// Iterate through all phases and steps in the reset config
	diags = append(diags, validateFilesGatePhases(ctx.Cfg, reg, resetCfg.Phases,
		relPath(ctx.ProjectRoot, resetPath), "config.reset")...)

	return diags
}
