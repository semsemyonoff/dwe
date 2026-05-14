package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
	"devbox-cli/internal/project"
	"devbox-cli/internal/reset"
	"devbox-cli/internal/validate"
)

var errNotExist = os.ErrNotExist

// relPath returns the relative path from projectRoot to path, or the path as-is if relative resolution fails.
func relPath(projectRoot, path string) string {
	rel, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return path
	}
	return rel
}

type devboxValidator struct{}

func (v *devboxValidator) ID() string {
	return "devbox"
}

func (v *devboxValidator) Domain() string {
	return "config"
}

func (v *devboxValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	configPath := ctx.ConfigPath
	if configPath == "" {
		configPath = filepath.Join(ctx.ProjectRoot, "devbox.yml")
	}

	// Check 1: Schema validation
	if err := project.ValidateSchema(configPath); err != nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.devbox.schema",
			File:     relPath(ctx.ProjectRoot, configPath),
			Message:  err.Error(),
			Hint:     "Ensure devbox.yml has schema_version: \"2\"",
		})
		// Continue to load check even if schema fails
	} else {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "config",
			Target:   "config.devbox.schema",
			File:     relPath(ctx.ProjectRoot, configPath),
		})
	}

	// Check 2: Config loading and validation
	_, err := config.LoadConfig(configPath)
	if err != nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.devbox",
			File:     relPath(ctx.ProjectRoot, configPath),
			Message:  err.Error(),
		})
	} else {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "config",
			Target:   "config.devbox",
			File:     relPath(ctx.ProjectRoot, configPath),
		})
	}

	// If LoadConfig failed, we still checked what we could. Don't emit "cross-ref" info
	// since there's no failure to explain — the devbox load check above captures it.

	return diags
}

type servicesValidator struct{}

func (v *servicesValidator) ID() string {
	return "services"
}

func (v *servicesValidator) Domain() string {
	return "config"
}

func (v *servicesValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	servicesPath := filepath.Join(ctx.ProjectRoot, "devbox", "services.yml")

	// Load services separately; missing is Info, not error
	services, err := config.LoadServicesConfig(servicesPath)
	if err != nil {
		if errors.Is(err, errNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.services",
				File:     relPath(ctx.ProjectRoot, servicesPath),
				Message:  "no services.yml; services may be defined inline",
			})
		} else {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.services",
				File:     relPath(ctx.ProjectRoot, servicesPath),
				Message:  err.Error(),
			})
		}
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.services",
		File:     relPath(ctx.ProjectRoot, servicesPath),
	})

	// Check service definitions
	for name, svc := range services {
		if svc.Dir == "" && svc.DirInternal == "" {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "config",
				Target:   "config.services:" + name,
				File:     relPath(ctx.ProjectRoot, servicesPath),
				Message:  "service has no dir or dir_internal",
			})
		}
		// Extends validation: check if parent exists
		if svc.Extends != "" {
			if _, exists := services[svc.Extends]; !exists {
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityError,
					Domain:   "config",
					Target:   "config.services:" + name,
					File:     relPath(ctx.ProjectRoot, servicesPath),
					Message:  "extends: parent service \"" + svc.Extends + "\" not found",
				})
			}
		}
	}

	return diags
}

type dockerValidator struct{}

func (v *dockerValidator) ID() string {
	return "docker"
}

func (v *dockerValidator) Domain() string {
	return "config"
}

func (v *dockerValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	dockerPath := filepath.Join(ctx.ProjectRoot, "devbox", "docker.yml")

	// Docker validation requires successful main config load for template resolution
	if ctx.Cfg == nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "config",
			Target:   "config.docker",
			File:     relPath(ctx.ProjectRoot, dockerPath),
			Message:  "docker.yml validation requires successful main config load; skipped",
		})
		return diags
	}

	// Load and validate docker config
	dockerCfg, err := config.LoadDockerConfig(ctx.ProjectRoot, ctx.Cfg)
	if err != nil {
		if errors.Is(err, errNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.docker",
				File:     relPath(ctx.ProjectRoot, dockerPath),
				Message:  "no docker.yml",
			})
		} else {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.docker",
				File:     relPath(ctx.ProjectRoot, dockerPath),
				Message:  err.Error(),
			})
		}
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.docker",
		File:     relPath(ctx.ProjectRoot, dockerPath),
	})

	_ = dockerCfg // Unused; just checking that it loads cleanly

	return diags
}

type infoValidator struct{}

func (v *infoValidator) ID() string {
	return "info"
}

func (v *infoValidator) Domain() string {
	return "config"
}

func (v *infoValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	infoPath := filepath.Join(ctx.ProjectRoot, "devbox", "info.yml")

	infoCfg, err := config.LoadInfoConfig(infoPath)
	if err != nil {
		if errors.Is(err, errNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.info",
				File:     relPath(ctx.ProjectRoot, infoPath),
				Message:  "no info.yml",
			})
		} else {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.info",
				File:     relPath(ctx.ProjectRoot, infoPath),
				Message:  err.Error(),
			})
		}
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.info",
		File:     relPath(ctx.ProjectRoot, infoPath),
	})

	_ = infoCfg // Unused; just checking that it loads cleanly

	return diags
}

type stylesValidator struct{}

func (v *stylesValidator) ID() string {
	return "styles"
}

func (v *stylesValidator) Domain() string {
	return "config"
}

func (v *stylesValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	stylesPath := filepath.Join(ctx.ProjectRoot, "devbox", "styles.yml")

	stylesCfg, err := config.LoadStylesConfig(stylesPath)
	if err != nil {
		if errors.Is(err, errNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.styles",
				File:     relPath(ctx.ProjectRoot, stylesPath),
				Message:  "no styles.yml",
			})
		} else {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.styles",
				File:     relPath(ctx.ProjectRoot, stylesPath),
				Message:  err.Error(),
			})
		}
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.styles",
		File:     relPath(ctx.ProjectRoot, stylesPath),
	})

	_ = stylesCfg // Unused; just checking that it loads cleanly

	return diags
}

type lifecycleValidator struct{}

func (v *lifecycleValidator) ID() string {
	return "lifecycle"
}

func (v *lifecycleValidator) Domain() string {
	return "config"
}

func (v *lifecycleValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	lifecyclePath := filepath.Join(ctx.ProjectRoot, "devbox", "lifecycle.yml")

	lifecycleCfg, err := config.LoadLifecycleConfig(lifecyclePath)
	if err != nil {
		if errors.Is(err, errNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.lifecycle",
				File:     relPath(ctx.ProjectRoot, lifecyclePath),
				Message:  "no lifecycle.yml",
			})
		} else {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.lifecycle",
				File:     relPath(ctx.ProjectRoot, lifecyclePath),
				Message:  err.Error(),
			})
		}
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.lifecycle",
		File:     relPath(ctx.ProjectRoot, lifecyclePath),
	})

	_ = lifecycleCfg // Unused; just checking that it loads cleanly

	return diags
}

type deployValidator struct{}

func (v *deployValidator) ID() string {
	return "deploy"
}

func (v *deployValidator) Domain() string {
	return "config"
}

func (v *deployValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	deployPath := filepath.Join(ctx.ProjectRoot, "devbox", "deploy.yml")

	deployCfg, err := config.LoadDeployConfig(deployPath)
	if err != nil {
		if errors.Is(err, errNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.deploy",
				File:     relPath(ctx.ProjectRoot, deployPath),
				Message:  "no deploy.yml",
			})
		} else {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.deploy",
				File:     relPath(ctx.ProjectRoot, deployPath),
				Message:  err.Error(),
			})
		}
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.deploy",
		File:     relPath(ctx.ProjectRoot, deployPath),
	})

	_ = deployCfg

	// Cross-reference: resolve the plan to catch step-level errors.
	if ctx.Cfg != nil {
		if _, err := deploy.ResolvePlan(ctx.Cfg); err != nil {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.deploy",
				File:     relPath(ctx.ProjectRoot, deployPath),
				Message:  fmt.Sprintf("plan resolution failed: %v", err),
				Hint:     "check that all phases and steps reference valid services and commands",
			})
		}
	} else {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "config",
			Target:   "config.deploy",
			File:     relPath(ctx.ProjectRoot, deployPath),
			Message:  "plan resolution skipped: main config did not load",
		})
	}

	return diags
}

type resetValidator struct{}

func (v *resetValidator) ID() string {
	return "reset"
}

func (v *resetValidator) Domain() string {
	return "config"
}

func (v *resetValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	resetPath := filepath.Join(ctx.ProjectRoot, "devbox", "reset.yml")

	resetCfg, err := config.LoadResetConfig(resetPath)
	if err != nil {
		if errors.Is(err, errNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.reset",
				File:     relPath(ctx.ProjectRoot, resetPath),
				Message:  "no reset.yml",
			})
		} else {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.reset",
				File:     relPath(ctx.ProjectRoot, resetPath),
				Message:  err.Error(),
			})
		}
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.reset",
		File:     relPath(ctx.ProjectRoot, resetPath),
	})

	_ = resetCfg

	// Cross-reference: resolve the plan to catch step-level errors.
	if ctx.Cfg != nil {
		if _, err := reset.ResolvePlan(ctx.Cfg); err != nil {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.reset",
				File:     relPath(ctx.ProjectRoot, resetPath),
				Message:  fmt.Sprintf("plan resolution failed: %v", err),
				Hint:     "check that all phases and steps reference valid services and commands",
			})
		}
	} else {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "config",
			Target:   "config.reset",
			File:     relPath(ctx.ProjectRoot, resetPath),
			Message:  "plan resolution skipped: main config did not load",
		})
	}

	return diags
}

type serviceDeployValidator struct{}

func (v *serviceDeployValidator) ID() string {
	return "service-deploy"
}

func (v *serviceDeployValidator) Domain() string {
	return "config"
}

func (v *serviceDeployValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	// Get services from main config if available, otherwise load them separately
	var services map[string]config.ServiceConfig
	var err error

	if ctx.Cfg != nil && ctx.Cfg.Services != nil {
		services = ctx.Cfg.Services
	} else {
		services, err = config.LoadServicesConfig(filepath.Join(ctx.ProjectRoot, "devbox", "services.yml"))
		if err != nil {
			if errors.Is(err, errNotExist) {
				// No services means no service deploy configs either
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityInfo,
					Domain:   "config",
					Target:   "config.service-deploy",
					File:     relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "devbox", "services.yml")),
					Message:  "no services.yml; no service deploy configs to validate",
				})
				return diags
			}
			// Error loading services; emit a diagnostic and skip service deploy validation
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.service-deploy",
				File:     relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "devbox", "services.yml")),
				Message:  fmt.Sprintf("failed to load services: %v", err),
			})
			return diags
		}
	}

	// Load service deploy configs for all services
	serviceDeployConfigs, err := config.LoadServiceDeployConfigs(ctx.ProjectRoot, services)
	if err != nil {
		// This could be a collection of per-service errors; emit what we can.
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.service-deploy",
			File:     relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "devbox")),
			Message:  err.Error(),
		})
		return diags
	}

	if len(serviceDeployConfigs) == 0 {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "config",
			Target:   "config.service-deploy",
			Message:  "no service deploy configs",
		})
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.service-deploy",
	})

	return diags
}
