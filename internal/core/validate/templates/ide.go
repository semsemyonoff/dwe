// Package templates provides validators for template packs (IDE and AI).
package templates

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/ide"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// IDEValidator validates IDE template packs for app services.
type IDEValidator struct{}

// ID returns the validator's unique ID within its domain.
func (v *IDEValidator) ID() string {
	return "ide"
}

// Domain returns the domain this validator belongs to.
func (v *IDEValidator) Domain() string {
	return "templates"
}

// Run validates app IDE template packs and returns a list of diagnostics.
func (v *IDEValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	if ctx.Cfg == nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityInfo,
			Domain:   "templates",
			Target:   "templates.ide",
			Message:  "IDE template validation requires successful main config load; skipped",
		}}
	}

	// Validate every service that actually participates in IDE rendering at
	// runtime — apps by default plus any non-app service that opted in via
	// render.ide.enabled: true. SelectServices honors the same gating that
	// `dwe render ide` uses, so the validator scope matches what would be
	// rendered.
	services := ctx.Cfg.Services
	selected, skipped := ide.SelectServices(services)

	// Emit info diagnostics for skipped services with actionable reasons
	for _, skip := range skipped {
		var message, hint string
		switch skip.Reason {
		case "service-disabled", "ide-disabled", "ide-policy":
			// Service or IDE render disabled (explicit or by-policy); nothing to report.
			continue
		case "empty-dir":
			message = "service has no dir or dir is project root"
			hint = "set service.dir to a subdirectory path"
		case "lost-collision":
			message = fmt.Sprintf("dir %s rendered by %s", skip.Dir, skip.Winner)
			hint = "multiple services share this dir; the deepest extends chain renders"
		}
		if message != "" {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "templates",
				Target:   fmt.Sprintf("templates.ide:%s", skip.Name),
				File:     "",
				Line:     0,
				Message:  message,
				Hint:     hint,
			})
		}
	}

	// Validate each selected service's template pack
	for _, name := range selected {
		svc := services[name]
		diags = append(diags, v.validateService(name, svc, ctx.Cfg, ctx.ProjectRoot)...)
	}

	// If no errors/infos, emit a single OK diagnostic
	if len(diags) == 0 {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "templates",
			Target:   "templates.ide",
			File:     "",
			Line:     0,
			Message:  "all IDE template packs valid",
			Hint:     "",
		})
	}

	return diags
}

// validateService validates one service's IDE template pack.
func (v *IDEValidator) validateService(name string, svc config.ServiceConfig, cfg *config.DweConfig, projectRoot string) []validate.Diagnostic {
	services := cfg.Services
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ide:%s", name),
			Message:  fmt.Sprintf("resolve project root: %v", err),
		}}
	}

	packDir, packName, found, err := ide.ResolveTemplatePack(svc, services, absRoot, name)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ide:%s", name),
			Message:  fmt.Sprintf("failed to resolve template pack: %v", err),
			Hint:     "check render.ide.template setting and workspace/templates/ide directory",
		}}
	}
	if !found {
		return []validate.Diagnostic{{
			Severity: validate.SeverityWarning,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ide:%s", name),
			Message:  fmt.Sprintf("template pack not found for service %q", name),
			Hint: fmt.Sprintf(
				"create workspace/templates/ide/%s or workspace/templates/ide/default\n"+
					"or set services.%s.render.ide.enabled: false in services.yml",
				name, name,
			),
		}}
	}

	m, err := ide.LoadManifest(packDir)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ide:%s", name),
			File:     filepath.Join("workspace", "templates", "ide", packName, "manifest.yml"),
			Message:  fmt.Sprintf("failed to load manifest: %v", err),
			Hint:     "IDE packs now require a manifest.yml; see docs/reference/render/ide.md for the migration",
		}}
	}

	absHubDir := filepath.Join(absRoot, svc.Dir)
	sink, getHits := overrideSink()
	if err := ide.ValidateManifest(m, absRoot, packName, absHubDir, sink); err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ide:%s", name),
			File:     filepath.Join("workspace", "templates", "ide", packName, "manifest.yml"),
			Message:  fmt.Sprintf("invalid manifest: %v", err),
			Hint:     "check render and symlink entries in manifest.yml",
		}}
	}

	var diags []validate.Diagnostic
	if d := overrideDiagnostic("templates", "ide", packName, fmt.Sprintf("templates.ide:%s", name), getHits()); d != nil {
		diags = append(diags, *d)
	}

	// Dry-run render every template against the actual TemplateData so missing
	// variables, parse errors, or other execution-time failures surface here
	// instead of at `dwe render ide` time.
	data := ide.TemplateData{
		Project:    cfg.Project,
		Service:    ide.ExtendsRoot(services, name),
		Resolved:   name,
		ServiceCfg: svc,
		Runtime:    cfg.Runtime,
		Services:   services,
		Cfg:        cfg,
	}
	failures := ide.DryRunRender(absRoot, packName, m, data)
	fromKeys := make([]string, 0, len(failures))
	for from := range failures {
		fromKeys = append(fromKeys, from)
	}
	sort.Strings(fromKeys)
	for _, from := range fromKeys {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ide:%s", name),
			File:     filepath.Join("workspace", "templates", "ide", packName, from),
			Message:  fmt.Sprintf("template render failed: %v", failures[from]),
			Hint:     "template references a value not present for this service; check the template's variable usage against the service config",
		})
	}
	return diags
}

// All returns all template validators.
func All() []validate.Validator {
	return []validate.Validator{
		&IDEValidator{},
		&AIValidator{},
		&GitValidator{},
	}
}
