package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/workflow/deploy"
	"devbox-cli/internal/validate"
)

type deployAfterValidator struct{}

func (v *deployAfterValidator) ID() string {
	return "deploy-after"
}

func (v *deployAfterValidator) Domain() string {
	return "config"
}

func (v *deployAfterValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	// --- Scope-limit rules: after: must not appear in project-wide deploy/reset or per-service reset ---

	projectDeployPath := filepath.Join(ctx.ProjectRoot, "devbox", "deploy.yml")
	diags = append(diags, checkAfterFieldNotAllowed(ctx, projectDeployPath,
		"config.deploy",
		`"after" is only valid in devbox/services/<name>/deploy.yml; remove from project-wide deploy.yml`,
	)...)

	projectResetPath := filepath.Join(ctx.ProjectRoot, "devbox", "reset.yml")
	diags = append(diags, checkAfterFieldNotAllowed(ctx, projectResetPath,
		"config.reset",
		`"after" is only valid in devbox/services/<name>/deploy.yml; remove from reset.yml`,
	)...)

	// Per-service reset.yml files.
	servicesDir := filepath.Join(ctx.ProjectRoot, "devbox", "services")
	serviceEntries, err := os.ReadDir(servicesDir)
	if err != nil && !errors.Is(err, errNotExist) {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.deploy-after",
			File:     relPath(ctx.ProjectRoot, servicesDir),
			Message:  err.Error(),
		})
		return diags
	}

	for _, entry := range serviceEntries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		resetPath := filepath.Join(servicesDir, name, "reset.yml")
		diags = append(diags, checkAfterFieldNotAllowed(ctx, resetPath,
			"config.services-reset:"+name,
			fmt.Sprintf(`"after" is only valid in devbox/services/<name>/deploy.yml; remove from %s/reset.yml`, name),
		)...)
	}

	// --- Cross-service after: validation for per-service deploy configs ---

	// Determine the services map.
	var services map[string]config.ServiceConfig
	if ctx.Cfg != nil && ctx.Cfg.Services != nil {
		services = ctx.Cfg.Services
	} else {
		loaded, err := config.LoadServices(ctx.ProjectRoot)
		if err != nil {
			// Can't cross-reference; skip.
			return diags
		}
		services = loaded
	}

	if len(services) == 0 {
		if len(diags) == 0 {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityOK,
				Domain:   "config",
				Target:   "config.deploy-after",
			})
		}
		return diags
	}

	// Load all per-service deploy configs using the parse-only variant so we can
	// inspect after: values even if the validator has not yet run the loader split.
	svcDeploys := make(map[string]*config.DeployConfig, len(services))
	for name := range services {
		depPath := filepath.Join(servicesDir, name, "deploy.yml")
		dc, err := config.ParseDeployConfigForValidation(depPath)
		if err != nil {
			if errors.Is(err, errNotExist) {
				// No deploy.yml for this service — that's fine.
				continue
			}
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.services-deploy:" + name,
				File:     relPath(ctx.ProjectRoot, depPath),
				Message:  fmt.Sprintf("load deploy.yml: %v", err),
			})
			continue
		}
		svcDeploys[name] = dc
	}

	// Per-service after: rules.
	var perDiags []validate.Diagnostic

	for name, dc := range svcDeploys {
		if len(dc.After) == 0 {
			continue
		}
		depPath := filepath.Join(servicesDir, name, "deploy.yml")
		file := relPath(ctx.ProjectRoot, depPath)
		target := "config.services-deploy:" + name

		emit := func(d validate.Diagnostic) {
			d.Domain = "config"
			d.File = file
			d.Target = target
			perDiags = append(perDiags, d)
		}

		for _, ref := range dc.After {
			if ref == name {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Message:  fmt.Sprintf("service %q after: references itself", name),
				})
				continue
			}

			refSvc, exists := services[ref]
			if !exists {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Message:  fmt.Sprintf("service %q after: references unknown service %q", name, ref),
				})
				continue
			}

			if svcDeploys[ref] == nil {
				emit(validate.Diagnostic{
					Severity: validate.SeverityWarning,
					Message:  fmt.Sprintf("service %q after: references %q which has no deploy.yml; ordering constraint will be ignored", name, ref),
				})
				continue
			}

			if !refSvc.Enabled {
				emit(validate.Diagnostic{
					Severity: validate.SeverityWarning,
					Message:  fmt.Sprintf("service %q after: references %q which is disabled; the constraint will not trigger because %q will not deploy", name, ref, ref),
				})
			}

			// Required services always deploy before optional ones; an after:
			// edge from required → optional inverts that and is invalid.
			if services[name].Required && !refSvc.Required {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Message:  fmt.Sprintf("required service %q after: references optional service %q; required services always deploy before optional ones", name, ref),
				})
			}
		}
	}

	// Cycle detection across the whole service deploy graph.
	if len(svcDeploys) > 0 {
		_, err := deploy.TopoSortByAfter(svcDeploys, services)
		if err != nil {
			switch {
			case errors.Is(err, deploy.ErrDeployCycle):
				perDiags = append(perDiags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "config",
					Target:   "config.deploy-after",
					Message:  err.Error(),
				})
			case errors.Is(err, deploy.ErrDeploySelfReference):
				// Per-rule checks above already emit these; safety net only if bypassed.
				perDiags = append(perDiags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "config",
					Target:   "config.deploy-after",
					Message:  err.Error(),
				})
			case errors.Is(err, deploy.ErrDeployUnknownAfterRef):
				perDiags = append(perDiags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "config",
					Target:   "config.deploy-after",
					Message:  err.Error(),
				})
			case errors.Is(err, deploy.ErrMandatoryAfterOptional):
				// Per-rule check above already emits this; safety net only.
				perDiags = append(perDiags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "config",
					Target:   "config.deploy-after",
					Message:  err.Error(),
				})
			}
		}
	}

	// Emit OK only when no errors from cross-service checks.
	hasError := false
	for _, d := range diags {
		if d.Severity == validate.SeverityError {
			hasError = true
			break
		}
	}
	for _, d := range perDiags {
		if d.Severity == validate.SeverityError {
			hasError = true
			break
		}
	}
	if !hasError {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "config",
			Target:   "config.deploy-after",
		})
	}
	diags = append(diags, perDiags...)
	return diags
}

// checkAfterFieldNotAllowed reads the file using ParseDeployConfigForValidation
// and emits an Error diagnostic if after: is present.
func checkAfterFieldNotAllowed(ctx validate.Context, path, target, message string) []validate.Diagnostic {
	dc, err := config.ParseDeployConfigForValidation(path)
	if err != nil {
		if errors.Is(err, errNotExist) {
			return nil
		}
		// Parse failure — another validator handles this; skip here.
		return nil
	}
	if dc != nil && len(dc.After) > 0 {
		return []validate.Diagnostic{
			{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   target,
				File:     relPath(ctx.ProjectRoot, path),
				Message:  message,
			},
		}
	}
	return nil
}
