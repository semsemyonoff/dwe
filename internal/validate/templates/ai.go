package templates

import (
	"fmt"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/templates/ai"
	"devbox-cli/internal/validate"
)

// AIValidator validates AI template packs for all services.
type AIValidator struct{}

// ID returns the validator's unique ID within its domain.
func (v *AIValidator) ID() string {
	return "ai"
}

// Domain returns the domain this validator belongs to.
func (v *AIValidator) Domain() string {
	return "templates"
}

// Run validates all AI template packs and returns a list of diagnostics.
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

	// Select services that participate in AI rendering
	selected, skipped := ai.SelectServices(ctx.Cfg.Services)

	// Emit info diagnostics for skipped services with actionable reasons
	for _, skip := range skipped {
		var message, hint string
		switch skip.Reason {
		case "service-disabled":
			// These are expected; don't emit diagnostics
			continue
		case "ai-disabled":
			message = "service has ai.enabled: false"
			hint = "set ai.enabled: true to include this service in AI rendering"
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
		svc := ctx.Cfg.Services[name]
		diags = append(diags, v.validateService(name, svc, ctx.ProjectRoot)...)
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
func (v *AIValidator) validateService(name string, svc config.ServiceConfig, projectRoot string) []validate.Diagnostic {
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
	packDir, packName, found, err := ai.ResolveTemplatePack(svc, absRoot, name)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ai:%s", name),
			Message:  fmt.Sprintf("failed to resolve template pack: %v", err),
			Hint:     "check render.ai.template setting and devbox/templates/ai directory",
		}}
	}
	if !found {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ai:%s", name),
			Message:  fmt.Sprintf("template pack not found (tried %s, default)", name),
			Hint:     "check devbox/templates/ai directory",
		}}
	}

	// Load and validate manifest
	m, err := ai.LoadManifest(packDir)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.ai:%s", name),
			File:     filepath.Join("devbox", "templates", "ai", packName, "manifest.yml"),
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
			File:     filepath.Join("devbox", "templates", "ai", packName, "manifest.yml"),
			Message:  fmt.Sprintf("invalid manifest: %v", err),
			Hint:     "check render and symlink entries in manifest.yml",
		}}
	}

	var diags []validate.Diagnostic
	if d := overrideDiagnostic("templates", "ai", packName, fmt.Sprintf("templates.ai:%s", name), getHits()); d != nil {
		diags = append(diags, *d)
	}
	return diags
}
