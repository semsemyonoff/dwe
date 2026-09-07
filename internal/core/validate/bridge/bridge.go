// Package bridge validates per-service host-bridge settings (the `bridge:`
// block in workspace/services/<name>/service.yml). The domain participates in
// `dwe validate` only — never in preflight (preflight consumes valconfig.All()
// exclusively): a bridge config mistake must not block unrelated lifecycle
// commands.
package bridge

import (
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// All returns all bridge validators. Mirrors the per-domain shape of
// internal/core/validate/config.All.
func All() []validate.Validator {
	return []validate.Validator{
		&servicesValidator{},
	}
}

// servicesValidator checks every service's `bridge:` block: the
// on_unreachable policy enum, shim_path absoluteness, and — for services with
// bridge effectively enabled — that the bridge can actually deliver commands
// (a container target exists and the dir/dir_internal workspace mapping
// needed for shim cwd translation is declared).
type servicesValidator struct{}

func (v *servicesValidator) ID() string     { return "services" }
func (v *servicesValidator) Domain() string { return "bridge" }

func (v *servicesValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	services, ok := resolveServices(ctx)
	if !ok || len(services) == 0 {
		return diags
	}

	servicesDir := filepath.Join(ctx.ProjectRoot, "workspace", "services")

	for _, name := range slices.Sorted(maps.Keys(services)) {
		svc := services[name]
		svcFile := relPath(ctx.ProjectRoot, filepath.Join(servicesDir, name, "service.yml"))
		target := "bridge.services:" + name

		emit := func(severity validate.Severity, msg, hint string) {
			diags = append(diags, validate.Diagnostic{
				Severity: severity,
				Domain:   "bridge",
				Target:   target,
				File:     svcFile,
				Message:  msg,
				Hint:     hint,
			})
		}

		// on_unreachable is a closed enum. An unknown value is inert at
		// runtime (anything but "warn" behaves as "fail"), which is exactly
		// how a typo like "wran" silently flips the shim's exit-code policy —
		// surface it as an error regardless of the enabled toggle.
		if ou := svc.Bridge.OnUnreachable; ou != "" &&
			ou != config.BridgeOnUnreachableFail && ou != config.BridgeOnUnreachableWarn {
			emit(validate.SeverityError,
				fmt.Sprintf("service %q bridge.on_unreachable: unknown value %q; valid: %s, %s",
					name, ou, config.BridgeOnUnreachableFail, config.BridgeOnUnreachableWarn),
				"use on_unreachable: fail (hooks block when the host daemon is down) or warn (exit 0 with a warning)")
		}

		// shim_path is a container path (always slash-separated), so the check
		// uses path.IsAbs, not filepath.IsAbs — the value describes the
		// container's filesystem and must never be judged by the host's path
		// rules. A relative target is invalid in a compose bind mount.
		if sp := svc.Bridge.ShimPath; sp != "" && !path.IsAbs(sp) {
			emit(validate.SeverityError,
				fmt.Sprintf("service %q bridge.shim_path: %q must be an absolute container path", name, sp),
				"use an absolute mount target, e.g. /usr/local/bin/dwe")
		}

		if !svc.BridgeEnabled() {
			continue
		}

		// Bridge-enabled service without a container target: the overlay
		// generator skips it, so the opt-in silently does nothing.
		// Unreachable after LoadConfig (container defaults to the folder
		// name) but kept for literal-built configs, mirroring the skip in
		// BuildOverlaySpec.
		if svc.Container == "" {
			emit(validate.SeverityWarning,
				fmt.Sprintf("service %q has bridge enabled but no container target; the bridge overlay skips it", name),
				"declare container: in service.yml or set bridge.enabled: false")
			continue
		}

		// Bridge-enabled service without the dir/dir_internal workspace
		// mapping: the overlay omits DWE_HOST_WORKSPACE/DWE_CONTAINER_WORKSPACE,
		// the shim cannot translate container working directories to host
		// paths, and the daemon rejects untranslated cwds via its project
		// containment check (design D7) — the bridge mounts fine but every
		// in-container invocation fails.
		if svc.Dir == "" || svc.DirInternal == "" {
			// dir/dir_internal are app-only fields (strict decode), so the
			// remedy differs by type: apps can declare the mapping, tool and
			// infra services can only opt out (or mount the project at an
			// identical path so cwds need no translation).
			hint := "declare both dir and dir_internal in service.yml, or set bridge.enabled: false"
			if !svc.IsApp() {
				hint = "set bridge.enabled: false, or mount the workspace at the same path inside the container so working directories need no translation"
			}
			emit(validate.SeverityWarning,
				fmt.Sprintf("service %q has bridge enabled but no dir/dir_internal workspace mapping; shim working-directory translation is unavailable and bridged commands will be rejected by the daemon containment check", name),
				hint)
		}
	}

	return diags
}

// resolveServices returns the services map from ctx.Cfg when present,
// otherwise loads it from disk. ok is false only when the map could not be
// resolved (load error), signalling the caller to skip silently — the config
// domain surfaces the underlying load error.
func resolveServices(ctx validate.Context) (map[string]config.ServiceConfig, bool) {
	if ctx.Cfg != nil && ctx.Cfg.Services != nil {
		return ctx.Cfg.Services, true
	}
	loaded, err := config.LoadServices(ctx.ProjectRoot)
	if err != nil {
		return nil, false
	}
	return loaded, true
}

// relPath returns the relative path from projectRoot to path, or the path
// as-is if relative resolution fails.
func relPath(projectRoot, p string) string {
	rel, err := filepath.Rel(projectRoot, p)
	if err != nil {
		return p
	}
	return rel
}
