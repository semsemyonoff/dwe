package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/usercommands/model"
	"devbox-cli/internal/core/usercommands/registry"
	"devbox-cli/internal/core/validate"
)

type serviceHooksValidator struct{}

func (v *serviceHooksValidator) ID() string     { return "services.hooks" }
func (v *serviceHooksValidator) Domain() string { return "config" }

func (v *serviceHooksValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	// Resolve the services map.
	var services map[string]config.ServiceConfig
	if ctx.Cfg != nil && ctx.Cfg.Services != nil {
		services = ctx.Cfg.Services
	} else {
		loaded, err := config.LoadServices(ctx.ProjectRoot)
		if err != nil {
			// Can't resolve; skip silently (other validators will surface load errors).
			return diags
		}
		services = loaded
	}

	if len(services) == 0 {
		return diags
	}

	reg, _ := ctx.CommandRegistry.(*registry.Registry)

	servicesDir := filepath.Join(ctx.ProjectRoot, "devbox", "services")

	for name, svc := range services {
		svcFile := relPath(ctx.ProjectRoot, filepath.Join(servicesDir, name, "service.yml"))
		target := "config.services.hooks:" + name

		emit := func(d validate.Diagnostic) {
			d.Domain = "config"
			d.File = svcFile
			d.Target = target
			diags = append(diags, d)
		}

		// Required service with hooks → warning (hooks never fire).
		if svc.Required && (svc.OnEnable != nil || svc.OnDisable != nil) {
			emit(validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Message:  fmt.Sprintf("hooks on required service %q will never fire (required services cannot be toggled)", name),
			})
		}

		if svc.OnEnable != nil {
			diags = append(diags, validateHooks(ctx, name, "on_enable", svc.OnEnable, true, svcFile, target, reg, servicesDir)...)
		}
		if svc.OnDisable != nil {
			diags = append(diags, validateHooks(ctx, name, "on_disable", svc.OnDisable, false, svcFile, target, reg, servicesDir)...)
		}
	}

	return diags
}

// validateHooks checks a single ServiceToggleHooks block for errors.
// isEnable distinguishes on_enable (allowDeploy=true) from on_disable (allowDeploy=false).
func validateHooks(
	_ validate.Context,
	svcName, hookName string,
	hooks *config.ServiceToggleHooks,
	isEnable bool,
	svcFile, target string,
	reg *registry.Registry,
	servicesDir string,
) []validate.Diagnostic {
	var diags []validate.Diagnostic

	emit := func(d validate.Diagnostic) {
		d.Domain = "config"
		d.File = svcFile
		d.Target = target
		diags = append(diags, d)
	}

	req := hooks.Requires

	switch {
	case !req.IsKnown():
		validList := "none, restart, deploy, deploy-or-restart"
		if !isEnable {
			validList = "none, restart"
		}
		emit(validate.Diagnostic{
			Severity: validate.SeverityError,
			Message:  fmt.Sprintf("service %q %s.requires: unknown value %q; valid: %s", svcName, hookName, string(req), validList),
		})
	case !isEnable && req == config.RequiresDeploy:
		emit(validate.Diagnostic{
			Severity: validate.SeverityError,
			Message:  fmt.Sprintf("service %q on_disable.requires: %q is not allowed for on_disable; valid: none, restart", svcName, "deploy"),
		})
	case !isEnable && req == config.RequiresDeployOrRestart:
		// deploy-or-restart resolves to deploy when never deployed — not
		// meaningful for a disable operation. Keep the on_disable surface
		// to {none, restart}.
		emit(validate.Diagnostic{
			Severity: validate.SeverityError,
			Message:  fmt.Sprintf("service %q on_disable.requires: %q is not allowed for on_disable; valid: none, restart", svcName, string(req)),
		})
	case isEnable && (req == config.RequiresDeploy || req == config.RequiresDeployOrRestart):
		// on_enable.requires == deploy (or deploy-or-restart which may resolve
		// to deploy) requires a deploy.yml for the service.
		deployPath := filepath.Join(servicesDir, svcName, "deploy.yml")
		if _, err := os.Stat(deployPath); errors.Is(err, os.ErrNotExist) {
			emit(validate.Diagnostic{
				Severity: validate.SeverityError,
				Message:  fmt.Sprintf("service %q declares on_enable.requires: %s but has no deploy.yml; either add deploy.yml or use requires: restart", svcName, string(req)),
			})
		}
	}

	// Validate before/after command refs.
	allRefs := make([]string, 0, len(hooks.Before)+len(hooks.After))
	allRefs = append(allRefs, hooks.Before...)
	allRefs = append(allRefs, hooks.After...)

	for _, ref := range allRefs {
		if reg == nil {
			// No registry available — skip ref-resolution.
			break
		}
		cmd, err := reg.Get(ref)
		if err != nil {
			emit(validate.Diagnostic{
				Severity: validate.SeverityError,
				Message:  fmt.Sprintf("service %q %s: references unknown command %q", svcName, hookName, ref),
			})
			continue
		}
		if cmd.Type != model.CommandTypeShell && cmd.Type != model.CommandTypeScript {
			emit(validate.Diagnostic{
				Severity: validate.SeverityError,
				Message:  fmt.Sprintf("service %q %s: command %q has type %q; only shell/script commands can be used in hooks", svcName, hookName, ref, string(cmd.Type)),
			})
		}
	}

	return diags
}
