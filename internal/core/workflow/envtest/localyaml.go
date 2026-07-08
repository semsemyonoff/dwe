package envtest

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/local"
)

// LoadSeedLocalYAML reads the original project's workspace/local.yml — via the
// same layer-path convention config.LoadConfig uses — to seed the disposable
// copy's generated local.yml. A missing file yields an empty map, not an
// error (same contract as local.LoadLocalYAML).
func LoadSeedLocalYAML(origWorkspacePath string) (map[string]any, error) {
	return local.LoadLocalYAML(config.LocalLayerPath(origWorkspacePath))
}

// BuildLocalOverlay assembles the copy's generated local.yml content from
// three layers, applied low -> high precedence (spec §5):
//
//  1. seed — the original project's local.yml map (absent file -> empty),
//     with compose.extra and services.<name>.compose.extra stripped (each
//     strip reported via warn) — a local compose overlay path has no meaning
//     transplanted into a disposable copy running under a different compose
//     project name;
//  2. the scenario's env overrides — env.vars dot-paths rooted at vars:, with
//     any value equal to AutoPortSentinel replaced by its allocated port
//     (ports is keyed by the var's dot-path, e.g. "app.http_port"); plus
//     env.services.enable/disable toggling services.<name>.enabled;
//  3. identity — project.prefix = projectName, update.mode = "off" (always
//     wins; an empty/absent mode means ON, see UpdateConfig.EffectiveMode).
//
// Layers merge with the same precedent as config's internal deepMerge: when
// both sides hold a map at a key they merge recursively, otherwise the
// higher-precedence layer wins outright — including replacing an earlier
// scalar with a map. This is the pinned resolution for the nesting-collision
// case (e.g. a seeded `vars: {app: "x"}` followed by a scenario var path
// `app.http_port` yields `vars: {app: {http_port: N}}`, discarding the seeded
// scalar) — deliberately consistent with how every other config layer in this
// repo resolves the same shape of collision.
func BuildLocalOverlay(seed map[string]any, scn *Scenario, projectName string, ports map[string]int, warn func(string)) (map[string]any, error) {
	if scn == nil {
		return nil, fmt.Errorf("envtest: BuildLocalOverlay: nil scenario")
	}
	if warn == nil {
		warn = func(string) {}
	}

	overlay := stripComposeExtra(seed, warn)

	scenarioOverlay, err := scenarioEnvOverlay(scn, ports)
	if err != nil {
		return nil, err
	}
	localDeepMerge(overlay, scenarioOverlay)

	localDeepMerge(overlay, map[string]any{
		"project": map[string]any{"prefix": projectName},
		"update":  map[string]any{"mode": "off"},
	})

	return overlay, nil
}

// WriteGeneratedLocalYAML writes overlay as the disposable copy's
// workspace/local.yml. The copy's file has nothing to preserve (it is
// generated, not developer-edited), so the map-based writer is the right
// tool here — unlike every other local.yml write path in this repo, which
// must route through the comment-preserving node writer.
func WriteGeneratedLocalYAML(copyRoot string, overlay map[string]any) error {
	path := config.LocalLayerPath(filepath.Join(copyRoot, "workspace.yml"))
	if err := local.WriteLocalYAML(path, overlay); err != nil {
		return fmt.Errorf("envtest: writing generated local.yml: %w", err)
	}
	return nil
}

// stripComposeExtra returns a deep copy of seed with compose.extra and every
// services.<name>.compose.extra removed, reporting each strip via warn.
func stripComposeExtra(seed map[string]any, warn func(string)) map[string]any {
	out := deepCopyMap(seed)

	if compose, ok := asMap(out["compose"]); ok {
		if _, has := compose["extra"]; has {
			delete(compose, "extra")
			warn("stripped compose.extra from seeded local.yml (not meaningful in a disposable test copy)")
		}
	}

	if services, ok := asMap(out["services"]); ok {
		names := make([]string, 0, len(services))
		for name := range services {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			svc, ok := asMap(services[name])
			if !ok {
				continue
			}
			compose, ok := asMap(svc["compose"])
			if !ok {
				continue
			}
			if _, has := compose["extra"]; has {
				delete(compose, "extra")
				warn(fmt.Sprintf("stripped services.%s.compose.extra from seeded local.yml (not meaningful in a disposable test copy)", name))
			}
		}
	}

	return out
}

// scenarioEnvOverlay builds the vars:/services: overlay from a scenario's
// env block. ports is keyed by the var's dot-path (relative to vars:) for
// every var whose value is AutoPortSentinel.
func scenarioEnvOverlay(scn *Scenario, ports map[string]int) (map[string]any, error) {
	overlay := make(map[string]any)

	if len(scn.Env.Vars) > 0 {
		paths := make([]string, 0, len(scn.Env.Vars))
		for path := range scn.Env.Vars {
			paths = append(paths, path)
		}
		sort.Strings(paths)

		vars := make(map[string]any)
		for _, path := range paths {
			value := scn.Env.Vars[path]
			if s, ok := value.(string); ok && s == AutoPortSentinel {
				port, ok := ports[path]
				if !ok {
					return nil, fmt.Errorf("envtest: scenario var %q is %q but no port was allocated for it", path, AutoPortSentinel)
				}
				value = port
			}
			if err := setDotPath(vars, path, value); err != nil {
				return nil, fmt.Errorf("envtest: scenario var %q: %w", path, err)
			}
		}
		overlay["vars"] = vars
	}

	if len(scn.Env.Services.Enable) > 0 || len(scn.Env.Services.Disable) > 0 {
		services := make(map[string]any)
		for _, name := range scn.Env.Services.Enable {
			services[name] = map[string]any{"enabled": true}
		}
		for _, name := range scn.Env.Services.Disable {
			services[name] = map[string]any{"enabled": false}
		}
		overlay["services"] = services
	}

	return overlay, nil
}

// setDotPath inserts value at the dot-path in m, creating intermediate maps
// as needed. A path segment already holding a non-map value is overwritten
// wholesale (map wins) — the same collision resolution BuildLocalOverlay
// documents for the seed/scenario merge.
func setDotPath(m map[string]any, path string, value any) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}
	parts := strings.Split(path, ".")
	for i, part := range parts {
		if part == "" {
			return fmt.Errorf("empty path segment in %q", path)
		}
		if i == len(parts)-1 {
			m[part] = value
			return nil
		}
		next, ok := m[part].(map[string]any)
		if !ok {
			next = make(map[string]any)
			m[part] = next
		}
		m = next
	}
	return nil
}

// localDeepMerge merges src into dst in place, mirroring config's internal
// deepMerge precedent: keys present in both as maps recurse, otherwise src
// wins outright (including replacing a scalar with a map or vice versa).
func localDeepMerge(dst, src map[string]any) {
	for k, sv := range src {
		if sv == nil {
			continue
		}
		dv, exists := dst[k]
		if !exists {
			dst[k] = sv
			continue
		}
		dsm, dIsMap := dv.(map[string]any)
		ssm, sIsMap := sv.(map[string]any)
		if dIsMap && sIsMap {
			localDeepMerge(dsm, ssm)
			continue
		}
		dst[k] = sv
	}
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func deepCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if nested, ok := v.(map[string]any); ok {
			out[k] = deepCopyMap(nested)
			continue
		}
		out[k] = v
	}
	return out
}
