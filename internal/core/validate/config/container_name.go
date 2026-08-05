package config

import (
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// containerNameValidator warns when a compose service's container_name:
// diverges from the name dwe derives for it ("<project>-<service>", the same
// pattern the daemon builtins (daemon.ResolveContainerName) build directly).
// The defect is divergence itself, not casing — any container_name that does
// not match the derived name is confusing for raw `docker`/`docker compose`
// usage, scripts, and documentation that assume the derived name, regardless
// of whether the declared value happens to be lowercase.
//
// Reuses config.ScanComposeIsolation's KindContainerName findings (the
// generic leaf scanner flags ANY non-empty container_name, since even a
// derived-matching one is a collision risk for parallel `dwe test` copies)
// but applies its own filter on top: skip a declared value that already
// matches the derived name, and skip an interpolated `${...}` value that
// cannot be compared without resolving env.
type containerNameValidator struct{}

var _ validate.Validator = (*containerNameValidator)(nil)

func (v *containerNameValidator) ID() string     { return "container_name" }
func (v *containerNameValidator) Domain() string { return "config" }

func (v *containerNameValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if ctx.Cfg == nil {
		return nil
	}

	resolved, err := config.ResolveComposeProjectName(ctx.ProjectRoot, ctx.Cfg)
	if err != nil || resolved == "" {
		// Resolution errors are surfaced by the docker validator; an empty
		// resolved name means dwe passes no -p at all, so there is nothing to
		// derive against.
		return nil
	}

	var diags []validate.Diagnostic
	for _, f := range config.ScanComposeIsolation(ctx.Cfg, ctx.ProjectRoot) {
		if f.Kind != config.KindContainerName {
			continue
		}
		if strings.Contains(f.Value, "$") {
			// Interpolated (e.g. ${COMPOSE_PROJECT_NAME}-app) — cannot prove
			// divergence without resolving the environment.
			continue
		}
		derived := resolved + "-" + f.Resource
		if f.Value == derived {
			continue
		}

		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityWarning,
			Domain:   "config",
			Target:   "config.container_name",
			File:     relPath(ctx.ProjectRoot, f.File),
			Message: fmt.Sprintf(
				"service %s sets container_name: %q, which diverges from the name dwe derives for it (%q)",
				f.Resource, f.Value, derived,
			),
			Hint: fmt.Sprintf(
				"raw `docker`/`docker compose` commands, scripts, and docs that assume the derived name %q "+
					"will not find this container under that name. Align container_name to %q, or drop it so "+
					"compose's own naming applies.",
				derived, derived,
			),
		})
	}
	return diags
}
