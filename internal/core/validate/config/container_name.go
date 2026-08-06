package config

import (
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// containerNameValidator warns when a compose service's container_name:
// diverges from the conventional "<project>-<service>" name — the pattern the
// daemon builtins (daemon.ResolveContainerName) build directly and the one
// scripts and docs habitually assume. The defect is divergence itself, not
// casing: any container_name that does not match is confusing for raw
// `docker`/`docker compose` usage regardless of whether the declared value
// happens to be lowercase.
//
// dwe's OWN per-service paths (`dwe stop`/`restart`/`logs <name>`,
// docker_stop_remove_container) resolve containers through the compose
// project+service labels (docker.LookupServiceContainer), never by guessing
// this name, so they keep working either way — the diagnostic must not claim
// otherwise. Removing container_name is likewise NOT equivalent to aligning
// it: compose then names the container "<project>-<service>-1".
//
// Reuses config.ScanComposeIsolation's KindContainerName findings (the
// generic leaf scanner flags ANY container_name the `-f` merge leaves in
// effect, since even a derived-matching one is a collision risk for parallel
// `dwe test` copies) but applies its own filter on top: skip a declared value
// that already matches the derived name, and skip an interpolated `${...}`
// value that cannot be compared without resolving env.
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
				"service %s sets container_name: %q, which diverges from the conventional %q for this project",
				f.Resource, f.Value, derived,
			),
			Hint: fmt.Sprintf(
				"raw `docker`/`docker compose` commands, scripts, and docs that assume %q will not find this "+
					"container under that name (dwe's own per-service commands resolve containers through "+
					"compose labels, so they are unaffected). Align container_name to %q if anything depends "+
					"on that name — dropping it is not equivalent, compose then names the container %q.",
				derived, derived, derived+"-1",
			),
		})
	}
	return diags
}
