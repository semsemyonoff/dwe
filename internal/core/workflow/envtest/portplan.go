package envtest

import (
	"slices"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// portPlan is everything the runner must know about host ports before it
// touches the disk: which declared service ports to remap, which vars: paths to
// allocate a port for, and the compose-isolation findings both were derived
// from. It is built ONCE per scenario — before CopyTree, on the scenario's view
// of the original project (see ScenarioView) — and then carried through the
// copy's local.yml generation, the isolation gate and the one deploy retry, so
// every consumer sees the same scan and the same batch of allocations.
type portPlan struct {
	// keys are the (service, portName) host ports the copy remaps.
	keys []hostPortKey
	// autoPaths are the vars:-relative dot-paths that get a freshly allocated
	// port: the scenario's explicit `auto` entries plus every path a compose
	// host port reads through an active exports.env rule, minus the paths the
	// scenario pins to a concrete value. Sorted and de-duplicated.
	autoPaths []string
	// findings is the raw scan output, handed to the isolation gate so it does
	// not have to scan a second time.
	findings []config.IsolationFinding
}

// buildPortPlan scans the scenario's view of the project and resolves the full
// set of ports the copy must allocate. It reads the working tree at baseDir
// rather than the copy (which does not exist yet); the divergence between the
// two is documented in packages.md and deliberately uncompensated.
func buildPortPlan(origCfg *config.DweConfig, scn *Scenario, baseDir string) portPlan {
	var env ScenarioEnv
	if scn != nil {
		env = scn.Env
	}

	plan := portPlan{keys: enabledHostPortKeys(origCfg, scn)}
	if origCfg == nil {
		return plan
	}
	plan.findings = config.ScanComposeIsolation(ScenarioView(origCfg, env), baseDir)

	// The scenario's env.vars, expanded exactly as scenarioEnvOverlay expands
	// them, so `ports.valkey: 6380` and `ports: {valkey: 6380}` answer the
	// pinned question the same way.
	overlay := expandVarPaths(env.Vars, nil)
	paths := scn.AutoPortVarPaths()
	for _, f := range plan.findings {
		if f.Kind != config.KindInterpolatedHostPort || f.VarPath == "" {
			continue
		}
		if pinnedVarPath(overlay, f.VarPath) {
			continue
		}
		paths = append(paths, f.VarPath)
	}
	slices.Sort(paths)
	plan.autoPaths = slices.Compact(paths)

	return plan
}

// hasAllocatedPorts reports whether the plan allocates anything — a remapped
// host port for an enabled service, or a vars: port. It is the FIRST of two
// gates on the one deploy retry (the second being isPortBindConflict): only a
// run that allocated ports can lose a TOCTOU race worth re-allocating for, but
// on its own it is true for nearly every project and so must never gate the
// retry alone.
func (p portPlan) hasAllocatedPorts() bool {
	return len(p.keys)+len(p.autoPaths) > 0
}

// pinnedVarPath reports whether the scenario fixed the vars: path to a concrete
// value, which disables the automatic remap of the compose host port reading it
// — the author asked for that number.
//
// Pinned means the path resolves, in the scenario's expanded env.vars overlay,
// to a scalar other than AutoPortSentinel, nil or "". An absent key obviously
// does not pin; nil and "" do not either, because a string-format export rule
// falls back to its default for both (envfile/render.go), so the copy would
// bind the ORIGINAL port. A falsy number (0) does pin: an int/bool-format rule
// keeps it, and either way the author wrote a value rather than asking for one.
// A map or a list at the path is structure, not a port, and does not pin — the
// implicit allocation wins there and BuildLocalOverlay's map-wins collision
// rule applies.
func pinnedVarPath(overlay map[string]any, path string) bool {
	value, ok := resolveVarPath(overlay, path)
	if !ok {
		return false
	}
	return pinsPortValue(value)
}

// pinsPortValue is pinnedVarPath's decision on an already-resolved value. It is
// shared with scenarioEnvOverlay so the plan and the overlay can never disagree
// about which declared values count as a pin: a value the plan allocated a port
// for must not be left standing in the generated local.yml, or the copy binds
// the ORIGINAL port with nothing warning about it.
func pinsPortValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != "" && v != AutoPortSentinel
	case map[string]any, []any:
		return false
	default:
		return true
	}
}

// resolveVarPath walks a dot-path through a nested map, returning the value and
// whether it resolved. A malformed path (empty, or with an empty segment) never
// resolves — config.ResolvePath alone would accept a trailing dot, and the same
// shape test gates what reaches here in the first place (classifyExportSource).
func resolveVarPath(m map[string]any, path string) (any, bool) {
	if !config.IsWellFormedDotPath(path) {
		return nil, false
	}
	return config.ResolvePath(m, path)
}
