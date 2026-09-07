package templates

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/git"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// GitValidator validates git-hooks template packs for app services.
type GitValidator struct{}

// ID returns the validator's unique ID within its domain.
func (v *GitValidator) ID() string {
	return "git"
}

// Domain returns the domain this validator belongs to.
func (v *GitValidator) Domain() string {
	return "templates"
}

// Run validates app git template packs and returns a list of diagnostics.
func (v *GitValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	if ctx.Cfg == nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityInfo,
			Domain:   "templates",
			Target:   "templates.git",
			Message:  "git template validation requires successful main config load; skipped",
		}}
	}

	// Validate every service that actually participates in git-hook rendering
	// at runtime — apps by default plus any non-app service that opted in via
	// render.git.enabled: true. SelectServices honors the same gating that
	// `dwe render git` uses, so the validator scope matches what would be
	// rendered.
	cfg := sanitizedCfg(ctx)
	services := cfg.Services
	selected, skipped := git.SelectServices(services)

	for _, skip := range skipped {
		var message, hint string
		switch skip.Reason {
		case "service-disabled", "git-disabled", "git-policy":
			// Service or git render disabled (explicit or by-policy); nothing to report.
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
				Target:   fmt.Sprintf("templates.git:%s", skip.Name),
				Message:  message,
				Hint:     hint,
			})
		}
	}

	for _, name := range selected {
		svc := services[name]
		serviceDiags := v.validateService(name, svc, cfg, ctx.ProjectRoot)
		diags = append(diags, serviceDiags...)
	}

	if len(diags) == 0 {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "templates",
			Target:   "templates.git",
			Message:  "all git template packs valid",
		})
	}

	return diags
}

// validateService validates one service's git template pack. Returns a slice so
// the caller can surface both an error (pack/manifest issue) and an info
// (missing src/.git or worktree pointer) for the same service.
func (v *GitValidator) validateService(name string, svc config.ServiceConfig, cfg *config.DweConfig, projectRoot string) []validate.Diagnostic {
	services := cfg.Services
	var diags []validate.Diagnostic
	_, gitExplicit := svc.GitRenderEnabledExplicit()

	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.git:%s", name),
			Message:  fmt.Sprintf("resolve project root: %v", err),
		}}
	}

	absHub, err := git.PrepareHub(absRoot, name, svc)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.git:%s", name),
			Message:  fmt.Sprintf("invalid service dir: %v", err),
			Hint:     "service.dir must be a contained, non-symlinked subdirectory of the project root",
		}}
	}

	packDir, packName, found, err := git.ResolveTemplatePack(svc, services, absRoot, name)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.git:%s", name),
			Message:  fmt.Sprintf("failed to resolve template pack: %v", err),
			Hint:     "check render.git.template setting and workspace/templates/git directory",
		}}
	}
	if !found {
		if !gitExplicit {
			// Implicit default (app type, no render.git key) + absent pack: the
			// scaffold ships with no template pack, so this is expected, not
			// broken. Warn only once the user has opted in explicitly.
			return nil
		}
		return []validate.Diagnostic{{
			Severity: validate.SeverityWarning,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.git:%s", name),
			Message:  fmt.Sprintf("template pack not found for service %q", name),
			// Top-level render: — see the same note in ai.go: service.yml's
			// strict per-type field allowlist rejects a `services` key, so the
			// qualified path would break the project if pasted verbatim.
			Hint: fmt.Sprintf(
				"create workspace/templates/git/%s or workspace/templates/git/default\n"+
					"or set render.git.enabled: false in workspace/services/%s/service.yml",
				name, name,
			),
		}}
	}

	// Synthesize destRoot for ValidateManifest containment math. The directory
	// does not need to exist for shape validation to be meaningful (git's `to`
	// is basename-only).
	destRoot := filepath.Join(absHub, "src", ".git", "hooks")

	m, err := git.LoadManifest(packDir)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.git:%s", name),
			File:     filepath.Join("workspace", "templates", "git", packName, "manifest.yml"),
			Message:  fmt.Sprintf("failed to load manifest: %v", err),
			Hint:     "check manifest.yml syntax and structure",
		}}
	}

	sink, getHits := overrideSink()
	if err := git.ValidateManifest(m, absRoot, packName, destRoot, sink); err != nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.git:%s", name),
			File:     filepath.Join("workspace", "templates", "git", packName, "manifest.yml"),
			Message:  fmt.Sprintf("invalid manifest: %v", err),
			Hint:     "check render entries (to must be a basename; symlinks not supported)",
		})
	} else if d := overrideDiagnostic("templates", "git", packName, fmt.Sprintf("templates.git:%s", name), getHits()); d != nil {
		diags = append(diags, *d)
	}

	// Dry-run render every template against the actual TemplateData so missing
	// variables, parse errors, or other execution-time failures surface here
	// instead of at `dwe render git` time.
	data := git.TemplateData{
		Project:    cfg.Project,
		Service:    git.ExtendsRoot(services, name),
		Resolved:   name,
		ServiceCfg: svc,
		Runtime:    cfg.Runtime,
		Services:   services,
		Cfg:        cfg,
	}
	failures := git.DryRunRender(absRoot, packName, m, data)
	fromKeys := make([]string, 0, len(failures))
	for from := range failures {
		fromKeys = append(fromKeys, from)
	}
	sort.Strings(fromKeys)
	for _, from := range fromKeys {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.git:%s", name),
			File:     filepath.Join("workspace", "templates", "git", packName, from),
			Message:  fmt.Sprintf("template render failed: %v", failures[from]),
			Hint:     "template references a value not present for this service; check the template's variable usage against the service config",
		})
	}

	// Optional advisory diagnostic — does not gate validation.
	_, status, hookErr := git.ResolveGitHooksDir(absHub)
	switch {
	case hookErr != nil:
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityWarning,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.git:%s", name),
			Message:  fmt.Sprintf("cannot inspect src/.git: %v", hookErr),
			Hint:     "render git will fail; check for unsupported symlinks in the service directory",
		})
	case status == git.DirMissing:
		// Implicit default (app type, no render.git key) + no src/.git yet: the
		// repo may still be populated by the deploy pipeline (e.g. a clone step)
		// before render ever runs, so flagging it here is premature. Report only
		// once the user has opted in explicitly.
		if gitExplicit {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityInfo,
				Domain:   "templates",
				Target:   fmt.Sprintf("templates.git:%s", name),
				Message:  "no src/.git in service dir; render will be skipped",
				Hint:     "initialize a git repository at " + filepath.Join(svc.Dir, "src") + " or remove render.git.enabled",
			})
		}
	case status == git.DirWorktree:
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.git:%s", name),
			Message:  "src/.git is a worktree pointer (not yet supported); render will be skipped",
		})
	}

	return diags
}
