package templates

import (
	"fmt"
	"path/filepath"

	"github.com/semsemyonoff/devbox/internal/core/execution/templates/git"
	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/core/validate"
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

	// Template validation is scoped to app services. Tools/infra may have
	// runtime UI or support containers, but they do not own source hubs.
	services := appServices(ctx.Cfg.Services)
	selected, skipped := git.SelectServices(services)

	for _, skip := range skipped {
		var message, hint string
		switch skip.Reason {
		case "service-disabled", "git-disabled":
			// Service or git render explicitly disabled; nothing to report.
			continue
		case "git-policy":
			message = "service does not render git hooks by default (only 'app' type services render by default)"
			hint = "set render.git.enabled: true to opt in"
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
		serviceDiags := v.validateService(name, svc, ctx.ProjectRoot)
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
func (v *GitValidator) validateService(name string, svc config.ServiceConfig, projectRoot string) []validate.Diagnostic {
	var diags []validate.Diagnostic

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

	packDir, packName, found, err := git.ResolveTemplatePack(svc, absRoot, name)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.git:%s", name),
			Message:  fmt.Sprintf("failed to resolve template pack: %v", err),
			Hint:     "check render.git.template setting and devbox/templates/git directory",
		}}
	}
	if !found {
		return []validate.Diagnostic{{
			Severity: validate.SeverityWarning,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.git:%s", name),
			Message:  fmt.Sprintf("template pack not found for service %q", name),
			Hint: fmt.Sprintf(
				"create devbox/templates/git/%s or devbox/templates/git/default\n"+
					"or set services.%s.render.git.enabled: false in services.yml",
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
			File:     filepath.Join("devbox", "templates", "git", packName, "manifest.yml"),
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
			File:     filepath.Join("devbox", "templates", "git", packName, "manifest.yml"),
			Message:  fmt.Sprintf("invalid manifest: %v", err),
			Hint:     "check render entries (to must be a basename; symlinks not supported)",
		})
	} else if d := overrideDiagnostic("templates", "git", packName, fmt.Sprintf("templates.git:%s", name), getHits()); d != nil {
		diags = append(diags, *d)
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
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityInfo,
			Domain:   "templates",
			Target:   fmt.Sprintf("templates.git:%s", name),
			Message:  "no src/.git in service dir; render will be skipped",
			Hint:     "initialize a git repository at " + filepath.Join(svc.Dir, "src") + " or remove render.git.enabled",
		})
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
