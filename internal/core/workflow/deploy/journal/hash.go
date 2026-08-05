package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sort"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// Hashes are computed from raw, untranslated fields; do not introduce locale-dependent
// inputs (e.g., i18n.Translator lookups) into any hash function. Changing the active
// locale must not invalidate cached steps or phases.

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

// StepHash computes a stable hash for a config.DeployStep including its action and files_gate.
// When FilesGate is nil, the hash is identical to ActionHash(step.Action()) for backwards compatibility.
// When FilesGate is present, the hash includes the canonical FilesGate representation.
// The hash is invariant to map key ordering and YAML formatting.
func StepHash(step config.DeployStep) string {
	h := sha256.New()
	// Hash the action part first (same as ActionHash)
	a := step.Action()
	h.Write([]byte(a.Type))
	h.Write([]byte{0})
	h.Write([]byte(a.Cmd))
	h.Write([]byte{0})
	h.Write(canonicalMap(a.With))

	// Hash the FilesGate part (if present)
	if step.FilesGate != nil {
		h.Write([]byte{0})
		h.Write(canonicalFilesGate(step.FilesGate))
	}

	return hex.EncodeToString(h.Sum(nil))
}

// canonicalFilesGate returns a canonical JSON representation of a FilesGate for hashing.
// This ensures that files_gate directives with the same semantic meaning hash identically,
// even when require IDs are specified in different order.
func canonicalFilesGate(fg *filesgate.FilesGate) []byte {
	if fg == nil {
		return []byte{}
	}

	// Build a map representing the FilesGate canonically
	m := map[string]any{
		"state": string(fg.State),
	}

	if fg.Command != "" {
		m["command"] = fg.Command
	}

	if len(fg.With) > 0 {
		m["with"] = fg.With
	}

	// Canonicalize the require spec
	m["require"] = canonicalRequireSpec(fg.Require)

	return canonicalMap(m)
}

// canonicalRequireSpec returns a canonical representation of a RequireSpec for hashing.
// This handles the different forms (required, all, list) and ensures consistent hashing.
func canonicalRequireSpec(r filesgate.RequireSpec) any {
	switch req := r.(type) {
	case filesgate.RequireRequired:
		return "required"
	case filesgate.RequireAll:
		return "all"
	case filesgate.RequireList:
		if len(req.IDs) == 0 {
			// Empty list is represented as-is
			return []string{}
		}
		// Sort the list for canonical ordering
		sorted := make([]string, len(req.IDs))
		copy(sorted, req.IDs)
		sort.Strings(sorted)
		return sorted
	case nil:
		return "required" // nil normalizes to RequireRequired via UnmarshalYAML
	default:
		return "unknown"
	}
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
// It combines the service config block, the parsed deploy config (if present),
// and the project's vars block. vars is included because resolve-time template
// rendering (pipeline.RenderStep) substitutes ${vars.*} into cmd/with/check, so a
// scoped (--service) deploy must re-run when a referenced var changes even though
// ProjectConfigHash is never consulted for that scope (deploy.go: computeScopeState /
// makeSkipDecider compare against ServiceConfigHash for service-scoped steps).
// The hash is invariant to key ordering and YAML formatting.
func ServiceConfigHash(svcCfg config.ServiceConfig, deployCfg *config.ServiceDeployConfig, vars map[string]any) string {
	h := sha256.New()
	h.Write(canonicalMap(serviceConfigToMap(svcCfg)))
	h.Write([]byte{0})
	if deployCfg != nil {
		h.Write(canonicalMap(serviceDeployConfigToMap(deployCfg)))
	} else {
		h.Write(canonicalMap(map[string]any{}))
	}
	h.Write([]byte{0})
	h.Write(canonicalMap(vars))
	return hex.EncodeToString(h.Sum(nil))
}

// ProjectConfigHash computes a stable hash for the entire project's configuration.
// It spans the tracked services' configs, the top-level deploy config, and per-service
// deploy configs. Edits to enabled-but-untracked services (e.g., main-debug) do not change
// the project hash.
//
// Parameters:
//   - cfg: the merged DweConfig
//   - deployCfg: the top-level deploy config (may be nil)
//   - svcDeploys: map of service names to their per-service deploy configs
//   - trackedServices: the canonical sorted list of tracked service names
func ProjectConfigHash(
	cfg *config.DweConfig,
	deployCfg *config.ProjectDeployConfig,
	svcDeploys map[string]*config.ServiceDeployConfig,
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
		h.Write(canonicalMap(projectDeployConfigToMap(deployCfg)))
	} else {
		h.Write(canonicalMap(map[string]any{}))
	}
	h.Write([]byte{0})

	// Hash the per-service deploy configs for tracked services (in sorted order)
	trackedSvcDeploys := make(map[string]any)
	for _, svcName := range trackedServices {
		if svcDeploy, ok := svcDeploys[svcName]; ok && svcDeploy != nil {
			trackedSvcDeploys[svcName] = serviceDeployConfigToMap(svcDeploy)
		}
	}
	h.Write(canonicalMap(trackedSvcDeploys))
	h.Write([]byte{0})

	// Hash the project's vars block. Resolve-time template rendering substitutes
	// ${vars.*} into step cmd/with/check, so a changed var must invalidate the
	// project hash or a full deploy would report "already up-to-date" while the
	// rendered command text has actually changed.
	h.Write(canonicalMap(cfg.Vars))

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
		slog.Error("journal: canonicalMap: json.Marshal failed; using empty fallback", "err", err)
		return []byte("{}")
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

		// encoding/json.Marshal guarantees sorted keys for map[string]any,
		// so building a new map with recursively processed values is sufficient.
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
		"required":          svc.Required,
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

// phasesToMap converts a slice of deploy phases to the []any representation
// used by both project and service deploy-config hash maps.
func phasesToMap(phases []config.DeployPhase) []any {
	result := make([]any, len(phases))
	for i, phase := range phases {
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
				steps[j] = deployStepToMap(step)
			}
			p["steps"] = steps
		}

		result[i] = p
	}
	return result
}

// projectDeployConfigToMap converts a ProjectDeployConfig to a map for hashing purposes.
func projectDeployConfigToMap(cfg *config.ProjectDeployConfig) map[string]any {
	if cfg == nil {
		return map[string]any{}
	}

	m := map[string]any{}

	if cfg.Log != nil {
		m["log"] = *cfg.Log
	}

	if len(cfg.Phases) > 0 {
		m["phases"] = phasesToMap(cfg.Phases)
	}

	return m
}

// serviceDeployConfigToMap converts a ServiceDeployConfig to a map for hashing purposes.
// Includes the After field which is specific to service-level configs.
func serviceDeployConfigToMap(cfg *config.ServiceDeployConfig) map[string]any {
	if cfg == nil {
		return map[string]any{}
	}

	m := map[string]any{}

	if len(cfg.After) > 0 {
		m["after"] = cfg.After
	}

	if cfg.Log != nil {
		m["log"] = *cfg.Log
	}

	if len(cfg.Phases) > 0 {
		m["phases"] = phasesToMap(cfg.Phases)
	}

	return m
}

// deployStepToMap converts a config.DeployStep to a map for hashing purposes.
// Works for both top-level steps and parallel sub-steps.
func deployStepToMap(step config.DeployStep) map[string]any {
	s := map[string]any{
		"name":              step.Name,
		"type":              step.Type,
		"cmd":               step.Cmd,
		"description":       step.Description,
		"continue_on_error": step.ContinueOnError,
		"skip_confirm":      step.SkipConfirm,
		"untracked":         step.Untracked,
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

	if step.FilesGate != nil {
		s["files_gate"] = filesGateToMap(step.FilesGate)
	}

	if step.Parallel != nil {
		s["parallel"] = parallelGroupToMap(step.Parallel)
	}

	return s
}

// parallelGroupToMap converts a config.ParallelGroup to a map for hashing purposes.
func parallelGroupToMap(pg *config.ParallelGroup) map[string]any {
	if pg == nil {
		return map[string]any{}
	}
	m := map[string]any{
		"max_concurrent": pg.MaxConcurrent,
	}
	if pg.FailFast != nil {
		m["fail_fast"] = *pg.FailFast
	}
	if len(pg.Steps) > 0 {
		steps := make([]any, len(pg.Steps))
		for i, sub := range pg.Steps {
			steps[i] = deployStepToMap(sub)
		}
		m["steps"] = steps
	}
	return m
}

// filesGateToMap converts a filesgate.FilesGate to a map for hashing purposes.
func filesGateToMap(fg *filesgate.FilesGate) map[string]any {
	if fg == nil {
		return map[string]any{}
	}

	m := map[string]any{
		"state": string(fg.State),
	}

	if fg.Command != "" {
		m["command"] = fg.Command
	}

	if len(fg.With) > 0 {
		m["with"] = fg.With
	}

	// Represent the require spec canonically
	m["require"] = canonicalRequireSpec(fg.Require)

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
