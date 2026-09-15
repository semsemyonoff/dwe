package envtest

import (
	"maps"
	"slices"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// AutoPortPlaceholder stands in for AutoPortSentinel wherever a scenario's
// env.vars are overlaid onto a config that is inspected rather than run
// (validate-time step rendering, the compose scan, the cost profile): the
// concrete number is arbitrary, it only has to satisfy the int/port-range
// checks a strict-int builtin param (e.g. tcp_reachable's port:) applies. The
// runner substitutes a real allocated port instead.
const AutoPortPlaceholder = 1

// ScenarioView returns the config the scenario's copy will run: cfg with the
// scenario's env.services toggles applied to both Services and
// Raw["services"], its env.vars merged into Raw["vars"] (AutoPortSentinel →
// AutoPortPlaceholder), and the per-developer compose overlays removed from
// both the typed fields (Compose.Extra, ServiceConfig.LocalComposeExtra) and
// Raw (compose.extra, services.<n>.compose.extra) — exactly as the copy's
// seeded local.yml removes them (see stripComposeExtra), so ComposeFiles()
// equals the chain the copy runs and an exports.env `when:` reading Raw sees
// the copy's state.
//
// This is the single definition of "the config the copy will run", shared by
// the runner's pre-copy port plan, `dwe validate`'s tests domain (render pass
// and isolation scan) and `dwe test list`'s cost profile.
//
// cfg is never mutated and two views never share state: Services is cloned,
// Compose is a value field, and every Raw subtree the view touches (compose,
// services, vars) is rebuilt from fresh maps rather than merged in place.
// A nil cfg yields nil.
func ScenarioView(cfg *config.DweConfig, env ScenarioEnv) *config.DweConfig {
	if cfg == nil {
		return nil
	}

	view := *cfg
	view.Compose.Extra = nil
	view.Services = maps.Clone(cfg.Services)
	if view.Services == nil {
		view.Services = map[string]config.ServiceConfig{}
	}

	// Enable runs before disable so disable wins on a service listed in both,
	// matching the runtime overlay order (scenarioEnvOverlay). A disabled
	// service keeps Enabled == Required, mirroring the loader's
	// Enabled = required || services.<name>.enabled computation, so a required
	// service a scenario "disables" stays enabled here too. Unknown names are
	// ignored (validate reports them as their own error).
	toggled := make(map[string]bool, len(env.Services.Enable)+len(env.Services.Disable))
	for _, name := range env.Services.Enable {
		if svc, ok := view.Services[name]; ok {
			svc.Enabled = true
			view.Services[name] = svc
			toggled[name] = true
		}
	}
	for _, name := range env.Services.Disable {
		if svc, ok := view.Services[name]; ok {
			svc.Enabled = svc.Required
			view.Services[name] = svc
			toggled[name] = true
		}
	}
	for name, svc := range view.Services {
		if len(svc.LocalComposeExtra) == 0 {
			continue
		}
		svc.LocalComposeExtra = nil
		view.Services[name] = svc
	}

	raw := maps.Clone(cfg.Raw)
	if raw == nil {
		// maps.Clone(nil) is nil, and the view must stay writable.
		raw = map[string]any{}
	}
	if compose, ok := raw["compose"].(map[string]any); ok {
		raw["compose"] = withoutComposeExtra(compose)
	}
	// A config whose Raw carries no services mirror gains one only when the
	// scenario has something to write into it.
	if entries, isMap := raw["services"].(map[string]any); isMap || len(toggled) > 0 {
		raw["services"] = rawServicesView(entries, view.Services, toggled)
	}
	if overlay := scenarioVarsOverlay(env.Vars); len(overlay) > 0 {
		existing, _ := raw["vars"].(map[string]any)
		raw["vars"] = mergeMaps(existing, overlay)
	}
	view.Raw = raw

	return &view
}

// rawServicesView rebuilds the Raw services mirror: every entry is cloned (not
// only the toggled ones — the overlay strip touches any service) with its
// compose.extra removed, and the effective enabled state written for the names
// the scenario toggles.
func rawServicesView(entries map[string]any, services map[string]config.ServiceConfig, toggled map[string]bool) map[string]any {
	out := make(map[string]any, len(entries)+len(toggled))
	for name, v := range entries {
		entry, ok := v.(map[string]any)
		if !ok {
			out[name] = v
			continue
		}
		clone := maps.Clone(entry)
		if compose, ok := clone["compose"].(map[string]any); ok {
			clone["compose"] = withoutComposeExtra(compose)
		}
		out[name] = clone
	}
	for name := range toggled {
		entry, ok := out[name].(map[string]any)
		if !ok {
			entry = map[string]any{}
		}
		entry["enabled"] = services[name].Enabled
		out[name] = entry
	}
	return out
}

// withoutComposeExtra returns a copy of a compose block without its extra key,
// or the block itself when it carries none.
func withoutComposeExtra(compose map[string]any) map[string]any {
	if _, has := compose["extra"]; !has {
		return compose
	}
	out := maps.Clone(compose)
	delete(out, "extra")
	return out
}

// scenarioVarsOverlay expands a scenario's env.vars dot-paths into a nested
// map, substituting AutoPortSentinel with AutoPortPlaceholder.
func scenarioVarsOverlay(vars map[string]any) map[string]any {
	if len(vars) == 0 {
		return nil
	}
	return expandVarPaths(vars, func(value any) any {
		if isAutoPort(value) {
			return AutoPortPlaceholder
		}
		return value
	})
}

// expandVarPaths expands env.vars dot-path keys into a nested map, passing each
// value through subst first (nil stores it as written). Paths are applied in
// sorted order so two paths colliding on a prefix resolve the same way on every
// run.
//
// A malformed path (empty, or with an empty segment) is silently skipped:
// callers here inspect rather than run, and the same path is rejected with an
// error by BuildLocalOverlay before anything runs, while validate's render pass
// reports the resulting unresolved ${vars.*} reference itself.
func expandVarPaths(vars map[string]any, subst func(any) any) map[string]any {
	out := make(map[string]any, len(vars))
	for _, path := range slices.Sorted(maps.Keys(vars)) {
		value := vars[path]
		if subst != nil {
			value = subst(value)
		}
		_ = setDotPath(out, path, value)
	}
	return out
}

// mergeMaps returns a new map holding dst's entries overlaid with src's (src
// wins on conflict; nested maps present on both sides merge recursively).
// Neither dst nor src is mutated — unlike localDeepMerge, which merges into
// dst's children in place and so cannot be used on a shared Raw subtree.
func mergeMaps(dst, src map[string]any) map[string]any {
	out := make(map[string]any, len(dst)+len(src))
	maps.Copy(out, dst)
	for k, sv := range src {
		if dm, ok := out[k].(map[string]any); ok {
			if sm, ok := sv.(map[string]any); ok {
				out[k] = mergeMaps(dm, sm)
				continue
			}
		}
		out[k] = sv
	}
	return out
}
