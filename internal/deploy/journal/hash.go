package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
)

// ActionHash computes a stable hash for a config.Action based on type, cmd, and with parameters.
// The hash is order-independent for the with map and invariant to YAML whitespace/comments.
// Returns the full sha256 hex digest (64 characters).
func ActionHash(a config.Action) string {
	h := sha256.New()
	h.Write([]byte(a.Type))
	h.Write([]byte{0})
	h.Write([]byte(a.Cmd))
	h.Write([]byte{0})
	h.Write(canonicalMap(a.With))
	return hex.EncodeToString(h.Sum(nil))
}

// ShortHash returns a shortened version of the hash suitable for UI display.
// Currently returns the first 12 characters.
func ShortHash(fullHash string) string {
	if len(fullHash) < 12 {
		return fullHash
	}
	return fullHash[:12]
}

// ServiceConfigHash computes a stable hash for a service's configuration.
// It combines the service config block and the parsed deploy config (if present).
// The hash is invariant to key ordering and YAML formatting.
func ServiceConfigHash(svcCfg config.ServiceConfig, deployCfg *config.DeployConfig) string {
	h := sha256.New()
	h.Write(canonicalMap(serviceConfigToMap(svcCfg)))
	h.Write([]byte{0})
	if deployCfg != nil {
		h.Write(canonicalMap(deployConfigToMap(deployCfg)))
	} else {
		h.Write(canonicalMap(map[string]any{}))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ProjectConfigHash computes a stable hash for the entire project's configuration.
// It spans the tracked services' configs, the top-level deploy config, and per-service
// deploy configs. Edits to enabled-but-untracked services (e.g., main-debug) do not change
// the project hash.
//
// Parameters:
//   - cfg: the merged DevboxConfig
//   - deployCfg: the top-level deploy config (may be nil)
//   - svcDeploys: map of service names to their per-service deploy configs
//   - trackedServices: the canonical sorted list of tracked service names
func ProjectConfigHash(
	cfg *config.DevboxConfig,
	deployCfg *config.DeployConfig,
	svcDeploys map[string]*config.DeployConfig,
	trackedServices []string,
) string {
	h := sha256.New()

	// Hash the services map restricted to tracked names (in sorted order)
	trackedServicesMap := make(map[string]any)
	for _, svcName := range trackedServices {
		if svc, ok := cfg.Services[svcName]; ok {
			trackedServicesMap[svcName] = serviceConfigToMap(svc)
		}
	}
	h.Write(canonicalMap(trackedServicesMap))
	h.Write([]byte{0})

	// Hash the top-level deploy config
	if deployCfg != nil {
		h.Write(canonicalMap(deployConfigToMap(deployCfg)))
	} else {
		h.Write(canonicalMap(map[string]any{}))
	}
	h.Write([]byte{0})

	// Hash the per-service deploy configs for tracked services (in sorted order)
	trackedSvcDeploys := make(map[string]any)
	for _, svcName := range trackedServices {
		if svcDeploy, ok := svcDeploys[svcName]; ok && svcDeploy != nil {
			trackedSvcDeploys[svcName] = deployConfigToMap(svcDeploy)
		}
	}
	h.Write(canonicalMap(trackedSvcDeploys))

	return hex.EncodeToString(h.Sum(nil))
}

// canonicalMap marshals a map to JSON with sorted keys, making the output
// order-independent and invariant to YAML formatting/comments. Returns the
// JSON bytes suitable for hashing.
func canonicalMap(m map[string]any) []byte {
	if len(m) == 0 {
		return []byte("{}")
	}

	// Use encoding/json.Marshal with a custom encoder to guarantee sorted keys
	data, err := json.Marshal(sortedMap(m))
	if err != nil {
		// Should not happen for simple maps; panic on unexpected encoding errors
		panic(fmt.Sprintf("failed to marshal map: %v", err))
	}
	return data
}

// sortedMap recursively converts a map to a JSON-compatible representation with sorted keys.
// This ensures deterministic JSON output regardless of Go map iteration order.
func sortedMap(v any) any {
	switch val := v.(type) {
	case map[string]any:
		if len(val) == 0 {
			return val
		}
		// Create a slice of key-value pairs, sorted by key
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		// Build an ordered representation (using a custom type or structured format)
		// For JSON, we rely on json.Marshal's behavior, but we need to ensure
		// the keys are processed in order. We'll use a helper that re-encodes.
		result := make(map[string]any)
		for _, k := range keys {
			result[k] = sortedMap(val[k])
		}
		return result
	case []any:
		if len(val) == 0 {
			return val
		}
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = sortedMap(item)
		}
		return result
	default:
		return v
	}
}

// serviceConfigToMap converts a ServiceConfig to a map for hashing purposes.
func serviceConfigToMap(svc config.ServiceConfig) map[string]any {
	m := map[string]any{
		"type":              svc.Type,
		"container":         svc.Container,
		"mandatory":         svc.Mandatory,
		"dir":               svc.Dir,
		"dir_internal":      svc.DirInternal,
		"work_dir_internal": svc.WorkDirInternal,
		"extends":           svc.Extends,
		"depends_on":        svc.DependsOn,
		"compose":           svc.Compose,
	}

	// Include configs if present
	if len(svc.Configs) > 0 {
		configs := make([]any, len(svc.Configs))
		for i, cfg := range svc.Configs {
			configs[i] = map[string]any{
				"file":       cfg.File,
				"mountpoint": cfg.Mountpoint,
			}
		}
		m["configs"] = configs
	}

	// Include dirs if present
	if len(svc.Dirs) > 0 {
		m["dirs"] = svc.Dirs
	}

	// Include CLI config if present
	if svc.CLI.Mode != "" || svc.CLI.Shell != "" || svc.CLI.User != "" || svc.CLI.WorkDir != "" || len(svc.CLI.Env) > 0 {
		cli := map[string]any{}
		if svc.CLI.Mode != "" {
			cli["mode"] = svc.CLI.Mode
		}
		if svc.CLI.Shell != "" {
			cli["shell"] = svc.CLI.Shell
		}
		if svc.CLI.User != "" {
			cli["user"] = svc.CLI.User
		}
		if svc.CLI.WorkDir != "" {
			cli["workdir"] = svc.CLI.WorkDir
		}
		if len(svc.CLI.Env) > 0 {
			cli["env"] = svc.CLI.Env
		}
		m["cli"] = cli
	}

	// Include IDE config if present
	if svc.Render.IDE.Enabled != nil || svc.Render.IDE.Template != "" {
		ide := map[string]any{}
		if svc.Render.IDE.Enabled != nil {
			ide["enabled"] = *svc.Render.IDE.Enabled
		}
		if svc.Render.IDE.Template != "" {
			ide["template"] = svc.Render.IDE.Template
		}
		m["ide"] = ide
	}

	// Include AI config if present
	if svc.Render.AI.Enabled != nil || svc.Render.AI.Template != "" {
		ai := map[string]any{}
		if svc.Render.AI.Enabled != nil {
			ai["enabled"] = *svc.Render.AI.Enabled
		}
		if svc.Render.AI.Template != "" {
			ai["template"] = svc.Render.AI.Template
		}
		m["ai"] = ai
	}

	// Include Git config if present
	if svc.Render.Git.Enabled != nil || svc.Render.Git.Template != "" {
		git := map[string]any{}
		if svc.Render.Git.Enabled != nil {
			git["enabled"] = *svc.Render.Git.Enabled
		}
		if svc.Render.Git.Template != "" {
			git["template"] = svc.Render.Git.Template
		}
		m["git"] = git
	}

	return m
}

// deployConfigToMap converts a DeployConfig to a map for hashing purposes.
func deployConfigToMap(cfg *config.DeployConfig) map[string]any {
	if cfg == nil {
		return map[string]any{}
	}

	m := map[string]any{}

	if cfg.Log != nil {
		m["log"] = *cfg.Log
	}

	if len(cfg.Phases) > 0 {
		phases := make([]any, len(cfg.Phases))
		for i, phase := range cfg.Phases {
			p := map[string]any{
				"name":            phase.Name,
				"description":     phase.Description,
				"untracked":       phase.Untracked,
				"deploy_services": phase.DeployServices,
			}

			if phase.When != nil {
				p["when"] = conditionToMap(phase.When)
			}

			if len(phase.Steps) > 0 {
				steps := make([]any, len(phase.Steps))
				for j, step := range phase.Steps {
					s := map[string]any{
						"name":              step.Name,
						"type":              step.Type,
						"cmd":               step.Cmd,
						"description":       step.Description,
						"continue_on_error": step.ContinueOnError,
						"skip_confirm":      step.SkipConfirm,
					}

					if len(step.With) > 0 {
						s["with"] = step.With
					}

					if step.When != nil {
						s["when"] = conditionToMap(step.When)
					}

					if step.Check != nil {
						s["check"] = map[string]any{
							"type": step.Check.Type,
							"cmd":  step.Check.Cmd,
							"with": step.Check.With,
						}
					}

					steps[j] = s
				}
				p["steps"] = steps
			}

			phases[i] = p
		}
		m["phases"] = phases
	}

	return m
}

// conditionToMap converts a condition.Condition to a map for hashing.
func conditionToMap(cond *condition.Condition) map[string]any {
	if cond == nil {
		return map[string]any{}
	}
	m := map[string]any{}
	if cond.Type != "" {
		m["type"] = string(cond.Type)
	}
	if cond.Cmd != "" {
		m["cmd"] = cond.Cmd
	}
	if cond.Expr != "" {
		m["expr"] = cond.Expr
	}
	return m
}
