package config

import (
	"fmt"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/filesgate"
	"devbox-cli/internal/filesgate/spec"
	"devbox-cli/internal/usercommands/registry"
	"devbox-cli/internal/validate"
)

type deployFilesGateValidator struct{}

func (v *deployFilesGateValidator) ID() string {
	return "deploy-files-gate"
}

func (v *deployFilesGateValidator) Domain() string {
	return "config"
}

func (v *deployFilesGateValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	if ctx.Cfg == nil {
		return diags
	}

	// If registry is unavailable, emit a self-skip info diagnostic.
	reg, ok := ctx.CommandRegistry.(*registry.Registry)
	if !ok || reg == nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "config",
			Target:   "config.deploy.files-gate",
			File:     relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "devbox", "deploy.yml")),
			Message:  "deploy.yml files_gate validation skipped: command registry not available",
		})
		return diags
	}

	deployPath := filepath.Join(ctx.ProjectRoot, "devbox", "deploy.yml")

	// Iterate through all phases and steps in the deploy config
	for phaseIdx, phase := range ctx.Cfg.Deploy.Phases {
		for stepIdx, step := range phase.Steps {
			if step.FilesGate == nil {
				continue
			}

			// Construct the step reference
			stepRef := filesgate.StepRef{
				Type: step.Type,
				Cmd:  step.Cmd,
				With: step.With,
			}

			// Validate the files_gate directive
			issues := spec.Validate(reg, stepRef, step.FilesGate)
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
	}

	return diags
}

type lifecycleFilesGateValidator struct{}

func (v *lifecycleFilesGateValidator) ID() string {
	return "lifecycle-files-gate"
}

func (v *lifecycleFilesGateValidator) Domain() string {
	return "config"
}

func (v *lifecycleFilesGateValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	if ctx.Cfg == nil {
		return diags
	}

	// If registry is unavailable, emit a self-skip info diagnostic.
	reg, ok := ctx.CommandRegistry.(*registry.Registry)
	if !ok || reg == nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "config",
			Target:   "config.lifecycle.files-gate",
			File:     relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "devbox", "lifecycle.yml")),
			Message:  "lifecycle.yml files_gate validation skipped: command registry not available",
		})
		return diags
	}

	lifecyclePath := filepath.Join(ctx.ProjectRoot, "devbox", "lifecycle.yml")

	lifecycleCfg, err := config.LoadLifecycleConfig(lifecyclePath)
	if err != nil {
		return diags // Silently return if lifecycle config doesn't load
	}

	if lifecycleCfg.Run == nil {
		return diags
	}

	// Iterate through all phases and steps in the lifecycle run config
	for phaseIdx, phase := range lifecycleCfg.Run.Phases {
		for stepIdx, step := range phase.Steps {
			if step.FilesGate == nil {
				continue
			}

			stepRef := filesgate.StepRef{
				Type: step.Type,
				Cmd:  step.Cmd,
				With: step.With,
			}

			issues := spec.Validate(reg, stepRef, step.FilesGate)
			for _, issue := range issues {
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "config",
					Target:   fmt.Sprintf("config.lifecycle.phases[%d].steps[%d].files-gate", phaseIdx, stepIdx),
					File:     relPath(ctx.ProjectRoot, lifecyclePath),
					Message:  issue.Message,
				})
			}
		}
	}

	return diags
}

type resetFilesGateValidator struct{}

func (v *resetFilesGateValidator) ID() string {
	return "reset-files-gate"
}

func (v *resetFilesGateValidator) Domain() string {
	return "config"
}

func (v *resetFilesGateValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	if ctx.Cfg == nil {
		return diags
	}

	// If registry is unavailable, emit a self-skip info diagnostic.
	reg, ok := ctx.CommandRegistry.(*registry.Registry)
	if !ok || reg == nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "config",
			Target:   "config.reset.files-gate",
			File:     relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "devbox", "reset.yml")),
			Message:  "reset.yml files_gate validation skipped: command registry not available",
		})
		return diags
	}

	resetPath := filepath.Join(ctx.ProjectRoot, "devbox", "reset.yml")

	resetCfg, err := config.LoadResetConfig(resetPath)
	if err != nil {
		return diags // Silently return if reset config doesn't load
	}

	// Iterate through all phases and steps in the reset config
	for phaseIdx, phase := range resetCfg.Phases {
		for stepIdx, step := range phase.Steps {
			if step.FilesGate == nil {
				continue
			}

			stepRef := filesgate.StepRef{
				Type: step.Type,
				Cmd:  step.Cmd,
				With: step.With,
			}

			issues := spec.Validate(reg, stepRef, step.FilesGate)
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
	}

	return diags
}
