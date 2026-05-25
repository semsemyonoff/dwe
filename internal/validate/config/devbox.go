package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"

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

// Compile-time interface check.
var _ validate.Validator = (*servicesValidator)(nil)

func (v *servicesValidator) ID() string {
	return "services"
}

func (v *servicesValidator) Domain() string {
	return "config"
}

// servicesAllowedFields mirrors the per-type field allowlist owned by the
// loader (config.allowedFieldsFor). Duplicated here intentionally — the loader
// helper stays unexported per project Go conventions, and the table is small
// and stable. Any change in the loader allowlist must be mirrored here (the
// services_loader tests guard the loader side; servicesValidator tests guard
// this side).
var servicesAllowedFields = map[config.ServiceType]map[string]bool{
	config.ServiceTypeApp: {
		"type": true, "container": true, "mandatory": true, "compose": true,
		"ports": true, "hosts": true, "status": true,
		"depends_on": true,
		"dir":        true, "dir_internal": true, "work_dir_internal": true,
		"configs": true, "dirs": true, "extends": true, "cli": true, "render": true,
	},
	config.ServiceTypeInfra: {
		"type": true, "container": true, "mandatory": true, "compose": true,
		"ports": true, "hosts": true, "status": true, "depends_on": true,
	},
	config.ServiceTypeTool: {
		"type": true, "container": true, "mandatory": true, "compose": true,
		"ports": true, "hosts": true, "status": true,
	},
}

func (v *servicesValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	servicesDir := filepath.Join(ctx.ProjectRoot, "devbox", "services")

	entries, err := os.ReadDir(servicesDir)
	if err != nil {
		if errors.Is(err, errNotExist) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.services",
				File:     relPath(ctx.ProjectRoot, servicesDir),
				Message:  "no devbox/services/ directory; no services defined",
			})
			return diags
		}
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.services",
			File:     relPath(ctx.ProjectRoot, servicesDir),
			Message:  err.Error(),
		})
		return diags
	}

	// Build name → type index from raw folder data so depends_on can
	// cross-reference targets. Merged map from ctx.Cfg wins when set.
	typesByName := make(map[string]config.ServiceType)

	perServiceDiags := []validate.Diagnostic{}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		svcFile := filepath.Join(servicesDir, name, "service.yml")
		file := relPath(ctx.ProjectRoot, svcFile)
		target := "config.services:" + name

		emit := func(d validate.Diagnostic) {
			d.Domain = "config"
			d.File = file
			perServiceDiags = append(perServiceDiags, d)
		}

		data, err := os.ReadFile(svcFile)
		if err != nil {
			if errors.Is(err, errNotExist) {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Target:   target,
					Message:  fmt.Sprintf("service directory %q has no service.yml", name),
				})
			} else {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Target:   target,
					Message:  err.Error(),
				})
			}
			continue
		}

		var rawEntry map[string]any
		if err := yaml.Unmarshal(data, &rawEntry); err != nil {
			emit(validate.Diagnostic{
				Severity: validate.SeverityError,
				Target:   target,
				Message:  fmt.Sprintf("parse service.yml: %v", err),
			})
			continue
		}
		if rawEntry == nil {
			rawEntry = map[string]any{}
		}

		// type required + valid.
		typeRaw, hasType := rawEntry["type"]
		if !hasType {
			emit(validate.Diagnostic{
				Severity: validate.SeverityError,
				Target:   target,
				Message:  fmt.Sprintf("service %q: missing type", name),
				Hint:     "add type: app | tool | infra",
			})
			continue
		}
		typeStr, _ := typeRaw.(string)
		svcType := config.ServiceType(typeStr)
		if err := svcType.Validate(); err != nil {
			emit(validate.Diagnostic{
				Severity: validate.SeverityError,
				Target:   target,
				Message:  fmt.Sprintf("service %q: %v", name, err),
				Hint:     "type must be one of: app, tool, infra",
			})
			continue
		}

		typesByName[name] = svcType

		// extends only for app.
		if extRaw, ok := rawEntry["extends"]; ok && extRaw != nil && !svcType.IsApp() {
			emit(validate.Diagnostic{
				Severity: validate.SeverityError,
				Target:   target,
				Message:  fmt.Sprintf("service %q (type %s): extends only permitted for type app", name, svcType),
			})
		}

		// Per-type field allowlist.
		allowed := servicesAllowedFields[svcType]
		for _, key := range slices.Sorted(maps.Keys(rawEntry)) {
			if key == "extends" && !svcType.IsApp() {
				continue
			}
			if !allowed[key] {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Target:   target,
					Message:  fmt.Sprintf("service %q (type %s): field %q not allowed", name, svcType, key),
				})
			}
		}

		// ports shape + range.
		if vv, ok := rawEntry["ports"]; ok && vv != nil {
			m, isMap := vv.(map[string]any)
			if !isMap {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Target:   target,
					Message:  fmt.Sprintf("service %q: ports must be a map of name to port number", name),
				})
			} else {
				for _, p := range slices.Sorted(maps.Keys(m)) {
					n, ok := m[p].(int)
					if !ok {
						emit(validate.Diagnostic{
							Severity: validate.SeverityError,
							Target:   target,
							Message:  fmt.Sprintf("service %q port %q: not an integer", name, p),
						})
						continue
					}
					if n < 1 || n > 65535 {
						emit(validate.Diagnostic{
							Severity: validate.SeverityError,
							Target:   target,
							Message:  fmt.Sprintf("service %q port %q = %d: out of range 1..65535", name, p, n),
						})
					}
				}
			}
		}
		// hosts shape.
		if vv, ok := rawEntry["hosts"]; ok && vv != nil {
			if _, isMap := vv.(map[string]any); !isMap {
				emit(validate.Diagnostic{
					Severity: validate.SeverityError,
					Target:   target,
					Message:  fmt.Sprintf("service %q: hosts must be a map of name to hostname", name),
				})
			}
		}

		// App missing dir/dir_internal → warning.
		if svcType.IsApp() {
			hasDir := false
			hasDirInternal := false
			hasMergedService := false
			if ctx.Cfg != nil {
				if svc, ok := ctx.Cfg.Services[name]; ok {
					hasMergedService = true
					hasDir = svc.Dir != ""
					hasDirInternal = svc.DirInternal != ""
				}
			}
			if !hasDir && !hasDirInternal {
				_, hasDir = rawEntry["dir"]
				_, hasDirInternal = rawEntry["dir_internal"]
			}
			if !hasMergedService && !hasDir && !hasDirInternal {
				if _, extends := rawEntry["extends"]; extends {
					continue
				}
			}
			if !hasDir && !hasDirInternal {
				emit(validate.Diagnostic{
					Severity: validate.SeverityWarning,
					Target:   target,
					Message:  fmt.Sprintf("service %q (type app) has no dir or dir_internal", name),
				})
			}
		}
	}

	// Augment typesByName from ctx.Cfg (catches services not yet validated above).
	if ctx.Cfg != nil {
		for name, svc := range ctx.Cfg.Services {
			typesByName[name] = svc.Type
		}
	}

	// depends_on cross-reference check (needs complete typesByName).
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		svcFile := filepath.Join(servicesDir, name, "service.yml")
		file := relPath(ctx.ProjectRoot, svcFile)
		target := "config.services:" + name

		emit := func(d validate.Diagnostic) {
			d.Domain = "config"
			d.File = file
			perServiceDiags = append(perServiceDiags, d)
		}

		data, err := os.ReadFile(svcFile)
		if err != nil {
			continue
		}
		var rawEntry map[string]any
		if err := yaml.Unmarshal(data, &rawEntry); err != nil || rawEntry == nil {
			continue
		}
		if vv, ok := rawEntry["depends_on"]; ok && vv != nil {
			if list, isList := vv.([]any); isList {
				for _, item := range list {
					t, _ := item.(string)
					if t == "" {
						continue
					}
					if parentType, has := typesByName[t]; has && parentType.IsTool() {
						emit(validate.Diagnostic{
							Severity: validate.SeverityError,
							Target:   target,
							Message:  fmt.Sprintf("service %q: depends_on target %q is type tool", name, t),
							Hint:     "tools cannot be depends_on targets; convert target to type infra",
						})
					}
				}
			}
		}
	}

	// Emit an OK summary diagnostic only when nothing errored.
	hasError := false
	for _, d := range perServiceDiags {
		if d.Severity == validate.SeverityError {
			hasError = true
			break
		}
	}
	if !hasError {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "config",
			Target:   "config.services",
			File:     relPath(ctx.ProjectRoot, servicesDir),
		})
	}
	diags = append(diags, perServiceDiags...)
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

	if _, statErr := os.Stat(stylesPath); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
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
				Message:  statErr.Error(),
			})
		}
		return diags
	}

	stylesCfg, err := config.LoadStylesConfig(stylesPath)
	if err != nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.styles",
			File:     relPath(ctx.ProjectRoot, stylesPath),
			Message:  err.Error(),
		})
		return diags
	}

	diags = append(diags, validate.Diagnostic{
		Severity: validate.SeverityOK,
		Domain:   "config",
		Target:   "config.styles",
		File:     relPath(ctx.ProjectRoot, stylesPath),
	})

	_ = stylesCfg // Unused; just checking that it loads cleanly

	diags = append(diags, stylesRenameDiagnostics(ctx, stylesPath)...)

	return diags
}

// stylesColorRenames maps old `colors:` keys to their new 7-token target.
// Keys present in the new schema (accent/success/warning/danger/muted/border/text)
// are not in this map and pass through silently.
var stylesColorRenames = map[string]string{
	"label":               "accent",
	"section_title":       "accent",
	"subheader":           "accent",
	"info":                "accent",
	"table_header":        "accent",
	"focus_border":        "accent",
	"filter_match":        "accent",
	"pagination_active":   "accent",
	"mandatory":           "accent",
	"enabled":             "success",
	"partial":             "warning",
	"description":         "muted",
	"tree_count":          "muted",
	"tree_arrow":          "muted",
	"pagination_inactive": "muted",
	"disabled":            "muted",
	"table_border":        "border",
}

func stylesRenameDiagnostics(ctx validate.Context, stylesPath string) []validate.Diagnostic {
	raw, err := os.ReadFile(stylesPath)
	if err != nil {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	root := &doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}

	var diags []validate.Diagnostic
	file := relPath(ctx.ProjectRoot, stylesPath)

	emit := func(target, message, hint string, line int) {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityWarning,
			Domain:   "config",
			Target:   target,
			File:     file,
			Line:     line,
			Message:  message,
			Hint:     hint,
		})
	}

	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]
		switch key.Value {
		case "colors":
			if val.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(val.Content); j += 2 {
				ck := val.Content[j]
				cv := val.Content[j+1]
				if ck.Value == "help" {
					emit(
						"config.styles",
						"colors.help is no longer supported",
						"remove colors.help — Fang help colors are derived from accent + muted",
						ck.Line,
					)
					continue
				}
				if target, ok := stylesColorRenames[ck.Value]; ok {
					emit(
						"config.styles",
						fmt.Sprintf("colors.%s is no longer supported", ck.Value),
						fmt.Sprintf("rename to colors.%s", target),
						ck.Line,
					)
				}
				_ = cv
			}
		case "header":
			if val.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(val.Content); j += 2 {
				hk := val.Content[j]
				if hk.Value == "color" {
					emit(
						"config.styles",
						"header.color is no longer supported",
						"remove header.color — the brand line is always rendered in accent",
						hk.Line,
					)
				}
			}
		}
	}
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

	deployCfg, err := config.ParseDeployConfigForValidation(deployPath)
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
	// Pass nil registry so files_gate validation is skipped here — the dedicated
	// deployFilesGateValidator handles that and emits structured diagnostics.
	if ctx.Cfg != nil {
		if _, err := deploy.ResolvePlan(ctx.Cfg, nil); err != nil {
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
	// Pass nil registry so files_gate validation is skipped here — the dedicated
	// resetFilesGateValidator handles that and emits structured diagnostics.
	if ctx.Cfg != nil {
		if _, err := reset.ResolvePlan(ctx.Cfg, nil); err != nil {
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
		services, err = config.LoadServices(ctx.ProjectRoot)
		if err != nil {
			// Error loading services; emit a diagnostic and skip service deploy validation
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.service-deploy",
				Message:  fmt.Sprintf("failed to load services: %v", err),
			})
			return diags
		}
		if len(services) == 0 {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "config",
				Target:   "config.service-deploy",
				File:     relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "devbox", "services")),
				Message:  "no services defined; no service deploy configs to validate",
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
