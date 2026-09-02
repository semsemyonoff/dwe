package templates

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/ai"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// AIValidator validates AI template packs for app services.
type AIValidator struct{}

// ID returns the validator's unique ID within its domain.
func (v *AIValidator) ID() string {
	return "ai"
}

// Domain returns the domain this validator belongs to.
func (v *AIValidator) Domain() string {
	return "templates"
}

// Run validates app AI template packs and returns a list of diagnostics.
func (v *AIValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	if ctx.Cfg == nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityInfo,
			Domain:   "templates",
			Target:   "templates.ai",
			Message:  "AI template validation requires successful main config load; skipped",
		}}
	}

	// Validate every service that actually participates in AI rendering at
	// runtime — apps by default plus any non-app service that opted in via
	// render.ai.enabled: true. SelectServices honors the same gating that
	// `dwe render ai` uses, so the validator scope matches what would be
	// rendered.
	cfg := sanitizedCfg(ctx)
	services := cfg.Services
	selected, skipped := ai.SelectServices(services)

	// Emit info diagnostics for skipped services with actionable reasons
	for _, skip := range skipped {
		var message, hint string
		switch skip.Reason {
		case "service-disabled", "ai-disabled", "ai-policy":
			// Service or AI render disabled (explicit or by-policy); nothing to report.
			continue
		case "empty-dir":
			message = "service has no dir or dir is project root"
			hint = "set service.dir to a subdirectory path"
		case "lost-collision":
			message = fmt.Sprintf("dir %s rendered by %s", skip.Dir, skip.Winner)
			hint = "multiple services share this dir; the shallowest extends chain (hub owner) renders"
		}
		if message != "" {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "templates",
				Target:   fmt.Sprintf("templates.ai:%s", skip.Name),
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
		diags = append(diags, v.validateService(name, svc, cfg, ctx.ProjectRoot)...)
	}

	// If no errors/infos, emit a single OK diagnostic
	if len(diags) == 0 {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "templates",
			Target:   "templates.ai",
			File:     "",
			Line:     0,
			Message:  "all AI template packs valid",
			Hint:     "",
		})
	}

	return diags
}

// validateService validates one service's AI template pack.
func (v *AIValidator) validateService(name string, svc config.ServiceConfig, cfg *config.DweConfig, projectRoot string) []validate.Diagnostic {
	services := cfg.Services
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ai:%s", name),
			Message:  fmt.Sprintf("resolve project root: %v", err),
		}}
	}

	// Resolve template pack
	packDir, packName, found, err := ai.ResolveTemplatePack(svc, services, absRoot, name)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ai:%s", name),
			Message:  fmt.Sprintf("failed to resolve template pack: %v", err),
			Hint:     "check render.ai.template setting and workspace/templates/ai directory",
		}}
	}
	if !found {
		if _, explicit := svc.AIRenderEnabledExplicit(); !explicit {
			// Implicit default (app type, no render.ai key) + absent pack: the
			// scaffold ships with no template pack, so this is expected, not
			// broken. Warn only once the user has opted in explicitly.
			return nil
		}
		return []validate.Diagnostic{{
			Severity: validate.SeverityWarning,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ai:%s", name),
			Message:  fmt.Sprintf("template pack not found for service %q", name),
			// The key is written as it appears IN service.yml — top-level
			// render:, not the services.<name>.render. path used to talk about
			// it elsewhere. service.yml is strict-decoded against a per-type
			// field allowlist that has no `services` key, so pasting the
			// qualified path there makes the project stop loading.
			Hint: fmt.Sprintf(
				"create workspace/templates/ai/%s or workspace/templates/ai/default\n"+
					"or set render.ai.enabled: false in workspace/services/%s/service.yml",
				name, name,
			),
		}}
	}

	// Load and validate manifest
	m, err := ai.LoadManifest(packDir)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ai:%s", name),
			File:     filepath.Join("workspace", "templates", "ai", packName, "manifest.yml"),
			Message:  fmt.Sprintf("failed to load manifest: %v", err),
			Hint:     "check manifest.yml syntax and structure",
		}}
	}

	absHubDir := filepath.Join(absRoot, svc.Dir)
	sink, getHits := overrideSink()
	if err := ai.ValidateManifest(m, absRoot, packName, absHubDir, sink); err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ai:%s", name),
			File:     filepath.Join("workspace", "templates", "ai", packName, "manifest.yml"),
			Message:  fmt.Sprintf("invalid manifest: %v", err),
			Hint:     "check render and symlink entries in manifest.yml",
		}}
	}

	var diags []validate.Diagnostic
	if d := overrideDiagnostic("templates", "ai", packName, fmt.Sprintf("templates.ai:%s", name), getHits()); d != nil {
		diags = append(diags, *d)
	}

	// Dry-run render every template against the actual TemplateData so missing
	// variables, parse errors, or other execution-time failures surface here
	// instead of at `dwe render ai` time.
	data := ai.TemplateData{
		Project:    cfg.Project,
		Service:    ai.ExtendsRoot(services, name),
		Resolved:   name,
		ServiceCfg: svc,
		Runtime:    cfg.Runtime,
		Services:   services,
		Cfg:        cfg,
	}
	failures := ai.DryRunRender(absRoot, packName, m, data)
	fromKeys := make([]string, 0, len(failures))
	for from := range failures {
		fromKeys = append(fromKeys, from)
	}
	sort.Strings(fromKeys)
	for _, from := range fromKeys {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ai:%s", name),
			File:     filepath.Join("workspace", "templates", "ai", packName, from),
			Message:  fmt.Sprintf("template render failed: %v", failures[from]),
			Hint:     "template references a value not present for this service; check the template's variable usage against the service config",
		})
	}
	return diags
}
