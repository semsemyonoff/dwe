package templates

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packcommon"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/workspace"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// WorkspaceValidator validates packs selected by render.workspace without writing files.
type WorkspaceValidator struct{}

// ID returns the validator's unique ID within its domain.
func (v *WorkspaceValidator) ID() string {
	return "workspace"
}

// Domain returns the domain this validator belongs to.
func (v *WorkspaceValidator) Domain() string {
	return "templates"
}

// Run plans all workspace packs together to check templates and cross-pack collisions.
func (v *WorkspaceValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if ctx.Cfg == nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityInfo,
			Domain:   "templates",
			Target:   "templates.workspace",
			Message:  "workspace template validation requires successful main config load; skipped",
		}}
	}
	cfg := sanitizedCfg(ctx)
	if len(cfg.Render.Workspace) == 0 {
		return []validate.Diagnostic{{
			Severity: validate.SeverityInfo,
			Domain:   "templates",
			Target:   "templates.workspace",
			Message:  "no workspace packs configured in render.workspace",
		}}
	}
	commands, groups := commandIndex(ctx)
	data := packcommon.TemplateData{
		Project:       cfg.Project,
		Runtime:       cfg.Runtime,
		Services:      cfg.Services,
		Cfg:           cfg,
		Commands:      commands,
		CommandGroups: groups,
	}
	if _, err := workspace.Plan(ctx.ProjectRoot, cfg.Render.Workspace, data); err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "templates",
			Target:   "templates.workspace",
			Message:  fmt.Sprintf("workspace pack validation failed: %v", err),
			Hint:     "check render.workspace and workspace/templates/workspace pack manifests and sources",
		}}
	}
	return []validate.Diagnostic{{
		Severity: validate.SeverityOK,
		Domain:   "templates",
		Target:   "templates.workspace",
		Message:  "all workspace template packs valid",
	}}
}
