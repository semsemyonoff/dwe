package config

import (
	"fmt"
	"path/filepath"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/filesgate"
	"devbox-cli/internal/filesgate/spec"
	"devbox-cli/internal/usercommands/registry"
	"devbox-cli/internal/validate"
)

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

	reg, ok := ctx.CommandRegistry.(*registry.Registry)
	if !ok || reg == nil {
		// Only emit a self-skip diagnostic when the project actually uses files_gate.
		if deployHasFilesGateSteps(ctx) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.deploy.files-gate",
				File:     relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "devbox", "deploy.yml")),
				Message:  "deploy.yml files_gate validation skipped: command registry not available",
			})
		}
		return diags
	}

	deployPath := filepath.Join(ctx.ProjectRoot, "devbox", "deploy.yml")

	// Iterate through all phases and steps in the deploy config
	for phaseIdx, phase := range ctx.Cfg.Deploy.Phases {
		for stepIdx, step := range phase.Steps {
			if step.FilesGate != nil {
				stepRef := filesgate.StepRef{
					Type: step.Type,
					Cmd:  step.Cmd,
					With: step.With,
				}
				issues := spec.Validate(ctx.Cfg, reg, stepRef, step.FilesGate)
				for _, issue := range issues {
					diags = append(diags, validate.Diagnostic{
						Severity: validate.SeverityError,
						Domain:   "config",
						Target:   fmt.Sprintf("config.deploy.phases[%d].steps[%d].files-gate", phaseIdx, stepIdx),
						File:     relPath(ctx.ProjectRoot, deployPath),
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
					issues := spec.Validate(ctx.Cfg, reg, stepRef, sub.FilesGate)
					for _, issue := range issues {
						diags = append(diags, validate.Diagnostic{
							Severity: validate.SeverityError,
							Domain:   "config",
							Target:   fmt.Sprintf("config.deploy.phases[%d].steps[%d].parallel.steps[%d].files-gate", phaseIdx, stepIdx, subIdx),
							File:     relPath(ctx.ProjectRoot, deployPath),
							Message:  issue.Message,
						})
					}
				}
			}
		}
	}

	// Also validate per-service deploy files.
	if ctx.Cfg != nil && len(ctx.Cfg.Services) > 0 {
		svcDeploys, err := config.LoadServiceDeployConfigs(ctx.ProjectRoot, ctx.Cfg.Services)
		if err == nil {
			for svcName, svcDeploy := range svcDeploys {
				svcDeployPath := filepath.Join(ctx.ProjectRoot, "devbox", "deploy", svcName+".yml")
				for phaseIdx, phase := range svcDeploy.Phases {
					for stepIdx, step := range phase.Steps {
						if step.FilesGate != nil {
							stepRef := filesgate.StepRef{
								Type: step.Type,
								Cmd:  step.Cmd,
								With: step.With,
							}
							issues := spec.Validate(ctx.Cfg, reg, stepRef, step.FilesGate)
							for _, issue := range issues {
								diags = append(diags, validate.Diagnostic{
									Severity: validate.SeverityError,
									Domain:   "config",
									Target:   fmt.Sprintf("config.service-deploy[%s].phases[%d].steps[%d].files-gate", svcName, phaseIdx, stepIdx),
									File:     relPath(ctx.ProjectRoot, svcDeployPath),
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
								issues := spec.Validate(ctx.Cfg, reg, stepRef, sub.FilesGate)
								for _, issue := range issues {
									diags = append(diags, validate.Diagnostic{
										Severity: validate.SeverityError,
										Domain:   "config",
										Target:   fmt.Sprintf("config.service-deploy[%s].phases[%d].steps[%d].parallel.steps[%d].files-gate", svcName, phaseIdx, stepIdx, subIdx),
										File:     relPath(ctx.ProjectRoot, svcDeployPath),
										Message:  issue.Message,
									})
								}
							}
						}
					}
				}
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

	lifecyclePath := filepath.Join(ctx.ProjectRoot, "devbox", "lifecycle.yml")

	lifecycleCfg, err := config.LoadLifecycleConfig(lifecyclePath)
	if err != nil {
		return diags // Silently return if lifecycle config doesn't load
	}

	reg, ok := ctx.CommandRegistry.(*registry.Registry)
	if !ok || reg == nil {
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

	validateLifecyclePhases := func(pipelineName string, phases []config.DeployPhase) {
		for phaseIdx, phase := range phases {
			for stepIdx, step := range phase.Steps {
				if step.FilesGate != nil {
					stepRef := filesgate.StepRef{
						Type: step.Type,
						Cmd:  step.Cmd,
						With: step.With,
					}
					issues := spec.Validate(ctx.Cfg, reg, stepRef, step.FilesGate)
					for _, issue := range issues {
						diags = append(diags, validate.Diagnostic{
							Severity: validate.SeverityError,
							Domain:   "config",
							Target:   fmt.Sprintf("config.lifecycle.%s.phases[%d].steps[%d].files-gate", pipelineName, phaseIdx, stepIdx),
							File:     relPath(ctx.ProjectRoot, lifecyclePath),
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
						issues := spec.Validate(ctx.Cfg, reg, stepRef, sub.FilesGate)
						for _, issue := range issues {
							diags = append(diags, validate.Diagnostic{
								Severity: validate.SeverityError,
								Domain:   "config",
								Target:   fmt.Sprintf("config.lifecycle.%s.phases[%d].steps[%d].parallel.steps[%d].files-gate", pipelineName, phaseIdx, stepIdx, subIdx),
								File:     relPath(ctx.ProjectRoot, lifecyclePath),
								Message:  issue.Message,
							})
						}
					}
				}
			}
		}
	}

	if lifecycleCfg.Run != nil {
		validateLifecyclePhases("run", lifecycleCfg.Run.Phases)
	}
	if lifecycleCfg.Stop != nil {
		validateLifecyclePhases("stop", lifecycleCfg.Stop.Phases)
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

	resetPath := filepath.Join(ctx.ProjectRoot, "devbox", "reset.yml")

	resetCfg, err := config.LoadResetConfig(resetPath)
	if err != nil {
		return diags // Silently return if reset config doesn't load
	}

	reg, ok := ctx.CommandRegistry.(*registry.Registry)
	if !ok || reg == nil {
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
	for phaseIdx, phase := range resetCfg.Phases {
		for stepIdx, step := range phase.Steps {
			if step.FilesGate != nil {
				stepRef := filesgate.StepRef{
					Type: step.Type,
					Cmd:  step.Cmd,
					With: step.With,
				}
				issues := spec.Validate(ctx.Cfg, reg, stepRef, step.FilesGate)
				for _, issue := range issues {
					diags = append(diags, validate.Diagnostic{
						Severity: validate.SeverityError,
						Domain:   "config",
						Target:   fmt.Sprintf("config.reset.phases[%d].steps[%d].files-gate", phaseIdx, stepIdx),
						File:     relPath(ctx.ProjectRoot, resetPath),
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
					issues := spec.Validate(ctx.Cfg, reg, stepRef, sub.FilesGate)
					for _, issue := range issues {
						diags = append(diags, validate.Diagnostic{
							Severity: validate.SeverityError,
							Domain:   "config",
							Target:   fmt.Sprintf("config.reset.phases[%d].steps[%d].parallel.steps[%d].files-gate", phaseIdx, stepIdx, subIdx),
							File:     relPath(ctx.ProjectRoot, resetPath),
							Message:  issue.Message,
						})
					}
				}
			}
		}
	}

	return diags
}
